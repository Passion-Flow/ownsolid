package runtime

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
)

const certificateTestNode = "11111111-2222-4333-8444-555555555555"

// signingControl is a control plane that signs what agents ask for and records each request.
type signingControl struct {
	mu       sync.Mutex
	ca       *x509.Certificate
	key      *ecdsa.PrivateKey
	lifetime time.Duration
	// renewals and confirmations record the request and the certificate it came with.
	renewals      []seenRenewal
	confirmations int
	// failRenewal makes the next renewals fail after they were recorded, like a lost reply.
	failRenewal int
	refusal     string
	refusalCode int
}

type seenRenewal struct {
	requestKey []byte
	presented  bool
}

func newSigningControl(t *testing.T, lifetime time.Duration) *signingControl {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "agent CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour * 24 * 365), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	ca, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	return &signingControl{ca: ca, key: key, lifetime: lifetime}
}

// issue signs a certificate naming the test node for public.
func (c *signingControl) issue(t *testing.T, public any, notBefore, notAfter time.Time) string {
	t.Helper()
	identity, e := wire.NodeURI(wire.LocalScope(), certificateTestNode)
	if e != nil {
		t.Fatal(e)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	der, e := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: serial, NotBefore: notBefore, NotAfter: notAfter, URIs: []*url.URL{identity}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}, c.ca, public, c.key)
	if e != nil {
		t.Fatal(e)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: wire.CertificatePEM, Bytes: der}))
}

func (c *signingControl) ServeHTTP(t *testing.T, w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	reply := func(status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(value)
	}
	if c.refusal != "" {
		reply(c.refusalCode, map[string]string{"code": c.refusal})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/certificates/confirmations") {
		c.confirmations++
		reply(http.StatusOK, map[string]any{"data": wire.CertificateConfirmed{Confirmed: true}})
		return
	}
	var input wire.CertificateRenewal
	if e := json.NewDecoder(r.Body).Decode(&input); e != nil {
		t.Error(e)
		return
	}
	request, e := wire.ParseCertificateRequest(input.CertificateRequest)
	if e != nil {
		t.Error(e)
		return
	}
	digest, _ := wire.KeyDigest(request.PublicKey)
	c.renewals = append(c.renewals, seenRenewal{requestKey: digest, presented: r.TLS != nil && len(r.TLS.PeerCertificates) > 0})
	if c.failRenewal > 0 {
		c.failRenewal--
		reply(http.StatusServiceUnavailable, map[string]string{"code": "STORAGE_UNAVAILABLE"})
		return
	}
	now := time.Now()
	reply(http.StatusOK, map[string]any{"data": wire.IssuedCertificate{Certificate: c.issue(t, request.PublicKey, now.Add(-time.Hour), now.Add(c.lifetime))}})
}

// certificateRig is an agent's certificate loop against a signing control plane.
type certificateRig struct {
	t       *testing.T
	control *signingControl
	dir     string
	agent   certificateAgent
	now     time.Time
}

func newCertificateRig(t *testing.T, lifetime time.Duration) *certificateRig {
	t.Helper()
	control := newSigningControl(t, lifetime)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { control.ServeHTTP(t, w, r) }))
	server.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
	server.StartTLS()
	t.Cleanup(server.Close)
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	caFile := filepath.Join(dir, "control-ca.pem")
	if e = os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); e != nil {
		t.Fatal(e)
	}
	connection, e := client.New(server.URL, caFile)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(connection.Close)
	connection.Credential = strings.Repeat("A", 43)
	rig := &certificateRig{t: t, control: control, dir: dir, now: time.Now()}
	rig.agent = certificateAgent{directory: dir, node: certificateTestNode, connection: connection, now: func() time.Time { return rig.now }}
	return rig
}

func (r *certificateRig) public(key string) any {
	r.t.Helper()
	request, e := wire.NewCertificateRequest(key)
	if e != nil {
		r.t.Fatal(e)
	}
	parsed, e := wire.ParseCertificateRequest(request)
	if e != nil {
		r.t.Fatal(e)
	}
	return parsed.PublicKey
}

// enrolled saves a certificate as enrollment would, valid from notBefore to notAfter.
func (r *certificateRig) enrolled(notBefore, notAfter time.Time) state.Certificate {
	r.t.Helper()
	key, e := wire.NewCertificateKey()
	if e != nil {
		r.t.Fatal(e)
	}
	saved := state.Certificate{PrivateKey: key, Certificate: r.control.issue(r.t, r.public(key), notBefore, notAfter), Confirmed: true, RenewAt: notBefore.Add(notAfter.Sub(notBefore) * 7 / 10)}
	if e := state.SaveCertificate(r.dir, saved); e != nil {
		r.t.Fatal(e)
	}
	if e := r.agent.connection.UseCertificate(saved.Certificate, saved.PrivateKey); e != nil {
		r.t.Fatal(e)
	}
	return saved
}

func (r *certificateRig) step() (time.Duration, error) {
	r.t.Helper()
	bounded, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.agent.step(bounded)
}

func (r *certificateRig) current() state.Certificate {
	r.t.Helper()
	saved, found, e := state.LoadCertificate(r.dir)
	if e != nil || !found {
		r.t.Fatal(found, e)
	}
	return saved
}

func keyDigestOf(t *testing.T, key string) []byte {
	t.Helper()
	request, e := wire.NewCertificateRequest(key)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := wire.ParseCertificateRequest(request)
	if e != nil {
		t.Fatal(e)
	}
	digest, _ := wire.KeyDigest(parsed.PublicKey)
	return digest
}

func TestRotationReplacesTheKeyAndConfirmsFromTheNewCertificate(t *testing.T) {
	r := newCertificateRig(t, 720*time.Hour)
	start := time.Now().Add(-time.Minute)
	old := r.enrolled(start, start.Add(720*time.Hour))
	if wait, e := r.step(); e != nil || wait <= 0 {
		t.Fatal("not due yet", wait, e)
	}
	if len(r.control.renewals) != 0 {
		t.Fatal("renewed too early")
	}
	r.now = start.Add(600 * time.Hour)
	if _, e := r.step(); e != nil {
		t.Fatal(e)
	}
	renewed := r.current()
	if renewed.PrivateKey == old.PrivateKey || renewed.Certificate == old.Certificate || renewed.Confirmed || renewed.PendingKey != "" {
		t.Fatalf("after renewal %+v", renewed)
	}
	if !r.control.renewals[0].presented {
		t.Fatal("the renewal must present the current certificate")
	}
	if string(r.control.renewals[0].requestKey) != string(keyDigestOf(t, renewed.PrivateKey)) {
		t.Fatal("the certificate was asked for another key")
	}
	if _, e := r.step(); e != nil {
		t.Fatal(e)
	}
	if r.control.confirmations != 1 || !r.current().Confirmed {
		t.Fatal("the rotation was not confirmed")
	}
	r.now = time.Now().Add(time.Minute)
	if wait, e := r.step(); e != nil || wait <= 0 {
		t.Fatal("the next rotation is far away", wait, e)
	}
}

func TestLostRenewalReplyRetriesWithTheSameKey(t *testing.T) {
	r := newCertificateRig(t, 720*time.Hour)
	start := time.Now().Add(-time.Minute)
	r.enrolled(start, start.Add(720*time.Hour))
	r.now = start.Add(600 * time.Hour)
	r.control.failRenewal = 1
	if _, e := r.step(); e == nil {
		t.Fatal("the reply was lost")
	}
	pending := r.current().PendingKey
	if pending == "" {
		t.Fatal("the key must be saved before it is asked about")
	}
	if _, e := r.step(); e != nil {
		t.Fatal(e)
	}
	if string(r.control.renewals[0].requestKey) != string(r.control.renewals[1].requestKey) {
		t.Fatal("a retry must ask for the same key")
	}
	if r.current().PrivateKey != pending {
		t.Fatal("the pending key was not adopted")
	}
}

func TestExpiredCertificateRecoversWithItsOwnKeyAndNoCertificate(t *testing.T) {
	r := newCertificateRig(t, 720*time.Hour)
	start := time.Now().Add(-2 * time.Hour)
	old := r.enrolled(start, time.Now().Add(-time.Minute))
	stale, e := wire.NewCertificateKey()
	if e != nil {
		t.Fatal(e)
	}
	waiting := old
	waiting.PendingKey = stale
	if e := state.SaveCertificate(r.dir, waiting); e != nil {
		t.Fatal(e)
	}
	r.now = time.Now()
	if _, e := r.step(); e != nil {
		t.Fatal(e)
	}
	seen := r.control.renewals[0]
	if seen.presented {
		t.Fatal("an expired certificate must not be presented")
	}
	if string(seen.requestKey) != string(keyDigestOf(t, old.PrivateKey)) {
		t.Fatal("recovery must use the key control has pinned, not a pending one")
	}
	if r.current().PendingKey != "" {
		t.Fatal("recovery leaves no pending key")
	}
}

func TestNodeWithoutACertificateTakesItsFirst(t *testing.T) {
	r := newCertificateRig(t, 720*time.Hour)
	if _, e := r.step(); e != nil {
		t.Fatal(e)
	}
	saved := r.current()
	if saved.Certificate == "" || r.control.renewals[0].presented {
		t.Fatalf("%+v", saved)
	}
	if _, e := r.step(); e != nil || r.control.confirmations != 1 {
		t.Fatal(e, r.control.confirmations)
	}
}

func TestRevocationStopsRotationAndKeepsTheCertificate(t *testing.T) {
	for code, status := range map[string]int{"AGENT_UNAUTHORIZED": 401, "AGENT_IDENTITY_UNAVAILABLE": 503} {
		r := newCertificateRig(t, 720*time.Hour)
		start := time.Now().Add(-time.Minute)
		r.enrolled(start, start.Add(720*time.Hour))
		r.now = start.Add(600 * time.Hour)
		r.control.refusal, r.control.refusalCode = code, status
		if _, e := r.step(); !errors.Is(e, errStopRotating) {
			t.Fatal(code, e)
		}
	}
}

func TestACertificateRefusalIsRetriedNotFatal(t *testing.T) {
	r := newCertificateRig(t, 720*time.Hour)
	start := time.Now().Add(-time.Minute)
	r.enrolled(start, start.Add(720*time.Hour))
	r.now = start.Add(600 * time.Hour)
	r.control.refusal, r.control.refusalCode = "AGENT_CERTIFICATE_SUPERSEDED", 401
	if _, e := r.step(); e == nil || errors.Is(e, errStopRotating) {
		t.Fatal(e)
	}
}

func TestEnrollmentCertificateMustMatchTheReturnedScope(t *testing.T) {
	r := newCertificateRig(t, 24*time.Hour)
	saved := r.enrolled(r.now.Add(-time.Hour), r.now.Add(23*time.Hour))
	if _, err := checkEnrollmentCertificate(saved.Certificate, saved.PrivateKey, certificateTestNode, wire.LocalScope()); err != nil {
		t.Fatal("matching enrollment certificate was refused", err)
	}
	foreign := wire.Scope{SiteID: wire.DefaultSiteID, CellID: "other-cell"}
	if _, err := checkEnrollmentCertificate(saved.Certificate, saved.PrivateKey, certificateTestNode, foreign); err == nil {
		t.Fatal("certificate for a different cell was accepted")
	}
}

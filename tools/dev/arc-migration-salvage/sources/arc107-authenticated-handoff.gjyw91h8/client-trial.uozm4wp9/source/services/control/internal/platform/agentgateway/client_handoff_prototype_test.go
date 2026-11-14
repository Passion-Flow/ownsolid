package agentgateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	bridge "github.com/Passion-Flow/ownsolid/services/agent/prototypebridge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

type handoffRetirements struct {
	slog.Handler
	mu      sync.Mutex
	records []map[string]any
}

func (h *handoffRetirements) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "http_connection_retired" {
		fields := map[string]any{}
		r.Attrs(func(a slog.Attr) bool { fields[a.Key] = a.Value.Any(); return true })
		h.mu.Lock()
		h.records = append(h.records, fields)
		h.mu.Unlock()
	}
	return h.Handler.Handle(ctx, r)
}

type handoffBackend struct {
	fakeBackend
	credential            string
	ids                   map[string]time.Time
	claimed, acknowledged bool
	holdAcrossAge         bool
	httpsAcks             int
}

func (b *handoffBackend) Admit(ctx context.Context, address, node, credential string, call wire.Call) (func(), error) {
	if credential != b.credential {
		return nil, errors.New("AGENT_UNAUTHORIZED")
	}
	return b.fakeBackend.Admit(ctx, address, node, credential, call)
}
func (b *handoffBackend) CheckPeer(_ context.Context, node string, p agentidentity.Peer) error {
	if !p.Presented || p.Malformed || p.Node != node || p.Scope != wire.LocalScope() {
		return errors.New("AGENT_CERTIFICATE_REQUIRED")
	}
	return nil
}
func (b *handoffBackend) Heartbeat(ctx context.Context, node, credential string, h wire.Handshake) (wire.Disposition, error) {
	b.mu.Lock()
	b.ids[httpserver.ConnectionID(ctx)] = httpserver.ConnectionDrainAt(ctx)
	b.mu.Unlock()
	return b.fakeBackend.Heartbeat(ctx, node, credential, h)
}
func (b *handoffBackend) Claim(ctx context.Context, node, credential, request string, input wire.ClaimRequest) (wire.Claim, error) {
	b.mu.Lock()
	b.cursors = append(b.cursors, input.AcknowledgedSequence)
	acknowledged := b.acknowledged
	hold := b.holdAcrossAge
	b.mu.Unlock()
	if acknowledged {
		return wire.Claim{}, nil
	}
	if hold {
		timer := time.NewTimer(time.Until(httpserver.ConnectionDrainAt(ctx)) + 75*time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return wire.Claim{}, ctx.Err()
		case <-timer.C:
		}
	}
	b.mu.Lock()
	b.claimed = true
	b.mu.Unlock()
	return wire.Claim{Lease: b.lease}, nil
}
func (b *handoffBackend) Acknowledge(ctx context.Context, node, credential, request string, ack wire.TaskAcknowledgement) (wire.RenewedLease, error) {
	b.mu.Lock()
	if !b.claimed || b.acknowledged || ack.TaskID != b.lease.TaskID || ack.Sequence != b.lease.Sequence || ack.LeaseToken != b.lease.LeaseToken {
		b.mu.Unlock()
		return wire.RenewedLease{}, errors.New("VALIDATION_FAILED")
	}
	b.acknowledged = true
	b.mu.Unlock()
	return b.fakeBackend.Acknowledge(ctx, node, credential, request, ack)
}

type handoffFixture struct {
	backend                      *handoffBackend
	gateway                      *Gateway
	client                       *bridge.Client
	session                      *bridge.Session
	origin, ca, certificate, key string
	logs                         *handoffRetirements
}
type hintFilteringWriter struct {
	http.ResponseWriter
	filter func(http.Header)
}

func (w hintFilteringWriter) WriteHeader(status int) {
	w.filter(w.Header())
	w.ResponseWriter.WriteHeader(status)
}
func (w hintFilteringWriter) Write(p []byte) (int, error) {
	w.filter(w.Header())
	return w.ResponseWriter.Write(p)
}
func (w hintFilteringWriter) Flush()                      { w.filter(w.Header()); w.ResponseWriter.(http.Flusher).Flush() }
func (w hintFilteringWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func newHandoffFixture(t *testing.T, sessions int, filter func(http.Header)) *handoffFixture {
	t.Helper()
	directory := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "owned handoff fixture CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	serverTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	serverPair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}), keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := agentidentity.NewAuthority(caPEM, keyPEM, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	nodeKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, nodeKey)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		t.Fatal(err)
	}
	certificate, _, err := authority.Issue(csr, wire.LocalScope(), testNode)
	if err != nil {
		t.Fatal(err)
	}
	nodeDER, err := x509.MarshalPKCS8PrivateKey(nodeKey)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("owned CA was not parsed")
	}
	f := &handoffFixture{ca: write("ca.pem", caPEM), certificate: write("node.pem", []byte(certificate)), key: write("node.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: nodeDER}))}
	f.backend = &handoffBackend{credential: rand.Text(), ids: make(map[string]time.Time)}
	f.backend.pacing = &wire.Pacing{HeartbeatSeconds: 5, JitterPercent: 0, ReconnectBaseSeconds: 1, ReconnectMaxSeconds: 2}
	leaseBytes := make([]byte, 32)
	if _, err = rand.Read(leaseBytes); err != nil {
		t.Fatal(err)
	}
	f.backend.lease = &wire.Lease{TaskID: testNode, Operation: wire.CollectFacts, LeaseToken: base64.RawURLEncoding.EncodeToString(leaseBytes), Attempt: 1, Sequence: 7}
	f.gateway, err = New(f.backend, Policy{MaxSessions: sessions, ExchangeSeconds: 3}, problem)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	f.origin = "https://localhost:" + port
	previous := slog.Default()
	previousOutput, previousFlags := log.Writer(), log.Flags()
	f.logs = &handoffRetirements{Handler: slog.NewTextHandler(os.Stderr, nil)}
	slog.SetDefault(slog.New(f.logs))
	t.Cleanup(func() { slog.SetDefault(previous); log.SetOutput(previousOutput); log.SetFlags(previousFlags) })
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Owns(r) {
			if filter != nil {
				w = hintFilteringWriter{w, filter}
			}
			f.gateway.ServeHTTP(w, r, "127.0.0.1")
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/"+wire.TaskAcknowledgementsPath) {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+f.backend.credential || f.backend.CheckPeer(r.Context(), testNode, agentidentity.PeerOf(r.TLS)) != nil {
			http.Error(w, "", 401)
			return
		}
		var request wire.TaskAcknowledgementRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "", 400)
			return
		}
		ack := wire.TaskAcknowledgement{TaskID: testNode, Sequence: request.Sequence, LeaseToken: request.LeaseToken}
		result, err := f.backend.Acknowledge(r.Context(), testNode, f.backend.credential, "owned HTTPS fallback", ack)
		if err != nil {
			http.Error(w, "", 400)
			return
		}
		f.backend.mu.Lock()
		f.backend.httpsAcks++
		f.backend.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- httpserver.ServeListener(ctx, listener, &tls.Config{Certificates: []tls.Certificate{serverPair}, ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: roots, NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS12}, handler, config.DefaultHTTPPolicy(), httpserver.ConnectionAge{Maximum: 2 * time.Second, Grace: time.Second})
	}()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("owned handoff listener failed to join")
		}
	})
	f.client, err = bridge.New(f.origin, f.ca)
	if err != nil {
		t.Fatal(err)
	}
	f.client.Credential = f.backend.credential
	f.client.SessionAddresses = []string{listener.Addr().String()}
	if err = f.client.UseCertificate(certificate, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: nodeDER}))); err != nil {
		t.Fatal(err)
	}
	f.session = f.client.Session(testNode)
	t.Cleanup(func() { f.session.Close(); f.client.Close() })
	return f
}
func heartbeatHandoff(t *testing.T, s *bridge.Session) {
	t.Helper()
	out, err := s.Heartbeat(t.Context(), wire.Handshake{ProtocolVersion: wire.Version})
	if err != nil || out.Status != "active" {
		t.Fatalf("healthy heartbeat failed: %+v %v", out, err)
	}
}

func TestPrototypeActualAgentHandoffPreservesNaturalRetirementsAndCursor(t *testing.T) {
	f := newHandoffFixture(t, 4, nil)
	heartbeatHandoff(t, f.session)
	claim, err := f.session.Claim(t.Context(), 0, 0)
	if err != nil || claim.Lease == nil {
		t.Fatalf("initial claim: %+v %v", claim, err)
	}
	if _, err = f.session.Acknowledge(t.Context(), *claim.Lease); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		heartbeatHandoff(t, f.session)
		if next, err := f.session.Claim(t.Context(), 0, claim.Lease.Sequence); err != nil || next.Lease != nil {
			t.Fatalf("acknowledged claim was repeated: %+v %v", next, err)
		}
		time.Sleep(60 * time.Millisecond)
	}
	f.logs.mu.Lock()
	records := append([]map[string]any(nil), f.logs.records...)
	f.logs.mu.Unlock()
	natural := 0
	for _, record := range records {
		if record["reason"] != "connection_age" {
			continue
		}
		natural++
		scheduled := record["scheduled_age_seconds"].(float64)
		started := record["drain_started_age_seconds"].(float64)
		elapsed := record["age_seconds"].(float64)
		if record["forced"] != false || scheduled < 1.8 || scheduled > 2 || started < scheduled || elapsed < started {
			t.Fatalf("natural retirement was changed: %+v", record)
		}
	}
	if natural < 3 {
		t.Fatalf("only %d original transports naturally retired", natural)
	}
	f.backend.mu.Lock()
	defer f.backend.mu.Unlock()
	if len(f.backend.ids) < 4 || len(f.backend.acks) != 1 || f.backend.httpsAcks != 0 {
		t.Fatalf("transport/ack outcomes: ids=%d ack=%d HTTPS=%d", len(f.backend.ids), len(f.backend.acks), f.backend.httpsAcks)
	}
	for i, cursor := range f.backend.cursors {
		if i > 0 && cursor != claim.Lease.Sequence {
			t.Fatalf("cursor changed after handoff: %v", f.backend.cursors)
		}
	}
	t.Logf("authenticated_transports=%d natural_retirements=%d healthy_heartbeat_or_claim_errors=0 acknowledgements=1 attempt=1 cursor=7", len(f.backend.ids), natural)
}

func TestPrototypeFullCapacityKeepsOriginalAuthenticatedSession(t *testing.T) {
	f := newHandoffFixture(t, 1, nil)
	heartbeatHandoff(t, f.session)
	timer := time.NewTimer(1150 * time.Millisecond)
	<-timer.C
	heartbeatHandoff(t, f.session)
	heartbeatHandoff(t, f.session)
	f.backend.mu.Lock()
	count := len(f.backend.ids)
	f.backend.mu.Unlock()
	if count != 1 || f.gateway.Sessions() != 1 {
		t.Fatalf("capacity refusal replaced the old session: ids=%d slots=%d", count, f.gateway.Sessions())
	}
}

func TestPrototypeInvalidAgeHintsKeepLegacySession(t *testing.T) {
	cases := map[string]func(http.Header){
		"missing": func(h http.Header) { h.Del("Ownsolid-Connection-Drain-Ns"); h.Del("Ownsolid-Connection-Close-Ns") },
		"duplicate": func(h http.Header) {
			if len(h.Values("Ownsolid-Connection-Drain-Ns")) > 0 {
				h.Add("Ownsolid-Connection-Drain-Ns", "1")
			}
		},
		"overflow":           func(h http.Header) { h.Set("Ownsolid-Connection-Drain-Ns", "9999999999999999999999999999999") },
		"negative":           func(h http.Header) { h.Set("Ownsolid-Connection-Drain-Ns", "-1") },
		"noncanonical":       func(h http.Header) { h.Set("Ownsolid-Connection-Drain-Ns", "01") },
		"close_before_drain": func(h http.Header) { h.Set("Ownsolid-Connection-Close-Ns", "1") },
	}
	for name, filter := range cases {
		t.Run(name, func(t *testing.T) {
			f := newHandoffFixture(t, 4, filter)
			heartbeatHandoff(t, f.session)
			timer := time.NewTimer(1150 * time.Millisecond)
			<-timer.C
			heartbeatHandoff(t, f.session)
			f.backend.mu.Lock()
			count := len(f.backend.ids)
			f.backend.mu.Unlock()
			if count != 1 {
				t.Fatalf("untrusted age hint replaced the legacy session: %d", count)
			}
		})
	}
}

func TestPrototypeUntrustedCandidateCannotReplaceAuthenticatedSession(t *testing.T) {
	f := newHandoffFixture(t, 4, nil)
	heartbeatHandoff(t, f.session)
	for _, presentCertificate := range []bool{false, true} {
		candidate, err := bridge.New(f.origin, f.ca)
		if err != nil {
			t.Fatal(err)
		}
		if presentCertificate {
			if err = candidate.UseCertificate(readFixturePEM(t, f.certificate), readFixturePEM(t, f.key)); err != nil {
				t.Fatal(err)
			}
			candidate.Credential = rand.Text()
		} else {
			candidate.Credential = f.backend.credential
		}
		session := candidate.Session(testNode)
		if _, err = session.Heartbeat(t.Context(), wire.Handshake{ProtocolVersion: wire.Version}); err == nil {
			t.Fatal("untrusted candidate authenticated")
		}
		session.Close()
		candidate.Close()
		heartbeatHandoff(t, f.session)
	}
	f.backend.mu.Lock()
	count := len(f.backend.ids)
	f.backend.mu.Unlock()
	if count != 1 {
		t.Fatalf("failed candidate changed physical session: %d", count)
	}
}

func TestPrototypeAdmittedLeaseAcrossAgeRetainsOneAcknowledgement(t *testing.T) {
	f := newHandoffFixture(t, 4, nil)
	f.backend.holdAcrossAge = true
	heartbeatHandoff(t, f.session)
	claim, err := f.session.Claim(t.Context(), 1, 0)
	if err != nil || claim.Lease == nil || claim.Lease.Sequence != 7 || claim.Lease.Attempt != 1 {
		t.Fatalf("admitted lease across age: %+v %v", claim, err)
	}
	if _, err = f.session.Acknowledge(t.Context(), *claim.Lease); err != nil {
		t.Fatalf("single admitted lease could not be acknowledged: %v", err)
	}
	heartbeatHandoff(t, f.session)
	if next, err := f.session.Claim(t.Context(), 0, 7); err != nil || next.Lease != nil {
		t.Fatalf("same lease was claimed again after age: %+v %v", next, err)
	}
	f.backend.mu.Lock()
	defer f.backend.mu.Unlock()
	if len(f.backend.acks) != 1 || f.backend.httpsAcks != 1 || len(f.backend.cursors) != 2 || f.backend.cursors[1] != 7 {
		t.Fatalf("age changed ACK/cursor ownership: %+v HTTPS=%d cursor=%v", f.backend.acks, f.backend.httpsAcks, f.backend.cursors)
	}
}

func readFixturePEM(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

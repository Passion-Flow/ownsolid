package client

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTLSVerificationRedirectAndCredentialBoundary(t *testing.T) {
	var requests atomic.Int32
	secret := "synthetic credential"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("credential header missing")
		}
		if r.URL.Path == "/agent/v1/redirect" {
			http.Redirect(w, r, "/agent/v1/stolen", 302)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"status":"active"}}`))
	}))
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	untrusted, e := New(server.URL, "")
	if e != nil {
		t.Fatal(e)
	}
	defer untrusted.Close()
	if e = untrusted.Post(context.Background(), "/agent/v1/test", struct{}{}, nil); e == nil {
		t.Fatal("untrusted CA accepted")
	}
	if requests.Load() != 0 {
		t.Fatal("credentials reached untrusted server")
	}
	connection, e := New(server.URL, ca)
	if e != nil {
		t.Fatal(e)
	}
	defer connection.Close()
	connection.Credential = secret
	var out struct {
		Status string `json:"status"`
	}
	if e = connection.Post(context.Background(), "/agent/v1/test", struct{}{}, &out); e != nil || out.Status != "active" {
		t.Fatal("verified TLS failed", e)
	}
	if e = connection.Post(context.Background(), "/agent/v1/redirect", struct{}{}, nil); e == nil {
		t.Fatal("redirect followed")
	}
	if requests.Load() != 2 {
		t.Fatal("credential leaked through redirect")
	}
	if e = connection.Post(context.Background(), "/api/v1/me", struct{}{}, nil); e == nil {
		t.Fatal("human route accepted")
	}
	for _, origin := range []string{"http://control.invalid", "https://user:password@control.invalid", "https://control.invalid/path", "https://control.invalid?secret=1", "https://control.invalid#secret"} {
		if _, e = New(origin, ca); e == nil {
			t.Fatal("unsafe origin accepted")
		}
	}
}

func certificateFor(t *testing.T, notAfter time.Time) (string, string) {
	t.Helper()
	key, e := wire.NewCertificateKey()
	if e != nil {
		t.Fatal(e)
	}
	request, e := wire.NewCertificateRequest(key)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := wire.ParseCertificateRequest(request)
	if e != nil {
		t.Fatal(e)
	}
	identity, e := wire.NodeURI(wire.LocalScope(), "11111111-2222-4333-8444-555555555555")
	if e != nil {
		t.Fatal(e)
	}
	ca, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, URIs: []*url.URL{identity}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, parsed.PublicKey, ca)
	if e != nil {
		t.Fatal(e)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: wire.CertificatePEM, Bytes: der})), key
}

func TestACertificateNearItsEndIsNotPresented(t *testing.T) {
	c, e := New("https://control.test", "")
	if e != nil {
		t.Fatal(e)
	}
	if got, e := c.presented(nil); e != nil || len(got.Certificate) != 0 {
		t.Fatal("no certificate yet", got, e)
	}
	certificate, key := certificateFor(t, time.Now().Add(time.Hour))
	if e := c.UseCertificate(certificate, key); e != nil {
		t.Fatal(e)
	}
	if got, e := c.presented(nil); e != nil || len(got.Certificate) != 1 {
		t.Fatal("a valid certificate is presented", got, e)
	}
	certificate, key = certificateFor(t, time.Now().Add(wire.ExpirySafety/2))
	if e := c.UseCertificate(certificate, key); e != nil {
		t.Fatal(e)
	}
	if got, e := c.presented(nil); e != nil || len(got.Certificate) != 0 {
		t.Fatal("a certificate about to expire is held back", got, e)
	}
	if e := c.UseCertificate("nonsense", key); e == nil {
		t.Fatal("nonsense was accepted")
	}
}

func TestCertificateRefusalsAreRetriableAndOthersEndTheAgent(t *testing.T) {
	for _, code := range []string{"AGENT_CERTIFICATE_REQUIRED", "AGENT_CERTIFICATE_EXPIRED", "AGENT_CERTIFICATE_SUPERSEDED"} {
		e := &Problem{Code: code, Status: 401}
		if !CertificateRefused(e) || !Interrupted(e) {
			t.Errorf("%s must be retriable", code)
		}
	}
	revoked := &Problem{Code: "AGENT_UNAUTHORIZED", Status: 401}
	if CertificateRefused(revoked) || Interrupted(revoked) {
		t.Error("a revoked node must stop")
	}
}

func TestStreamErrorsKeepCertificateRefusals(t *testing.T) {
	var p *Problem
	if e := streamError(status.Error(codes.Unauthenticated, "AGENT_CERTIFICATE_SUPERSEDED")); !errors.As(e, &p) || p.Code != "AGENT_CERTIFICATE_SUPERSEDED" {
		t.Fatal(e)
	}
	if e := streamError(status.Error(codes.Unauthenticated, "something else")); !errors.As(e, &p) || p.Code != "AGENT_UNAUTHORIZED" {
		t.Fatal(e)
	}
}

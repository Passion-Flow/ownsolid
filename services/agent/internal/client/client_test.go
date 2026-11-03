package client

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
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

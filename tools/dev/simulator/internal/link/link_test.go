package link

import (
	"crypto/tls"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// server answers every request with handle, over TLS for the origin example.com.
func server(t *testing.T, handle http.HandlerFunc) *Dialer {
	t.Helper()
	s := httptest.NewTLSServer(handle)
	t.Cleanup(s.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0o600); e != nil {
		t.Fatal(e)
	}
	d, e := NewDialer("https://example.com", ca, s.Listener.Addr().String(), 2, 2*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(d.Close)
	return d
}

func TestPostSendsTheCredentialAndDecodesTheData(t *testing.T) {
	d := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "example.com" || r.Header.Get("Authorization") != "Bearer secret" || r.URL.Path != "/agent/v1/x" {
			http.Error(w, "", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"data":{"state":"succeeded"}}`))
	})
	var out struct {
		State string `json:"state"`
	}
	if e := d.Post(t.Context(), "/agent/v1/x", "secret", map[string]int{}, &out); e != nil || out.State != "succeeded" {
		t.Fatalf("%v %+v", e, out)
	}
}

func TestPostKeepsTheCodeAndTheBoundedHint(t *testing.T) {
	d := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "99999")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"code":"RATE_LIMITED"}`))
	})
	e := d.Post(t.Context(), "/agent/v1/x", "", nil, nil)
	hint, ok := RetryHint(e)
	if Code(e) != "RATE_LIMITED" || !ok || hint != 900*time.Second || Revoked(e) {
		t.Fatalf("%v %s %v", e, hint, ok)
	}
}

func TestPostNamesAnAnswerWithoutACode(t *testing.T) {
	d := server(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "gateway", http.StatusBadGateway) })
	if e := d.Post(t.Context(), "/agent/v1/x", "", nil, nil); Code(e) != "HTTP_502" {
		t.Fatalf("%v", e)
	}
}

func TestPostRefusesDataOutsideTheContract(t *testing.T) {
	d := server(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"data":{"state":"x","extra":1}}`)) })
	var out struct {
		State string `json:"state"`
	}
	if e := d.Post(t.Context(), "/agent/v1/x", "", nil, &out); !errors.Is(e, ErrInvalid) {
		t.Fatalf("%v", e)
	}
}

func TestUnreachableControl(t *testing.T) {
	d, e := NewDialer("https://example.com", "", "127.0.0.1:1", 1, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if e := d.Post(t.Context(), "/agent/v1/x", "", nil, nil); !errors.Is(e, ErrUnreachable) {
		t.Fatalf("%v", e)
	}
}

func TestRevokedIsA401(t *testing.T) {
	if !Revoked(&Problem{Code: "AGENT_UNAUTHORIZED", Status: http.StatusUnauthorized}) || Revoked(ErrUnreachable) {
		t.Fatal("revocation misread")
	}
}

func TestForAgentHasItsOwnIdentityAndConnections(t *testing.T) {
	d, e := NewDialer("https://control.test", "", "", 4, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	certificate := tls.Certificate{Certificate: [][]byte{{1}}}
	own := d.ForAgent(certificate)
	defer own.Close()
	if len(own.tls.Certificates) != 1 || len(d.tls.Certificates) != 0 {
		t.Fatal("the certificate must stay with the agent's dialer")
	}
	if own.https == d.https || own.poll == d.poll {
		t.Fatal("a certificate's connections must not be shared between agents")
	}
	transport := own.https.Transport.(*http.Transport)
	if len(transport.TLSClientConfig.Certificates) != 1 || transport.MaxConnsPerHost != agentConnections {
		t.Fatal("the short calls must present the certificate on a pool of the agent's own")
	}
	poll := own.poll.Transport.(*http.Transport)
	if len(poll.TLSClientConfig.Certificates) != 1 {
		t.Fatal("long polls must present the certificate")
	}
}

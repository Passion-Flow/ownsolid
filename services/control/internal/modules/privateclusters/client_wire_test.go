package privateclusters

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests run real HTTPS transport with synthetic remote bytes. They do not
// bypass newClient in production or establish signed-image, SQL or cluster proof.
func wireClient(t *testing.T, handler http.HandlerFunc) (*client, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	policy := Defaults().Policy
	policy.RequestSeconds = 1
	policy.MaximumBodyBytes = 4096
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	c := &client{profile: Profile{ServerOrigin: server.URL}, policy: policy, transport: transport, http: &http.Client{Transport: bearerTransport{transport, "synthetic-scope-token"}, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	t.Cleanup(c.close)
	return c, server
}
func TestPrivateClusterHTTPSRefusalAndUnknownWrites(t *testing.T) {
	var mode atomic.Int32
	release := make(chan struct{})
	defer close(release) // Release the synthetic peer before httptest cleanup.
	c, _ := wireClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.Header.Get("Authorization") != "Bearer synthetic-scope-token" {
			t.Error("real authenticated TLS missing")
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch mode.Load() {
		case 1:
			w.WriteHeader(403)
		case 2:
			w.Header().Set("Location", "https://elsewhere.invalid/private")
			w.WriteHeader(307)
		case 3:
			w.WriteHeader(503)
		case 4:
			w.Header().Set("Content-Length", "4097")
			w.Write([]byte(strings.Repeat("x", 4097)))
		case 5:
			w.Write([]byte(`{"accepted":true,"accepted":false}`))
		case 6:
			select {
			case <-r.Context().Done():
			case <-release:
			}
		default:
			json.NewEncoder(w).Encode(map[string]bool{"accepted": true})
		}
	})
	for _, v := range []struct {
		mode         int32
		method, code string
		unknown      bool
	}{
		{0, "GET", "", false}, {1, "POST", "PRIVATE_CLUSTER_ACCESS_FORBIDDEN", false},
		{2, "GET", "PRIVATE_CLUSTER_REDIRECT_REFUSED", false}, {2, "POST", "PRIVATE_CLUSTER_REDIRECT_REFUSED", true}, {3, "POST", "PRIVATE_CLUSTER_API_STATUS_REFUSED", true},
		{4, "POST", "PRIVATE_CLUSTER_RESPONSE_LIMIT", true}, {5, "POST", "PRIVATE_CLUSTER_RESPONSE_INVALID", true},
		{6, "POST", "PRIVATE_CLUSTER_REQUEST_INTERRUPTED", true},
	} {
		mode.Store(v.mode)
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		var out map[string]bool
		e := c.json(ctx, v.method, "/resource", map[string]bool{"fixed": true}, &out)
		cancel()
		if v.code == "" {
			if e != nil || !out["accepted"] {
				t.Fatal(e)
			}
			continue
		}
		var remote *apiError
		if e == nil || e.Error() != v.code || !errors.As(e, &remote) || remote.unknown != v.unknown {
			t.Fatalf("mode %d: %v", v.mode, e)
		}
	}
}
func TestPrivateClusterTLSVerificationIsRequired(t *testing.T) {
	c, _ := wireClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	})
	// An empty trust pool must not gain the fixture server certificate implicitly.
	bad := c.transport.Clone()
	bad.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	c.http.Transport = bearerTransport{bad, "synthetic-scope-token"}
	t.Cleanup(bad.CloseIdleConnections)
	if _, e := c.raw(t.Context(), "GET", "/resource", nil, false); e == nil || e.Error() != "PRIVATE_CLUSTER_TRANSPORT_OR_TLS_FAILED" {
		t.Fatalf("untrusted server accepted: %v", e)
	}
}

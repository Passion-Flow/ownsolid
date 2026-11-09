package alerting

import (
	"crypto/x509"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestMatrixRelayRequiresAnAcknowledgedEventAndRefusesRedirects(t *testing.T) {
	var escaped atomic.Bool
	destination := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		escaped.Store(true)
	}))
	defer destination.Close()
	for _, scenario := range []struct {
		name   string
		status int
		body   string
		want   deliveryOutcome
	}{
		{"accepted", http.StatusOK, `{"event_id":"$verified-event"}`, delivered},
		{"missing_receipt", http.StatusOK, "{}", matrixRetry},
		{"rate_limited", http.StatusTooManyRequests, "", matrixRetry},
		{"unauthorized", http.StatusForbidden, "", matrixRejected},
		{"redirect", http.StatusTemporaryRedirect, "", matrixRejected},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut || r.URL.Path != "/_matrix/client/v3/rooms/!fixture/send/m.room.message/transaction" {
					t.Errorf("unexpected Matrix request: %s %s", r.Method, r.URL.Path)
				}
				if scenario.status == http.StatusTemporaryRedirect {
					w.Header().Set("Location", destination.URL)
				}
				w.WriteHeader(scenario.status)
				io.WriteString(w, scenario.body)
			}))
			defer server.Close()
			origin, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			roots.AddCert(destination.Certificate())
			relay := newMatrixRelay(relaySettings{matrix: origin, roots: roots, concurrent: 1, timeout: time.Second}, slog.Default())
			defer relay.client.CloseIdleConnections()
			if result := relay.send(t.Context(), tenantRoute{room: "!fixture"}, matrixMessage{"m.text", "fixture"}, "transaction"); result != scenario.want {
				t.Errorf("delivery result %s, expected %s", result, scenario.want)
			}
		})
	}
	if escaped.Load() {
		t.Fatal("Matrix redirect escaped the configured origin")
	}
}

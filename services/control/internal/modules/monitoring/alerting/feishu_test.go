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

func TestFeishuSignatureVector(t *testing.T) {
	if got := signFeishu("1599360473", "demo"); got != "l1N0gAcBjdwBvGm1xMjOF0XSyaLRpR7tuO5dHfhAYc8=" {
		t.Fatalf("incorrect custom bot signature: %s", got)
	}
}

func TestFeishuRequiresCapabilityAndDurableReceipt(t *testing.T) {
	for _, test := range []struct {
		name, capability, receipt, body string
		status                          int
		want                            deliveryOutcome
		posts                           int32
	}{
		{"no_contract", "", "", "", 200, feishuRejected, 0},
		{"lost_receipt", relayCapabilityVersion, "", `{"code":0}`, 200, feishuRetry, 1},
		{"wrong_receipt", relayCapabilityVersion, `"different"`, `{"code":0}`, 200, feishuRetry, 1},
		{"rate_limit", relayCapabilityVersion, "", `{"code":11232}`, 200, feishuRetry, 1},
		{"signature_rejected", relayCapabilityVersion, "", `{"code":19021}`, 200, feishuRejected, 1},
		{"redirect", relayCapabilityVersion, "", "", 307, feishuRejected, 1},
		{"durable", relayCapabilityVersion, `"fixture-transaction"`, `{"code":0}`, 200, delivered, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != feishuHookPath+"fixture-hook-key" {
					t.Errorf("wrong private hook: %s", r.URL.Path)
				}
				if r.Method == http.MethodOptions {
					w.Header().Set(relayCapabilityHeader, test.capability)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				posts.Add(1)
				if r.Method != http.MethodPost || r.Header.Get(idempotencyKeyHeader) != `"fixture-transaction"` {
					t.Error("incorrect signed delivery request")
				}
				w.Header().Set(idempotencyKeyHeader, test.receipt)
				if test.status == http.StatusTemporaryRedirect {
					w.Header().Set("Location", "/redirected")
				}
				w.WriteHeader(test.status)
				io.WriteString(w, test.body)
			}))
			defer server.Close()
			origin, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			relay := newWebhookRelay(relaySettings{
				channel: feishuChannel, origin: origin, roots: roots, concurrent: 1, timeout: 2 * time.Second,
			}, slog.Default())
			defer relay.client.CloseIdleConnections()
			result := relay.send(t.Context(), tenantRoute{target: "fixture-hook-key", outbound: "synthetic-secret"}, "firing", "fixture-transaction")
			if result != test.want || posts.Load() != test.posts {
				t.Fatalf("unexpected delivery: %s after %d posts", result, posts.Load())
			}
		})
	}
}

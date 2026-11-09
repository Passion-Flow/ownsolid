package alerting

import (
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDingTalkSignatureVector(t *testing.T) {
	const want = "wbFgwVart1BHvB+cObYqm2Kj8IKHPUA5/4SmKBIXj2g="
	if got := signDingTalk("1700000000123", "public-signature-vector"); got != want {
		t.Fatalf("incorrect DingTalk signature: %q", got)
	}
}

func TestDingTalkRequiresCapabilityAndDurableReceipt(t *testing.T) {
	for _, test := range []struct {
		name, body         string
		capability, echoes int
		status, posts      int
		want               deliveryOutcome
	}{
		{"no_contract", "", 0, 0, 200, 0, dingTalkRejected},
		{"ambiguous_contract", "", 2, 0, 200, 0, dingTalkRejected},
		{"lost_receipt", `{"errcode":0}`, 1, 0, 200, 1, dingTalkRetry},
		{"ambiguous_receipt", `{"errcode":0}`, 1, 2, 200, 1, dingTalkRetry},
		{"duplicate_code", `{"errcode":0,"errcode":310000}`, 1, 1, 200, 1, dingTalkRetry},
		{"wrong_provider_receipt", `{"code":0}`, 1, 1, 200, 1, dingTalkRetry},
		{"busy", `{"errcode":-1}`, 1, 0, 200, 1, dingTalkRetry},
		{"rate_limit", `{"errcode":410100}`, 1, 0, 200, 1, dingTalkRetry},
		{"signature_rejected", `{"errcode":310000}`, 1, 0, 200, 1, dingTalkRejected},
		{"redirect", "", 1, 0, 307, 1, dingTalkRejected},
		{"durable", `{"errcode":0,"errmsg":"ok"}`, 1, 1, 200, 1, delivered},
	} {
		t.Run(test.name, func(t *testing.T) {
			var posts atomic.Int32
			token := strings.Repeat("x", 32)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/robot/send" {
					t.Error("incorrect destination path or redirect followed")
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if r.Method == http.MethodOptions {
					if r.URL.RawQuery != "" {
						t.Error("capability request carried query credentials")
					}
					for range test.capability {
						w.Header().Add(relayCapabilityHeader, relayCapabilityVersion)
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				posts.Add(1)
				query := r.URL.Query()
				if r.Method != http.MethodPost || len(query) != 3 || query.Get("access_token") != token ||
					len(query.Get("timestamp")) != 13 || query.Get("sign") == "" ||
					r.Header.Get(idempotencyKeyHeader) != `"fixture-transaction"` {
					t.Error("incorrect signed delivery request")
				}
				var fields map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&fields); err != nil || len(fields) != 2 ||
					string(fields["msgtype"]) != `"text"` || string(fields["text"]) != `{"content":"test firing"}` {
					t.Error("incorrect DingTalk text body")
				}
				for range test.echoes {
					w.Header().Add(idempotencyKeyHeader, `"fixture-transaction"`)
				}
				if test.status == http.StatusTemporaryRedirect {
					w.Header().Set("Location", "/redirected")
				}
				w.WriteHeader(test.status)
				io.WriteString(w, test.body)
			}))
			defer server.Close()
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			relay := newBotTestRelay(t, server.URL, dingTalkChannel, roots)
			outcome := relay.send(t.Context(), tenantRoute{target: token, outbound: "public-signature-vector"}, "test firing", "fixture-transaction")
			if outcome != test.want || int(posts.Load()) != test.posts {
				t.Fatalf("unexpected delivery: %s after %d posts", outcome, posts.Load())
			}
		})
	}
}

func TestDingTalkRejectsUntrustedTLS(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	relay := newBotTestRelay(t, server.URL, dingTalkChannel, x509.NewCertPool())
	if got := relay.send(t.Context(), tenantRoute{}, "test firing", "fixture-transaction"); got != dingTalkRetry || calls.Load() != 0 {
		t.Fatalf("untrusted TLS produced %s with %d HTTP requests", got, calls.Load())
	}
}

func newBotTestRelay(t *testing.T, target string, channel relayChannel, roots *x509.CertPool) *webhookRelay {
	t.Helper()
	origin, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	relay := newWebhookRelay(relaySettings{
		channel: channel, origin: origin, roots: roots, concurrent: 1, timeout: 2 * time.Second,
	}, slog.Default())
	t.Cleanup(relay.client.CloseIdleConnections)
	return relay
}

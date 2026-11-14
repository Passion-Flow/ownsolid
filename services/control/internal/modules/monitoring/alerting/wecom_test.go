package alerting

import (
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

func TestWeComSummaryUsesUTF8ByteLimit(t *testing.T) {
	exact := strings.Repeat("x", 2048)
	if got := weComSummary(exact); got != exact {
		t.Fatal("changed an allowed message")
	}
	withRunes := strings.Repeat("x", 2020) + strings.Repeat("\u754c", 20)
	if got := weComSummary(withRunes); !utf8.ValidString(got) || len(got) > 2048 ||
		!strings.HasSuffix(got, "\n[Alert details shortened]") || !strings.HasPrefix(withRunes, strings.SplitN(got, "\n", 2)[0]) {
		t.Fatal("incorrect UTF-8 clipping")
	}
	group := "ownsolid firing | tenant-a | 40 firing, 0 resolved"
	if got := weComSummary(group + "\n" + strings.Repeat("x", 2100)); got != group+"\n[Alert details shortened]" {
		t.Fatal("lost the group summary or kept an incomplete detail")
	}
}

func TestWeComPostsOneBoundedText(t *testing.T) {
	for _, test := range []struct {
		name, body string
		want       deliveryOutcome
	}{
		{"durable", `{"errcode":0}`, delivered},
		{"busy", `{"errcode":-1}`, weComRetry},
		{"rate_limit", `{"errcode":45009}`, weComRetry},
		{"invalid_key", `{"errcode":93000}`, weComRejected},
	} {
		t.Run(test.name, func(t *testing.T) {
			group := "ownsolid firing | tenant-a | 40 firing, 0 resolved"
			text := group + "\n" + strings.Repeat("x", 2100)
			token := strings.Repeat("x", 32)
			var posts atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/cgi-bin/webhook/send" {
					t.Error("incorrect WeCom path")
				}
				if r.Method == http.MethodOptions {
					if r.URL.RawQuery != "" {
						t.Error("capability request carried query credentials")
					}
					w.Header().Set(relayCapabilityHeader, relayCapabilityVersion)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				posts.Add(1)
				query := r.URL.Query()
				if r.Method != http.MethodPost || len(query) != 1 || query.Get("key") != token ||
					r.Header.Get(idempotencyKeyHeader) != `"fixture-transaction"` {
					t.Error("incorrect authorized WeCom request")
				}
				var fields struct {
					MsgType string `json:"msgtype"`
					Text    struct {
						Content string `json:"content"`
					} `json:"text"`
				}
				decoder := json.NewDecoder(r.Body)
				decoder.DisallowUnknownFields()
				if decoder.Decode(&fields) != nil || fields.MsgType != "text" ||
					fields.Text.Content != group+"\n[Alert details shortened]" {
					t.Error("incorrect bounded text payload or unexpected mentions")
				}
				w.Header().Set(idempotencyKeyHeader, `"fixture-transaction"`)
				io.WriteString(w, test.body)
			}))
			defer server.Close()
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			relay := newBotTestRelay(t, server.URL, weComChannel, roots)
			got := relay.send(t.Context(), tenantRoute{target: token}, text, "fixture-transaction")
			if got != test.want || posts.Load() != 1 {
				t.Fatalf("unexpected delivery %s after %d posts", got, posts.Load())
			}
		})
	}
}

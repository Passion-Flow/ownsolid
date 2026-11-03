package knowledge

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const explicitCompletionDouble = `{"id":"chatcmpl-explicit-double","object":"chat.completion","created":1,"model":"synthetic-alias","choices":[{"index":0,"message":{"role":"assistant","content":"Lisbon [S1]."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

func TestM12GatewayBoundaryExplicitTLSResponseDoublesPinsRedirectAndStrictOutput(t *testing.T) {
	var calls atomic.Int32
	type reply struct {
		body     string
		status   int
		encoding string
	}
	var state atomic.Value
	state.Store(reply{explicitCompletionDouble, 200, ""})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		current := state.Load().(reply)
		response, status, encoding := current.body, current.status, current.encoding
		if r.URL.Path != "/v1/chat/completions" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic-test-secret" {
			t.Error("request boundary")
		}
		w.Header().Set("Content-Type", "application/json")
		if encoding != "" {
			w.Header().Set("Content-Encoding", encoding)
		}
		if status == 302 {
			w.Header().Set("Location", "https://untrusted.example.invalid")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()
	certificate, e := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	ca := filepath.Join(t.TempDir(), "gateway-ca.pem")
	if os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0600) != nil {
		t.Fatal("CA file")
	}
	destination := config.KnowledgeGateway{Origin: server.URL, DialAddresses: []string{"127.0.0.1"}, CAFile: ca}
	s := &Service{Identity: &identity.Service{Config: config.Config{}}}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	out, e := s.completion(context.Background(), destination, "synthetic-alias", "synthetic-test-secret", "Where?", []Match{{CitationID: "S1", Text: "The launch city is Lisbon."}})
	if e != nil || out.Choices[0].Message.Content != "Lisbon [S1]." {
		t.Fatal("actual pinned TLS against explicit response double", e)
	}
	for _, bad := range []string{strings.Replace(explicitCompletionDouble, `"model":"synthetic-alias"`, `"model":"foreign-alias"`, 1), strings.Replace(explicitCompletionDouble, `"total_tokens":15`, `"total_tokens":16`, 1), strings.Replace(explicitCompletionDouble, `"finish_reason":"stop"`, `"finish_reason":"length"`, 1), strings.Replace(explicitCompletionDouble, `"role":"assistant"`, `"role":"assistant","tool_calls":[]`, 1), strings.Replace(explicitCompletionDouble, `"id":"chatcmpl-explicit-double"`, `"id":"x","id":"y"`, 1), strings.Repeat("x", s.policy().GatewayResponseBytes+1)} {
		state.Store(reply{bad, 200, ""})
		if _, e = s.completion(context.Background(), destination, "synthetic-alias", "synthetic-test-secret", "Where?", nil); e == nil || e.Error() != "KNOWLEDGE_GATEWAY_RESPONSE_INVALID" {
			t.Fatal("invalid explicit response accepted", e)
		}
	}
	state.Store(reply{explicitCompletionDouble, 302, ""})
	if _, e = s.completion(context.Background(), destination, "synthetic-alias", "synthetic-test-secret", "Where?", nil); e == nil {
		t.Fatal("redirect accepted")
	}
	state.Store(reply{explicitCompletionDouble, 200, "gzip"})
	if _, e = s.completion(context.Background(), destination, "synthetic-alias", "synthetic-test-secret", "Where?", nil); e == nil {
		t.Fatal("encoded response accepted")
	}
	state.Store(reply{explicitCompletionDouble, 200, ""})
	transport, e := gatewayTransport(destination, s.policy())
	if e != nil {
		t.Fatal(e)
	}
	defer transport.CloseIdleConnections()
	parsed, _ := url.Parse(server.URL)
	_, port, _ := net.SplitHostPort(parsed.Host)
	before := calls.Load()
	if _, e = transport.DialContext(context.Background(), "tcp", net.JoinHostPort("foreign.example.invalid", port)); e == nil {
		t.Fatal("arbitrary destination accepted")
	}
	if calls.Load() != before {
		t.Fatal("rejected target performed IO")
	}
	destination.CAFile = ""
	if _, e = s.completion(context.Background(), destination, "synthetic-alias", "synthetic-test-secret", "Where?", nil); e == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
}
func TestM12CitationsAndTotalUTF8ContextBound(t *testing.T) {
	matches := []Match{{CitationID: "S1", Text: "Caf\u00e9 😀 first"}, {CitationID: "S2", Text: "Second source."}, {CitationID: "S3", Text: "Unsent source."}}
	bounded := boundedContext(matches, 18)
	if len(bounded) != 2 || len(bounded[0].Text)+len(bounded[1].Text) > 18 || !bounded[1].ExcerptTruncated {
		t.Fatal("total context boundary")
	}
	out, e := citedSources("Answer [S2] and [S1], repeat [S2].", bounded)
	if e != nil || len(out) != 2 || out[0].CitationID != "S1" {
		t.Fatal("exact source references", e)
	}
	for _, answer := range []string{"No source.", "[S0]", "[S01]", "[S99999]", "[S1, S2]", "[S1] then [S3]", "[S1] and [S2", "[Source]"} {
		if _, e = citedSources(answer, bounded); e == nil {
			t.Fatal("missing/malformed/invented source accepted")
		}
	}
	var empty *Processor
	if empty.Available() || (&Processor{}).Available() {
		t.Fatal("uninitialized encoder reported ready")
	}
}

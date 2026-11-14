//go:build training_artifact_integration && model_lifecycle_admission_integration && model_lifecycle_output_acceptance_integration && model_lifecycle_gateway_integration

package training_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	distributionhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution/http"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func init() { nativeAliasGatewaySetup = actualLifecycleRustGateway }

// Real approved/derived files and owning deployment/cutover transactions are
// supplied by the caller. Only tokenizer/generation and physical reports are
// explicit synthetic upstream envelopes; this does not qualify CUDA/vLLM.
func actualLifecycleRustGateway(t *testing.T, g preparationScenario, s *distribution.Service) func(*nativeAliasCutover) func(*testing.T, string) {
	t.Helper()
	root := snapshotTestDirectory(t)
	_, caFile, certFile, keyFile := smtpsink.Certificate(t, root)
	pair, e := tls.LoadX509KeyPair(certFile, keyFile)
	if e != nil {
		t.Fatal(e)
	}
	control := httptest.NewUnstartedServer(nil)
	control.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	s.Identity.Config.Server.AdminOrigin = "https://" + control.Listener.Addr().String()
	h := identityhttp.New(s.Identity)
	h.Distribution = &distributionhttp.Handler{Service: s}
	if e = h.InitializationError(); e != nil {
		t.Fatal(e)
	}
	control.Config.Handler = h
	control.StartTLS()
	t.Cleanup(control.Close)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("upstream received credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"object": "model", "id": agent.InferenceModelName}}})
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["model"] != agent.InferenceModelName {
			w.WriteHeader(400)
			return
		}
		if r.URL.Path == "/tokenize" {
			json.NewEncoder(w).Encode(map[string]any{"count": 7, "tokens": []int{1, 2, 3, 4, 5, 6, 7}, "max_model_len": 2048})
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(404)
			return
		}
		usage := map[string]any{"prompt_tokens": 7, "completion_tokens": 2, "total_tokens": 9}
		if body["stream"] == false {
			json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl-synthetic", "object": "chat.completion", "created": 1, "model": agent.InferenceModelName, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "Explicit synthetic lifecycle upstream"}, "finish_reason": "stop"}}, "usage": usage})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{
			{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "Explicit synthetic lifecycle SSE"}, "finish_reason": nil}}, "usage": nil},
			{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": nil},
			{"choices": []any{}, "usage": usage},
		} {
			event["id"] = "chatcmpl-synthetic"
			event["object"] = "chat.completion.chunk"
			event["created"] = 1
			event["model"] = agent.InferenceModelName
			b, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", b)
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}))
	t.Cleanup(backend.Close)
	return func(a *nativeAliasCutover) func(*testing.T, string) {
		binary := os.Getenv("PROJECT_ONE_GATEWAY_BINARY")
		info, e := os.Lstat(binary)
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0755 {
			t.Fatal("canonical0755 native gateway required")
		}
		state := filepath.Join(root, "gateway-state")
		if e = os.Mkdir(state, 0700); e != nil {
			t.Fatal(e)
		}
		credential := filepath.Join(root, "gateway-credential")
		if e = os.WriteFile(credential, []byte(*a.gateway.Secret+"\n"), 0600); e != nil {
			t.Fatal(e)
		}
		listener := httptest.NewUnstartedServer(nil)
		listen := listener.Listener.Addr().String()
		listener.Close()
		values := map[string]any{"gateway_id": a.gateway.Gateway.ID, "node_id": g.node.NodeID, "listen_address": listen, "backend_address": strings.TrimPrefix(backend.URL, "http://"), "control_origin": control.URL, "control_ca_file": caFile, "gateway_credential_file": credential, "tls_certificate_file": certFile, "tls_private_key_file": keyFile, "state_directory": state, "limits": map[string]any{"active_requests": 2, "queued_requests": 2, "backend_idle_connections": 2, "connect_seconds": 1, "control_seconds": 2, "request_seconds": 5, "idle_seconds": 2, "header_seconds": 1, "shutdown_seconds": 2, "outcome_retry_seconds": 1}}
		b, _ := json.Marshal(values)
		configFile := filepath.Join(root, "gateway.json")
		if e = os.WriteFile(configFile, b, 0600); e != nil {
			t.Fatal(e)
		}
		logPath := filepath.Join(root, "gateway.log")
		log, e := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		command := exec.Command(binary, "serve", "--config", configFile)
		command.Stdout = log
		command.Stderr = log
		if e = command.Start(); e != nil {
			log.Close()
			t.Fatal(e)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		t.Cleanup(func() {
			command.Process.Signal(syscall.SIGTERM)
			select {
			case e := <-done:
				if e != nil {
					t.Error("Rust lifecycle shutdown", e)
				}
			case <-time.After(8 * time.Second):
				command.Process.Kill()
				<-done
				t.Error("Rust lifecycle join timeout")
			}
			log.Close()
			raw, e := os.ReadFile(logPath)
			if e != nil {
				t.Error(e)
				return
			}
			for _, secret := range []string{*a.key.Secret, *a.gateway.Secret} {
				raw = bytes.ReplaceAll(raw, []byte(secret), []byte("[redacted]"))
			}
			if output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"); output != "" {
				if e = os.WriteFile(filepath.Join(output, "lifecycle-rust-"+uuid.NewString()+".log"), raw, 0600); e != nil {
					t.Error(e)
				}
			}
		})
		ca, e := os.ReadFile(caFile)
		if e != nil {
			t.Fatal(e)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(ca) {
			t.Fatal("private CA")
		}
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, Proxy: nil}, Timeout: 8 * time.Second}
		t.Cleanup(client.CloseIdleConnections)
		origin := "https://" + listen
		deadline := time.Now().Add(4 * time.Second)
		for {
			r, e := client.Get(origin + "/health/ready")
			if e == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("Rust lifecycle readiness", e)
			}
			time.Sleep(20 * time.Millisecond)
		}
		request := func(t *testing.T, path string, body any) (int, []byte) {
			t.Helper()
			method := http.MethodGet
			var reader io.Reader
			if body != nil {
				method = http.MethodPost
				b, _ := json.Marshal(body)
				reader = bytes.NewReader(b)
			}
			r, e := http.NewRequest(method, origin+path, reader)
			if e != nil {
				t.Fatal(e)
			}
			r.Header.Set("Authorization", "Bearer "+*a.key.Secret)
			if body != nil {
				r.Header.Set("Content-Type", "application/json")
			}
			response, e := client.Do(r)
			if e != nil {
				t.Fatal(e)
			}
			defer response.Body.Close()
			raw, e := io.ReadAll(io.LimitReader(response.Body, 65537))
			if e != nil || len(raw) > 65536 {
				t.Fatal("bounded Rust lifecycle result", e)
			}
			return response.StatusCode, raw
		}
		return func(t *testing.T, deployment string) {
			t.Helper()
			chat := func(stream bool) map[string]any {
				v := map[string]any{"model": a.alias.Name, "messages": []any{map[string]any{"role": "user", "content": "Explicit synthetic lifecycle transport"}}, "max_tokens": 16, "stream": stream}
				if stream {
					v["stream_options"] = map[string]any{"include_usage": true}
				}
				return v
			}
			if deployment == "" {
				status, raw := request(t, "/v1/chat/completions", chat(false))
				if status != 503 || !bytes.Contains(raw, []byte("model_unavailable")) {
					t.Fatal("paused same-key Rust refusal", status, string(raw))
				}
				return
			}
			status, raw := request(t, "/v1/models", nil)
			if status != 200 || !bytes.Contains(raw, []byte(a.alias.Name)) {
				t.Fatal("same-key Rust models", status)
			}
			for _, stream := range []bool{false, true} {
				status, raw = request(t, "/v1/chat/completions", chat(stream))
				if status != 200 || !bytes.Contains(raw, []byte(a.alias.Name)) || stream && !bytes.HasSuffix(raw, []byte("data: [DONE]\n\n")) {
					t.Fatal("same-key Rust chat/SSE", status, stream)
				}
			}
			deadline := time.Now().Add(4 * time.Second)
			for {
				var held int
				if e := s.DB.QueryRow(context.Background(), "SELECT count(*) FROM gateway_requests WHERE alias_id=$1 AND capacity_held", a.alias.ID).Scan(&held); e != nil {
					t.Fatal(e)
				}
				if held == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("Rust outcomes remain unresolved; no artificial drain")
				}
				time.Sleep(20 * time.Millisecond)
			}
			var count int
			if e := s.DB.QueryRow(context.Background(), "SELECT count(*) FROM gateway_requests WHERE alias_id=$1 AND deployment_id=$2 AND alias_revision=$3 AND state='reported' AND NOT capacity_held", a.alias.ID, deployment, a.alias.Revision).Scan(&count); e != nil || count < 2 {
				t.Fatal("Rust outcomes wrong immutable target", count, e)
			}
			t.Log("Actual native Rust/TLS/control/PostgreSQL same original key models/chat/SSE at immutable deployment", deployment, "revision", a.alias.Revision, "synthetic upstream only")
		}
	}
}

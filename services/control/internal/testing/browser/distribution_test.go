package browser_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// Browser fixture executes the real native gateway/Go/PostgreSQL but explicitly synthetic model/node responses.
func configureDistributionBrowser(t *testing.T, server *httptest.Server, h *transport.Handler, manifest map[string]any, directory, caFile, certFile, keyFile, password string) {
	t.Helper()
	binary := os.Getenv("PROJECT_ONE_GATEWAY_BINARY")
	if binary == "" {
		t.Fatal("M07 browser requires an explicit native gateway binary")
	}
	s := h.Service
	actor := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	node, model, stop := startSyntheticInferenceMachineProfile(t, server, h.Nodes.Service, actor, false)
	t.Cleanup(stop)
	d, e := h.Deployments.Service.Create(context.Background(), actor, deployments.CreateRequest{NodeID: node, ModelID: model, GPUUUID: "GPU-12345678-1234-1234-1234-123456789abc", Reason: "Synthetic distribution browser preparation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		d, e = h.Deployments.Service.Get(context.Background(), actor, "", d.ID)
		if e != nil {
			t.Fatal(e)
		}
		if d.Observation != nil && d.Observation.Ready && !d.Stale {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("synthetic inference setup timeout")
		}
		time.Sleep(25 * time.Millisecond)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("backend received delegated authority")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"object": "model", "id": wire.InferenceModelName}}})
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
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
			json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl-browser-synthetic", "object": "chat.completion", "created": 1, "model": wire.InferenceModelName, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "Synthetic browser response \u4e2d\u6587"}, "finish_reason": "stop"}}, "usage": usage})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flush := w.(http.Flusher)
		event := func(choices any, usage any) {
			b, _ := json.Marshal(map[string]any{"id": "chatcmpl-browser-synthetic", "object": "chat.completion.chunk", "created": 1, "model": wire.InferenceModelName, "choices": choices, "usage": usage})
			w.Write(append(append([]byte("data: "), b...), []byte("\n\n")...))
			flush.Flush()
		}
		event([]any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "Synthetic stream"}, "finish_reason": nil}}, nil)
		messages, _ := body["messages"].([]any)
		for _, raw := range messages {
			message, _ := raw.(map[string]any)
			if message["content"] == "Synthetic interrupted stream" {
				select {
				case <-r.Context().Done():
				case <-time.After(15 * time.Second):
				}
				return
			}
		}
		event([]any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, nil)
		event([]any{}, usage)
		w.Write([]byte("data: [DONE]\n\n"))
		flush.Flush()
	}))
	t.Cleanup(backend.Close)
	root := filepath.Join(directory, "gateway")
	if e = os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	for name, source := range map[string]string{"ca.pem": caFile, "certificate.pem": certFile, "key.pem": keyFile} {
		b, e := os.ReadFile(source)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(root, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	listener := httptest.NewUnstartedServer(nil)
	listen := listener.Listener.Addr().String()
	listener.Close()
	manifest["inference_node_id"] = node
	manifest["deployment_id"] = d.ID
	manifest["gateway_root"] = root
	manifest["gateway_binary"] = binary
	manifest["gateway_listen"] = listen
	manifest["gateway_backend"] = strings.TrimPrefix(backend.URL, "http://")
	manifest["gateway_ca"] = filepath.Join(root, "ca.pem")
}

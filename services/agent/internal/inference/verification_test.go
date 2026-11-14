package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestHealthIdentityAndActualResponseBoundsGateGenerationProof(t *testing.T) {
	// Explicit synthetic upstream responses exercise validation; no model/device execution is claimed.
	mode := "valid"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			if mode == "unhealthy" {
				w.WriteHeader(503)
				return
			}
			w.WriteHeader(200)
		case "/v1/models":
			model := wire.InferenceModelName
			if mode == "wrong-model" {
				model = "unowned"
			}
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]string{{"id": model, "object": "model"}}})
		case "/v1/chat/completions":
			var input map[string]any
			if json.NewDecoder(r.Body).Decode(&input) != nil || input["model"] != wire.InferenceModelName || input["max_tokens"] != float64(16) || input["stream"] != false {
				t.Error("unbounded or wrong internal verification request")
			}
			content := "SYNTHETIC_MODEL_RESPONSE"
			count := int64(2)
			if mode == "empty" {
				content = ""
			}
			if mode == "fabricated-usage" {
				count = 0
			}
			json.NewEncoder(w).Encode(map[string]any{"model": wire.InferenceModelName, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": content}}}, "usage": map[string]int64{"prompt_tokens": 9, "completion_tokens": count}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	probe := &verifier{client: server.Client(), base: server.URL}
	value, e := probe.Generate(context.Background())
	if e != nil || value.OutputTokens != 2 || value.ResponseSHA256 == "" {
		t.Fatal(e)
	}
	for _, bad := range []string{"unhealthy", "wrong-model", "empty", "fabricated-usage"} {
		mode = bad
		if _, e = probe.Generate(context.Background()); e == nil {
			t.Fatal("false generation proof accepted", bad)
		}
	}
}

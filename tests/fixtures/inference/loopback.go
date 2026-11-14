// This test-only HTTP fixture validates Docker loopback transport; it never loads or runs a model.
package main

import (
	"encoding/json"
	"io"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]string{{"id": "projectone-qwen3-0.6b", "object": "model"}}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var value struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			Stream    bool   `json:"stream"`
		}
		if r.Method != "POST" || json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&value) != nil || value.Model != "projectone-qwen3-0.6b" || value.MaxTokens != 16 || value.Stream {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"model": value.Model, "choices": []map[string]any{{"index": 0, "message": map[string]string{"role": "assistant", "content": "synthetic loopback fixture"}}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 1}})
	})
	server := &http.Server{Addr: "0.0.0.0:8000", Handler: mux, MaxHeaderBytes: 16384}
	if server.ListenAndServe() != nil {
		panic("ISOLATED_LOOPBACK_FIXTURE_FAILED")
	}
}

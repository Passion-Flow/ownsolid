package inference

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

type verifier struct {
	client    *http.Client
	transport *http.Transport
	base      string
}

func localVerifier(policies ...settings.Policy) *verifier {
	policy := settings.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	address := "127.0.0.1:" + strconv.Itoa(wire.ApprovedInferenceRecipe().HostPort)
	transport := &http.Transport{Proxy: nil, MaxConnsPerHost: 1, MaxIdleConns: 1, MaxIdleConnsPerHost: 1, IdleConnTimeout: settings.Seconds(policy.IdleConnectionSeconds), ResponseHeaderTimeout: settings.Seconds(policy.ProbeHeaderSeconds), DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: settings.Seconds(policy.ProbeDialSeconds)}).DialContext(ctx, "tcp4", address)
	}}
	return &verifier{client: &http.Client{Transport: transport, Timeout: settings.Seconds(policy.ProbeSeconds), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport: transport, base: "http://" + address}
}
func (v *verifier) Close() {
	if v.transport != nil {
		v.transport.CloseIdleConnections()
	}
}
func (v *verifier) call(ctx context.Context, path string, input any, limit int) ([]byte, error) {
	var body []byte
	if input != nil {
		body, _ = json.Marshal(input)
	}
	method := "GET"
	if input != nil {
		method = "POST"
	}
	request, e := http.NewRequestWithContext(ctx, method, v.base+path, bytes.NewReader(body))
	if e != nil {
		return nil, fail("INFERENCE_RESPONSE_INVALID")
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, e := v.client.Do(request)
	if e != nil {
		return nil, fail("INFERENCE_NOT_READY")
	}
	defer response.Body.Close()
	data, e := io.ReadAll(io.LimitReader(response.Body, int64(limit+1)))
	if e != nil || len(data) > limit || !utf8.Valid(data) {
		return nil, fail("INFERENCE_RESPONSE_INVALID")
	}
	if response.StatusCode != 200 {
		return nil, fail("INFERENCE_NOT_READY")
	}
	return data, nil
}
func (v *verifier) Health(ctx context.Context) error {
	if _, e := v.call(ctx, "/health", nil, 4096); e != nil {
		return e
	}
	body, e := v.call(ctx, "/v1/models", nil, 32768)
	if e != nil {
		return e
	}
	var value struct {
		Object string `json:"object"`
		Data   []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &value) != nil || value.Object != "list" || len(value.Data) != 1 || value.Data[0].ID != wire.InferenceModelName || value.Data[0].Object != "model" {
		return fail("INFERENCE_RESPONSE_INVALID")
	}
	return nil
}
func (v *verifier) Generate(ctx context.Context) (wire.GenerationVerification, error) {
	var out wire.GenerationVerification
	if e := v.Health(ctx); e != nil {
		return out, e
	}
	input := map[string]any{"model": wire.InferenceModelName, "messages": []map[string]string{{"role": "user", "content": wire.InferenceVerificationPrompt}}, "temperature": 0, "max_tokens": 16, "stream": false, "chat_template_kwargs": map[string]bool{"enable_thinking": false}}
	body, e := v.call(ctx, "/v1/chat/completions", input, 65536)
	if e != nil {
		return out, e
	}
	var value struct {
		Model   string `json:"model"`
		Choices []struct {
			Index   int `json:"index"`
			Message struct {
				Role    string  `json:"role"`
				Content *string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt     int64 `json:"prompt_tokens"`
			Completion int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &value) != nil || value.Model != wire.InferenceModelName || len(value.Choices) != 1 || value.Choices[0].Index != 0 || value.Choices[0].Message.Role != "assistant" || value.Choices[0].Message.Content == nil || strings.TrimSpace(*value.Choices[0].Message.Content) == "" || len(*value.Choices[0].Message.Content) > 4096 {
		return out, fail("INFERENCE_RESPONSE_INVALID")
	}
	prompt := sha256.Sum256([]byte(wire.InferenceVerificationPrompt))
	answer := sha256.Sum256([]byte(*value.Choices[0].Message.Content))
	out = wire.GenerationVerification{Model: value.Model, PromptSHA256: hex.EncodeToString(prompt[:]), ResponseSHA256: hex.EncodeToString(answer[:]), InputTokens: value.Usage.Prompt, OutputTokens: value.Usage.Completion, ObservedAt: time.Now().UTC()}
	if e = out.Validate(); e != nil {
		return wire.GenerationVerification{}, e
	}
	return out, nil
}

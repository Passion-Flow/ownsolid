package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// hintServer answers every request with handler on TLS and returns a client for it.
func hintServer(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := New(server.URL, ca)
	if e != nil {
		t.Fatal(e)
	}
	c.Credential = strings.Repeat("C", 43)
	t.Cleanup(c.Close)
	return c
}

func refuse(w http.ResponseWriter, status int, code, retry string) {
	if retry != "" {
		w.Header().Set("Retry-After", retry)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"code": code, "status": status})
}

func TestRetryAfterHeaderBecomesTheHint(t *testing.T) {
	for _, c := range []struct {
		header string
		want   time.Duration
	}{
		{"7", 7 * time.Second},
		{"100000", time.Duration(wire.MaxRetryAfterSeconds) * time.Second},
		{"0", 0},
		{"-3", 0},
		{"Wed, 21 Oct 2026 07:28:00 GMT", 0},
		{"", 0},
	} {
		header := c.header
		client := hintServer(t, func(w http.ResponseWriter, r *http.Request) { refuse(w, http.StatusTooManyRequests, "RATE_LIMITED", header) })
		e := client.Post(t.Context(), "/agent/v1/nodes/"+sessionNode+"/heartbeat", wire.Handshake{}, nil)
		var p *Problem
		if !errors.As(e, &p) || p.Code != "RATE_LIMITED" || p.Status != 429 {
			t.Fatalf("%q: %v", header, e)
		}
		if got, ok := RetryHint(e); got != c.want || ok != (c.want > 0) {
			t.Errorf("%q: hint %s %v, want %s", header, got, ok, c.want)
		}
	}
}

func TestUnknownRefusalKeepsItsHint(t *testing.T) {
	if e := remoteProblem("PRIVATE_DETAIL", 503, 0); e.Error() != "CONTROL_RESPONSE_REJECTED" {
		t.Fatalf("unknown code %v", e)
	}
	e := remoteProblem("PRIVATE_DETAIL", 503, 4*time.Second)
	if hint, ok := RetryHint(e); e.Error() != "CONTROL_RESPONSE_REJECTED" || !ok || hint != 4*time.Second {
		t.Fatalf("unknown code with a hint %v %s", e, hint)
	}
}

func TestAfterHintNeverWaitsLessThanTheHint(t *testing.T) {
	hint := 10 * time.Second
	if got := afterHint(func(int64) int64 { return 0 }, hint); got != hint {
		t.Fatalf("shortest wait %s", got)
	}
	if got := afterHint(func(n int64) int64 { return n - 1 }, hint); got != hint+hint*hintSpreadPercent/100 {
		t.Fatalf("longest wait %s", got)
	}
	for range 100 {
		if got := AfterHint(hint); got < hint || got > hint*3/2 {
			t.Fatalf("wait %s", got)
		}
	}
}

func TestSessionProblemCarriesTheHint(t *testing.T) {
	c := &control{problem: &wire.SessionProblem{Code: "AGENT_OVERLOADED", Status: 503, RetryAfterSeconds: 5}}
	s := serve(t, c, true)
	_, e := s.Heartbeat(t.Context(), wire.Handshake{})
	if hint, ok := RetryHint(e); e == nil || e.Error() != "AGENT_OVERLOADED" || !ok || hint != 5*time.Second {
		t.Fatalf("overloaded heartbeat %v hint %s", e, hint)
	}
}

func TestModelRangeWaitsForTheHintAndRetries(t *testing.T) {
	const size = 64
	var calls atomic.Int32
	var first atomic.Int64
	content := []byte(strings.Repeat("m", size))
	client := hintServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			first.Store(time.Now().UnixNano())
			refuse(w, http.StatusTooManyRequests, "RATE_LIMITED", "1")
			return
		}
		if waited := time.Since(time.Unix(0, first.Load())); waited < time.Second {
			t.Errorf("retried after %s, before the hint", waited)
		}
		writeRange(w, content)
	})
	got, e := client.modelRangeWithRetry(t.Context(), "/agent/v1/nodes/"+sessionNode+"/tasks/"+sessionNode+"/model-files", wire.ModelFileRequest{LeaseToken: "lease", File: "model.safetensors", Offset: 0, Length: size})
	if e != nil || string(got) != string(content) || calls.Load() != 2 {
		t.Fatalf("range %q %v after %d calls", got, e, calls.Load())
	}
}

func TestModelRangeStopsOnARefusalWithoutAHint(t *testing.T) {
	var calls atomic.Int32
	client := hintServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		refuse(w, http.StatusConflict, "AGENT_LEASE_STALE", "")
	})
	_, e := client.modelRangeWithRetry(t.Context(), "/agent/v1/nodes/"+sessionNode+"/tasks/"+sessionNode+"/model-files", wire.ModelFileRequest{LeaseToken: "lease", File: "model.safetensors", Offset: 0, Length: 8})
	if e == nil || calls.Load() != 1 {
		t.Fatalf("stale lease %v after %d calls", e, calls.Load())
	}
}

// writeRange answers a model range request with content.
func writeRange(w http.ResponseWriter, content []byte) {
	sum := sha256.Sum256(content)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(content)))
	w.Header().Set("X-Artifact-SHA256", hex.EncodeToString(sum[:]))
	w.Write(content)
}

func TestInterruptedSeparatesSilenceFromARefusal(t *testing.T) {
	for _, tc := range []struct {
		failure error
		want    bool
	}{
		{nil, false},
		{errors.New("CONTROL_CONNECTION_FAILED"), true},
		{errors.New("CONTROL_RESPONSE_INVALID"), true},
		{errors.New("CONTROL_RESPONSE_REJECTED"), true},
		{context.Canceled, true},
		{context.DeadlineExceeded, true},
		{&Problem{Code: "STORAGE_UNAVAILABLE", Status: 503}, true},
		{&Problem{Code: "AGENT_OVERLOADED", Status: 503, RetryAfter: time.Second}, true},
		{&Problem{Code: "RATE_LIMITED", Status: 429}, true},
		{&Problem{Code: "AGENT_THROTTLED", Status: 409, RetryAfter: time.Second}, true},
		{&Problem{Code: "TRAINING_UPLOAD_BUSY", Status: 409}, true},
		{&Problem{Code: "MODEL_PREPARATION_UPLOAD_TIMEOUT", Status: 409}, true},
		{&Problem{Code: "AGENT_LEASE_STALE", Status: 409}, false},
		{&Problem{Code: "ACCESS_DENIED", Status: 403}, false},
		{&Problem{Code: "AGENT_UNAUTHORIZED", Status: 401}, false},
		{&Problem{Code: "CONTROL_RESPONSE_REJECTED", Status: 409}, false},
		{errors.New("AGENT_LEASE_STALE"), false},
		{errors.New("TRAINING_OUTPUT_CHANGED"), false},
	} {
		if got := Interrupted(tc.failure); got != tc.want {
			t.Errorf("Interrupted(%v) = %v", tc.failure, got)
		}
	}
}

func TestOnlyAnAnswerControlWroteCountsAsARefusal(t *testing.T) {
	for _, tc := range []struct {
		code    string
		status  int
		refused bool
	}{
		{"TRAINING_ATTEMPT_STALE", 409, true},
		{"AGENT_LEASE_STALE", 409, true},
		{"", 404, false},
		{"not a code", 403, false},
		{"SOME_PROXY_CODE", 401, false},
		{"", 502, false},
	} {
		e := remoteProblem(tc.code, tc.status, 0)
		if Interrupted(e) == tc.refused {
			t.Errorf("code %q status %d: %v", tc.code, tc.status, e)
		}
	}
}

func TestRetryBacksOffAndResets(t *testing.T) {
	var retry Retry
	now := time.Now()
	if !retry.Due(now) || retry.Succeeded() {
		t.Fatal("a fresh retry is due and has nothing to recover from")
	}
	delay, first := retry.Failed(now)
	if !first || delay > time.Second {
		t.Fatalf("first failure: %v, %v", delay, first)
	}
	if _, first = retry.Failed(now); first {
		t.Fatal("second failure reported as first")
	}
	retry.due = now.Add(time.Hour)
	if retry.Due(now) {
		t.Fatal("due before its time")
	}
	if !retry.Succeeded() || !retry.Due(now) {
		t.Fatal("success did not reset the backoff")
	}
}

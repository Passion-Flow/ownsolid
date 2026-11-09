package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLaneDAgentPacingTLS(t *testing.T) {
	var loseResponse, committed atomic.Bool
	c := newWorkflowCLI(t, nil, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/admin/agent-pacing") &&
				loseResponse.CompareAndSwap(true, false) {
				reply := httptest.NewRecorder()
				next.ServeHTTP(reply, r)
				committed.Store(reply.Code == http.StatusOK)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	type pacing struct {
		Heartbeat int   `json:"heartbeat_seconds"`
		Jitter    int   `json:"jitter_percent"`
		Online    int   `json:"online_window_seconds"`
		Version   int64 `json:"version"`
	}
	read := func() pacing {
		t.Helper()
		var out pacing
		raw := c.ok("", "node", "pacing", "get", "--output", "json")
		if err := json.Unmarshal([]byte(raw), &out); err != nil || out.Version < 1 || out.Online < 1 {
			t.Fatalf("invalid pacing state: %v", err)
		}
		return out
	}
	set := func(heartbeat int, version int64) []string {
		return []string{"node", "pacing", "set", "--heartbeat-seconds", strconv.Itoa(heartbeat),
			"--jitter-percent", "0", "--reconnect-base-seconds", "2", "--reconnect-max-seconds", "120",
			"--version", strconv.FormatInt(version, 10), "--reason", "Synthetic maintenance pacing", "--confirm"}
	}
	const events = "SELECT count(*) FROM audit_events WHERE action='node.pacing_updated'"
	initial := read()
	c.ok("ONLINE WINDOW (S)", "node", "pacing", "get")
	valid := set(15, initial.Version)
	c.refused("pass --confirm", valid[:len(valid)-1]...)
	c.refused("--jitter-percent is required", "node", "pacing", "set", "--heartbeat-seconds", "15", "--confirm")
	c.refused("AGENT_PACING_EXCEEDS_ONLINE_WINDOW", set(120, initial.Version)...)
	c.count(0, events)

	loseResponse.Store(true)
	stdout, stderr, err := c.run(valid...)
	if !committed.Load() || err == nil || stdout != "" || !strings.Contains(stderr, "inspect the resource before retrying") {
		t.Fatal("the CLI reported success after losing a committed pacing reply")
	}
	changed := read()
	if changed.Version != initial.Version+1 || changed.Heartbeat != 15 || changed.Jitter != 0 || changed.Online != initial.Online {
		t.Fatal("the read after the lost reply did not show the committed pacing")
	}
	c.refused("VERSION_CONFLICT", valid...)
	c.count(1, events)

	type result struct {
		value  int
		out    string
		stderr string
		err    error
	}
	results := make(chan result, 2)
	for _, value := range []int{10, 11} {
		go func() {
			out, stderr, err := c.run(set(value, changed.Version)...)
			results <- result{value: value, out: out, stderr: stderr, err: err}
		}()
	}
	winner, conflicts := 0, 0
	for range 2 {
		r := <-results
		switch {
		case r.err == nil:
			if winner != 0 {
				t.Fatal("two writes committed at one version")
			}
			winner = r.value
		case r.out == "" && strings.Contains(r.stderr, "VERSION_CONFLICT"):
			conflicts++
		default:
			t.Fatalf("unexpected racing write error: %v %s", r.err, r.stderr)
		}
	}
	if current := read(); winner == 0 || conflicts != 1 || current.Heartbeat != winner || current.Version != changed.Version+1 {
		t.Fatal("the retained settings did not identify the winning write")
	}
	c.count(2, events)
	if _, err := c.service.DB.Exec(t.Context(), "UPDATE users SET is_platform_admin=false WHERE id=$1", c.auth.Me.User.ID); err != nil {
		t.Fatal(err)
	}
	c.refused("HTTP 401", "node", "pacing", "get")
	c.refused("HTTP 401", set(15, changed.Version+1)...)
	c.count(2, events)
}

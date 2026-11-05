package runtime

import (
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
)

func TestJitteredIntervalCoversTheWholeSpread(t *testing.T) {
	p := wire.Pacing{HeartbeatSeconds: 10, JitterPercent: 10}
	if got := jitteredInterval(func(int64) int64 { return 0 }, p); got != 9*time.Second {
		t.Fatalf("shortest %s", got)
	}
	if got := jitteredInterval(func(n int64) int64 { return n - 1 }, p); got != 11*time.Second {
		t.Fatalf("longest %s", got)
	}
	if got := jitteredInterval(func(n int64) int64 { return n - 1 }, wire.Pacing{HeartbeatSeconds: 10}); got != 10*time.Second {
		t.Fatalf("no jitter %s", got)
	}
}

func TestPacerKeepsTheIntervalWhenIdleAndGoesOnAfterATask(t *testing.T) {
	p := newPacer()
	pacing := wire.Pacing{HeartbeatSeconds: 10, JitterPercent: 0}
	if got := p.afterSuccess(pacing, 4*time.Second); got != 6*time.Second {
		t.Fatalf("idle cycle waits %s", got)
	}
	if got := p.afterSuccess(pacing, time.Hour); got != 0 {
		t.Fatalf("after a long task waits %s", got)
	}
}

func TestPacerBacksOffUntilACycleSucceeds(t *testing.T) {
	pacing := wire.Pacing{HeartbeatSeconds: 10, ReconnectBaseSeconds: 1, ReconnectMaxSeconds: 4}
	p := newPacer()
	for i := range 6 {
		if got := p.afterFailure(pacing, nil); got < 0 || got > 4*time.Second {
			t.Fatalf("failure %d waits %s", i, got)
		}
	}
	if p.failures != 6 {
		t.Fatalf("failures %d", p.failures)
	}
	p.afterSuccess(pacing, 0)
	if p.failures != 0 {
		t.Fatal("a success keeps the backoff")
	}
	// The first delay after a success is back to the base.
	for range 100 {
		q := *p
		if got := q.afterFailure(pacing, nil); got > time.Second {
			t.Fatalf("first failure waits %s", got)
		}
	}
}

func TestPacerUsesTheLatestPacing(t *testing.T) {
	p := newPacer()
	if got := p.afterSuccess(wire.Pacing{HeartbeatSeconds: 60}, 0); got != time.Minute {
		t.Fatalf("waits %s", got)
	}
	if got := p.afterSuccess(wire.Pacing{HeartbeatSeconds: 5}, 0); got != 5*time.Second {
		t.Fatalf("waits %s", got)
	}
	slow := wire.Pacing{HeartbeatSeconds: 10, ReconnectBaseSeconds: 60, ReconnectMaxSeconds: 900}
	seen := false
	for range 200 {
		q := newPacer()
		seen = seen || q.afterFailure(slow, nil) > time.Second
	}
	if !seen {
		t.Fatal("the reconnect base from control is ignored")
	}
}

func TestPacerWaitsAtLeastTheRetryHint(t *testing.T) {
	pacing := wire.Pacing{HeartbeatSeconds: 10, ReconnectBaseSeconds: 1, ReconnectMaxSeconds: 4}
	refused := &client.Problem{Code: "AGENT_OVERLOADED", Status: 503, RetryAfter: 30 * time.Second}
	for range 100 {
		p := newPacer()
		// The hint is longer than the reconnect maximum, and control's word wins.
		if got := p.afterFailure(pacing, refused); got < 30*time.Second || got > 45*time.Second {
			t.Fatalf("after a 30 s hint waits %s", got)
		}
		if p.failures != 1 {
			t.Fatalf("failures %d", p.failures)
		}
	}
	short := &client.Problem{Code: "RATE_LIMITED", Status: 429, RetryAfter: time.Millisecond}
	p := newPacer()
	p.failures = 10
	seen := false
	for range 100 {
		q := *p
		seen = seen || q.afterFailure(pacing, short) > time.Second
	}
	if !seen {
		t.Fatal("a short hint cut the backoff")
	}
}

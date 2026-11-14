package v1

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

func lowest(int64) int64    { return 0 }
func highest(n int64) int64 { return n - 1 }

func TestFullJitterCeilingDoublesUpToTheLimit(t *testing.T) {
	for _, c := range []struct {
		failures int
		want     time.Duration
	}{{0, time.Second}, {1, 2 * time.Second}, {2, 4 * time.Second}, {3, 8 * time.Second}, {4, 10 * time.Second}, {math.MaxInt, 10 * time.Second}} {
		if got := FullJitter(highest, time.Second, 10*time.Second, c.failures); got != c.want {
			t.Errorf("failures %d: %s, want %s", c.failures, got, c.want)
		}
	}
	if got := FullJitter(lowest, time.Second, time.Minute, 5); got != 0 {
		t.Fatalf("lowest delay %s", got)
	}
}

func TestFullJitterStaysInRangeAndSpreads(t *testing.T) {
	seen := map[time.Duration]bool{}
	for range 1000 {
		got := FullJitter(rand.Int64N, time.Second, time.Minute, 3)
		if got < 0 || got > 8*time.Second {
			t.Fatalf("delay %s", got)
		}
		seen[got.Truncate(time.Second)] = true
	}
	if len(seen) < 8 {
		t.Fatalf("1000 delays fell in %d one-second buckets of 8", len(seen))
	}
}

func TestAfterHintNeverWaitsLessThanTheHint(t *testing.T) {
	hint := 10 * time.Second
	if got := AfterHint(lowest, hint); got != hint {
		t.Fatalf("shortest wait %s", got)
	}
	if got := AfterHint(highest, hint); got != hint+hint*HintSpreadPercent/100 {
		t.Fatalf("longest wait %s", got)
	}
}

func TestIntervalCoversTheWholeSpread(t *testing.T) {
	p := Pacing{HeartbeatSeconds: 10, JitterPercent: 10}
	if got := p.Interval(lowest); got != 9*time.Second {
		t.Fatalf("shortest %s", got)
	}
	if got := p.Interval(highest); got != 11*time.Second {
		t.Fatalf("longest %s", got)
	}
	if got := (Pacing{HeartbeatSeconds: 10}).Interval(highest); got != 10*time.Second {
		t.Fatalf("no jitter %s", got)
	}
}

func TestBackoffUsesTheReconnectBounds(t *testing.T) {
	p := Pacing{ReconnectBaseSeconds: 2, ReconnectMaxSeconds: 5}
	if got := p.Backoff(highest, 0); got != 2*time.Second {
		t.Fatalf("first failure %s", got)
	}
	if got := p.Backoff(highest, 9); got != 5*time.Second {
		t.Fatalf("capped %s", got)
	}
}

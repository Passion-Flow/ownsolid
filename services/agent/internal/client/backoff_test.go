package client

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestFullJitterCeilingDoublesUpToTheLimit(t *testing.T) {
	highest := func(n int64) int64 { return n - 1 }
	for _, c := range []struct {
		failures int
		want     time.Duration
	}{{0, time.Second}, {1, 2 * time.Second}, {2, 4 * time.Second}, {3, 8 * time.Second}, {4, 10 * time.Second}, {math.MaxInt, 10 * time.Second}} {
		if got := fullJitter(highest, time.Second, 10*time.Second, c.failures); got != c.want {
			t.Errorf("failures %d: %s, want %s", c.failures, got, c.want)
		}
	}
}

func TestFullJitterStartsFromZero(t *testing.T) {
	if got := fullJitter(func(int64) int64 { return 0 }, time.Second, time.Minute, 5); got != 0 {
		t.Fatalf("lowest delay %s", got)
	}
}

func TestFullJitterStaysInRangeAndSpreads(t *testing.T) {
	seen := map[time.Duration]bool{}
	for range 1000 {
		got := FullJitter(time.Second, time.Minute, 3)
		if got < 0 || got > 8*time.Second {
			t.Fatalf("delay %s", got)
		}
		seen[got.Truncate(time.Second)] = true
	}
	if len(seen) < 8 {
		t.Fatalf("1000 delays fell in %d one-second buckets of 8", len(seen))
	}
}

func TestSleepEndsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleep(ctx, time.Hour) {
		t.Fatal("slept through a cancelled context")
	}
	if !sleep(context.Background(), time.Millisecond) {
		t.Fatal("did not sleep")
	}
}

package client

import (
	"context"
	"testing"
	"time"
)

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

package v1

import (
	"testing"
	"time"
)

func TestSessionRebalanceChecksOnlyBetterRanksAtSpacedIntervals(t *testing.T) {
	var schedule SessionRebalance
	now := time.Now()
	interval := time.Minute
	if got := schedule.Candidate(now, interval, 2); got != -1 {
		t.Fatalf("initial check: %d", got)
	}
	if got := schedule.Candidate(now.Add(interval-time.Nanosecond), interval, 2); got != -1 {
		t.Fatalf("checked before the minimum interval: %d", got)
	}
	now = now.Add(2 * interval)
	if got := schedule.Candidate(now, interval, 2); got != 0 {
		t.Fatalf("first candidate: %d", got)
	}
	if got := schedule.Candidate(now, interval, 2); got != -1 {
		t.Fatalf("checked twice at the same time: %d", got)
	}
	now = now.Add(2 * interval)
	if got := schedule.Candidate(now, interval, 2); got != 1 {
		t.Fatalf("an unavailable first choice hid the second: %d", got)
	}
	if got := schedule.Candidate(now.Add(2*interval), interval, 0); got != -1 {
		t.Fatalf("the preferred gateway should stay in use: %d", got)
	}
	if got := schedule.Candidate(now.Add(4*interval), interval, 1); got != -1 {
		t.Fatalf("a new fallback must wait before its first check: %d", got)
	}
}

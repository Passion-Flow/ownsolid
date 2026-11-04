package nodes

import (
	"testing"
	"time"
)

func TestResyncDecision(t *testing.T) {
	cases := []struct {
		name                                     string
		cursor, assigned, acknowledged, inflight uint64
		resync                                   bool
	}{
		{"fresh node", 0, 0, 0, 0, false},
		{"in step", 5, 6, 5, 6, false},
		{"acknowledgement reply lost", 6, 6, 5, 6, false},
		{"idle and in step", 6, 6, 6, 0, false},
		{"control restored to an older state", 9, 6, 6, 0, true},
		{"agent lost its cursor", 0, 6, 6, 0, true},
		{"agent cursor behind control", 4, 6, 5, 6, true},
		{"older task still in flight", 6, 6, 4, 5, true},
	}
	for _, c := range cases {
		if got := resyncDecision(c.cursor, c.assigned, c.acknowledged, c.inflight); got != c.resync {
			t.Errorf("%s: resync %v", c.name, got)
		}
	}
}

func TestResyncBucketBoundsTheFleetRate(t *testing.T) {
	var b resyncBucket
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	granted := 0
	for range 10 {
		if b.take(3, now) {
			granted++
		}
	}
	if granted != 3 {
		t.Fatalf("burst granted %d", granted)
	}
	if b.take(3, now.Add(100*time.Millisecond)) {
		t.Fatal("refilled too fast")
	}
	if !b.take(3, now.Add(400*time.Millisecond)) {
		t.Fatal("did not refill")
	}
	if granted = 0; true {
		for range 10 {
			if b.take(3, now.Add(time.Hour)) {
				granted++
			}
		}
	}
	if granted != 3 {
		t.Fatalf("a long pause accumulated %d grants", granted)
	}
}

func TestNeverDelivered(t *testing.T) {
	exhausted, other := taskDeliveriesExhausted, "TASK_DEADLINE_EXCEEDED"
	for name, c := range map[string]struct {
		state   string
		code    *string
		attempt int
		events  int64
		outcome bool
		want    bool
	}{
		"pending":                    {state: "pending", want: true},
		"delivery bound":             {state: "failed", code: &exhausted, want: true},
		"failed for no known reason": {state: "failed"},
		"failed for another reason":  {state: "failed", code: &other},
		"attempts spent":             {state: "failed", code: &exhausted, attempt: 3},
		"acknowledged":               {state: "pending", attempt: 1},
		"events":                     {state: "pending", events: 1},
		"outcome":                    {state: "pending", outcome: true},
		"running":                    {state: "running"},
	} {
		if got := NeverDelivered(c.state, c.code, c.attempt, c.events, c.outcome); got != c.want {
			t.Error(name, got)
		}
	}
}

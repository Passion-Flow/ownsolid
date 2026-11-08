package scheduling

import (
	"errors"
	"math"
	"slices"
	"testing"
)

func TestRankQueueUsesPriorityDominantShareAndFIFO(t *testing.T) {
	entries := []QueueEntry{
		{JobID: "late", QueueID: "gpu", Sequence: 4},
		{JobID: "earlier", QueueID: "gpu", Sequence: 3},
		{JobID: "cpu-heavy", QueueID: "cpu", Sequence: 2},
		{JobID: "high", QueueID: "cpu", Priority: 10, Sequence: 1},
		{JobID: "equal-share", QueueID: "other", Sequence: 5},
	}
	queues := map[string]QueueShare{
		"cpu":   {Capacity: Resources{CPU: 1000, NVIDIAGPU: 8}, Reserved: Resources{CPU: 900}},
		"gpu":   {Capacity: Resources{CPU: 1000, NVIDIAGPU: 8}, Reserved: Resources{NVIDIAGPU: 2}},
		"other": {Capacity: Resources{CPU: 1000}, Reserved: Resources{CPU: 250}},
	}
	ranked, err := RankQueue(entries, queues)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, entry := range ranked {
		ids = append(ids, entry.JobID)
	}
	if !slices.Equal(ids, []string{"high", "earlier", "late", "equal-share", "cpu-heavy"}) || entries[0].JobID != "late" {
		t.Fatalf("unexpected queue order: %v", ids)
	}
	queues["gpu"] = QueueShare{Capacity: Resources{NVIDIAGPU: 8}, Reserved: Resources{NVIDIAGPU: 4}}
	ranked, err = RankQueue(entries, queues)
	if err != nil || ranked[1].JobID != "equal-share" {
		t.Fatalf("share did not respond to a reservation: %+v, %v", ranked, err)
	}
}

func TestRankQueueDoesNotRoundLargeRatios(t *testing.T) {
	entries := []QueueEntry{
		{JobID: "older", QueueID: "a", Sequence: 1},
		{JobID: "newer", QueueID: "b", Sequence: 2},
	}
	queues := map[string]QueueShare{
		"a": {Capacity: Resources{Memory: math.MaxInt64}, Reserved: Resources{Memory: math.MaxInt64 - 1}},
		"b": {Capacity: Resources{Memory: math.MaxInt64}, Reserved: Resources{Memory: math.MaxInt64 - 2}},
	}
	ranked, err := RankQueue(entries, queues)
	if err != nil || ranked[0].JobID != "newer" {
		t.Fatalf("dominant shares rounded to the same value: %+v, %v", ranked, err)
	}
	entries[1].Sequence = entries[0].Sequence
	if _, err := RankQueue(entries, queues); !errors.Is(err, ErrInvalidQueue) {
		t.Fatalf("duplicate sequence: %v", err)
	}
	queues["a"] = QueueShare{Capacity: Resources{CPU: 1000}, Reserved: Resources{Memory: 1}}
	if _, err := RankQueue(entries[:1], queues); !errors.Is(err, ErrInvalidQueue) {
		t.Fatalf("missing resource dimension: %v", err)
	}
}

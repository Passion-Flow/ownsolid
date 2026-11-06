package nodes

import (
	"net/url"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestFoldStepsPairsEachStepWithItsEnd(t *testing.T) {
	start := time.Unix(100, 0).UTC()
	steps := foldSteps([]stepReport{
		{wire.ProgressStepDownload, wire.ProgressStepRunning, start},
		{wire.ProgressStepDownload, wire.ProgressStepSucceeded, start.Add(3 * time.Second)},
		{wire.ProgressStepStart, wire.ProgressStepRunning, start.Add(3 * time.Second)},
	}, TaskSummary{State: "running"})
	if len(steps) != 2 || steps[0].State != wire.ProgressStepSucceeded || steps[0].FinishedAt == nil || !steps[0].FinishedAt.Equal(start.Add(3*time.Second)) {
		t.Fatal(steps)
	}
	if steps[1].State != wire.ProgressStepRunning || steps[1].FinishedAt != nil {
		t.Fatal("a step of a running task stays open", steps[1])
	}
}

func TestFoldStepsKeepsAStepThatRanTwiceApart(t *testing.T) {
	start := time.Unix(100, 0).UTC()
	steps := foldSteps([]stepReport{
		{wire.ProgressStepStart, wire.ProgressStepRunning, start},
		{wire.ProgressStepStart, wire.ProgressStepFailed, start.Add(time.Second)},
		{wire.ProgressStepStart, wire.ProgressStepRunning, start.Add(2 * time.Second)},
		{wire.ProgressStepStart, wire.ProgressStepSucceeded, start.Add(3 * time.Second)},
	}, TaskSummary{State: "succeeded"})
	if len(steps) != 2 || steps[0].State != wire.ProgressStepFailed || steps[1].State != wire.ProgressStepSucceeded {
		t.Fatal(steps)
	}
}

func TestFoldStepsEndsWhatADeadAgentLeftOpen(t *testing.T) {
	start := time.Unix(100, 0).UTC()
	end := start.Add(time.Minute)
	open := []stepReport{{wire.ProgressStepDownload, wire.ProgressStepRunning, start}}
	for state, want := range map[string]string{
		"failed":    wire.ProgressStepFailed,
		"cancelled": wire.ProgressStepFailed,
		"succeeded": wire.ProgressStepSucceeded,
	} {
		steps := foldSteps(open, TaskSummary{State: state, CompletedAt: &end})
		if len(steps) != 1 || steps[0].State != want || steps[0].FinishedAt == nil || !steps[0].FinishedAt.Equal(end) {
			t.Fatal(state, steps)
		}
	}
}

func TestFoldStepsIgnoresAnEndWithoutAStart(t *testing.T) {
	steps := foldSteps([]stepReport{{wire.ProgressStepDownload, wire.ProgressStepSucceeded, time.Unix(1, 0)}}, TaskSummary{State: "running"})
	if len(steps) != 0 {
		t.Fatal(steps)
	}
}

func TestTaskListQuerySeparatesFiltersFromPaging(t *testing.T) {
	filter, page, e := taskListQuery(url.Values{"state": {"running"}, "mine": {"true"}, "limit": {"5"}})
	if e != nil || filter.state != "running" || !filter.mine || page.Get("limit") != "5" || page.Has("state") {
		t.Fatal(filter, page, e)
	}
	node := "6f1f4c8e-7b1a-4f7c-9d2e-3a5b6c7d8e9f"
	filter, page, e = taskListQuery(url.Values{"node_id": {node}, "q": {"gpu"}})
	if e != nil || filter.node != node || filter.search != "gpu" || page.Has("q") || page.Has("node_id") {
		t.Fatal(filter, page, e)
	}
	for _, bad := range []url.Values{{"state": {"paused"}}, {"mine": {"false"}}, {"state": {"failed", "running"}}, {"mine": {"true"}, "state": {"succeeded"}}, {"node_id": {"x"}}, {"q": {""}}, {"q": {"a\x00"}}, {"q": {strings.Repeat("a", maximumNodeName+1)}}} {
		if _, _, e := taskListQuery(bad); e == nil || e.Error() != "VALIDATION_FAILED" {
			t.Fatal(bad, e)
		}
	}
}

func TestLogQueryReadsItsCursorAndBounds(t *testing.T) {
	attempt, sequence, limit, e := logQuery(url.Values{"after": {"2.41"}, "limit": {"10"}})
	if e != nil || attempt != 2 || sequence != 41 || limit != 10 {
		t.Fatal(attempt, sequence, limit, e)
	}
	if _, _, limit, e = logQuery(url.Values{}); e != nil || limit != maximumLogPage {
		t.Fatal(limit, e)
	}
	for _, bad := range []string{"", "1", "1.", ".1", "a.b", "1.2.3", "999.1", "1.999999", "-1.2"} {
		if _, _, _, e := logQuery(url.Values{"after": {bad}}); e == nil || e.Error() != "INVALID_CURSOR" {
			t.Fatal(bad, e)
		}
	}
	for _, bad := range []url.Values{{"limit": {"0"}}, {"limit": {"501"}}, {"limit": {"x"}}, {"other": {"1"}}} {
		if _, _, _, e := logQuery(bad); e == nil || e.Error() != "VALIDATION_FAILED" {
			t.Fatal(bad, e)
		}
	}
}

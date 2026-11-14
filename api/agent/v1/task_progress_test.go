package v1

import (
	"strings"
	"testing"
	"time"
)

func progressStep(sequence int) ProgressEntry {
	return ProgressEntry{Sequence: sequence, Kind: ProgressKindStep, At: time.Unix(1, 0), Step: ProgressStepDownload, State: ProgressStepRunning}
}

func progressLine(sequence int, message string) ProgressEntry {
	return ProgressEntry{Sequence: sequence, Kind: ProgressKindLog, At: time.Unix(1, 0), Level: ProgressLevelInfo, Message: message}
}

func TestTaskProgressAcceptsStepsAndLines(t *testing.T) {
	report := TaskProgress{Entries: []ProgressEntry{progressStep(1), progressLine(2, "Fetched 3 files")}}
	if e := report.Validate(); e != nil {
		t.Fatal(e)
	}
}

func TestTaskProgressRefusals(t *testing.T) {
	step := progressStep(1)
	unknownStep, unknownState, stepWithText := step, step, step
	unknownStep.Step = "mine_coins"
	unknownState.State = "paused"
	stepWithText.Message = "text"
	line := progressLine(1, "text")
	unknownLevel, lineWithStep, unknownKind, noTime := line, line, line, line
	unknownLevel.Level = "debug"
	lineWithStep.Step = ProgressStepDownload
	unknownKind.Kind = "metric"
	noTime.At = time.Time{}
	many := make([]ProgressEntry, MaximumProgressBatch+1)
	for i := range many {
		many[i] = progressLine(i+1, "text")
	}
	cases := map[string][]ProgressEntry{
		"nothing":            {},
		"too many":           many,
		"unknown step":       {unknownStep},
		"unknown state":      {unknownState},
		"step with text":     {stepWithText},
		"unknown level":      {unknownLevel},
		"line with a step":   {lineWithStep},
		"unknown kind":       {unknownKind},
		"no time":            {noTime},
		"zero sequence":      {progressLine(0, "text")},
		"sequence too high":  {progressLine(MaximumProgressSequence+1, "text")},
		"out of order":       {progressLine(2, "text"), progressLine(1, "text")},
		"repeated sequence":  {progressLine(1, "text"), progressLine(1, "text")},
		"empty line":         {progressLine(1, "")},
		"line too long":      {progressLine(1, strings.Repeat("x", MaximumProgressMessageBytes+1))},
		"second line":        {progressLine(1, "one\ntwo")},
		"terminal sequence":  {progressLine(1, "\x1b[2Jcleared")},
		"invalid encoding":   {progressLine(1, "\xff")},
		"bidirectional mark": {progressLine(1, "a‮b")},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			if e := (TaskProgress{Entries: entries}).Validate(); e == nil || e.Error() != "TASK_PROGRESS_INVALID" {
				t.Fatal(e)
			}
		})
	}
}

func TestTaskProgressIsAnEventsCall(t *testing.T) {
	const path = "/agent/v1/nodes/11111111-1111-4111-8111-111111111111/tasks/22222222-2222-4222-8222-222222222222/" + TaskProgressPath
	call, node, ok := PathCall(path)
	if !ok || call != CallEvents || node != "11111111-1111-4111-8111-111111111111" {
		t.Fatal(call, node, ok)
	}
}

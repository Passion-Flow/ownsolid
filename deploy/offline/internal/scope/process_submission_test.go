package scope

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

type unsubmittedApplicationWorker struct {
	*applicationWorkerDouble
	adapter *compose.Client
	calls   int
}

func (w *unsubmittedApplicationWorker) StartControlRuntime(ctx context.Context, reader compose.RuntimeReader, scope compose.RenderContext, model compose.Model, recorded compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	w.calls++
	return w.adapter.StartControlRuntime(ctx, reader, scope, model, recorded)
}

func actualCreationFailureAdapter(t *testing.T, c configuration.Config) *compose.Client {
	t.Helper()
	directory := t.TempDir()
	for name, body := range map[string]string{"docker": "#!/nonexistent-projectone-test-interpreter\n", "docker-compose": "#!/bin/sh\nexit 0\n", "socket": "explicit nonconnecting socket reference"} {
		if e := os.WriteFile(filepath.Join(directory, name), []byte(body), 0700); e != nil {
			t.Fatal(e)
		}
	}
	settings := *c.Compose
	settings.DockerBinary, settings.PluginDirectory = filepath.Join(directory, "docker"), directory
	c.Compose = &settings
	engineSettings := *c.Engine
	engineSettings.SocketFile = filepath.Join(directory, "socket")
	c.Engine = &engineSettings
	client, e := compose.New(c)
	if e != nil {
		t.Fatal(e)
	}
	return client
}

func TestProcessSubmissionActualPrecheckDurablyClosesOnlyExactUnsubmittedAttempt(t *testing.T) {
	// Real adapter/subprocess creation failure and private journal. Container,
	// SQL/store and resource observations remain explicit metadata doubles.
	c, _, d, base := applicationFixture(t)
	w := &unsubmittedApplicationWorker{applicationWorkerDouble: base, adapter: actualCreationFailureAdapter(t, c)}
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	out, actionError := l.processSelectedWithAdmissionLocked(context.Background(), d, w, "running", false, []string{"control"}, restoredApplicationProcess)
	l.close()
	if actionError == nil || actionError.Error() != "OFFLINE_SCOPE_PROCESS_ACTION_NOT_SUBMITTED" || out.Pending || out.Status != "recorded_process_unsubmitted" || w.calls != 1 {
		t.Fatal("real precheck failure must close as unsubmitted", out, actionError)
	}
	s, e := Read(context.Background(), c)
	if e != nil || s.Process == nil || s.Process.Pending || s.Process.Outcome != "unsubmitted" || s.Process.Submission != processSubmissionNotSubmitted {
		t.Fatal("unsubmitted journal did not reopen", s, e)
	}
	l, e = open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	before := len(l.events)
	if _, e = l.processSelectedWithAdmissionLocked(context.Background(), d, w, "running", false, []string{"control"}, restoredApplicationProcess); e == nil || e.Error() != "OFFLINE_SCOPE_PROCESS_EXPLICIT_RETRY_REQUIRED" || len(l.events) != before || w.calls != 1 {
		t.Fatal("ordinary start silently retried", e)
	}
	terminal := l.events[len(l.events)-1]
	prefix := l.events[:len(l.events)-1]
	for _, fault := range []string{"missing-proof", "changed-state", "wrong-code", "reconcile", "downgrade", "unknown-version", "negative-version"} {
		t.Run(fault, func(t *testing.T) {
			forged := terminal
			r := *terminal.Process
			forged.Process = &r
			switch fault {
			case "missing-proof":
				r.Submission = ""
			case "changed-state":
				r.After = nil
			case "wrong-code":
				forged.Code = "OFFLINE_SCOPE_UNRELATED_CODE"
			case "reconcile":
				forged.Action = "process_reconcile"
			case "downgrade":
				r.AttemptVersion = 0
			case "unknown-version":
				r.AttemptVersion = processAttemptVersion + 1
			case "negative-version":
				r.AttemptVersion = -1
			}
			if validateProcessEvent(prefix, forged) == nil {
				t.Fatal("invalid durable unsubmitted proof accepted")
			}
		})
	}
}

func TestProcessRetryableStartHistorySurvivesSafeStopAndSeparatesRuntimeAndService(t *testing.T) {
	// Explicit synthetic history reducer test, not an admitted journal fixture.
	record := &ProcessRecord{CreationSHA256: "current", Target: "running", SelectedServices: []string{"control"}}
	events := []Event{{OperationID: "start", Action: "process_start", Outcome: "failed", Process: record}, {OperationID: "stop", Action: "process_stop", Outcome: "completed", Process: &ProcessRecord{CreationSHA256: "current", Target: "stopped"}}}
	if retryableStartID(events, "current", []string{"control"}) != "start" || retryableStartID(events, "next", []string{"control"}) != "" || retryableStartID(events, "current", []string{"postgres"}) != "" {
		t.Fatal("stop erased failure or unrelated context gained retry authority")
	}
	events = append(events, Event{OperationID: "observation", ReferenceOperationID: "start", Action: "process_reconcile", Outcome: "failed", Process: record})
	if retryableStartID(events, "current", []string{"control"}) != "start" {
		t.Fatal("reconciliation lost original intent")
	}
	events = append(events, Event{OperationID: "new-start", Action: "process_start", Outcome: "completed", Process: record})
	if retryableStartID(events, "current", []string{"control"}) != "" {
		t.Fatal("completed later start retained old retry obligation")
	}
}

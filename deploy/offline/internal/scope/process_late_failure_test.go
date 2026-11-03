package scope

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

// These are explicit Engine/Compose doubles with real private plans and journal
// files. A late exit must not rewrite a successfully completed activation.
func TestRestoredApplicationExplicitRetryAfterObservedLateExit(t *testing.T) {
	for _, exitCode := range []int{1, 137} {
		t.Run(fmt.Sprintf("exit%d", exitCode), func(t *testing.T) {
			c, _, d, w := applicationFixture(t)
			if _, err := startApplicationDouble(t, c, d, w); err != nil {
				t.Fatal(err)
			}
			prefix, err := os.ReadFile(c.ScopeRoot + "/" + journalName)
			if err != nil {
				t.Fatal(err)
			}
			pg := w.state.Services["postgres"]
			control := w.state.Services["control"]
			control.State, control.ExitCode = "exited", exitCode
			w.state.Services["control"] = control
			if _, err = startApplicationDouble(t, c, d, w); err == nil || w.controlStarts != 1 {
				t.Fatal("ordinary start must not restart a late failure", err)
			}
			out, err := retrySelectedDouble(t, c, d, w, "control", restoredApplicationProcess)
			if err != nil || out.Pending || w.controlStarts != 2 || w.databaseStarts != 1 || w.state.Services["postgres"] != pg {
				t.Fatal("explicit same-process late-failure retry", out, err)
			}
			after, err := os.ReadFile(c.ScopeRoot + "/" + journalName)
			if err != nil || !bytes.HasPrefix(after, prefix) {
				t.Fatal("completed activation history changed", err)
			}
			l, err := open(c, false)
			if err != nil {
				t.Fatal(err)
			}
			record := processState(l.events).record
			if record == nil || record.AttemptVersion != 3 || record.RetryOfOperationID == "" || record.Before["control"].ExitCode != exitCode {
				l.close()
				t.Fatal("versioned late-failure reference or nonzero observation lost")
			}
			l.close()
			if _, err = retrySelectedDouble(t, c, d, w, "control", restoredApplicationProcess); err == nil || w.controlStarts != 2 {
				t.Fatal("completed retry must not resubmit", err)
			}
			if _, err = Read(context.Background(), c); err != nil {
				t.Fatal("new journal cannot reopen", err)
			}
		})
	}
}

type lateFailureStateEngine struct {
	*applicationEngineDouble
	fault string
}

func (d *lateFailureStateEngine) InspectRuntime(ctx context.Context, scope, name string) (engine.RuntimeContainer, bool, error) {
	object, found, err := d.applicationEngineDouble.InspectRuntime(ctx, scope, name)
	if name == "control" {
		switch d.fault {
		case "oom":
			object.State.OOMKilled = true
		case "paused":
			object.State.Paused = true
		case "restarting":
			object.State.Restarting = true
		case "dead":
			object.State.Dead = true
		case "pid":
			object.State.PID = 987
		}
	}
	return object, found, err
}

func TestRestoredApplicationLateFailureRetryRequiresConfirmedNonOOMStoppedProcess(t *testing.T) {
	for _, fault := range []string{"oom", "paused", "restarting", "dead", "pid"} {
		t.Run(fault, func(t *testing.T) {
			c, _, d, w := applicationFixture(t)
			if _, err := startApplicationDouble(t, c, d, w); err != nil {
				t.Fatal(err)
			}
			control := w.state.Services["control"]
			control.State, control.ExitCode = "exited", 1
			w.state.Services["control"] = control
			if _, err := retrySelectedDouble(t, c, &lateFailureStateEngine{d, fault}, w, "control", restoredApplicationProcess); err == nil || w.controlStarts != 1 {
				t.Fatal("unsafe late-failure observation authorized a start", err)
			}
		})
	}
}

func TestRestoredApplicationLateFailureRetryRejectsChangedContext(t *testing.T) {
	for _, fault := range []string{"control_id", "control_time", "database_id", "database_time", "clean_exit", "running", "payload"} {
		t.Run(fault, func(t *testing.T) {
			c, _, d, w := applicationFixture(t)
			if _, err := startApplicationDouble(t, c, d, w); err != nil {
				t.Fatal(err)
			}
			control := w.state.Services["control"]
			control.State, control.ExitCode = "exited", 1
			w.state.Services["control"] = control
			switch fault {
			case "control_id":
				control.ID = strings.Repeat("b", 64)
				w.state.Services["control"] = control
			case "control_time":
				d.times["control"] = "2026-10-18T00:00:59Z"
			case "database_id":
				pg := w.state.Services["postgres"]
				pg.ID = strings.Repeat("b", 64)
				w.state.Services["postgres"] = pg
			case "database_time":
				d.times["postgres"] = "2026-10-18T00:00:59Z"
			case "clean_exit":
				control.ExitCode = 0
				w.state.Services["control"] = control
			case "running":
				control.State, control.ExitCode = "running", 0
				w.state.Services["control"] = control
			case "payload":
				d.readFault = true
			}
			prefix, err := os.ReadFile(c.ScopeRoot + "/" + journalName)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = retrySelectedDouble(t, c, d, w, "control", restoredApplicationProcess); err == nil || w.controlStarts != 1 {
				t.Fatal("changed late-failure context submitted a start", err)
			}
			after, err := os.ReadFile(c.ScopeRoot + "/" + journalName)
			if err != nil || !bytes.Equal(prefix, after) {
				t.Fatal("refusal changed history", err)
			}
		})
	}
}

func TestRestoredApplicationLateFailureLostReplyReconcilesWithoutResend(t *testing.T) {
	c, _, d, w := applicationFixture(t)
	if _, err := startApplicationDouble(t, c, d, w); err != nil {
		t.Fatal(err)
	}
	control := w.state.Services["control"]
	control.State, control.ExitCode = "exited", 1
	w.state.Services["control"] = control
	w.controlFault = "after"
	if out, err := retrySelectedDouble(t, c, d, w, "control", restoredApplicationProcess); err == nil || !out.Pending || w.controlStarts != 2 {
		t.Fatal("lost reply must remain uncertain", out, err)
	}
	if out, err := retrySelectedDouble(t, c, d, w, "control", restoredApplicationProcess); err == nil || !out.Pending || w.controlStarts != 2 {
		t.Fatal("uncertain retry was resent", out, err)
	}
	if out, err := processRuntime(context.Background(), c, d, w, "", true); err != nil || out.Pending || w.controlStarts != 2 {
		t.Fatal("late-failure reply did not reconcile without resend", out, err)
	}
	if _, err := Read(context.Background(), c); err != nil {
		t.Fatal("reconciled version3 history cannot reopen", err)
	}
}

func TestLateControlRetryJournalRejectsForgedPredecessorAndAdmission(t *testing.T) {
	c, _, d, w := applicationFixture(t)
	if _, err := startApplicationDouble(t, c, d, w); err != nil {
		t.Fatal(err)
	}
	control := w.state.Services["control"]
	control.State, control.ExitCode = "exited", 1
	w.state.Services["control"] = control
	if _, err := retrySelectedDouble(t, c, d, w, "control", restoredApplicationProcess); err != nil {
		t.Fatal(err)
	}
	l, err := open(c, false)
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()
	var intent Event
	var prefix []Event
	for i, event := range l.events {
		if event.Outcome == "started" && event.Process != nil && event.Process.AttemptVersion == processLateFailureAttemptVersion {
			intent, prefix = event, l.events[:i]
			break
		}
	}
	if intent.Process == nil || validateProcessEvent(prefix, intent) != nil {
		t.Fatal("actual new intent is invalid")
	}
	for _, fault := range []string{"reference", "missing_reference", "version", "control_time", "database", "sql_proof", "clean_exit"} {
		t.Run(fault, func(t *testing.T) {
			forged := intent
			record := *intent.Process
			record.Before = maps.Clone(record.Before)
			admission := *record.RestoredApplication
			record.RestoredApplication = &admission
			forged.Process = &record
			switch fault {
			case "reference":
				record.RetryOfOperationID = strings.Repeat("4", 32)
			case "missing_reference":
				record.RetryOfOperationID = ""
			case "version":
				record.AttemptVersion = processAttemptVersion
			case "control_time":
				x := record.Before["control"]
				x.StartedAt = "2026-10-18T00:00:59Z"
				record.Before["control"] = x
			case "database":
				x := record.Before["postgres"]
				x.StartedAt = "2026-10-18T00:00:59Z"
				record.Before["postgres"] = x
			case "sql_proof":
				record.RestoredApplication.DatabaseProofSHA256 = strings.Repeat("4", 64)
			case "clean_exit":
				x := record.Before["control"]
				x.ExitCode = 0
				record.Before["control"] = x
			}
			if validateProcessEvent(prefix, forged) == nil {
				t.Fatal("forged late-failure intent accepted")
			}
		})
	}
	if validateProcessEvent(l.events, intent) == nil {
		t.Fatal("consumed late-failure predecessor reused")
	}
}

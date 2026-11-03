package scope

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func retrySelectedDouble(t *testing.T, c configuration.Config, d processEngine, w processOperator, selected string, mode restoredProcessMode) (ProcessResult, error) {
	t.Helper()
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	return l.processSelectedAttemptLocked(context.Background(), d, w, "running", false, []string{selected}, mode, true)
}

func TestRestoredApplicationExplicitRetryPreservesFailureAndRejectsReplay(t *testing.T) {
	c, _, d, w := applicationFixture(t)
	w.controlFault = "exit"
	if out, e := startApplicationDouble(t, c, d, w); e == nil || out.Pending {
		t.Fatal("failed exit not determinate", out, e)
	}
	if _, e := startApplicationDouble(t, c, d, w); e == nil || w.controlStarts != 1 {
		t.Fatal("ordinary start retried failure")
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	prefix := append([]Event{}, l.events...)
	creation := effectiveRuntimeState(prefix)
	priorID := retryableStartID(prefix, wire.Digest(encode(*creation.record)), []string{"control"})
	l.close()
	w.controlFault = ""
	out, e := retrySelectedDouble(t, c, d, w, "control", restoredApplicationProcess)
	if e != nil || out.Pending || w.controlStarts != 2 || w.databaseStarts != 1 {
		t.Fatal("explicit current retry", out, e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	intent := l.events[len(prefix)]
	if priorID == "" || intent.Process.RetryOfOperationID != priorID || intent.Process.AttemptVersion != processAttemptVersion || l.events[len(prefix)-1].Outcome != "failed" || !processImmutable(*intent.Process, *l.events[len(l.events)-1].Process) {
		t.Fatal("retry rewrote failure or lost immutable predecessor")
	}
	for _, fault := range []string{"reference", "before", "version", "selection"} {
		t.Run(fault, func(t *testing.T) {
			forged := intent
			r := *intent.Process
			forged.Process = &r
			switch fault {
			case "reference":
				r.RetryOfOperationID = "unrelated"
			case "before":
				r.Before = nil
			case "version":
				r.AttemptVersion = 0
			case "selection":
				r.SelectedServices = []string{"postgres"}
			}
			if validateProcessEvent(prefix, forged) == nil {
				t.Fatal("forged retry accepted")
			}
		})
	}
	// Reusing an already consumed predecessor must not create another intent,
	// even when its original snapshots/admission are copied byte-for-byte.
	if validateProcessEvent(l.events, intent) == nil {
		t.Fatal("consumed retry predecessor reused")
	}
	l.close()
	if _, e = retrySelectedDouble(t, c, d, w, "control", restoredApplicationProcess); e == nil || e.Error() != "OFFLINE_SCOPE_PROCESS_NO_RETRYABLE_START" || w.controlStarts != 2 {
		t.Fatal("completed retry resent", e)
	}
	if out, e = startApplicationDouble(t, c, d, w); e != nil || out.Status != "observed_already_running" || w.controlStarts != 2 {
		t.Fatal("completed ordinary readback", out, e)
	}
}

func TestRestoredApplicationExplicitRetryAfterRealUnsubmittedPrecheck(t *testing.T) {
	c, _, d, base := applicationFixture(t)
	w := &unsubmittedApplicationWorker{applicationWorkerDouble: base, adapter: actualCreationFailureAdapter(t, c)}
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	out, e := l.processSelectedWithAdmissionLocked(context.Background(), d, w, "running", false, []string{"control"}, restoredApplicationProcess)
	l.close()
	if e == nil || out.Pending || out.Status != "recorded_process_unsubmitted" {
		t.Fatal("real precheck did not close", out, e)
	}
	out, e = retrySelectedDouble(t, c, d, base, "control", restoredApplicationProcess)
	if e != nil || out.Pending || base.controlStarts != 1 || w.calls != 1 {
		t.Fatal("unsubmitted explicit retry", out, e)
	}
}

func TestRestoredDependencyExplicitRetryRequiresUnchangedFailedProcessAndPeers(t *testing.T) {
	c, _, d, w := completedDependencyFixture(t)
	w.databaseFault = "exit"
	if out, e := startDependencyDouble(t, c, d, w); e == nil || out.Pending {
		t.Fatal("failed database exit", out, e)
	}
	beforeTime := d.times["postgres"]
	d.times["postgres"] = "2026-10-17T00:00:09Z"
	if _, e := retrySelectedDouble(t, c, d, w, "postgres", restoredDependencyProcess); e == nil || w.databaseStarts != 1 {
		t.Fatal("changed failed process gained retry")
	}
	d.times["postgres"] = beforeTime
	w.databaseFault = ""
	out, e := retrySelectedDouble(t, c, d, w, "postgres", restoredDependencyProcess)
	if e != nil || out.Pending || w.databaseStarts != 2 || out.Resources.Services["control"].State != "created" {
		t.Fatal("selected database retry", out, e)
	}
	if _, e = retrySelectedDouble(t, c, d, w, "postgres", restoredDependencyProcess); e == nil || w.databaseStarts != 2 {
		t.Fatal("database retry repeated")
	}
}

func TestRestoredApplicationUnknownAttemptCannotUseExplicitRetry(t *testing.T) {
	c, _, d, w := applicationFixture(t)
	w.controlFault = "before" // A string assertion is not adapter submission proof.
	if out, e := startApplicationDouble(t, c, d, w); e == nil || !out.Pending {
		t.Fatal(out, e)
	}
	if out, e := retrySelectedDouble(t, c, d, w, "control", restoredApplicationProcess); e == nil || !out.Pending || e.Error() != "OFFLINE_SCOPE_PROCESS_RECONCILIATION_REQUIRED" || w.controlStarts != 1 {
		t.Fatal("unknown action resent", out, e)
	}
}

func TestRestoredApplicationExplicitRetryLostSuccessReconcilesOriginalAttemptWithoutResend(t *testing.T) {
	c, _, d, w := applicationFixture(t)
	w.controlFault = "exit"
	if out, e := startApplicationDouble(t, c, d, w); e == nil || out.Pending {
		t.Fatal("determinate predecessor required", out, e)
	}
	w.controlFault = "after"
	out, e := retrySelectedDouble(t, c, d, w, "control", restoredApplicationProcess)
	if e == nil || !out.Pending || w.controlStarts != 2 {
		t.Fatal("retry response loss did not remain pending", out, e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	state := processState(l.events)
	intentID, failedID := state.pending, state.record.RetryOfOperationID
	l.close()
	if intentID == "" || failedID == "" || intentID == failedID {
		t.Fatal("retry and predecessor identities collapsed")
	}
	if _, e = retrySelectedDouble(t, c, d, w, "control", restoredApplicationProcess); e == nil || w.controlStarts != 2 {
		t.Fatal("pending retry resent")
	}
	out, e = processRuntime(context.Background(), c, d, w, "", true)
	if e != nil || out.Pending || w.controlStarts != 2 {
		t.Fatal("retry readonly reconciliation failed", out, e)
	}
	s, e := Read(context.Background(), c)
	if e != nil || s.Process == nil || s.Process.Pending || s.Process.OperationID != intentID || s.Process.RetryOfOperationID != failedID || s.Process.Action != "process_reconcile" || s.Process.Outcome != "completed" {
		t.Fatal("reconciled retry lost separate attempt lineage", s, e)
	}
}

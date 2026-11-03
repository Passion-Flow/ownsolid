package scope

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Effect and metadata doubles are confined to these tests. Private plans,
// certificates, durable journals and all production admission checks are real.
func stoppedControlFixture(t *testing.T) (configuration.Config, configuration.TLSGenerationRequest, *applicationEngineDouble, *applicationWorkerDouble, configuration.ProcessRecoveryRequest) {
	t.Helper()
	c, tls, d, w := applicationFixture(t)
	w.controlFault = "exit"
	if out, err := startApplicationDouble(t, c, d, w); err == nil || out.Pending {
		t.Fatal("determinate synthetic failed start required", out, err)
	}
	l, err := open(c, false)
	if err != nil {
		t.Fatal(err)
	}
	failedID := l.events[len(l.events)-1].OperationID
	l.close()
	if _, err = processRuntime(context.Background(), c, d, w, "stopped", false); err != nil {
		t.Fatal("owned safe shutdown", err)
	}
	w.controlFault = ""
	return c, tls, d, w, configuration.ProcessRecoveryRequest{FormatVersion: 1, FailedControlStartOperationID: failedID, Reason: "Recover the exact synthetic stopped failure"}
}

func resumeControlDouble(t *testing.T, c configuration.Config, d *applicationEngineDouble, w *applicationWorkerDouble, r configuration.ProcessRecoveryRequest, mode restoredProcessMode) (ProcessResult, error) {
	t.Helper()
	l, err := open(c, true)
	if err != nil {
		t.Fatal("reopen durable recovery history", err)
	}
	defer l.close()
	return l.resumeStoppedControlLocked(context.Background(), d, w, r, mode)
}

func qualifyRecoveryDouble(t *testing.T, c configuration.Config, d *applicationEngineDouble, w *applicationWorkerDouble) {
	t.Helper()
	if _, err := runRestoredLiveDatabase(context.Background(), c, "requalify-restored-live-database", d, w); err != nil {
		t.Fatal("fresh SQL qualification", err)
	}
	if _, err := runRestoredCurrentStores(context.Background(), c, "requalify-restored-current-stores", d, w); err != nil {
		t.Fatal("fresh store qualification", err)
	}
}

func TestProcessRecoveryStoppedControlEndToEndAndImmutableReferences(t *testing.T) {
	c, _, d, w, request := stoppedControlFixture(t)
	failed := w.state.Services["control"]
	before, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if _, err := resumeControlDouble(t, c, d, w, request, restoredApplicationProcess); err == nil {
		t.Fatal("control recovery implicitly started stopped database")
	}
	if _, err := startApplicationDouble(t, c, d, w); err == nil {
		t.Fatal("ordinary control start admitted failed predecessor")
	}
	l, err := open(c, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.processSelectedAttemptLocked(context.Background(), d, w, "running", false, []string{"postgres"}, restoredDependencyProcess, false)
	l.close()
	if err == nil {
		t.Fatal("ordinary dependency start admitted failed control")
	}
	if after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName); !bytes.Equal(before, after) {
		t.Fatal("refusal changed journal")
	}
	if out, err := resumeControlDouble(t, c, d, w, request, restoredDependencyProcess); err != nil || out.Pending || w.databaseStarts != 2 || w.controlStarts != 1 || w.state.Services["control"] != failed {
		t.Fatal("exact selected dependency recovery", out, err)
	}
	pgJournal, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if out, err := resumeControlDouble(t, c, d, w, request, restoredDependencyProcess); err != nil || out.Status != "observed_completed_recovery_running" || w.databaseStarts != 2 {
		t.Fatal("dependency repeat resent", out, err)
	}
	if _, err = processRuntime(context.Background(), c, d, w, "stopped", false); err != nil {
		t.Fatal("second owned shutdown", err)
	}
	if out, err := resumeControlDouble(t, c, d, w, request, restoredDependencyProcess); err != nil || out.Pending || w.databaseStarts != 3 || w.controlStarts != 1 {
		t.Fatal("recovery did not bind the next owned shutdown", out, err)
	}
	if _, err := resumeControlDouble(t, c, d, w, request, restoredApplicationProcess); err == nil {
		t.Fatal("stale SQL/store checks admitted")
	}
	if _, err := runRestoredLiveDatabase(context.Background(), c, "requalify-restored-live-database", d, w); err != nil {
		t.Fatal(err)
	}
	if _, err := resumeControlDouble(t, c, d, w, request, restoredApplicationProcess); err == nil {
		t.Fatal("new SQL with old store checks admitted")
	}
	if _, err := runRestoredCurrentStores(context.Background(), c, "requalify-restored-current-stores", d, w); err != nil {
		t.Fatal(err)
	}
	if out, err := resumeControlDouble(t, c, d, w, request, restoredApplicationProcess); err != nil || out.Pending || w.controlStarts != 2 {
		t.Fatal("explicit qualified application recovery", out, err)
	}
	completed, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if !bytes.HasPrefix(completed, pgJournal) {
		t.Fatal("original history rewritten")
	}
	if out, err := resumeControlDouble(t, c, d, w, request, restoredApplicationProcess); err != nil || out.Status != "observed_completed_recovery_running" || w.controlStarts != 2 {
		t.Fatal("control repeat resent", out, err)
	}
	if after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName); !bytes.Equal(completed, after) {
		t.Fatal("completed repeat changed journal")
	}
	l, err = open(c, false)
	if err != nil {
		t.Fatal(err)
	}
	for i, event := range l.events {
		if event.Process != nil && event.Process.RestoredApplication != nil && event.Process.AttemptVersion == processAttemptVersion && event.Outcome == "started" {
			var forged Event
			if err := wire.StrictJSON(encode(event), &forged); err != nil {
				t.Fatal(err)
			}
			forged.Process.AttemptVersion = processRecoveryAttemptVersion
			forged.Process.RecoveryOfOperationID, forged.Process.RecoveryStopOperationID, forged.Process.RecoveryReason = request.FailedControlStartOperationID, strings.Repeat("d", 32), request.Reason
			if validateImageEvent(l.events[:i], forged) == nil {
				t.Fatal("version2 metadata granted ordinary unbound activation")
			}
		}
		if event.Process == nil || event.Process.AttemptVersion != processRecoveryAttemptVersion || event.Outcome != "started" {
			continue
		}
		if event.Process.RecoveryOfOperationID != request.FailedControlStartOperationID || event.Process.RecoveryStopOperationID == "" || event.Process.RecoveryReason != request.Reason {
			t.Fatal("immutable references absent")
		}
		for _, fault := range []string{"original", "stop", "reason", "legacy", "selection", "before", "missing-admission"} {
			var forged Event
			if err := wire.StrictJSON(encode(event), &forged); err != nil {
				t.Fatal(err)
			}
			r := forged.Process
			switch fault {
			case "original":
				r.RecoveryOfOperationID = strings.Repeat("f", 32)
			case "stop":
				r.RecoveryStopOperationID = strings.Repeat("e", 32)
			case "reason":
				r.RecoveryReason = ""
			case "legacy":
				r.AttemptVersion = 1
			case "selection":
				r.SelectedServices = nil
			case "before":
				s := r.Before["control"]
				s.StartedAt = "2026-10-18T02:00:00Z"
				r.Before["control"] = s
			case "missing-admission":
				r.RestoredApplication, r.RestoredDependency = nil, nil
			}
			if validateImageEvent(l.events[:i], forged) == nil {
				t.Fatal("forged recovery accepted", fault)
			}
		}
	}
	status := recordedProcessStatus(l.events, false)
	if status == nil || status.RecoveryOfOperationID != request.FailedControlStartOperationID || bytes.Contains(encode(status), []byte(request.Reason)) {
		t.Fatal("public status lost references or exposed private reason")
	}
	l.close()
	if _, err = processRuntime(context.Background(), c, d, w, "stopped", false); err != nil {
		t.Fatal(err)
	}
	if _, err = resumeControlDouble(t, c, d, w, request, restoredDependencyProcess); err == nil {
		t.Fatal("consumed original failure reused after successful recovery")
	}
}

func TestProcessRecoveryRefusesChangedSnapshotPKIConfigurationAndResources(t *testing.T) {
	c, tls, d, w, request := stoppedControlFixture(t)
	before, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	for _, fault := range []string{"original", "reason", "snapshot", "configuration", "resources"} {
		r := request
		timestamp := d.times["control"]
		switch fault {
		case "original":
			r.FailedControlStartOperationID = strings.Repeat("f", 32)
		case "reason":
			r.Reason = ""
		case "snapshot":
			d.times["control"] = "2026-10-18T02:00:00Z"
		case "configuration":
			d.readFault = true
		case "resources":
			d.refuse = true
		}
		_, err := resumeControlDouble(t, c, d, w, r, restoredDependencyProcess)
		d.times["control"], d.readFault, d.refuse = timestamp, false, false
		if err == nil || w.databaseStarts != 1 || w.controlStarts != 1 {
			t.Fatal("unsafe recovery admitted", fault, err)
		}
		if after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName); !bytes.Equal(before, after) {
			t.Fatal("refusal wrote intent", fault)
		}
	}
	if err := os.Remove(tls.PrivateKeyFile); err != nil {
		t.Fatal(err)
	}
	if _, err := resumeControlDouble(t, c, d, w, request, restoredDependencyProcess); err == nil || w.databaseStarts != 1 {
		t.Fatal("missing current PKI admitted recovery")
	}
	if after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName); !bytes.Equal(before, after) {
		t.Fatal("PKI refusal wrote intent")
	}
}

func TestProcessRecoveryUnknownSelectedStartsReconcileWithoutResend(t *testing.T) {
	for _, selected := range []string{"postgres", "control"} {
		t.Run(selected, func(t *testing.T) {
			c, tls, d, w, request := stoppedControlFixture(t)
			mode := restoredDependencyProcess
			if selected == "control" {
				if _, err := resumeControlDouble(t, c, d, w, request, mode); err != nil {
					t.Fatal(err)
				}
				qualifyRecoveryDouble(t, c, d, w)
				mode, w.controlFault = restoredApplicationProcess, "after"
			} else {
				w.databaseFault = "after"
			}
			out, err := resumeControlDouble(t, c, d, w, request, mode)
			if err == nil || !out.Pending {
				t.Fatal("lost response must remain pending", out, err)
			}
			pgStarts, controlStarts := w.databaseStarts, w.controlStarts
			if _, err = resumeControlDouble(t, c, d, w, request, mode); err == nil || pgStarts != w.databaseStarts || controlStarts != w.controlStarts {
				t.Fatal("pending request resent")
			}
			if err = os.Remove(tls.PrivateKeyFile); err != nil {
				t.Fatal(err)
			}
			if out, err = processRuntime(context.Background(), c, d, w, "", true); err != nil || out.Pending || pgStarts != w.databaseStarts || controlStarts != w.controlStarts {
				t.Fatal("original intent reconciliation", out, err)
			}
			if _, err = resumeControlDouble(t, c, d, w, request, mode); err == nil {
				t.Fatal("completed repeat ignored missing current PKI")
			}
			if _, err = processRuntime(context.Background(), c, d, w, "stopped", false); err != nil {
				t.Fatal("safe stop without source PKI", err)
			}
		})
	}
}

package scope

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Named existing Engine/Compose doubles establish only target lineage. These
// tests exercise actual private files, fsync and the canonical scope journal;
// they do not manufacture SQL, imported data or installation evidence.
func restoreReservationFixture(t *testing.T) (configuration.Config, *locked, configuration.RestoreRequest, RestoreSourceResult, RestoreTargetResult) {
	t.Helper()
	c, d, w := databaseProcessFixture(t)
	if _, e := processRuntimeSelected(context.Background(), c, d, w, "running", false, []string{"postgres"}); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	r, source, target := restoreReservationInput(l)
	return c, l, r, source, target
}

func restoreReservationInput(l *locked) (configuration.RestoreRequest, RestoreSourceResult, RestoreTargetResult) {
	r := configuration.RestoreRequest{RestoreSourceRequest: configuration.RestoreSourceDefaults(), Reason: "Explicit isolated synthetic restore reservation"}
	r.BackupRoot = "/synthetic/external/complete-backup"
	r.ManifestSHA256 = strings.Repeat("b", 64)
	source := RestoreSourceResult{Status: "verified_independent_backup_source_not_restored", RequestSHA256: wire.Digest(encode(r.RestoreSourceRequest)), ManifestSHA256: r.ManifestSHA256, PlanSHA256: strings.Repeat("c", 64), SourceScopeID: strings.Repeat("a", 32), ReleaseID: l.marker.ReleaseID, Profile: l.marker.Profile, Stores: []string{}, Source: engine.PostgresBackupSource{Database: "synthetic_source", Operator: "synthetic_source_operator", TenantID: "11111111-1111-4111-8111-111111111111", ServerVersionNumber: 180006, SchemaVersion: 16, VectorVersion: "0.8.7"}}
	input, _, _ := inputsState(l.events)
	runtime := runtimeState(l.events)
	target := RestoreTargetResult{Status: "verified_restore_target_lineage_not_restored", SourceScopeID: source.SourceScopeID, TargetScopeID: l.marker.ScopeID, BackupManifestSHA256: r.ManifestSHA256, DescriptorSHA256: input.DescriptorSHA256, RuntimeSHA256: wire.Digest(encode(*runtime.record)), Stores: []string{}}
	target.BindingSHA256 = wire.Digest(encode(target))
	return r, source, target
}

func TestRestoreReservationRejectsAlreadyRunningApplicationBeforeFastReturn(t *testing.T) {
	c, d, w := databaseProcessFixture(t)
	ctx := context.Background()
	if _, e := processRuntimeSelected(ctx, c, d, w, "running", false, []string{"postgres"}); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	r, source, target := restoreReservationInput(l)
	if _, e = l.reserveRestore(ctx, r, source, target, "prepare-restore", func(p RestorePlan) error { return writeBackupFile(l.root, restorePlanFile, encode(p)) }); e != nil {
		l.close()
		t.Fatal(e)
	}
	l.close()
	// A local Docker owner could manually start an existing container outside
	// the operator. That observation must not produce an accepted start result.
	s := w.state.Services["control"]
	s.State = "running"
	w.state.Services["control"] = s
	d.times["control"] = "2026-10-17T00:00:02Z"
	if _, e = processRuntime(ctx, c, d, w, "running", false); e == nil || e.Error() != "OFFLINE_SCOPE_RESTORE_EXECUTION_OR_ABORT_REQUIRED" {
		t.Fatal("reserved application fast return did not refuse", e)
	}
	if w.starts != 0 {
		t.Fatal("unqualified application start attempted")
	}
}

func TestRestoreReservationDurableReopenRepeatAbortAndStatusSeparation(t *testing.T) {
	c, l, r, source, target := restoreReservationFixture(t)
	publishes := 0
	publish := func(p RestorePlan) error { publishes++; return writeBackupFile(l.root, restorePlanFile, encode(p)) }
	out, e := l.reserveRestore(context.Background(), r, source, target, "prepare-restore", publish)
	if e != nil || out.Status != "restore_reserved_execution_pending" || !out.Pending || publishes != 1 {
		t.Fatal(out, e)
	}
	before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	planBefore, _ := l.root.Stat(restorePlanFile)
	if _, e = l.reserveRestore(context.Background(), r, source, target, "prepare-restore", publish); e != nil || publishes != 1 {
		t.Fatal("reservation repeat replayed", e)
	}
	after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(before, after) {
		t.Fatal("repeat changed journal")
	}
	l.close()
	status, e := Read(context.Background(), c)
	if e != nil || status.Restore == nil || !status.Restore.Pending || status.BootstrapQualified || status.SchemaQualified || status.RuntimeAccessQualified || status.Status != "isolated_scope_restore_reserved_execution_pending" {
		t.Fatal(status, e)
	}
	l, e = open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	for _, action := range []string{"database_task_create", "model_task_create", "volume_prepare", "backup_effect", "inputs_prepare", "process_start"} {
		if e := validateImageEvent(l.events, Event{Action: action}); e == nil || e.Error() != "OFFLINE_SCOPE_RESTORE_EXECUTION_OR_ABORT_REQUIRED" {
			t.Fatal("restore exclusion missing", action, e)
		}
	}
	for _, event := range []Event{{Action: "process_stop"}, {Action: "process_start", Process: &ProcessRecord{SelectedServices: []string{"postgres"}}}, {Action: "process_reconcile", Process: &ProcessRecord{Target: "stopped"}}} {
		if e := restoreMutationGuard(l.events, event); e != nil {
			t.Fatal("safe dependency recovery blocked", e)
		}
	}
	if out, e = l.abortRestoreReservation(r); e != nil || out.Pending || out.Status != "restore_reservation_aborted_no_execution" {
		t.Fatal(out, e)
	}
	if _, e = l.abortRestoreReservation(r); e != nil {
		t.Fatal("abort repeat", e)
	}
	planAfter, _ := l.root.Stat(restorePlanFile)
	if !os.SameFile(planBefore, planAfter) {
		t.Fatal("abort replaced retained plan")
	}
	if _, e = l.reserveRestore(context.Background(), r, source, target, "prepare-restore", publish); e == nil || publishes != 1 {
		t.Fatal("aborted generation reused")
	}
}

func TestRestoreReservationLostPublicationAndAbsentFileReconcileWithoutOverwrite(t *testing.T) {
	for _, fault := range []string{"after-publication", "before-publication"} {
		t.Run(fault, func(t *testing.T) {
			c, l, r, source, target := restoreReservationFixture(t)
			writes := 0
			lost := func(p RestorePlan) error {
				if fault == "after-publication" {
					writes++
					if e := writeBackupFile(l.root, restorePlanFile, encode(p)); e != nil {
						return e
					}
				}
				return errors.New("SYNTHETIC_PUBLICATION_RESULT_LOST")
			}
			out, e := l.reserveRestore(context.Background(), r, source, target, "prepare-restore", lost)
			if e == nil || out.Status != "restore_reservation_outcome_unresolved" {
				t.Fatal(out, e)
			}
			if _, e = l.reserveRestore(context.Background(), r, source, target, "prepare-restore", lost); e == nil {
				t.Fatal("uncertain generation replayed")
			}
			l.close()
			l, e = open(c, true)
			if e != nil {
				t.Fatal(e)
			}
			defer l.close()
			publish := func(p RestorePlan) error { writes++; return writeBackupFile(l.root, restorePlanFile, encode(p)) }
			out, e = l.reserveRestore(context.Background(), r, source, target, "reconcile-restore-reservation", publish)
			if e != nil || out.Status != "restore_reserved_execution_pending" || writes != 1 {
				t.Fatal("independent plan recovery", out, writes, e)
			}
		})
	}
}

func TestRestoreReservationPlanTamperLinksAndContextRefused(t *testing.T) {
	for _, fault := range []string{"bytes", "partial", "symlink", "hardlink", "missing", "wrong-reason", "wrong-target-binding", "wrong-source-digest"} {
		t.Run(fault, func(t *testing.T) {
			c, l, r, source, target := restoreReservationFixture(t)
			publish := func(p RestorePlan) error { return writeBackupFile(l.root, restorePlanFile, encode(p)) }
			if _, e := l.reserveRestore(context.Background(), r, source, target, "prepare-restore", publish); e != nil {
				t.Fatal(e)
			}
			if fault == "wrong-reason" || fault == "wrong-target-binding" || fault == "wrong-source-digest" {
				if fault == "wrong-reason" {
					r.Reason = "Changed context"
				}
				if fault == "wrong-target-binding" {
					target.BindingSHA256 = strings.Repeat("d", 64)
				}
				if fault == "wrong-source-digest" {
					source.ManifestSHA256 = strings.Repeat("d", 64)
				}
				if _, e := l.reserveRestore(context.Background(), r, source, target, "prepare-restore", publish); e == nil {
					t.Fatal("different reservation context accepted")
				}
				l.close()
				return
			}
			l.close()
			name := filepath.Join(c.ScopeRoot, restorePlanFile)
			switch fault {
			case "bytes":
				if e := os.WriteFile(name, []byte(`{}`), 0600); e != nil {
					t.Fatal(e)
				}
			case "partial":
				if e := os.WriteFile(name, []byte(`{"format_version":`), 0600); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				if e := os.Remove(name); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(filepath.Join(c.ScopeRoot, markerName), name); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(name, filepath.Join(filepath.Dir(c.ScopeRoot), "outside-plan-link")); e != nil {
					t.Fatal(e)
				}
			case "missing":
				if e := os.Remove(name); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := Read(context.Background(), c); e == nil {
				t.Fatal("unsafe reserved plan accepted")
			}
		})
	}
}

func TestRestoreReservationAbortIntentReopensWithoutDuplicateIntent(t *testing.T) {
	c, l, r, source, target := restoreReservationFixture(t)
	if _, e := l.reserveRestore(context.Background(), r, source, target, "prepare-restore", func(p RestorePlan) error { return writeBackupFile(l.root, restorePlanFile, encode(p)) }); e != nil {
		t.Fatal(e)
	}
	s := restoreState(l.events)
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	if e = l.appendEvent(Event{OperationID: id, Action: "restore_abort_reservation", Outcome: "started", Restore: s.record}); e != nil {
		t.Fatal(e)
	}
	count := len(l.events)
	l.close()
	l, e = open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	out, e := l.abortRestoreReservation(r)
	if e != nil || out.Pending || len(l.events) != count+1 {
		t.Fatal("recorded abort intent not recovered", out, e)
	}
}

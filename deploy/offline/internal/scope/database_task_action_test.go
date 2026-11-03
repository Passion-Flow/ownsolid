package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit state/effect doubles; actual migration/SQL is a separate tagged test.
type databaseActionDouble struct {
	*scopedDatabaseTaskDouble
	process                ProcessSnapshot
	starts, stops, removes int
	actionFault            string
	oom                    bool
}

func (d *databaseActionDouble) CreateDatabaseTask(ctx context.Context, task engine.DatabaseTask) (engine.RuntimeContainer, error) {
	if task.Kind == engine.DatabaseSchemaCheck {
		d.id = strings.Repeat("f", 64)
	}
	if task.Kind == engine.DatabaseRuntimePrepare {
		d.id = strings.Repeat("1", 64)
	}
	if task.Kind == engine.DatabaseRuntimeCheck {
		d.id = strings.Repeat("2", 64)
	}
	if bootstrapKind(task.Kind) {
		d.id = strings.Repeat("3", 64)
	}
	if storeKind(task.Kind) {
		d.id = wire.Digest([]byte(task.Owner.OperationID))
	}
	r, e := d.scopedDatabaseTaskDouble.CreateDatabaseTask(ctx, task)
	if r.ID != "" {
		d.process = taskProcessSnapshot(r)
		d.oom = false
	}
	return r, e
}

func (d *databaseActionDouble) InspectDatabaseTask(ctx context.Context, task engine.DatabaseTask) (engine.RuntimeContainer, bool, error) {
	r, found, e := d.scopedDatabaseTaskDouble.InspectDatabaseTask(ctx, task)
	r.ID = d.process.ID
	r.State.Status = d.process.State
	r.State.ExitCode = d.process.ExitCode
	r.State.StartedAt = d.process.StartedAt
	r.State.Running = d.process.State == "running"
	r.State.PID = 0
	r.State.OOMKilled = d.oom
	if r.State.Running {
		r.State.PID = 42
	}
	return r, found, e
}
func (d *databaseActionDouble) InspectDatabaseTaskInput(_ context.Context, _ engine.DatabaseTask, _ string, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	if d.actionFault == "input" {
		return engine.ConfigurationObservation{}, errors.New("TEST_INPUT_READBACK_FAILED")
	}
	var total int64
	for _, f := range files {
		total += int64(len(f.Contents))
	}
	return engine.ConfigurationObservation{Status: "verified_private_volume_payload", Files: len(files), Bytes: total}, nil
}
func (d *databaseActionDouble) StartDatabaseTask(ctx context.Context, _ engine.DatabaseTask, _ string, _ []engine.ConfigurationFile, reserve int) (engine.ResourceAdmission, error) {
	d.starts++
	if d.actionFault == "before-start" {
		return engine.ResourceAdmission{}, errors.New("TEST_START_UNKNOWN")
	}
	d.process.State = "exited"
	d.process.StartedAt = "2026-10-17T00:00:02Z"
	d.process.ExitCode = 0
	if d.actionFault == "exit" {
		d.process.ExitCode = 1
	}
	if d.actionFault == "oom" {
		d.process.ExitCode = 137
		d.oom = true
	}
	if d.actionFault == "running" || d.actionFault == "running-lost" {
		d.process.State = "running"
	}
	if d.actionFault == "after-start" || d.actionFault == "running-lost" {
		return engine.ResourceAdmission{}, errors.New("TEST_START_RESPONSE_LOST")
	}
	return d.AdmitRuntimeResources(ctx, engine.ResourceRequirement{MemoryBytes: 64 << 20, MilliCPU: 100}, reserve)
}
func (d *databaseActionDouble) StopDatabaseTask(context.Context, engine.DatabaseTask, string, int) error {
	d.stops++
	if d.actionFault == "before-stop" {
		return errors.New("TEST_STOP_UNKNOWN")
	}
	d.process.State = "exited"
	d.process.ExitCode = 143
	if d.actionFault == "after-stop" {
		return errors.New("TEST_STOP_RESPONSE_LOST")
	}
	return nil
}
func (d *databaseActionDouble) RemoveDatabaseTask(context.Context, engine.DatabaseTask, string) error {
	d.removes++
	if d.actionFault == "before-remove" {
		return errors.New("TEST_REMOVE_UNKNOWN")
	}
	d.exists = false
	if d.actionFault == "after-remove" {
		return errors.New("TEST_REMOVE_RESPONSE_LOST")
	}
	return nil
}
func databaseActionFixture(t *testing.T, withBootstrap ...bool) (configuration.Config, *databaseActionDouble, *databaseWorkerDouble) {
	t.Helper()
	c, d, w := scopedDatabaseTaskFixture(t, withBootstrap...)
	if _, e := createDatabaseMigrationTask(context.Background(), c, d, w, false); e != nil {
		t.Fatal(e)
	}
	return c, &databaseActionDouble{scopedDatabaseTaskDouble: d, process: ProcessSnapshot{ID: d.id, State: "created", StartedAt: "0001-01-01T00:00:00Z"}}, w
}
func TestDatabaseMigrationActionSuccessRemovalAndNoReplay(t *testing.T) {
	c, d, w := databaseActionFixture(t)
	ctx := context.Background()
	r, e := databaseTaskAction(ctx, c, d, w, "execute", false)
	if e != nil || r.Pending || !r.ProcessSucceeded || d.starts != 1 {
		t.Fatal(r, e)
	}
	before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if _, e = databaseTaskAction(ctx, c, d, w, "execute", false); e != nil || d.starts != 1 {
		t.Fatal("start repeated", e)
	}
	after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(before, after) {
		t.Fatal("repeat changed journal")
	}
	d.refuse = true
	r, e = databaseTaskAction(ctx, c, d, w, "removed", false)
	if e != nil || !r.Removed || !r.ProcessSucceeded || d.removes != 1 {
		t.Fatal(r, e)
	}
	if _, e = databaseTaskAction(ctx, c, d, w, "removed", false); e != nil || d.removes != 1 {
		t.Fatal("removal repeated", e)
	}
	s, e := Read(ctx, c)
	if e != nil || !s.MigrationProcessSucceeded || !s.DatabaseTaskRemoved || s.DatabaseTaskPending {
		t.Fatal(s, e)
	}
	d.refuse = false
	if _, e = processRuntime(ctx, c, d, w, "running", false); e == nil || e.Error() != "OFFLINE_SCOPE_INDEPENDENT_SCHEMA_QUALIFICATION_REQUIRED" || w.starts != 0 {
		t.Fatal("application started before independent schema gate", e, w.starts)
	}
}
func TestDatabaseMigrationActionUnknownStartAndRemovalReconcile(t *testing.T) {
	for _, fault := range []string{"before-start", "after-start", "before-remove", "after-remove"} {
		t.Run(fault, func(t *testing.T) {
			c, d, w := databaseActionFixture(t)
			ctx := context.Background()
			target := "execute"
			if strings.Contains(fault, "remove") {
				if _, e := databaseTaskAction(ctx, c, d, w, "execute", false); e != nil {
					t.Fatal(e)
				}
				target = "removed"
			}
			d.actionFault = fault
			r, e := databaseTaskAction(ctx, c, d, w, target, false)
			if e == nil || !r.Pending {
				t.Fatal(r, e)
			}
			starts, removes := d.starts, d.removes
			if _, e = databaseTaskAction(ctx, c, d, w, target, false); e == nil || d.starts != starts || d.removes != removes {
				t.Fatal("unknown effect resent")
			}
			r, e = databaseTaskAction(ctx, c, d, w, "", true)
			if strings.HasPrefix(fault, "before") {
				if e == nil || !r.Pending {
					t.Fatal("unknown absence/state accepted")
				}
				if target == "execute" {
					d.process.State = "exited"
					d.process.StartedAt = "2026-10-17T00:00:02Z"
				} else {
					d.exists = false
				}
				r, e = databaseTaskAction(ctx, c, d, w, "", true)
			}
			if e != nil || r.Pending || d.starts != starts || d.removes != removes {
				t.Fatal(r, e)
			}
		})
	}
}
func TestDatabaseMigrationFailureOOMAndInputCannotBecomeSuccess(t *testing.T) {
	for _, fault := range []string{"exit", "oom", "input"} {
		t.Run(fault, func(t *testing.T) {
			c, d, w := databaseActionFixture(t)
			d.actionFault = fault
			r, e := databaseTaskAction(context.Background(), c, d, w, "execute", false)
			if e == nil || r.ProcessSucceeded {
				t.Fatal("failed migration succeeded", r, e)
			}
			starts := d.starts
			if _, e = databaseTaskAction(context.Background(), c, d, w, "execute", false); e == nil || d.starts != starts {
				t.Fatal("failed execute repeated")
			}
		})
	}
}
func TestDatabaseMigrationCancellationStopsOncePreservesFailure(t *testing.T) {
	for _, fault := range []string{"", "before-stop", "after-stop"} {
		t.Run(fault, func(t *testing.T) {
			c, d, w := databaseActionFixture(t)
			ctx := context.Background()
			d.actionFault = "running-lost"
			r, e := databaseTaskAction(ctx, c, d, w, "execute", false)
			if e == nil || !r.Pending {
				t.Fatal(r, e)
			}
			d.refuse = true
			d.actionFault = fault
			r, e = databaseTaskAction(ctx, c, d, w, "stopped", false)
			if fault == "" {
				if e != nil || r.ProcessSucceeded || r.Pending || d.stops != 1 {
					t.Fatal(r, e)
				}
			} else {
				if e == nil || !r.Pending || d.stops != 1 {
					t.Fatal(r, e)
				}
				if _, e = databaseTaskAction(ctx, c, d, w, "stopped", false); e == nil || d.stops != 1 {
					t.Fatal("unknown cancellation repeated")
				}
				if fault == "before-stop" {
					d.process.State = "exited"
					d.process.ExitCode = 143
				}
				r, e = databaseTaskAction(ctx, c, d, w, "", true)
				if e == nil || r.Pending || r.ProcessSucceeded {
					t.Fatal("cancel failed migration not preserved", r, e)
				}
			}
			if _, e = databaseTaskAction(ctx, c, d, w, "execute", false); e == nil || d.starts != 1 {
				t.Fatal("cancelled migration repeated")
			}
		})
	}
}

func TestDatabaseMigrationActionJournalRejectsForgedBindingsAndOutcomes(t *testing.T) {
	c, d, w := databaseActionFixture(t)
	d.actionFault = "after-start"
	if _, e := databaseTaskAction(context.Background(), c, d, w, "execute", false); e == nil {
		t.Fatal("expected uncertain start")
	}
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	s := databaseTaskActionState(l.events)
	if s.record == nil || s.pending == "" {
		t.Fatal("missing pending action")
	}
	for _, change := range []func(*DatabaseTaskActionRecord){func(r *DatabaseTaskActionRecord) { r.CreationSHA256 = strings.Repeat("0", 64) }, func(r *DatabaseTaskActionRecord) { r.Before.ID = strings.Repeat("f", 64) }, func(r *DatabaseTaskActionRecord) { r.Admission.Requirement.MemoryBytes++ }, func(r *DatabaseTaskActionRecord) { r.After.StartedAt = "2026-10-17T00:00:03Z" }, func(r *DatabaseTaskActionRecord) { r.InputVerified = false }, func(r *DatabaseTaskActionRecord) { r.After.ExitCode = 1 }, func(r *DatabaseTaskActionRecord) { r.OOMKilled = true }} {
		var r DatabaseTaskActionRecord
		if json.Unmarshal(encode(*s.record), &r) != nil {
			t.Fatal("record copy")
		}
		change(&r)
		if e = l.appendEvent(Event{OperationID: strings.Repeat("9", 32), Action: "database_task_action_reconcile", ReferenceOperationID: s.pending, Outcome: "completed", DatabaseTaskAction: &r}); e == nil {
			t.Fatal("forged action accepted")
		}
	}
	r := *s.record
	if e = l.appendEvent(Event{OperationID: strings.Repeat("9", 32), Action: "database_task_action_reconcile", ReferenceOperationID: strings.Repeat("8", 32), Outcome: "completed", DatabaseTaskAction: &r}); e == nil {
		t.Fatal("wrong reference accepted")
	}
	if e = l.appendEvent(Event{OperationID: strings.Repeat("9", 32), Action: "database_task_action_reconcile", ReferenceOperationID: s.pending, Outcome: "completed", DatabaseTaskAction: &r, ModelTask: &ModelTaskRecord{}}); e == nil {
		t.Fatal("mixed task event accepted")
	}
	if e = l.appendEvent(Event{OperationID: strings.Repeat("9", 32), Action: "process_stop", Outcome: "started"}); e == nil {
		t.Fatal("pending action allowed unrelated effect")
	}
	policy := *c.Engine.Utility
	policy.MemoryBytes++
	copyConfig := c
	engineConfig := *c.Engine
	engineConfig.Utility = &policy
	copyConfig.Engine = &engineConfig
	if e = validateDatabaseTaskActionInputs(l.events, copyConfig); e == nil {
		t.Fatal("changed resource policy accepted")
	}
}

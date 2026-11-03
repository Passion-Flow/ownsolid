package scope

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

// Explicit SDK/counter doubles exercise real scope files and journal transitions.
type taskActionDouble struct {
	*modelTaskDouble
	snapshot                                  *ProcessSnapshot
	oom                                       bool
	fault                                     string
	refuse                                    bool
	starts, stops, removes, reads, admissions int
}

func (d *taskActionDouble) InspectModelTask(ctx context.Context, t engine.ModelStoreTask) (engine.RuntimeContainer, bool, error) {
	r, found, e := d.modelTaskDouble.InspectModelTask(ctx, t)
	if d.snapshot != nil {
		r.ID = d.snapshot.ID
		r.State.Status = d.snapshot.State
		r.State.ExitCode = d.snapshot.ExitCode
		r.State.StartedAt = d.snapshot.StartedAt
		r.State.Running = d.snapshot.State == "running"
		if r.State.Running {
			r.State.PID = 123
		} else {
			r.State.PID = 0
		}
		r.State.OOMKilled = d.oom
	}
	return r, found, e
}
func (d *taskActionDouble) AdmitRuntimeResources(ctx context.Context, r engine.ResourceRequirement, reserve int) (engine.ResourceAdmission, error) {
	d.admissions++
	return (&processEngineDouble{refuse: d.refuse}).AdmitRuntimeResources(ctx, r, reserve)
}
func (d *taskActionDouble) StartModelTask(context.Context, engine.ModelStoreTask, string, int) (engine.ResourceAdmission, error) {
	d.starts++
	if d.fault == "before-start" {
		return engine.ResourceAdmission{}, errors.New("EXPLICIT_TEST_START_OUTCOME_UNKNOWN")
	}
	d.snapshot = &ProcessSnapshot{ID: d.taskID, State: "exited", StartedAt: "2026-10-17T00:00:01Z"}
	if d.fault == "running" {
		d.snapshot.State = "running"
	}
	if d.fault == "failed" {
		d.snapshot.ExitCode = 1
	}
	if d.fault == "oom" {
		d.snapshot.ExitCode = 137
		d.oom = true
	}
	if d.fault == "after-start" {
		return engine.ResourceAdmission{}, errors.New("EXPLICIT_TEST_START_RESPONSE_LOST")
	}
	return engine.ResourceAdmission{}, nil
}
func (d *taskActionDouble) StopModelTask(context.Context, engine.ModelStoreTask, string, int) error {
	d.stops++
	if d.fault == "before-stop" {
		return errors.New("EXPLICIT_TEST_STOP_OUTCOME_UNKNOWN")
	}
	d.snapshot.State = "exited"
	d.snapshot.ExitCode = 137
	if d.fault == "after-stop" {
		return errors.New("EXPLICIT_TEST_STOP_RESPONSE_LOST")
	}
	return nil
}
func (d *taskActionDouble) RemoveModelTask(context.Context, engine.ModelStoreTask, string) error {
	d.removes++
	if d.fault == "before-remove" {
		return errors.New("EXPLICIT_TEST_REMOVE_OUTCOME_UNKNOWN")
	}
	d.task = false
	if d.fault == "after-remove" {
		return errors.New("EXPLICIT_TEST_REMOVE_RESPONSE_LOST")
	}
	return nil
}
func (d *taskActionDouble) InspectModelTaskStorage(context.Context, engine.ModelStoreTask, string, []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	d.reads++
	if d.fault == "readback" {
		return engine.ConfigurationObservation{}, errors.New("EXPLICIT_TEST_RESULT_READBACK_UNAVAILABLE")
	}
	return engine.ConfigurationObservation{Status: "verified_private_volume_payload"}, nil
}
func (d *taskActionDouble) VerifyVolumeConsumers(context.Context, engine.Ownership, []string) error {
	if d.fault == "consumer" {
		return errors.New("EXPLICIT_TEST_FOREIGN_VOLUME_CONSUMER")
	}
	return nil
}
func taskActionFixture(t *testing.T) (configuration.Config, *taskActionDouble) {
	t.Helper()
	c, d := scopedModelFixture(t)
	if _, e := createModelTask(context.Background(), c, d, false); e != nil {
		t.Fatal(e)
	}
	return c, &taskActionDouble{modelTaskDouble: d}
}
func TestModelTaskLifecycleExecuteVerifiedRemoveAndRepeat(t *testing.T) {
	c, d := taskActionFixture(t)
	ctx := context.Background()
	r, e := modelTaskAction(ctx, c, d, "execute", false)
	if e != nil || r.Pending || !r.Initialized || d.starts != 1 || d.reads != 1 {
		t.Fatal(r, e)
	}
	if _, e = modelTaskAction(ctx, c, d, "execute", false); e != nil || d.starts != 1 {
		t.Fatal("initializer replay", e)
	}
	s, e := Read(ctx, c)
	if e != nil || !s.ModelDirectoryInitialized || s.ModelTaskRemoved || s.ModelTaskPending {
		t.Fatal(s, e)
	}
	d.refuse = true
	if _, e = modelTaskAction(ctx, c, d, "stopped", false); e != nil || d.stops != 0 {
		t.Fatal("already-stopped effect", e)
	}
	r, e = modelTaskAction(ctx, c, d, "removed", false)
	if e != nil || !r.Removed || !r.Initialized || d.removes != 1 || d.admissions != 1 {
		t.Fatal(r, e)
	}
	if _, e = modelTaskAction(ctx, c, d, "removed", false); e != nil || d.removes != 1 {
		t.Fatal("removal replay", e)
	}
	if _, e = modelTaskAction(ctx, c, d, "execute", false); e == nil || d.starts != 1 {
		t.Fatal("removed initializer replay")
	}
	s, e = Read(ctx, c)
	if e != nil || !s.ModelDirectoryInitialized || !s.ModelTaskRemoved || s.ModelTaskPending {
		t.Fatal(s, e)
	}
}
func TestModelTaskLifecycleStartUnknownRequiresReadonlyReconciliation(t *testing.T) {
	for _, fault := range []string{"before-start", "after-start", "running", "readback"} {
		t.Run(fault, func(t *testing.T) {
			c, d := taskActionFixture(t)
			d.fault = fault
			ctx := context.Background()
			r, e := modelTaskAction(ctx, c, d, "execute", false)
			if e == nil || !r.Pending || r.Initialized || d.starts != 1 {
				t.Fatal(r, e)
			}
			if _, e = modelTaskAction(ctx, c, d, "execute", false); e == nil || d.starts != 1 {
				t.Fatal("unknown start replay")
			}
			r, e = modelTaskAction(ctx, c, d, "", true)
			if fault == "after-start" {
				if e != nil || r.Pending || !r.Initialized {
					t.Fatal(r, e)
				}
			} else {
				if e == nil || !r.Pending {
					t.Fatal("unresolved start incorrectly passed", r, e)
				}
				d.fault = ""
				d.snapshot = &ProcessSnapshot{ID: d.taskID, State: "exited", StartedAt: "2026-10-17T00:00:01Z"}
				r, e = modelTaskAction(ctx, c, d, "", true)
				if e != nil || r.Pending || !r.Initialized {
					t.Fatal(r, e)
				}
			}
			if d.starts != 1 {
				t.Fatal("reconciliation started initializer")
			}
		})
	}
}
func TestModelTaskLifecycleFailedExitNeverBecomesInitializedAfterCleanup(t *testing.T) {
	for _, fault := range []string{"failed", "oom"} {
		t.Run(fault, func(t *testing.T) {
			c, d := taskActionFixture(t)
			d.fault = fault
			ctx := context.Background()
			r, e := modelTaskAction(ctx, c, d, "execute", false)
			if e == nil || r.Pending || r.Initialized || d.reads != 0 {
				t.Fatal(r, e)
			}
			if _, e = modelTaskAction(ctx, c, d, "execute", false); e == nil || d.starts != 1 {
				t.Fatal("failed initializer replay")
			}
			d.refuse = true
			r, e = modelTaskAction(ctx, c, d, "removed", false)
			if e != nil || !r.Removed || r.Initialized {
				t.Fatal(r, e)
			}
			s, e := Read(ctx, c)
			if e != nil || s.ModelDirectoryInitialized || !s.ModelTaskRemoved {
				t.Fatal(s, e)
			}
		})
	}
}
func TestModelTaskLifecycleRemovalUncertaintyNeverResends(t *testing.T) {
	for _, fault := range []string{"before-remove", "after-remove"} {
		t.Run(fault, func(t *testing.T) {
			c, d := taskActionFixture(t)
			ctx := context.Background()
			d.fault = fault
			r, e := modelTaskAction(ctx, c, d, "removed", false)
			if e == nil || !r.Pending || d.removes != 1 {
				t.Fatal(r, e)
			}
			if _, e = modelTaskAction(ctx, c, d, "removed", false); e == nil || d.removes != 1 {
				t.Fatal("unknown remove replay")
			}
			r, e = modelTaskAction(ctx, c, d, "", true)
			if fault == "before-remove" {
				if e == nil || !r.Pending {
					t.Fatal(r, e)
				}
				d.task = false
				r, e = modelTaskAction(ctx, c, d, "", true)
			}
			if e != nil || r.Pending || !r.Removed || r.Initialized || d.removes != 1 {
				t.Fatal(r, e)
			}
		})
	}
}
func TestModelTaskLifecycleAdmissionCancellationAndChangedExecution(t *testing.T) {
	c, d := taskActionFixture(t)
	ctx := context.Background()
	before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	d.refuse = true
	if _, e := modelTaskAction(ctx, c, d, "execute", false); e == nil || d.starts != 0 {
		t.Fatal("refused admission submitted start")
	}
	after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if string(before) != string(after) {
		t.Fatal("refused admission wrote intent")
	}
	d.refuse = false
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := modelTaskAction(canceled, c, d, "execute", false); e == nil || d.starts != 0 {
		t.Fatal("canceled start submitted")
	}
	if _, e := modelTaskAction(ctx, c, d, "execute", false); e != nil {
		t.Fatal(e)
	}
	d.snapshot.StartedAt = "2026-10-17T00:00:02Z"
	if _, e := modelTaskAction(ctx, c, d, "removed", false); e == nil || d.removes != 0 {
		t.Fatal("changed execution removed")
	}
	d.snapshot.ID = strings.Repeat("f", 64)
	if _, e := modelTaskAction(ctx, c, d, "execute", false); e == nil || d.starts != 1 {
		t.Fatal("changed ID accepted")
	}
}

func TestModelTaskLifecycleExplicitStopOfPendingExecution(t *testing.T) {
	for _, fault := range []string{"", "after-stop"} {
		t.Run(fault, func(t *testing.T) {
			c, d := taskActionFixture(t)
			ctx := context.Background()
			d.fault = "running"
			if r, e := modelTaskAction(ctx, c, d, "execute", false); e == nil || !r.Pending {
				t.Fatal(r, e)
			}
			d.refuse = true
			d.fault = fault
			r, e := modelTaskAction(ctx, c, d, "stopped", false)
			if d.stops != 1 || d.admissions != 1 || r.Initialized {
				t.Fatal("stop admission or fake initialization", r, e)
			}
			if fault == "after-stop" {
				if e == nil || !r.Pending {
					t.Fatal("lost stop accepted", r, e)
				}
				if _, e = modelTaskAction(ctx, c, d, "stopped", false); e == nil || d.stops != 1 {
					t.Fatal("uncertain cancellation resent")
				}
				r, e = modelTaskAction(ctx, c, d, "", true)
				if e == nil || r.Pending || r.Initialized {
					t.Fatal("canceled initializer outcome", r, e)
				}
			} else if e != nil || r.Pending || r.Initialized || r.Status != "recorded_model_initializer_canceled_and_stopped" {
				t.Fatal(r, e)
			}
			s, e := Read(ctx, c)
			if e != nil || s.ModelDirectoryInitialized || s.ModelTaskPending || s.Status != "isolated_scope_model_initializer_failed" {
				t.Fatal(s, e)
			}
			if _, e = modelTaskAction(ctx, c, d, "removed", false); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestModelTaskLifecycleIndependentStopAndLostStopOutcome(t *testing.T) {
	for _, fault := range []string{"", "after-stop"} {
		t.Run(fault, func(t *testing.T) {
			c, d := taskActionFixture(t)
			ctx := context.Background()
			d.snapshot = &ProcessSnapshot{ID: d.taskID, State: "running", StartedAt: "2026-10-17T00:00:01Z"}
			d.fault = fault
			d.refuse = true
			r, e := modelTaskAction(ctx, c, d, "stopped", false)
			if d.stops != 1 || d.admissions != 0 || r.Initialized {
				t.Fatal(r, e)
			}
			if fault == "after-stop" {
				if e == nil || !r.Pending {
					t.Fatal(r, e)
				}
				r, e = modelTaskAction(ctx, c, d, "", true)
			}
			if e != nil || r.Pending || r.Initialized {
				t.Fatal(r, e)
			}
		})
	}
}

func TestModelTaskLifecycleForgedActionAndPolicyRefusal(t *testing.T) {
	c, d := taskActionFixture(t)
	ctx := context.Background()
	d.fault = "after-start"
	modelTaskAction(ctx, c, d, "execute", false)
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	events := l.events
	terminal := events[len(events)-1]
	prefix := events[:len(events)-1]
	for _, change := range []func(*Event){
		func(e *Event) { e.Action = "model_task_stop" }, func(e *Event) { e.ModelTaskAction.CreationSHA256 = strings.Repeat("f", 64) }, func(e *Event) { e.ModelTaskAction.Before.ID = strings.Repeat("f", 64) }, func(e *Event) { e.ModelTaskAction.After.StartedAt = e.ModelTaskAction.Before.StartedAt }, func(e *Event) { e.ModelTaskAction.After.ID = strings.Repeat("f", 64) }, func(e *Event) { e.ModelTaskAction.Admission.ReservePercent++ }, func(e *Event) { e.ModelTaskAction.ResultVerified = false; e.Outcome = "completed"; e.Code = "" }, func(e *Event) { e.ModelTask = modelTaskState(events).record }, func(e *Event) { e.ReferenceOperationID = e.OperationID },
	} {
		copy := terminal
		r := *terminal.ModelTaskAction
		after := *r.After
		admission := *r.Admission
		r.After = &after
		r.Admission = &admission
		copy.ModelTaskAction = &r
		change(&copy)
		if validateModelTaskActionEvent(prefix, copy) == nil {
			t.Fatal("forged action accepted")
		}
	}
	changed := c
	resources := *c.Engine
	utility := *c.Engine.Utility
	utility.MilliCPU++
	resources.Utility = &utility
	changed.Engine = &resources
	if validateModelTaskActionInputs(events, changed) == nil {
		t.Fatal("changed runtime admission policy accepted")
	}
}

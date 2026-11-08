package training_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/scheduling"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func schedulingTraining(t *testing.T) (scenario, training.Job) {
	t.Helper()
	f := setup(t)
	for _, name := range []string{"quota.sql", "reservations.sql"} {
		ddl, err := os.ReadFile(filepath.Join("..", "scheduling", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		schedulingSQL(t, f, string(ddl))
	}
	f.s.EnableSchedulingCandidateForTest()
	hardware(t, f)
	return f, queued(t, f)
}

func schedulingSQL(t *testing.T, f scenario, statement string, args ...any) {
	t.Helper()
	if _, err := f.s.DB.Exec(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
}

func schedulingQuota(t *testing.T, f scenario, revision, memory int64, includeWorkspace bool) {
	t.Helper()
	tenant := f.portal.Me.Workspace.TenantID
	limits := scheduling.Quota{Burst: scheduling.Resources{scheduling.NVIDIAGPU: 1, scheduling.Memory: memory}}
	policy := []scheduling.QuotaNode{
		{ID: "platform", Level: scheduling.PlatformQuota, Limits: limits},
		{ID: tenant, ParentID: "platform", TenantID: tenant, Level: scheduling.TenantQuota, Limits: limits},
		{ID: "training", ParentID: tenant, TenantID: tenant, Level: scheduling.DepartmentQuota, Limits: limits},
	}
	if includeWorkspace {
		policy = append(policy, scheduling.QuotaNode{
			ID: f.input.WorkspaceID, ParentID: "training", TenantID: tenant, Level: scheduling.WorkspaceQuota, Limits: limits,
		})
	}
	ctx := t.Context()
	if err := pgx.BeginFunc(ctx, f.s.DB, func(tx pgx.Tx) error {
		next, changed, err := scheduling.ReplaceQuotaWithin(ctx, tx, revision, policy)
		if err == nil && (!changed || next != revision+1) {
			t.Errorf("quota setup: revision=%d changed=%t", next, changed)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func schedulingDispatchState(t *testing.T, f scenario, job string, state, wait string, rows int) training.Job {
	t.Helper()
	got, err := f.s.Get(t.Context(), f.portal, f.input.WorkspaceID, job)
	if err != nil || got.State != state || (wait == "" && got.WaitCode != nil) ||
		(wait != "" && (got.WaitCode == nil || *got.WaitCode != wait)) {
		t.Fatalf("dispatch state=%q wait=%v error=%v, want %q %q", got.State, got.WaitCode, err, state, wait)
	}
	for _, check := range []struct {
		name, query string
		arg         string
	}{
		{"attempt", "SELECT count(*) FROM training_attempts WHERE job_id=$1", job},
		{"physical lease", "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1", job},
		{"scheduling reservation", "SELECT count(*) FROM scheduling_reservations WHERE source_id=$1", job},
		{"training task", "SELECT count(*) FROM node_tasks WHERE node_id=$1 AND operation='training.run.v1'", f.input.NodeID},
	} {
		if n := count(t, f, check.query, check.arg); n != rows {
			t.Fatalf("%s rows=%d, want %d", check.name, n, rows)
		}
	}
	return got
}

func schedulingDispatchDue(t *testing.T, f scenario, job string) {
	t.Helper()
	schedulingSQL(t, f, "UPDATE training_jobs SET next_dispatch_at=clock_timestamp() WHERE id=$1", job)
}

func schedulingRejectAudit(t *testing.T, f scenario, action string) {
	t.Helper()
	schedulingSQL(t, f, `CREATE FUNCTION reject_scheduling_test_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.action = TG_ARGV[0] THEN RAISE EXCEPTION 'SYNTHETIC_SCHEDULING_AUDIT_FAILURE'; END IF;
  RETURN NEW;
END $$`)
	schedulingSQL(t, f, "CREATE TRIGGER reject_scheduling_test_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_scheduling_test_audit("+"'"+strings.ReplaceAll(action, "'", "''")+"')")
}

func schedulingHoldState(t *testing.T, f scenario, job training.Job, gpuState string, released bool) {
	t.Helper()
	if job.CurrentAttemptID == nil {
		t.Fatal("dispatched job has no attempt")
	}
	if count(t, f, `SELECT count(*) FROM scheduling_reservations
 WHERE tenant_id=$1 AND workspace_id=$2 AND source_id=$3 AND kind='training'
 AND attempt_id=$4 AND attempt_number=$5 AND (released_at IS NOT NULL)=$6`,
		f.portal.Me.Workspace.TenantID, f.input.WorkspaceID, job.ID, *job.CurrentAttemptID, job.AttemptNumber, released) != 1 ||
		count(t, f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND revision=$2 AND state=$3", job.ID, job.AttemptNumber, gpuState) != 1 {
		t.Fatalf("reservation and GPU did not settle together for %s, released=%t", gpuState, released)
	}
}

func TestActualTrainingSchedulingDispatchQuotaWaitSavepointAndRetry(t *testing.T) {
	f, job := schedulingTraining(t)
	ctx := t.Context()
	schedulingDispatchState(t, f, job.ID, "queued", "", 0)
	wait := func(code string) {
		t.Helper()
		if worked, err := f.s.DispatchNext(ctx); err != nil || worked {
			t.Fatalf("denied dispatch %q: %t %v", code, worked, err)
		}
		schedulingDispatchState(t, f, job.ID, "queued", code, 0)
		schedulingDispatchDue(t, f, job.ID)
	}
	memory := job.Recipe.Resources.MemoryBytes
	wait("TRAINING_SCHEDULING_QUOTA_UNCONFIGURED")
	schedulingQuota(t, f, 0, memory, false)
	wait("TRAINING_SCHEDULING_QUOTA_SCOPE_MISSING")
	schedulingQuota(t, f, 1, memory-1, true)
	wait("TRAINING_SCHEDULING_QUOTA_EXCEEDED")
	schedulingQuota(t, f, 2, memory, true)

	busy, err := f.s.Nodes.CreateTask(ctx, f.admin, f.input.NodeID, agentwire.CollectFacts, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal("create competing node task", err)
	}
	wait("NODE_BUSY")
	schedulingSQL(t, f, "UPDATE node_tasks SET state='cancelled',completed_at=clock_timestamp() WHERE id=$1", busy.ID)

	schedulingRejectAudit(t, f, "training.dispatched")
	if worked, err := f.s.DispatchNext(ctx); err == nil || worked {
		t.Fatalf("late audit failure reported dispatch: %t %v", worked, err)
	}
	schedulingDispatchState(t, f, job.ID, "queued", "NODE_BUSY", 0)
	schedulingSQL(t, f, "DROP TRIGGER reject_scheduling_test_audit ON audit_events")

	if worked, err := f.s.DispatchNext(ctx); err != nil || !worked {
		t.Fatalf("dispatch after rollback: %t %v", worked, err)
	}
	job = schedulingDispatchState(t, f, job.ID, "preparing", "", 1)
	schedulingHoldState(t, f, job, "held", false)
	if count(t, f, `SELECT count(*) FROM scheduling_reservations WHERE attempt_id=$1
 AND demand=jsonb_build_object('nvidia.com/gpu',1,'memory',$2::bigint)`, *job.CurrentAttemptID, memory) != 1 {
		t.Fatal("reservation differs from the frozen GPU and memory demand")
	}
	if worked, err := f.s.DispatchNext(ctx); err != nil || worked {
		t.Fatalf("dispatch retry changed an accepted attempt: %t %v", worked, err)
	}
	replayed := schedulingDispatchState(t, f, job.ID, "preparing", "", 1)
	if replayed.CurrentAttemptID == nil || *replayed.CurrentAttemptID != *job.CurrentAttemptID ||
		count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.dispatched' AND target_id=$1", job.ID) != 1 {
		t.Fatal("dispatch retry changed attempt or audit identity")
	}
}

func TestActualTrainingSchedulingStopKeepsUncertainHoldAndReplaysExactRelease(t *testing.T) {
	f, job := schedulingTraining(t)
	ctx := t.Context()
	schedulingQuota(t, f, 0, job.Recipe.Resources.MemoryBytes, true)
	if worked, err := f.s.DispatchNext(ctx); err != nil || !worked {
		t.Fatalf("dispatch: %t %v", worked, err)
	}
	job = schedulingDispatchState(t, f, job.ID, "preparing", "", 1)
	lease := claimed(t, f)
	receipt := stopFixture(t, f, lease)
	schedulingSQL(t, f, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second',deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", lease.TaskID)
	schedulingDispatchDue(t, f, job.ID)
	if worked, err := f.s.ReconcileNext(ctx); err != nil || !worked {
		t.Fatalf("fence delivered timeout: %t %v", worked, err)
	}
	schedulingHoldState(t, f, job, "recovery_required", false)

	other := uuid.NewString()
	schedulingSQL(t, f, "UPDATE scheduling_reservations SET attempt_id=$2 WHERE attempt_id=$1", lease.Training.AttemptID, other)
	out, err := f.s.RecordStop(ctx, f.input.NodeID, f.credential, lease.TaskID, lease.LeaseToken, receipt, nil)
	expected(t, err, "TRAINING_SCHEDULING_RESERVATION_INVALID")
	if out.GPUReleased || count(t, f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='recovery_required'", job.ID) != 1 ||
		count(t, f, "SELECT count(*) FROM training_attempts WHERE id=$1 AND physical_stopped_at IS NULL", lease.Training.AttemptID) != 1 {
		t.Fatal("stop without the exact hold changed owner state")
	}
	schedulingSQL(t, f, "UPDATE scheduling_reservations SET attempt_id=$2 WHERE attempt_id=$1", other, lease.Training.AttemptID)

	schedulingRejectAudit(t, f, "training.physical_stop_accepted")
	if out, err := f.s.RecordStop(ctx, f.input.NodeID, f.credential, lease.TaskID, lease.LeaseToken, receipt, nil); err == nil || out.GPUReleased {
		t.Fatalf("failed audit reported release: %+v %v", out, err)
	}
	schedulingHoldState(t, f, job, "recovery_required", false)
	schedulingSQL(t, f, "DROP TRIGGER reject_scheduling_test_audit ON audit_events")
	out, err = f.s.RecordStop(ctx, f.input.NodeID, f.credential, lease.TaskID, lease.LeaseToken, receipt, nil)
	if err != nil || !out.GPUReleased || out.Duplicate || out.AttemptID != lease.Training.AttemptID {
		t.Fatalf("exact stop: %+v %v", out, err)
	}
	schedulingHoldState(t, f, job, "released", true)
	out, err = f.s.RecordStop(ctx, f.input.NodeID, f.credential, lease.TaskID, lease.LeaseToken, receipt, nil)
	if err != nil || !out.Duplicate || count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.physical_stop_accepted' AND target_id=$1", job.ID) != 1 {
		t.Fatalf("exact stop replay: %+v %v", out, err)
	}
}

func TestActualTrainingSchedulingNeverDeliveredReleaseIsAtomic(t *testing.T) {
	f, job := schedulingTraining(t)
	ctx := t.Context()
	schedulingQuota(t, f, 0, job.Recipe.Resources.MemoryBytes, true)
	if worked, err := f.s.DispatchNext(ctx); err != nil || !worked {
		t.Fatalf("dispatch: %t %v", worked, err)
	}
	job = schedulingDispatchState(t, f, job.ID, "preparing", "", 1)
	if _, err := f.s.Cancel(ctx, f.portal, f.input.WorkspaceID, job.ID,
		training.ActionRequest{ExpectedRevision: job.Revision, Reason: "Cancel before command delivery"}, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	schedulingHoldState(t, f, job, "held", false)
	schedulingRejectAudit(t, f, "training.undelivered_stopped")
	if worked, err := f.s.ReconcileNext(ctx); err == nil || worked {
		t.Fatalf("failed audit reported reconciliation: %t %v", worked, err)
	}
	schedulingHoldState(t, f, job, "held", false)
	schedulingSQL(t, f, "DROP TRIGGER reject_scheduling_test_audit ON audit_events")
	if worked, err := f.s.ReconcileNext(ctx); err != nil || !worked {
		t.Fatalf("reconcile exact never-delivered attempt: %t %v", worked, err)
	}
	schedulingHoldState(t, f, job, "released", true)
	if count(t, f, "SELECT count(*) FROM training_attempts WHERE id=$1 AND physical_stop_receipt->>'proof'='control_never_delivered'", *job.CurrentAttemptID) != 1 {
		t.Fatal("release lacks proof of non-delivery")
	}
	if worked, err := f.s.ReconcileNext(ctx); err != nil || worked ||
		count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.undelivered_stopped' AND target_id=$1", job.ID) != 1 {
		t.Fatalf("never-delivered replay: %t %v", worked, err)
	}
}

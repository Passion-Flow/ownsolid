package training_test

import (
	"context"
	"sync"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/google/uuid"
)

func stoppedJob(t *testing.T, f scenario) training.Job {
	t.Helper()
	dispatched(t, f)
	l := claimed(t, f)
	if _, e := f.s.RecordStop(context.Background(), f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, stopFixture(t, f, l), nil); e != nil {
		t.Fatal(e)
	}
	j, e := f.s.Get(context.Background(), f.portal, f.input.WorkspaceID, l.Training.Scope.JobID)
	if e != nil {
		t.Fatal(e)
	}
	return j
}

func TestActualTrainingRecoveryFreshEnrollmentPreservesOriginalNodeAndRequiresSameGPU(t *testing.T) {
	f := setup(t)
	j := stoppedJob(t, f)
	ctx := context.Background()
	old, e := f.s.Nodes.Get(ctx, f.admin, "", f.input.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	join, e := f.s.Nodes.MintForProfile(ctx, f.admin, f.input.WorkspaceID, "Explicit fresh synthetic training enrollment", agentwire.DockerAIProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := old.Facts
	facts.ObservedAt = time.Now().UTC()
	enrolled, e := f.s.Nodes.Enroll(ctx, agentwire.Enrollment{JoinToken: join.Token, Handshake: agentwire.Handshake{ProtocolVersion: agentwire.AIVersion, ExecutionProfile: agentwire.DockerAIProfile, Capabilities: agentwire.Capabilities(agentwire.DockerAIProfile), Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	newTarget := f
	newTarget.input.NodeID = enrolled.NodeID
	newTarget.credential = enrolled.Credential
	wrongGPU := newTarget
	wrongGPU.input.GPUUUID = "GPU-" + uuid.NewString()
	hardware(t, wrongGPU)
	input := training.RecoveryRequest{ExpectedRevision: j.Revision, Reason: "Explicit same-physical-GPU fresh enrollment after exact old stop", NodeID: enrolled.NodeID, Mode: "restart"}
	_, e = f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, input, uuid.NewString(), uuid.NewString())
	expected(t, e, "TRAINING_GPU_UNAVAILABLE")
	hardware(t, newTarget)
	queued, e := f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, input, uuid.NewString(), uuid.NewString())
	if e != nil || queued.NodeID != old.ID || queued.CurrentNodeID != enrolled.NodeID {
		t.Fatal("fresh recovery overwrote initial provenance", e)
	}
	if worked, e := f.s.DispatchNext(ctx); e != nil || !worked {
		t.Fatal("fresh enrollment dispatch", e)
	}
	next := claimed(t, newTarget)
	if count(t, f, "SELECT count(*) FROM training_attempts WHERE id=$1 AND node_id=$2", *j.CurrentAttemptID, old.ID) != 1 || count(t, f, "SELECT count(*) FROM training_attempts WHERE id=$1 AND node_id=$2", next.Training.AttemptID, enrolled.NodeID) != 1 || count(t, f, "SELECT count(*) FROM gpu_leases WHERE node_id=$1 AND revision=2 AND state='held'", enrolled.NodeID) != 1 {
		t.Fatal("new physical owner/history scope incorrect")
	}
	if count(t, f, "SELECT count(*) FROM training_recoveries WHERE previous_node_id=$1 AND next_node_id=$2", old.ID, enrolled.NodeID) != 1 {
		t.Fatal("fresh enrollment recovery lineage missing")
	}
}

func TestActualTrainingRecoveryCurrentRoleEpochScopeAndAttemptLimitDenyWithoutEffects(t *testing.T) {
	f := setup(t)
	j := stoppedJob(t, f)
	ctx := context.Background()
	input := training.RecoveryRequest{ExpectedRevision: j.Revision, Reason: "Current authority and frozen attempt policy", NodeID: f.input.NodeID, Mode: "restart"}
	if _, e := f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	_, e := f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, input, uuid.NewString(), uuid.NewString())
	expected(t, e, "ACCESS_DENIED")
	if _, e = f.s.Recover(ctx, f.admin, uuid.NewString(), j.ID, input, uuid.NewString(), uuid.NewString()); e == nil {
		t.Fatal("foreign recovery scope accepted")
	}
	policy := f.s.Identity.Config.Policy()
	policy.Training.MaxAttempts = 1
	f.s.Identity.Config.Runtime = &policy
	_, e = f.s.Recover(ctx, f.admin, f.input.WorkspaceID, j.ID, input, uuid.NewString(), uuid.NewString())
	expected(t, e, "TRAINING_ATTEMPTS_EXHAUSTED")
	if _, e = f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.admin.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Recover(ctx, f.admin, f.input.WorkspaceID, j.ID, input, uuid.NewString(), uuid.NewString()); e == nil {
		t.Fatal("stale epoch recovered training")
	}
	if count(t, f, "SELECT count(*) FROM training_recoveries") != 0 || count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='released'") != 1 {
		t.Fatal("rejected recovery changed history/physical ownership")
	}
}

func TestActualTrainingExplicitRestartRecoveryIntentReplayAuditRollbackAndDispatch(t *testing.T) {
	f := setup(t)
	j := stoppedJob(t, f)
	ctx := context.Background()
	input := training.RecoveryRequest{ExpectedRevision: j.Revision, Reason: "Explicit restart of a physically stopped synthetic attempt", NodeID: f.input.NodeID, Mode: "restart"}
	key := uuid.NewString()
	if _, e := f.s.DB.Exec(ctx, `CREATE FUNCTION reject_recovery() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='training.recovery_requested' THEN RAISE EXCEPTION 'SYNTHETIC_RECOVERY_AUDIT_FAILURE'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_recovery BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_recovery()`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, input, key, uuid.NewString()); e == nil {
		t.Fatal("failed recovery audit reported success")
	}
	if count(t, f, "SELECT count(*) FROM training_recoveries") != 0 || count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='released'") != 1 {
		t.Fatal("audit failure retained recovery/resource effects")
	}
	if _, e := f.s.DB.Exec(ctx, "DROP TRIGGER reject_recovery ON audit_events"); e != nil {
		t.Fatal(e)
	}
	first, e := f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, input, key, uuid.NewString())
	if e != nil || first.State != "queued" || first.PendingRecoveryID == nil || first.NodeID != j.NodeID || first.AttemptNumber != 1 {
		t.Fatal("actual explicit restart recovery", e)
	}
	again, e := f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, input, key, uuid.NewString())
	if e != nil || again.PendingRecoveryID == nil || *again.PendingRecoveryID != *first.PendingRecoveryID {
		t.Fatal("recovery replay created new intent", e)
	}
	changed := input
	changed.Reason = "Changed recovery replay"
	_, e = f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, changed, key, uuid.NewString())
	expected(t, e, "IDEMPOTENCY_CONFLICT")
	if count(t, f, "SELECT count(*) FROM training_attempts") != 1 || count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='held'") != 0 {
		t.Fatal("recovery request created premature attempt/reservation")
	}
	if worked, e := f.s.DispatchNext(ctx); e != nil || !worked {
		t.Fatal("explicit recovered dispatch", e)
	}
	l := claimed(t, f)
	if l.Training.AttemptNumber != 2 || l.Training.Resume != nil || l.Training.AttemptID == *j.CurrentAttemptID {
		t.Fatal("restart silently reused attempt/checkpoint")
	}
	if count(t, f, "SELECT count(*) FROM training_recoveries WHERE next_attempt_id=$1", l.Training.AttemptID) != 1 || count(t, f, "SELECT revision FROM gpu_leases WHERE state='held'") != 2 {
		t.Fatal("new physical revision/recovery linkage missing")
	}
	if _, e = f.s.DB.Exec(ctx, "UPDATE training_recoveries SET mode='checkpoint' WHERE id=$1", *first.PendingRecoveryID); e == nil {
		t.Fatal("recovery intent mutable")
	}
	if _, e = f.s.DB.Exec(ctx, "UPDATE training_recoveries SET next_attempt_id=previous_attempt_id WHERE id=$1", *first.PendingRecoveryID); e == nil {
		t.Fatal("recovery completion link mutable")
	}
}

func TestActualTrainingRecoveryRequiresStoppedOwnerRevisionCheckpointAndOneConcurrentIntent(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	ctx := context.Background()
	input := training.RecoveryRequest{ExpectedRevision: j.Revision, Reason: "Reject recovery of uncertain running work", NodeID: f.input.NodeID, Mode: "restart"}
	_, e := f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, input, uuid.NewString(), uuid.NewString())
	expected(t, e, "TRAINING_RECOVERY_NOT_ALLOWED")
	l := claimed(t, f)
	if _, e = f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, stopFixture(t, f, l), nil); e != nil {
		t.Fatal(e)
	}
	j, e = f.s.Get(ctx, f.portal, f.input.WorkspaceID, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, input, uuid.NewString(), uuid.NewString())
	expected(t, e, "TRAINING_REVISION_CONFLICT")
	input.ExpectedRevision = j.Revision
	foreign := uuid.NewString()
	bad := input
	bad.Mode = "checkpoint"
	bad.CheckpointID = &foreign
	_, e = f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, bad, uuid.NewString(), uuid.NewString())
	expected(t, e, "TRAINING_CHECKPOINT_UNAVAILABLE")
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, e := f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, input, uuid.NewString(), uuid.NewString())
			errs <- e
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		} else if e.Error() != "TRAINING_REVISION_CONFLICT" {
			t.Fatal(e)
		}
	}
	if success != 1 || count(t, f, "SELECT count(*) FROM training_recoveries") != 1 || count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.recovery_requested'") != 1 {
		t.Fatal("concurrent recovery accepted more than one revision")
	}
}

// A terminal failure before the recovered attempt starts must preserve its
// historical intent without leaving it pending.
func TestActualTrainingFailedDispatchClearsPendingRecovery(t *testing.T) {
	f := setup(t)
	j := stoppedJob(t, f)
	ctx := context.Background()
	input := training.RecoveryRequest{
		ExpectedRevision: j.Revision, Reason: "Queue retry before authority loss",
		NodeID: f.input.NodeID, Mode: "restart",
	}
	recovery, err := f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, input, uuid.NewString(), uuid.NewString())
	if err != nil || recovery.PendingRecoveryID == nil {
		t.Fatal("queue retry", err)
	}
	if _, err = f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := f.s.DispatchNext(ctx); err != nil || !worked {
		t.Fatal("settle revoked retry", worked, err)
	}
	job, err := f.s.Get(ctx, f.admin, f.input.WorkspaceID, j.ID)
	if err != nil || job.State != "failed" || job.ErrorCode == nil || *job.ErrorCode != "TRAINING_ACTOR_UNAVAILABLE" ||
		job.PendingRecoveryID != nil || count(t, f, "SELECT count(*) FROM training_jobs WHERE id=$1 AND pending_recovery_id IS NULL", j.ID) != 1 {
		t.Fatalf("terminal retry still pending: %+v, %v", job, err)
	}
	if count(t, f, "SELECT count(*) FROM training_recoveries WHERE id=$1 AND next_attempt_id IS NULL", *recovery.PendingRecoveryID) != 1 ||
		count(t, f, "SELECT count(*) FROM training_attempts WHERE job_id=$1", j.ID) != 1 {
		t.Fatal("rejected retry altered attempt history")
	}
}

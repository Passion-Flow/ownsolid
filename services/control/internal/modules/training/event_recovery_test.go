package training_test

import (
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/google/uuid"
)

func TestActualTrainingResumedFinalCheckpointBatchAndReplay(t *testing.T) {
	p := newPublicationCostFixture(t, 0)
	f := p.f
	ctx := t.Context()
	checkpoint := p.stage("checkpoint", p.checkpoint, p.checkpointFiles, "")
	p.finish(checkpoint, "checkpoint", false)
	stop := stopFixture(t, f, p.lease)
	stop.Proof = "owned_container_removed"
	container := strings.Repeat("e", 64)
	stop.ContainerID = &container
	if _, err := f.s.RecordStop(ctx, f.input.NodeID, f.credential, p.lease.TaskID, p.lease.LeaseToken, stop, nil); err != nil {
		t.Fatal("stop the synthetic producer after sealed checkpoint acceptance", err)
	}
	j, err := f.s.Get(ctx, f.portal, f.input.WorkspaceID, p.lease.Training.Scope.JobID)
	if err != nil {
		t.Fatal(err)
	}
	recovery := training.RecoveryRequest{ExpectedRevision: j.Revision, Reason: "Resume an accepted checkpoint at the frozen training horizon", NodeID: f.input.NodeID, Mode: "checkpoint", CheckpointID: &checkpoint}
	if _, err = f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, recovery, uuid.NewString(), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if worked, err := f.s.DispatchNext(ctx); err != nil || !worked {
		t.Fatal("dispatch final-horizon recovery", err)
	}
	next := claimed(t, f)
	if next.Training.Resume == nil || next.Training.Resume.CheckpointID != checkpoint ||
		next.Training.Resume.GlobalStep != next.Training.Configuration.MaximumSteps || next.Training.AttemptNumber != 2 {
		t.Fatal("recovery did not freeze the exact final checkpoint")
	}
	beforeRevision := count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID)
	loading := eventFixture(*next.Training, 1, 0, "phase", map[string]any{"phase": "loading_model"})
	wrongPhase := eventFixture(*next.Training, 2, 1, "phase", map[string]any{"phase": "training"})
	out, err := f.s.RecordEvents(ctx, f.input.NodeID, f.credential, next.TaskID, next.LeaseToken, [][]byte{loading, wrongPhase})
	expected(t, err, "TRAINING_EVENT_ORDER_CONFLICT")
	if len(out) != 0 || count(t, f, "SELECT count(*) FROM training_events WHERE attempt_id=$1", next.Training.AttemptID) != 0 ||
		count(t, f, "SELECT last_event_sequence FROM training_attempts WHERE id=$1", next.Training.AttemptID) != 0 ||
		count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID) != beforeRevision {
		t.Fatal("rejected final-horizon phase retained partial progress")
	}
	batch := [][]byte{loading,
		eventFixture(*next.Training, 2, 1, "phase", map[string]any{"phase": "finalizing_checkpoint"}),
		eventFixture(*next.Training, 3, 1, "checkpoint", map[string]any{"path": "checkpoint-1", "receipt_sha256": digest(p.checkpoint)}),
	}
	out, err = f.s.RecordEvents(ctx, f.input.NodeID, f.credential, next.TaskID, next.LeaseToken, batch)
	if err != nil || len(out) != 3 || out[0].JobState != "preparing" || out[1].JobState != "running" {
		t.Fatal("accept final-horizon event batch", err)
	}
	out, err = f.s.RecordEvents(ctx, f.input.NodeID, f.credential, next.TaskID, next.LeaseToken, batch)
	if err != nil || len(out) != 3 {
		t.Fatal("replay final-horizon event batch", err)
	}
	for _, item := range out {
		if !item.Duplicate || item.JobState != "running" {
			t.Fatal("final-horizon replay repeated a transition")
		}
	}
	if count(t, f, "SELECT last_event_sequence FROM training_attempts WHERE id=$1", next.Training.AttemptID) != 3 ||
		count(t, f, "SELECT last_global_step FROM training_attempts WHERE id=$1", next.Training.AttemptID) != 1 ||
		count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID) != beforeRevision+1 ||
		count(t, f, "SELECT count(*) FROM training_checkpoints WHERE job_id=$1", j.ID) != 1 ||
		count(t, f, "SELECT count(*) FROM training_artifacts WHERE job_id=$1", j.ID) != 0 ||
		count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.event_recorded' AND target_id=$1", j.ID) != 6 {
		t.Fatal("final-horizon events changed accepted references, progress or audits")
	}
}

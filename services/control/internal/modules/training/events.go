package training

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

type EventAcceptance = wire.EventAcceptance

// RecordEvent accepts one event through the same atomic bounded batch path.
func (s *Service) RecordEvent(ctx context.Context, node, credential, task, token string, raw []byte) (EventAcceptance, error) {
	values, e := s.RecordEvents(ctx, node, credential, task, token, [][]byte{raw})
	if e != nil {
		return EventAcceptance{}, e
	}
	return values[0], nil
}

// RecordEvents accepts one atomic finite batch under current authority.
// New members share one transaction-local progress write; checkpoint metadata never publishes files.
func (s *Service) RecordEvents(ctx context.Context, node, credential, task, token string, raw [][]byte) ([]EventAcceptance, error) {
	out := []EventAcceptance{}
	if len(raw) < 1 || len(raw) > wire.MaximumEventBatch {
		return out, fail("TRAINING_EVENT_INVALID")
	}
	e := s.withMachineLeaseActor(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, actor identity.Auth, w workspaces.Workspace, j Job, command wire.Command) error {
		var progress eventBatchProgress
		for _, item := range raw {
			accepted, e := s.recordEvent(ctx, tx, actor, w, j, command, item, &progress)
			if e != nil {
				return e
			}
			out = append(out, accepted)
			j.State = accepted.JobState
		}
		return progress.persist(ctx, tx, w, j.ID, command.AttemptID)
	})
	if e != nil {
		return []EventAcceptance{}, e
	}
	return out, nil
}

func (s *Service) recordEvent(ctx context.Context, tx transaction.Executor, actor identity.Auth, w workspaces.Workspace, j Job, command wire.Command, raw []byte, progress *eventBatchProgress) (EventAcceptance, error) {
	var out EventAcceptance
	event, e := wire.DecodeEvent(raw, j.ID, command.AttemptID, j.Configuration.MaximumSteps)
	if e != nil {
		return out, fail("TRAINING_EVENT_INVALID")
	}
	canonical, _ := json.Marshal(event)
	sum := sha256.Sum256(canonical)
	digest := hex.EncodeToString(sum[:])
	if e = progress.load(ctx, tx, w, j.ID, command.AttemptID); e != nil {
		return out, e
	}
	lastSequence, lastStep := progress.sequence, progress.step
	out = EventAcceptance{JobID: j.ID, AttemptID: command.AttemptID, Sequence: event.Sequence, JobState: j.State}
	if event.Sequence <= lastSequence {
		var previous string
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT canonical_sha256 FROM training_events WHERE attempt_id=$1 AND sequence=$2", "SELECT canonical_sha256 FROM training_events WHERE attempt_id=? AND sequence=?", []any{command.AttemptID, event.Sequence}, []any{command.AttemptID, event.Sequence})).Scan(&previous); e != nil {
			return out, e
		}
		if previous != digest {
			return out, fail("TRAINING_EVENT_CONFLICT")
		}
		out.Duplicate = true
		return out, nil
	}
	if event.Sequence != lastSequence+1 || event.GlobalStep < lastStep {
		return out, fail("TRAINING_EVENT_ORDER_CONFLICT")
	}
	if event.Kind == "phase" {
		resumeStep := int64(0)
		if command.Resume != nil {
			resumeStep = command.Resume.GlobalStep
		}
		validPhase := event.Sequence == 1 && *event.Phase == "loading_model" && event.GlobalStep == 0 || event.Sequence == 2 && event.GlobalStep == resumeStep && (*event.Phase == "training" && resumeStep < j.Configuration.MaximumSteps || *event.Phase == "finalizing_checkpoint" && resumeStep == j.Configuration.MaximumSteps)
		if !validPhase {
			return out, fail("TRAINING_EVENT_ORDER_CONFLICT")
		}
	} else if lastSequence < 2 || j.State != "running" {
		return out, fail("TRAINING_EVENT_ORDER_CONFLICT")
	}
	if event.Kind == "checkpoint" {
		var present bool
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT EXISTS(SELECT 1 FROM training_events WHERE attempt_id=$1 AND kind='checkpoint' AND global_step=$2)", "SELECT EXISTS(SELECT 1 FROM training_events WHERE attempt_id=? AND kind='checkpoint' AND global_step=?)", []any{command.AttemptID, event.GlobalStep}, []any{command.AttemptID, event.GlobalStep})).Scan(&present); e != nil {
			return out, e
		}
		if present {
			return out, fail("TRAINING_CHECKPOINT_CONFLICT")
		}
	}
	if _, e = tx.Exec(ctx, transferStatement(tx, `INSERT INTO training_events(tenant_id,workspace_id,job_id,attempt_id,sequence,kind,global_step,payload,canonical_sha256,attempt_created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,(SELECT created_at FROM training_attempts WHERE id=$4))`, `INSERT INTO training_events(tenant_id,workspace_id,job_id,attempt_id,sequence,kind,global_step,payload,canonical_sha256) VALUES(?,?,?,?,?,?,?,CAST(? AS JSON),?)`, []any{w.TenantID, w.ID, j.ID, command.AttemptID, event.Sequence, event.Kind, event.GlobalStep, canonical, digest}, []any{w.TenantID, w.ID, j.ID, command.AttemptID, event.Sequence, event.Kind, event.GlobalStep, canonical, digest})); e != nil {
		return out, e
	}
	if event.Kind == "phase" && event.Phase != nil && (*event.Phase == "training" || *event.Phase == "finalizing_checkpoint") && j.State == "preparing" {
		if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE training_jobs SET state='running',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_jobs SET state='running',revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{j.ID}, []any{j.ID})); e != nil {
			return out, e
		}
		progress.started = true
		out.JobState = "running"
	}
	progress.sequence, progress.step, progress.dirty = event.Sequence, event.GlobalStep, true
	return out, transferAuditWithin(ctx, tx, actor, w, "training.event_recorded", j.ID, "Accepted current-authority ordered training metadata", uuid.NewString())
}

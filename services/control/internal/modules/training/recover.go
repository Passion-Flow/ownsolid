package training

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"reflect"

	"database/sql"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type RecoveryRequest struct {
	ExpectedRevision int64   `json:"expected_revision"`
	Reason           string  `json:"reason"`
	NodeID           string  `json:"node_id"`
	Mode             string  `json:"mode"`
	CheckpointID     *string `json:"checkpoint_id"`
}

func recoveryFingerprint(a identity.Auth, workspace, job string, input RecoveryRequest) []byte {
	raw, _ := json.Marshal(struct {
		Actor, Audience, Workspace, Job string
		Input                           RecoveryRequest
	}{a.Me.User.ID, a.Me.Audience, workspace, job, input})
	sum := sha256.Sum256(raw)
	return sum[:]
}

func commandForJob(j Job, w workspaces.Workspace, attempt string, number int, inputs wire.InputInventory) wire.Command {
	return wire.Command{SchemaVersion: 1, Scope: wire.Scope{TenantID: w.TenantID, WorkspaceID: w.ID, JobID: j.ID}, AttemptID: attempt, AttemptNumber: number, ModelID: j.ModelID, DatasetID: j.DatasetID, DatasetVersionID: j.DatasetVersionID, DatasetManifestSHA256: j.DatasetManifestSHA256, ModelCatalogSHA256: j.ModelCatalogSHA256, GPUUUID: j.GPUUUID, Configuration: j.Configuration, ConfigurationSHA256: j.ConfigurationSHA256, Recipe: j.Recipe, Inputs: inputs}
}

func (s *Service) recoveryPreflight(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, id string, input RecoveryRequest, key string, intent []byte) (Job, *acceptedCheckpoint, bool, error) {
	var empty Job
	var previous []byte
	e := tx.QueryRow(ctx, transferStatement(tx, "SELECT creation_fingerprint FROM training_recoveries WHERE tenant_id=$1 AND workspace_id=$2 AND job_id=$3 AND creation_key=$4", "SELECT creation_fingerprint FROM training_recoveries WHERE tenant_id=? AND workspace_id=? AND job_id=? AND creation_key=?", []any{w.TenantID, w.ID, id, key}, []any{w.TenantID, w.ID, id, key})).Scan(&previous)
	if e == nil {
		if subtle.ConstantTimeCompare(previous, intent) != 1 {
			return empty, nil, false, fail("IDEMPOTENCY_CONFLICT")
		}
		j, e := lockedJobWithin(ctx, tx, w, id)
		return j, nil, true, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return empty, nil, false, e
	}
	n, e := s.Nodes.TrainingTargetWithin(ctx, tx, w.TenantID, w.ID, input.NodeID)
	if e != nil {
		return empty, nil, false, e
	}
	j, e := lockedJobWithin(ctx, tx, w, id)
	if e != nil {
		return j, nil, false, e
	}
	if j.Revision != input.ExpectedRevision {
		return j, nil, false, fail("TRAINING_REVISION_CONFLICT")
	}
	if j.State != "failed" && j.State != "cancelled" && j.State != "recovery_required" || j.CurrentAttemptID == nil {
		return j, nil, false, fail("TRAINING_RECOVERY_NOT_ALLOWED")
	}
	if j.AttemptNumber >= s.policy().MaxAttempts {
		return j, nil, false, fail("TRAINING_ATTEMPTS_EXHAUSTED")
	}
	var cleared bool
	e = tx.QueryRow(ctx, transferStatement(tx, `SELECT EXISTS(SELECT 1 FROM training_attempts a JOIN gpu_leases g ON g.training_job_id=a.job_id AND g.node_id=a.node_id AND g.revision=a.attempt_number WHERE a.id=$1 AND a.job_id=$2 AND a.physical_stopped_at IS NOT NULL AND g.state='released') AND NOT EXISTS(SELECT 1 FROM training_attempts WHERE job_id=$2 AND physical_stopped_at IS NULL)`, `SELECT EXISTS(SELECT 1 FROM training_attempts a JOIN gpu_leases g ON g.training_job_id=a.job_id AND g.node_id=a.node_id AND g.revision=a.attempt_number WHERE a.id=? AND a.job_id=? AND a.physical_stopped_at IS NOT NULL AND g.state='released') AND NOT EXISTS(SELECT 1 FROM training_attempts WHERE job_id=? AND physical_stopped_at IS NULL)`, []any{*j.CurrentAttemptID, j.ID}, []any{*j.CurrentAttemptID, j.ID, j.ID})).Scan(&cleared)
	if e != nil {
		return j, nil, false, e
	}
	if !cleared {
		return j, nil, false, fail("TRAINING_PHYSICAL_STOP_REQUIRED")
	}
	var active int
	if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT count(*) FROM training_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND state NOT IN ('succeeded','failed','cancelled') AND id<>$3", "SELECT count(*) FROM training_jobs WHERE tenant_id=? AND workspace_id=? AND state NOT IN ('succeeded','failed','cancelled') AND id<>?", []any{w.TenantID, w.ID, j.ID}, []any{w.TenantID, w.ID, j.ID})).Scan(&active); e != nil {
		return j, nil, false, e
	}
	if active >= s.policy().MaxActivePerWorkspace {
		return j, nil, false, fail("TRAINING_JOB_LIMIT_REACHED")
	}
	recipe, e := s.recipe()
	if e != nil {
		return j, nil, false, e
	}
	frozen, _ := json.Marshal(j.Recipe)
	installed, _ := json.Marshal(recipe)
	if string(frozen) != string(installed) {
		return j, nil, false, fail("TRAINING_RECIPE_UNAVAILABLE")
	}
	if e = s.resourcePreflightWithin(ctx, tx, n, j.GPUUUID); e != nil {
		return j, nil, false, e
	}
	if e = s.Models.ReadyWithin(ctx, tx, w.TenantID, w.ID, j.ModelID); e != nil {
		return j, nil, false, e
	}
	inputs, e := s.Datasets.TrainingInputSnapshotWithin(ctx, tx, w.TenantID, w.ID, j.DatasetID, j.DatasetVersionID, true)
	if e != nil {
		return j, nil, false, e
	}
	if inputs.Version.ManifestSHA256 != j.DatasetManifestSHA256 {
		return j, nil, false, fail("TRAINING_INPUT_UNAVAILABLE")
	}
	var checkpoint *acceptedCheckpoint
	if input.CheckpointID != nil {
		command := commandForJob(j, w, *j.CurrentAttemptID, j.AttemptNumber, wire.InputInventory{Train: inputs.Train, Validation: inputs.Validation})
		c, e := s.acceptedCheckpointWithin(ctx, tx, w, j, command, *input.CheckpointID)
		if e != nil {
			return j, nil, false, e
		}
		checkpoint = &c
	}
	return j, checkpoint, false, nil
}

// Recover authorizes an explicit immutable restart/checkpoint intent. It creates no resource owner or attempt until actual dispatch.
func (s *Service) Recover(ctx context.Context, a identity.Auth, workspace, id string, input RecoveryRequest, key, request string) (Job, error) {
	var out Job
	if !datawire.ValidID(id) || !datawire.ValidID(key) || !datawire.ValidID(input.NodeID) || input.ExpectedRevision < 1 || !validReason(input.Reason) || input.Mode != "checkpoint" && input.Mode != "restart" || (input.Mode == "checkpoint") != (input.CheckpointID != nil) || input.CheckpointID != nil && !datawire.ValidID(*input.CheckpointID) {
		return out, fail("VALIDATION_FAILED")
	}
	intent := recoveryFingerprint(a, workspace, id, input)
	var checkpoint *acceptedCheckpoint
	var sources datasets.TrainingInputs
	replay := false
	e := s.trainingActorTransaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, checkpoint, replay, e = s.recoveryPreflight(ctx, tx, a, w, id, input, key, intent)
		if e != nil || replay {
			return e
		}
		sources, e = s.Datasets.TrainingInputSnapshotWithin(ctx, tx, w.TenantID, w.ID, out.DatasetID, out.DatasetVersionID, true)
		return e
	})
	if e != nil {
		return Job{}, e
	}
	if replay {
		return out, nil
	}
	if e = s.Datasets.VerifyTrainingInputSnapshot(ctx, sources); e != nil {
		return Job{}, trainingRepositoryError(e)
	}
	if checkpoint != nil {
		if s.Store == nil {
			return Job{}, fail("TRAINING_STORAGE_UNAVAILABLE")
		}
		verification, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().UploadSeconds))
		e = s.Store.VerifyPublished(verification, checkpoint.Descriptor)
		cancel()
		if e != nil {
			return Job{}, e
		}
	}
	e = s.trainingActorTransaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		j, current, replay, e := s.recoveryPreflight(ctx, tx, a, w, id, input, key, intent)
		if e != nil {
			return e
		}
		if replay {
			out = j
			return nil
		}
		if checkpoint != nil && (current == nil || current.ID != checkpoint.ID || current.Descriptor.ManifestSHA256 != checkpoint.Descriptor.ManifestSHA256) {
			return fail("TRAINING_CHECKPOINT_INVALID")
		}
		inputs, e := s.Datasets.TrainingInputSnapshotWithin(ctx, tx, w.TenantID, w.ID, j.DatasetID, j.DatasetVersionID, true)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(inputs, sources) {
			return fail("TRAINING_INPUT_UNAVAILABLE")
		}
		var membership *string
		if !admin(a) {
			var authority string
			if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", "SELECT authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", []any{w.TenantID, w.ID, a.Me.User.ID}, []any{w.TenantID, w.ID, a.Me.User.ID})).Scan(&authority); e != nil {
				return e
			}
			membership = &authority
		}
		recovery := uuid.NewString()
		_, e = tx.Exec(ctx, transferStatement(tx, `INSERT INTO training_recoveries(id,tenant_id,workspace_id,job_id,previous_attempt_id,checkpoint_id,previous_node_id,next_node_id,mode,requested_by,reason,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, `INSERT INTO training_recoveries(id,tenant_id,workspace_id,job_id,previous_attempt_id,checkpoint_id,previous_node_id,next_node_id,mode,requested_by,reason,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, []any{recovery, w.TenantID, w.ID, j.ID, j.CurrentAttemptID, input.CheckpointID, j.CurrentNodeID, input.NodeID, input.Mode, a.Me.User.ID, input.Reason, key, intent}, []any{recovery, w.TenantID, w.ID, j.ID, j.CurrentAttemptID, input.CheckpointID, j.CurrentNodeID, input.NodeID, input.Mode, a.Me.User.ID, input.Reason, key, intent}))
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, transferStatement(tx, `UPDATE training_jobs SET state='queued',current_node_id=$2,pending_recovery_id=$3,requested_by=$4,requested_audience=$5,requested_epoch=$6,membership_authority=$7,cancel_requested_at=NULL,accepted_artifact_id=NULL,error_code=NULL,wait_code=NULL,finished_at=NULL,next_dispatch_at=clock_timestamp(),revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE training_jobs SET state='queued',current_node_id=?,pending_recovery_id=?,requested_by=?,requested_audience=?,requested_epoch=?,membership_authority=?,cancel_requested_at=NULL,accepted_artifact_id=NULL,error_code=NULL,wait_code=NULL,finished_at=NULL,next_dispatch_at=UTC_TIMESTAMP(6),revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{j.ID, input.NodeID, recovery, a.Me.User.ID, a.Me.Audience, a.Epoch, membership}, []any{input.NodeID, recovery, a.Me.User.ID, a.Me.Audience, a.Epoch, membership, j.ID}))
		if e != nil {
			return e
		}
		if e = transferAuditWithin(ctx, tx, a, w, "training.recovery_requested", j.ID, input.Reason, request); e != nil {
			return e
		}
		out, e = lockedJobWithin(ctx, tx, w, id)
		return e
	})
	if e != nil {
		return Job{}, e
	}
	return out, nil
}

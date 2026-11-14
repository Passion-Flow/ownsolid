package training

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"reflect"

	"database/sql"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) machineCandidate(ctx context.Context, node, task string) (candidate, string, error) {
	var c candidate
	var taskID string
	if !datawire.ValidID(node) || task != "" && !datawire.ValidID(task) {
		return c, taskID, fail("AGENT_UNAUTHORIZED")
	}
	pg := `SELECT j.id::text,j.tenant_id::text,j.workspace_id::text,j.current_node_id::text,j.requested_by::text,j.requested_audience,j.requested_epoch,j.membership_authority::text,t.id::text FROM training_jobs j JOIN training_attempts a ON a.id=j.current_attempt_id AND a.job_id=j.id JOIN node_tasks t ON t.id=a.task_id AND t.node_id=j.current_node_id AND t.tenant_id=j.tenant_id AND t.workspace_id=j.workspace_id JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND j.current_node_id=$1 AND t.operation='training.run.v1'`
	my := `SELECT j.id,j.tenant_id,j.workspace_id,j.current_node_id,j.requested_by,j.requested_audience,j.requested_epoch,j.membership_authority,t.id FROM training_jobs j JOIN training_attempts a ON a.id=j.current_attempt_id AND a.job_id=j.id JOIN node_tasks t ON t.id=a.task_id AND t.node_id=j.current_node_id AND t.tenant_id=j.tenant_id AND t.workspace_id=j.workspace_id JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND j.current_node_id=? AND t.operation='training.run.v1'`
	args := []any{node}
	if task != "" {
		pg += " AND t.id=$2"
		my += " AND t.id=?"
		args = append(args, task)
	} else {
		pg += " AND t.state IN ('pending','running')"
		my += " AND t.state IN ('pending','running')"
	}
	pg += " ORDER BY t.created_at,t.id LIMIT 1"
	my += " ORDER BY t.created_at,t.id LIMIT 1"
	missing := false
	e := s.trainingStorageTransaction(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		e := q.QueryRow(ctx, acceptedAdapterSQL(q, pg, my, args...)).Scan(&c.id, &c.tenant, &c.workspace, &c.node, &c.actor, &c.audience, &c.epoch, &c.membership, &taskID)
		if errors.Is(e, sql.ErrNoRows) {
			missing = true
			return nil
		}
		return e
	})
	if e != nil {
		return candidate{}, "", e
	}
	if missing {
		return candidate{}, "", sql.ErrNoRows
	}
	return c, taskID, nil
}
func bindFrozenCommand(j Job, command wire.Command) error {
	if j.CurrentAttemptID == nil || command.AttemptID != *j.CurrentAttemptID || command.Scope.JobID != j.ID || command.AttemptNumber != j.AttemptNumber || command.ConfigurationSHA256 != j.ConfigurationSHA256 || command.DatasetManifestSHA256 != j.DatasetManifestSHA256 || command.ModelID != j.ModelID || command.DatasetID != j.DatasetID || command.DatasetVersionID != j.DatasetVersionID || command.GPUUUID != j.GPUUUID || command.ModelCatalogSHA256 != j.ModelCatalogSHA256 {
		return fail("TRAINING_ATTEMPT_STALE")
	}
	frozen, _ := json.Marshal(j.Recipe)
	given, _ := json.Marshal(command.Recipe)
	if string(frozen) != string(given) {
		return fail("TRAINING_ATTEMPT_STALE")
	}
	return nil
}
func bindAttempt(ctx context.Context, tx transaction.Executor, j Job, command wire.Command, task string) (identity.Auth, error) {
	if e := bindFrozenCommand(j, command); e != nil {
		return identity.Auth{}, e
	}
	inventory, _ := json.Marshal(command.Inputs)
	var checkpoint *string
	if command.Resume != nil {
		checkpoint = &command.Resume.CheckpointID
	}
	// Read the immutable audit principal from the same exact attempt that passed the fence.
	var actor identity.Auth
	e := tx.QueryRow(ctx, transferStatement(tx, `SELECT a.requested_by::text,a.requested_audience FROM training_attempts a JOIN gpu_leases g ON g.training_job_id=a.job_id AND g.node_id=a.node_id AND g.revision=a.attempt_number WHERE a.id=$1 AND a.job_id=$2 AND a.task_id=$3 AND a.input_inventory=$4::jsonb AND a.resume_checkpoint_id IS NOT DISTINCT FROM $5::uuid AND a.physical_stopped_at IS NULL AND a.state IN ('pending','preparing','running') AND g.state='held' AND g.gpu_uuid=$6`, `SELECT a.requested_by,a.requested_audience FROM training_attempts a JOIN gpu_leases g ON g.training_job_id=a.job_id AND g.node_id=a.node_id AND g.revision=a.attempt_number WHERE a.id=? AND a.job_id=? AND a.task_id=? AND a.input_inventory=CAST(? AS JSON) AND a.resume_checkpoint_id <=> ? AND a.physical_stopped_at IS NULL AND a.state IN ('pending','preparing','running') AND g.state='held' AND g.gpu_uuid=?`, []any{command.AttemptID, j.ID, task, inventory, checkpoint, command.GPUUUID}, []any{command.AttemptID, j.ID, task, inventory, checkpoint, command.GPUUUID})).Scan(&actor.Me.User.ID, &actor.Me.Audience)
	if errors.Is(e, sql.ErrNoRows) {
		return identity.Auth{}, fail("TRAINING_ATTEMPT_STALE")
	}
	return actor, e
}
func (s *Service) ClaimTraining(ctx context.Context, node, credential, request string) (agentwire.Claim, bool, error) {
	var out agentwire.Claim
	c, task, e := s.machineCandidate(ctx, node, "")
	if errors.Is(e, sql.ErrNoRows) {
		return out, false, nil
	}
	if e != nil {
		return agentwire.Claim{}, true, e
	}
	var snapshot datasets.TrainingInputs
	e = s.delegatedRepositoryTransaction(ctx, c, func(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, j Job, valid bool) error {
		current, e := s.Nodes.CredentialWithin(ctx, q, c.tenant, c.workspace, node, credential)
		if e != nil {
			return e
		}
		if !valid || current.Status != "active" || j.State != "preparing" || j.CancelRequestedAt != nil {
			return fail("ACCESS_DENIED")
		}
		snapshot, e = s.Datasets.TrainingInputSnapshotWithin(ctx, q, w.TenantID, w.ID, j.DatasetID, j.DatasetVersionID, j.AttemptNumber > 1)
		if e != nil {
			return e
		}
		if snapshot.Version.ManifestSHA256 != j.DatasetManifestSHA256 {
			return fail("TRAINING_INPUT_UNAVAILABLE")
		}
		return nil
	})
	if e != nil {
		return out, true, e
	}
	if e = s.Datasets.VerifyTrainingInputSnapshot(ctx, snapshot); e != nil {
		return out, true, trainingRepositoryError(e)
	}
	e = s.delegatedRepositoryTransaction(ctx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, j Job, valid bool) error {
		current, e := s.Nodes.CredentialWithin(ctx, tx, c.tenant, c.workspace, node, credential)
		if e != nil {
			return e
		}
		if !valid || current.Status != "active" || j.State != "preparing" || j.CancelRequestedAt != nil {
			return fail("ACCESS_DENIED")
		}
		if e = s.Models.ReadyWithin(ctx, tx, w.TenantID, w.ID, j.ModelID); e != nil {
			return e
		}
		input, e := s.Datasets.TrainingInputSnapshotWithin(ctx, tx, w.TenantID, w.ID, j.DatasetID, j.DatasetVersionID, j.AttemptNumber > 1)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(input, snapshot) || input.Version.ManifestSHA256 != j.DatasetManifestSHA256 {
			return fail("TRAINING_INPUT_UNAVAILABLE")
		}
		out, e = s.Nodes.ClaimTrainingWithin(ctx, tx, current, task, request)
		if e != nil {
			return e
		}
		if out.Lease == nil {
			return nil
		}
		if _, e = bindAttempt(ctx, tx, j, *out.Lease.Training, task); e != nil {
			return e
		}
		// A lease delivered again after it was never acknowledged binds a new stop
		// authority; the schema allows that only while the task is unacknowledged.
		hash := stopTokenHash(out.Lease.LeaseToken)
		affected, e := tx.Exec(ctx, transferStatement(tx, "UPDATE training_attempts SET state='preparing',stop_token_hash=$2,started_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND (stop_token_hash IS NULL OR state='preparing')", "UPDATE training_attempts SET state='preparing',stop_token_hash=?,started_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=? AND (stop_token_hash IS NULL OR state='preparing')", []any{*j.CurrentAttemptID, hash[:]}, []any{hash[:], *j.CurrentAttemptID}))
		if e == nil && affected != 1 {
			return fail("TRAINING_ATTEMPT_STALE")
		}
		return e
	})
	if e != nil {
		return agentwire.Claim{}, true, e
	}
	return out, true, nil
}

func stopTokenHash(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte("projectone-training-stop-token:" + token))
}
func (s *Service) RenewTraining(ctx context.Context, node, credential, task, token string) (agentwire.RenewedLease, bool, error) {
	var out agentwire.RenewedLease
	c, _, e := s.machineCandidate(ctx, node, task)
	if errors.Is(e, sql.ErrNoRows) {
		return out, false, nil
	}
	if e != nil {
		return out, true, e
	}
	e = s.delegatedRepositoryTransaction(ctx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, j Job, valid bool) error {
		current, e := s.Nodes.CredentialWithin(ctx, tx, c.tenant, c.workspace, node, credential)
		if e != nil {
			return e
		}
		if !valid || current.Status != "active" || j.State != "preparing" && j.State != "running" || j.CancelRequestedAt != nil {
			return fail("ACCESS_DENIED")
		}
		var command wire.Command
		out, command, e = s.Nodes.RenewTrainingWithin(ctx, tx, current, task, token)
		if e != nil {
			return e
		}
		_, e = bindAttempt(ctx, tx, j, command, task)
		return e
	})
	if e != nil {
		return agentwire.RenewedLease{}, true, e
	}
	return out, true, nil
}

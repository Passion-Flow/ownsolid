package training

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"

	"database/sql"
	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// RecordStop accepts only restricted exact-owner absence evidence. It grants no execution/output authority.
// Current human revocation or cancellation wins over success, but cannot prevent a still-authenticated node from freeing its stopped owner.
func (s *Service) RecordStop(ctx context.Context, node, credential, task, token string, receipt wire.StopReceipt, outcome []byte) (wire.StopAcceptance, error) {
	var out wire.StopAcceptance
	if !security.ValidToken(token) {
		return out, fail("TRAINING_STOP_INVALID")
	}
	c, _, e := s.machineCandidate(ctx, node, task)
	if errors.Is(e, sql.ErrNoRows) {
		return out, fail("TRAINING_ATTEMPT_STALE")
	}
	if e != nil {
		return out, e
	}
	verifiedArtifact, e := s.verifyStopArtifact(ctx, c, node, credential, task, token, receipt)
	if e != nil {
		return out, e
	}
	e = s.delegatedRepositoryTransaction(ctx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, j Job, valid bool) error {
		if j.State == "queued" {
			return fail("TRAINING_ATTEMPT_STALE")
		}
		current, e := s.Nodes.CredentialWithin(ctx, tx, c.tenant, c.workspace, node, credential)
		if e != nil {
			return e
		}
		t, command, e := s.Nodes.TrainingStopTaskWithin(ctx, tx, current, task)
		if e != nil {
			return e
		}
		if receipt.ValidateFor(command, node, task) != nil || j.CurrentAttemptID == nil || *j.CurrentAttemptID != command.AttemptID || j.AttemptNumber != command.AttemptNumber || command.Scope.JobID != j.ID || j.CurrentNodeID != node {
			return fail("TRAINING_STOP_INVALID")
		}
		if e = bindFrozenCommand(j, command); e != nil {
			return e
		}
		var stopHash []byte
		var existingReceipt, existingOutcome *string
		var storedOutcome []byte
		var attemptNumber int64
		var attemptNode string
		var inventory []byte
		var eventCount int64
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT stop_token_hash,physical_stop_sha256,outcome_sha256,outcome,attempt_number,node_id::text,input_inventory,last_event_sequence FROM training_attempts WHERE id=$1 AND job_id=$2 AND task_id=$3 FOR UPDATE", "SELECT stop_token_hash,physical_stop_sha256,outcome_sha256,outcome,attempt_number,node_id,input_inventory,last_event_sequence FROM training_attempts WHERE id=? AND job_id=? AND task_id=? FOR UPDATE", []any{command.AttemptID, j.ID, task}, []any{command.AttemptID, j.ID, task})).Scan(&stopHash, &existingReceipt, &existingOutcome, &storedOutcome, &attemptNumber, &attemptNode, &inventory, &eventCount); e != nil {
			return e
		}
		var frozenInputs wire.InputInventory
		if wire.DecodeStrict(inventory, &frozenInputs) != nil || frozenInputs != command.Inputs {
			return fail("TRAINING_ATTEMPT_STALE")
		}
		given := stopTokenHash(token)
		if subtle.ConstantTimeCompare(stopHash, given[:]) != 1 || t.Attempt != 1 || attemptNumber != int64(command.AttemptNumber) || attemptNode != node {
			return fail("TRAINING_STOP_INVALID")
		}
		var result wire.Result
		if len(outcome) > 0 {
			request, e := command.ContainerRequest()
			if e != nil {
				return e
			}
			result, e = wire.DecodeResult(outcome, request, agentwire.ApprovedModel().Repository)
			if e != nil || result.Status == "trained" && existingOutcome == nil {
				return fail("TRAINING_RESULT_INVALID")
			}
			sha := rawDigest(outcome)
			if receipt.OutcomeSHA256 == nil || *receipt.OutcomeSHA256 != sha || existingOutcome != nil && *existingOutcome != sha {
				return fail("TRAINING_RESULT_CONFLICT")
			}
		} else if existingOutcome != nil {
			if receipt.OutcomeSHA256 == nil || *receipt.OutcomeSHA256 != *existingOutcome || json.Unmarshal(storedOutcome, &result) != nil {
				return fail("TRAINING_RESULT_CONFLICT")
			}
		} else if receipt.OutcomeSHA256 != nil {
			return fail("TRAINING_RESULT_CONFLICT")
		}
		if (eventCount > 0 || result.Status == "trained" || result.GlobalStep > 0) && receipt.Proof != "owned_container_removed" {
			return fail("TRAINING_STOP_INVALID")
		}
		raw, e := json.Marshal(receipt)
		if e != nil || len(raw) > wire.MaximumStopReceiptBytes {
			return fail("TRAINING_STOP_INVALID")
		}
		sha := rawDigest(raw)
		if existingReceipt != nil {
			if *existingReceipt != sha {
				return fail("TRAINING_STOP_CONFLICT")
			}
			out = wire.StopAcceptance{JobID: j.ID, AttemptID: command.AttemptID, ReceiptID: receipt.ReceiptID, State: j.State, GPUReleased: true, Duplicate: true}
			return nil
		}
		var gpuNode, gpuUUID, gpuState string
		var revision int64
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT node_id::text,gpu_uuid,revision,state FROM gpu_leases WHERE training_job_id=$1 FOR UPDATE", "SELECT node_id,gpu_uuid,revision,state FROM gpu_leases WHERE training_job_id=? FOR UPDATE", []any{j.ID}, []any{j.ID})).Scan(&gpuNode, &gpuUUID, &revision, &gpuState); e != nil {
			return e
		}
		if gpuNode != node || gpuUUID != command.GPUUUID || revision != int64(command.AttemptNumber) || gpuState != "held" && gpuState != "recovery_required" {
			return fail("TRAINING_ATTEMPT_STALE")
		}
		state, code := "failed", "TRAINING_RESULT_UNAVAILABLE"
		var artifact *string
		if j.CancelRequestedAt != nil || j.State == "cancelling" || !valid || result.Status == "cancelled" {
			state = "cancelled"
			code = ""
			if !valid {
				code = "TRAINING_ACTOR_UNAVAILABLE"
			}
		} else if receipt.AgentErrorCode != "" {
			code = receipt.AgentErrorCode
		} else if result.Status == "trained" && current.Status == "active" {
			var id, digest string
			if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT id::text,manifest_sha256 FROM training_artifacts WHERE tenant_id=$1 AND workspace_id=$2 AND job_id=$3 AND attempt_id=$4", "SELECT id,manifest_sha256 FROM training_artifacts WHERE tenant_id=? AND workspace_id=? AND job_id=? AND attempt_id=?", []any{w.TenantID, w.ID, j.ID, command.AttemptID}, []any{w.TenantID, w.ID, j.ID, command.AttemptID})).Scan(&id, &digest); e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					return fail("TRAINING_RESULT_INVALID")
				}
				return e
			}
			if existingOutcome == nil || digest != *existingOutcome || verifiedArtifact == nil || *verifiedArtifact != digest {
				return fail("TRAINING_RESULT_CONFLICT")
			}
			state = "succeeded"
			code = ""
			artifact = &id
		} else if result.Status == "failed" {
			code = result.ErrorCode
		}
		if len(outcome) > 0 && existingOutcome == nil {
			if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE training_attempts SET outcome=$2,outcome_sha256=$3 WHERE id=$1", "UPDATE training_attempts SET outcome=CAST(? AS JSON),outcome_sha256=? WHERE id=?", []any{command.AttemptID, outcome, rawDigest(outcome)}, []any{outcome, rawDigest(outcome), command.AttemptID})); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE training_attempts SET state=$2,error_code=NULLIF($3,''),physical_stop_receipt=$4,physical_stop_sha256=$5,physical_stopped_at=clock_timestamp(),finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_attempts SET state=?,error_code=NULLIF(?,''),physical_stop_receipt=CAST(? AS JSON),physical_stop_sha256=?,physical_stopped_at=UTC_TIMESTAMP(6),finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{command.AttemptID, state, code, raw, sha}, []any{state, code, raw, sha, command.AttemptID})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE gpu_leases SET state='released',updated_at=clock_timestamp() WHERE training_job_id=$1 AND revision=$2 AND node_id=$3", "UPDATE gpu_leases SET state='released',updated_at=UTC_TIMESTAMP(6) WHERE training_job_id=? AND revision=? AND node_id=?", []any{j.ID, command.AttemptNumber, node}, []any{j.ID, command.AttemptNumber, node})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE node_tasks SET state=$2,error_code=NULLIF($3,''),completed_lease_hash=COALESCE(completed_lease_hash,lease_token_hash),lease_token_hash=NULL,lease_expires_at=NULL,completed_at=COALESCE(completed_at,clock_timestamp()),updated_at=clock_timestamp() WHERE id=$1", "UPDATE node_tasks SET state=?,error_code=NULLIF(?,''),completed_lease_hash=COALESCE(completed_lease_hash,lease_token_hash),lease_token_hash=NULL,lease_expires_at=NULL,completed_at=COALESCE(completed_at,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{task, state, code}, []any{state, code, task})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE training_jobs SET state=$2,error_code=NULLIF($3,''),accepted_artifact_id=$4,wait_code=NULL,revision=revision+1,finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_jobs SET state=?,error_code=NULLIF(?,''),accepted_artifact_id=?,wait_code=NULL,revision=revision+1,finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{j.ID, state, code, artifact}, []any{state, code, artifact, j.ID})); e != nil {
			return e
		}
		if e = transferAuditWithin(ctx, tx, a, w, "training.physical_stop_accepted", j.ID, "Authenticated exact attempt/container absence; physical reservation released", uuid.NewString()); e != nil {
			return e
		}
		out = wire.StopAcceptance{JobID: j.ID, AttemptID: command.AttemptID, ReceiptID: receipt.ReceiptID, State: state, GPUReleased: true}
		return nil
	})
	if e != nil {
		return wire.StopAcceptance{}, e
	}
	return out, nil
}

// Successful stop checks already accepted immutable bytes outside business locks, then RecordStop repeats authority/fence checks.
func (s *Service) verifyStopArtifact(ctx context.Context, c candidate, node, credential, task, token string, receipt wire.StopReceipt) (*string, error) {
	if receipt.OutcomeSHA256 == nil {
		return nil, nil
	}
	var descriptor UploadDescriptor
	var accepted *string
	e := s.delegatedRepositoryTransaction(ctx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, j Job, valid bool) error {
		current, e := s.Nodes.CredentialWithin(ctx, tx, c.tenant, c.workspace, node, credential)
		if e != nil {
			return e
		}
		_, command, e := s.Nodes.TrainingStopTaskWithin(ctx, tx, current, task)
		if e != nil {
			return e
		}
		if receipt.ValidateFor(command, node, task) != nil {
			return fail("TRAINING_STOP_INVALID")
		}
		var hash []byte
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT stop_token_hash FROM training_attempts WHERE id=$1 AND job_id=$2 AND task_id=$3", "SELECT stop_token_hash FROM training_attempts WHERE id=? AND job_id=? AND task_id=?", []any{command.AttemptID, j.ID, task}, []any{command.AttemptID, j.ID, task})).Scan(&hash); e != nil {
			return e
		}
		given := stopTokenHash(token)
		if subtle.ConstantTimeCompare(hash, given[:]) != 1 {
			return fail("TRAINING_STOP_INVALID")
		}
		// Revoked/cancelled/disabled execution can release its exact stopped owner without requiring a readable export.
		// It cannot become successful, so output corruption remains a separate retained integrity failure.
		if !valid || j.CancelRequestedAt != nil || j.State == "cancelling" || current.Status != "active" || receipt.AgentErrorCode != "" {
			return nil
		}
		var id, sha string
		var raw []byte
		e = tx.QueryRow(ctx, transferStatement(tx, "SELECT r.id::text,r.manifest_sha256,r.file_inventory FROM training_artifacts r JOIN training_attempts a ON a.id=r.attempt_id WHERE r.tenant_id=$1 AND r.workspace_id=$2 AND r.job_id=$3 AND r.attempt_id=$4 AND a.physical_stopped_at IS NULL", "SELECT r.id,r.manifest_sha256,r.file_inventory FROM training_artifacts r JOIN training_attempts a ON a.id=r.attempt_id WHERE r.tenant_id=? AND r.workspace_id=? AND r.job_id=? AND r.attempt_id=? AND a.physical_stopped_at IS NULL", []any{w.TenantID, w.ID, j.ID, command.AttemptID}, []any{w.TenantID, w.ID, j.ID, command.AttemptID})).Scan(&id, &sha, &raw)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if sha != *receipt.OutcomeSHA256 || wire.DecodeStrict(raw, &descriptor) != nil || descriptor.UploadID != id || descriptor.Scope != command.Scope || descriptor.AttemptID != command.AttemptID || descriptor.Kind != "artifact" || descriptor.ManifestSHA256 != sha {
			return fail("TRAINING_RESULT_CONFLICT")
		}
		accepted = &sha
		return nil
	})
	if e != nil {
		return nil, e
	}
	if accepted == nil {
		return nil, nil
	}
	if s.Store == nil {
		return nil, fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	verification, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().UploadSeconds))
	defer cancel()
	if e = s.Store.VerifyPublished(verification, descriptor); e != nil {
		return nil, e
	}
	return accepted, nil
}

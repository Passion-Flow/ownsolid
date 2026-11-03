package modelpreparations

import (
	"database/sql"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type stopArtifactHealth struct {
	sha     string
	healthy bool
}

func (s *Service) stopArtifact(ctx context.Context, candidate candidate, node, credential, task, token string, receipt wire.StopReceipt) (stopArtifactHealth, error) {
	var out stopArtifactHealth
	var descriptor UploadDescriptor
	check := false
	e := s.delegatedTransaction(ctx, candidate, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, p Preparation, valid bool) error {
		current, e := s.Nodes.CredentialWithin(ctx, tx, w.TenantID, w.ID, node, credential)
		if e != nil {
			return e
		}
		t, c, e := s.Nodes.PreparationStopTaskWithin(ctx, tx, current, task)
		if e != nil {
			return e
		}
		if receipt.ValidateFor(c, node, task) != nil || bindFrozenCommand(p, c) != nil || t.Attempt != 1 {
			return fail("MODEL_PREPARATION_STOP_INVALID")
		}
		var stopHash []byte
		var sha, stopped *string
		if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "STOP_TOKEN_HASH","OUTCOME_SHA256","PHYSICAL_STOP_SHA256" FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "ID"=? AND "PREPARATION_ID"=? AND "TASK_ID"=?`), "SELECT stop_token_hash,outcome_sha256,physical_stop_sha256 FROM model_preparation_attempts WHERE id=$1 AND preparation_id=$2 AND task_id=$3", `SELECT stop_token_hash,outcome_sha256,physical_stop_sha256 FROM model_preparation_attempts WHERE id=? AND preparation_id=? AND task_id=?`, c.AttemptID, p.ID, task)).Scan(&stopHash, &sha, &stopped); e != nil {
			return e
		}
		given := stopTokenHash(token)
		if subtle.ConstantTimeCompare(stopHash, given[:]) != 1 {
			return fail("MODEL_PREPARATION_STOP_INVALID")
		}
		if stopped != nil || sha == nil {
			return nil
		}
		out.sha = *sha
		var raw, report []byte
		var manifest string
		e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "DESCRIPTOR","REPORT_BYTES","MANIFEST_SHA256" FROM "MODEL_PREPARATION_ARTIFACTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "PREPARATION_ID"=? AND "ATTEMPT_ID"=?`), `SELECT descriptor,report_bytes,manifest_sha256 FROM model_preparation_artifacts WHERE tenant_id=$1 AND workspace_id=$2 AND preparation_id=$3 AND attempt_id=$4`, `SELECT descriptor,report_bytes,manifest_sha256 FROM model_preparation_artifacts WHERE tenant_id=? AND workspace_id=? AND preparation_id=? AND attempt_id=?`, w.TenantID, w.ID, p.ID, c.AttemptID)).Scan(&raw, &report, &manifest)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if wire.DecodeStrict(raw, &descriptor) != nil || descriptor.Validate(wire.MaximumModelBytes, wire.MaximumModelFiles) != nil || descriptor.Scope != c.Scope || descriptor.AttemptID != c.AttemptID || descriptor.ManifestSHA256 != out.sha || manifest != out.sha || hash(report) != out.sha {
			return nil
		}
		check = true
		return nil
	})
	if e != nil {
		return out, e
	}
	if check && s.Store != nil {
		e = s.Store.VerifyPublished(ctx, descriptor)
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.healthy = e == nil
	}
	return out, nil
}

// RecordStop grants only exact cleanup authority, including after human revocation or file corruption.
// It never accepts a new prepared report, grants execution, or publishes a model.
func (s *Service) RecordStop(ctx context.Context, node, credential, task, token string, receipt wire.StopReceipt, outcome []byte) (wire.StopAcceptance, error) {
	var out wire.StopAcceptance
	if !security.ValidToken(token) {
		return out, fail("MODEL_PREPARATION_STOP_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().OutputSeconds))
	defer cancel()
	candidate, _, e := s.machineCandidate(ctx, node, task)
	if errors.Is(e, sql.ErrNoRows) {
		return out, fail("MODEL_PREPARATION_ATTEMPT_STALE")
	}
	if e != nil {
		return out, e
	}
	health, e := s.stopArtifact(ctx, candidate, node, credential, task, token, receipt)
	if e != nil {
		return out, e
	}
	e = s.delegatedTransaction(ctx, candidate, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, p Preparation, valid bool) error {
		current, e := s.Nodes.CredentialWithin(ctx, tx, w.TenantID, w.ID, node, credential)
		if e != nil {
			return e
		}
		t, c, e := s.Nodes.PreparationStopTaskWithin(ctx, tx, current, task)
		if e != nil {
			return e
		}
		if receipt.ValidateFor(c, node, task) != nil || bindFrozenCommand(p, c) != nil || t.Attempt != 1 {
			return fail("MODEL_PREPARATION_STOP_INVALID")
		}
		var stopHash []byte
		var storedSHA, existingStop *string
		var storedOutcome []byte
		var events int64
		var commandSHA string
		if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "STOP_TOKEN_HASH","OUTCOME_SHA256","OUTCOME_BYTES","PHYSICAL_STOP_SHA256","LAST_EVENT_SEQUENCE","COMMAND_SHA256" FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "ID"=? AND "PREPARATION_ID"=? AND "TASK_ID"=? AND "NODE_ID"=? FOR UPDATE`), `SELECT stop_token_hash,outcome_sha256,outcome_bytes,physical_stop_sha256,last_event_sequence,command_sha256 FROM model_preparation_attempts WHERE id=$1 AND preparation_id=$2 AND task_id=$3 AND node_id=$4 FOR UPDATE`, `SELECT stop_token_hash,outcome_sha256,outcome_bytes,physical_stop_sha256,last_event_sequence,command_sha256 FROM model_preparation_attempts WHERE id=? AND preparation_id=? AND task_id=? AND node_id=? FOR UPDATE`, c.AttemptID, p.ID, task, node)).Scan(&stopHash, &storedSHA, &storedOutcome, &existingStop, &events, &commandSHA); e != nil {
			return e
		}
		given := stopTokenHash(token)
		sha, e := c.Digest()
		if e != nil || sha != commandSHA || subtle.ConstantTimeCompare(stopHash, given[:]) != 1 {
			return fail("MODEL_PREPARATION_STOP_INVALID")
		}
		if int64(len(outcome)) > c.Recipe.Resources.MaximumResultBytes {
			return fail("MODEL_PREPARATION_REPORT_INVALID")
		}
		var prepared wire.PreparedResult
		var failed wire.FailedResult
		if storedSHA != nil {
			if hash(storedOutcome) != *storedSHA {
				return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
			}
			if receipt.OutcomeSHA256 == nil || *receipt.OutcomeSHA256 != *storedSHA || len(outcome) > 0 && !bytes.Equal(outcome, storedOutcome) {
				return fail("MODEL_PREPARATION_REPORT_CONFLICT")
			}
			var status struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(storedOutcome, &status) != nil {
				return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
			}
			if status.Status == "prepared" {
				if wire.DecodeStrict(storedOutcome, &prepared) != nil || prepared.PreparationID != p.ID || prepared.AttemptID != c.AttemptID || prepared.EventCount != events {
					return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
				}
			} else {
				request, e := c.ContainerRequest()
				if e != nil {
					return e
				}
				failed, e = wire.DecodeFailedResult(storedOutcome, request)
				if e != nil {
					return e
				}
			}
		} else if len(outcome) > 0 {
			request, e := c.ContainerRequest()
			if e != nil {
				return e
			}
			failed, e = wire.DecodeFailedResult(outcome, request)
			if e != nil {
				return e
			}
			if receipt.OutcomeSHA256 == nil || *receipt.OutcomeSHA256 != hash(outcome) {
				return fail("MODEL_PREPARATION_REPORT_CONFLICT")
			}
		} else if receipt.OutcomeSHA256 != nil && (receipt.Proof != "owned_container_removed" || receipt.AgentErrorCode == "") {
			return fail("MODEL_PREPARATION_REPORT_CONFLICT")
		}
		if (events > 0 || storedSHA != nil || len(outcome) > 0) && receipt.Proof != "owned_container_removed" {
			return fail("MODEL_PREPARATION_STOP_INVALID")
		}
		raw, e := json.Marshal(receipt)
		if e != nil || len(raw) > wire.MaximumStopReceiptBytes {
			return fail("MODEL_PREPARATION_STOP_INVALID")
		}
		receiptSHA := hash(raw)
		if existingStop != nil {
			if *existingStop != receiptSHA {
				return fail("MODEL_PREPARATION_STOP_CONFLICT")
			}
			out = wire.StopAcceptance{PreparationID: p.ID, AttemptID: c.AttemptID, ReceiptID: receipt.ReceiptID, State: p.State, GPUReleased: true, Duplicate: true}
			return nil
		}
		var gpuNode, gpuUUID, gpuState string
		var revision int
		if e = preparationIDRow(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "NODE_ID","GPU_UUID","STATE","REVISION" FROM "GPU_LEASES" WHERE "MODEL_PREPARATION_ID"=? FOR UPDATE`), "SELECT node_id::text,gpu_uuid,state,revision FROM gpu_leases WHERE model_preparation_id=$1 FOR UPDATE", `SELECT node_id,gpu_uuid,state,revision FROM gpu_leases WHERE model_preparation_id=? FOR UPDATE`, p.ID)), 0).Scan(&gpuNode, &gpuUUID, &gpuState, &revision); e != nil {
			return e
		}
		if gpuNode != node || gpuUUID != c.GPUUUID || revision != c.AttemptNumber || gpuState != "held" && gpuState != "recovery_required" {
			return fail("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		state, attemptState, taskState, code := "failed", "failed", "failed", "MODEL_PREPARATION_RESULT_UNAVAILABLE"
		switch {
		case !valid || p.CancelRequestedAt != nil || p.State == "cancelling" || failed.Status == "cancelled":
			state, attemptState, taskState, code = "cancelled", "cancelled", "cancelled", ""
			if !valid {
				code = "MODEL_PREPARATION_ACTOR_UNAVAILABLE"
			}
		case current.Status != "active":
			code = "MODEL_PREPARATION_NODE_UNAVAILABLE"
		case receipt.AgentErrorCode != "":
			code = receipt.AgentErrorCode
		case prepared.Status == "prepared":
			if health.sha != *storedSHA || !health.healthy {
				code = "MODEL_PREPARATION_ARTIFACT_INVALID"
			} else {
				attemptState, taskState, code = "prepared", "succeeded", ""
				state = "quality_failed"
				if prepared.GatePassed {
					state = "awaiting_review"
				}
			}
		case failed.Status == "failed":
			code = failed.ErrorCode
		}
		if storedSHA == nil && len(outcome) > 0 {
			if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_ATTEMPTS" SET "OUTCOME"=CAST(? AS CLOB),"OUTCOME_BYTES"=?,"OUTCOME_SHA256"=? WHERE "ID"=?`, 0), "UPDATE model_preparation_attempts SET outcome=$2,outcome_bytes=$3,outcome_sha256=$4 WHERE id=$1", `UPDATE model_preparation_attempts SET outcome=CAST(? AS JSON),outcome_bytes=?,outcome_sha256=? WHERE id=?`, []any{c.AttemptID, outcome, outcome, hash(outcome)}, []any{outcome, outcome, hash(outcome), c.AttemptID})); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_ATTEMPTS" SET "STATE"=?,"ERROR_CODE"=NULLIF(?,''),"PHYSICAL_STOP_RECEIPT"=CAST(? AS CLOB),"PHYSICAL_STOP_SHA256"=?,"PHYSICAL_STOPPED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"FINISHED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`, 2), `UPDATE model_preparation_attempts SET state=$2,error_code=NULLIF($3,''),physical_stop_receipt=$4,physical_stop_sha256=$5,physical_stopped_at=clock_timestamp(),finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, `UPDATE model_preparation_attempts SET state=?,error_code=NULLIF(?,''),physical_stop_receipt=CAST(? AS JSON),physical_stop_sha256=?,physical_stopped_at=UTC_TIMESTAMP(6),finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{c.AttemptID, attemptState, code, raw, receiptSHA}, []any{attemptState, code, raw, receiptSHA, c.AttemptID})); e != nil {
			return e
		}
		tag, e := tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`UPDATE "GPU_LEASES" SET "STATE"='released',"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "MODEL_PREPARATION_ID"=? AND "NODE_ID"=? AND "REVISION"=?`), "UPDATE gpu_leases SET state='released',updated_at=clock_timestamp() WHERE model_preparation_id=$1 AND node_id=$2 AND revision=$3", `UPDATE gpu_leases SET state='released',updated_at=UTC_TIMESTAMP(6) WHERE model_preparation_id=? AND node_id=? AND revision=?`, p.ID, node, c.AttemptNumber))
		if e != nil {
			return e
		}
		if tag != 1 {
			return fail("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "NODE_TASKS" SET "STATE"=?,"ERROR_CODE"=NULLIF(?,''),"COMPLETED_LEASE_HASH"=COALESCE("COMPLETED_LEASE_HASH","LEASE_TOKEN_HASH"),"LEASE_TOKEN_HASH"=NULL,"LEASE_EXPIRES_AT"=NULL,"COMPLETED_AT"=COALESCE("COMPLETED_AT",SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), `UPDATE node_tasks SET state=$2,error_code=NULLIF($3,''),completed_lease_hash=COALESCE(completed_lease_hash,lease_token_hash),lease_token_hash=NULL,lease_expires_at=NULL,completed_at=COALESCE(completed_at,clock_timestamp()),updated_at=clock_timestamp() WHERE id=$1`, `UPDATE node_tasks SET state=?,error_code=NULLIF(?,''),completed_lease_hash=COALESCE(completed_lease_hash,lease_token_hash),lease_token_hash=NULL,lease_expires_at=NULL,completed_at=COALESCE(completed_at,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{task, taskState, code}, []any{taskState, code, task})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATIONS" SET "STATE"=?,"ERROR_CODE"=NULLIF(?,''),"WAIT_CODE"=NULL,"REVISION"="REVISION"+1,"FINISHED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), `UPDATE model_preparations SET state=$2,error_code=NULLIF($3,''),wait_code=NULL,revision=revision+1,finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, `UPDATE model_preparations SET state=?,error_code=NULLIF(?,''),wait_code=NULL,revision=revision+1,finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{p.ID, state, code}, []any{state, code, p.ID})); e != nil {
			return e
		}
		if e = preparationAuditWithin(ctx, tx, a, w, "model.preparation_physical_stop_accepted", p.ID, "Authenticated exact owned-container absence; physical reservation released", uuid.NewString()); e != nil {
			return e
		}
		out = wire.StopAcceptance{PreparationID: p.ID, AttemptID: c.AttemptID, ReceiptID: receipt.ReceiptID, State: state, GPUReleased: true}
		return nil
	})
	if e != nil {
		return wire.StopAcceptance{}, e
	}
	return out, e
}

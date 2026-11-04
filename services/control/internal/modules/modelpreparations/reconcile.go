package modelpreparations

import (
	"database/sql"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type undeliveredReceipt struct {
	SchemaVersion int        `json:"schema_version"`
	ReceiptID     string     `json:"receipt_id"`
	Proof         string     `json:"proof"`
	Scope         wire.Scope `json:"scope"`
	NodeID        string     `json:"node_id"`
	TaskID        string     `json:"task_id"`
	AttemptID     string     `json:"attempt_id"`
	AttemptNumber int        `json:"attempt_number"`
	CommandSHA256 string     `json:"command_sha256"`
	ClaimAttempt  int        `json:"claim_attempt"`
}

// ReconcileNext can release only a transactionally proven never-delivered command. Delivered uncertainty always retains hardware ownership.
func (s *Service) ReconcileNext(ctx context.Context) (bool, error) {
	store, repositoryError := s.runtimeRepository()
	if repositoryError != nil {
		return false, repositoryError
	}
	var c candidate
	e := preparationIDRow(preparationQueryRow(ctx, store, preparationSelectedSQL(store, preparationDM(`SELECT "J"."ID","J"."TENANT_ID","J"."WORKSPACE_ID","J"."CURRENT_NODE_ID","J"."REQUESTED_BY","J"."REQUESTED_AUDIENCE","J"."REQUESTED_EPOCH","J"."MEMBERSHIP_AUTHORITY" FROM "MODEL_PREPARATIONS" "J" JOIN "MODEL_PREPARATION_ATTEMPTS" "A" ON "A"."ID"="J"."CURRENT_ATTEMPT_ID" AND "A"."PREPARATION_ID"="J"."ID" JOIN "INSTALLATION_STATE" "I" ON "I"."TENANT_ID"="J"."TENANT_ID" WHERE "I"."ID"=1 AND "I"."BOOTSTRAP_COMPLETE"=1 AND "J"."STATE" IN ('preparing','running','cancelling','recovery_required') AND "A"."PHYSICAL_STOPPED_AT" IS NULL AND "J"."NEXT_DISPATCH_AT"<=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) ORDER BY "J"."NEXT_DISPATCH_AT","J"."CREATED_AT","J"."ID" LIMIT 1`), `SELECT j.id::text,j.tenant_id::text,j.workspace_id::text,j.current_node_id::text,j.requested_by::text,j.requested_audience,j.requested_epoch,j.membership_authority::text FROM model_preparations j JOIN model_preparation_attempts a ON a.id=j.current_attempt_id AND a.preparation_id=j.id JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND j.state IN ('preparing','running','cancelling','recovery_required') AND a.physical_stopped_at IS NULL AND j.next_dispatch_at<=clock_timestamp() ORDER BY j.next_dispatch_at,j.created_at,j.id LIMIT 1`, `SELECT j.id,j.tenant_id,j.workspace_id,j.current_node_id,j.requested_by,j.requested_audience,j.requested_epoch,j.membership_authority FROM model_preparations j JOIN model_preparation_attempts a ON a.id=j.current_attempt_id AND a.preparation_id=j.id JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND j.state IN ('preparing','running','cancelling','recovery_required') AND a.physical_stopped_at IS NULL AND j.next_dispatch_at<=UTC_TIMESTAMP(6) ORDER BY j.next_dispatch_at,j.created_at,j.id LIMIT 1`)), 0, 1, 2, 3, 4, 7).Scan(&c.id, &c.tenant, &c.workspace, &c.node, &c.actor, &c.audience, &c.epoch, &c.membership)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	worked := false
	e = s.delegatedTransaction(ctx, c, func(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, n nodes.Node, j Preparation, valid bool) error {
		if j.State != "preparing" && j.State != "running" && j.State != "cancelling" && j.State != "recovery_required" || j.CurrentAttemptID == nil {
			return nil
		}
		// An expired lease nobody acknowledged was never delivered (ADR 0011).
		if e := s.Nodes.ReleaseUnacknowledgedWithin(ctx, tx, n, uuid.NewString()); e != nil {
			return e
		}
		var task, storedSHA string
		var stopHash []byte
		var events int64
		var hasOutcome, stopped bool
		if e = preparationIDRow(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "TASK_ID","COMMAND_SHA256","STOP_TOKEN_HASH","LAST_EVENT_SEQUENCE",CASE WHEN "OUTCOME" IS NOT NULL THEN 1 ELSE 0 END,CASE WHEN "PHYSICAL_STOPPED_AT" IS NOT NULL THEN 1 ELSE 0 END FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "ID"=? AND "PREPARATION_ID"=? FOR UPDATE`), "SELECT task_id::text,command_sha256,stop_token_hash,last_event_sequence,outcome IS NOT NULL,physical_stopped_at IS NOT NULL FROM model_preparation_attempts WHERE id=$1 AND preparation_id=$2 FOR UPDATE", `SELECT task_id,command_sha256,stop_token_hash,last_event_sequence,outcome IS NOT NULL,physical_stopped_at IS NOT NULL FROM model_preparation_attempts WHERE id=? AND preparation_id=? FOR UPDATE`, *j.CurrentAttemptID, j.ID)), 0).Scan(&task, &storedSHA, &stopHash, &events, &hasOutcome, &stopped); e != nil {
			return e
		}
		if stopped {
			return nil
		}
		t, e := retainedPreparationTask(ctx, tx, n, task)
		if e != nil {
			return e
		}
		command := t.command
		if e = bindFrozenCommand(j, command); e != nil {
			return e
		}
		commandSHA, e := command.Digest()
		if e != nil || storedSHA != commandSHA {
			return fail("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATIONS" SET "NEXT_DISPATCH_AT"=DATEADD(MICROSECOND,?*1000,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) WHERE "ID"=?`), "UPDATE model_preparations SET next_dispatch_at=clock_timestamp()+make_interval(secs=>$2::double precision/1000) WHERE id=$1", `UPDATE model_preparations SET next_dispatch_at=TIMESTAMPADD(MICROSECOND,?*1000,UTC_TIMESTAMP(6)) WHERE id=?`, []any{j.ID, s.policy().PollMilliseconds}, []any{s.policy().PollMilliseconds, j.ID})); e != nil {
			return e
		}
		if valid && n.Status == "active" && j.CancelRequestedAt == nil && !t.expired && (t.state == "pending" || t.state == "running") {
			return nil
		}
		var gpuNode, gpuUUID, gpuState string
		var revision int
		if e = preparationIDRow(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "NODE_ID","GPU_UUID","REVISION","STATE" FROM "GPU_LEASES" WHERE "MODEL_PREPARATION_ID"=? FOR UPDATE`), "SELECT node_id::text,gpu_uuid,revision,state FROM gpu_leases WHERE model_preparation_id=$1 FOR UPDATE", `SELECT node_id,gpu_uuid,revision,state FROM gpu_leases WHERE model_preparation_id=? FOR UPDATE`, j.ID)), 0).Scan(&gpuNode, &gpuUUID, &revision, &gpuState); e != nil {
			return e
		}
		if gpuNode != n.ID || gpuUUID != j.GPUUUID || revision != j.AttemptNumber || gpuState != "held" && gpuState != "recovery_required" {
			return fail("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		code := "MODEL_PREPARATION_LEASE_EXPIRED"
		if !valid {
			code = "MODEL_PREPARATION_ACTOR_UNAVAILABLE"
		} else if n.Status != "active" {
			code = "MODEL_PREPARATION_NODE_UNAVAILABLE"
		} else if j.CancelRequestedAt != nil {
			code = "MODEL_PREPARATION_CANCEL_PENDING_STOP"
		}
		if nodes.NeverDelivered(t.state, t.errorCode, t.attempt, events, hasOutcome) {
			sha, e := command.Digest()
			if e != nil {
				return e
			}
			receipt := undeliveredReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), Proof: "control_never_delivered", Scope: command.Scope, NodeID: n.ID, TaskID: task, AttemptID: command.AttemptID, AttemptNumber: command.AttemptNumber, CommandSHA256: sha, ClaimAttempt: 0}
			raw, _ := json.Marshal(receipt)
			state := "failed"
			if j.CancelRequestedAt != nil || !valid {
				state = "cancelled"
			}
			if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_ATTEMPTS" SET "STATE"=?,"ERROR_CODE"=?,"PHYSICAL_STOP_RECEIPT"=CAST(? AS CLOB),"PHYSICAL_STOP_SHA256"=?,"PHYSICAL_STOPPED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"FINISHED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`, 2), "UPDATE model_preparation_attempts SET state=$2,error_code=$3,physical_stop_receipt=$4,physical_stop_sha256=$5,physical_stopped_at=clock_timestamp(),finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", `UPDATE model_preparation_attempts SET state=?,error_code=?,physical_stop_receipt=CAST(? AS JSON),physical_stop_sha256=?,physical_stopped_at=UTC_TIMESTAMP(6),finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{command.AttemptID, state, code, raw, hash(raw)}, []any{state, code, raw, hash(raw), command.AttemptID})); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "NODE_TASKS" SET "STATE"=?,"ERROR_CODE"=?,"LEASE_TOKEN_HASH"=NULL,"LEASE_EXPIRES_AT"=NULL,"COMPLETED_AT"=COALESCE("COMPLETED_AT",SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), "UPDATE node_tasks SET state=$2,error_code=$3,lease_token_hash=NULL,lease_expires_at=NULL,completed_at=COALESCE(completed_at,clock_timestamp()),updated_at=clock_timestamp() WHERE id=$1", `UPDATE node_tasks SET state=?,error_code=?,lease_token_hash=NULL,lease_expires_at=NULL,completed_at=COALESCE(completed_at,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{task, state, code}, []any{state, code, task})); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`UPDATE "GPU_LEASES" SET "STATE"='released',"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "MODEL_PREPARATION_ID"=? AND "REVISION"=?`), "UPDATE gpu_leases SET state='released',updated_at=clock_timestamp() WHERE model_preparation_id=$1 AND revision=$2", `UPDATE gpu_leases SET state='released',updated_at=UTC_TIMESTAMP(6) WHERE model_preparation_id=? AND revision=?`, j.ID, command.AttemptNumber)); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATIONS" SET "STATE"=?,"ERROR_CODE"=?,"WAIT_CODE"=NULL,"REVISION"="REVISION"+1,"FINISHED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), "UPDATE model_preparations SET state=$2,error_code=$3,wait_code=NULL,revision=revision+1,finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", `UPDATE model_preparations SET state=?,error_code=?,wait_code=NULL,revision=revision+1,finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{j.ID, state, code}, []any{state, code, j.ID})); e != nil {
				return e
			}
			worked = true
			return preparationAuditWithin(ctx, tx, a, w, "model.preparation_undelivered_stopped", j.ID, "Control transaction proved command never delivered; exact physical reservation released", uuid.NewString())
		}
		if j.State == "recovery_required" && gpuState == "recovery_required" {
			return nil
		}
		if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATIONS" SET "STATE"='recovery_required',"ERROR_CODE"=?,"REVISION"="REVISION"+1,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), "UPDATE model_preparations SET state='recovery_required',error_code=$2,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1", `UPDATE model_preparations SET state='recovery_required',error_code=?,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{j.ID, code}, []any{code, j.ID})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_ATTEMPTS" SET "STATE"='recovery_required',"ERROR_CODE"=?,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), "UPDATE model_preparation_attempts SET state='recovery_required',error_code=$2,updated_at=clock_timestamp() WHERE id=$1", `UPDATE model_preparation_attempts SET state='recovery_required',error_code=?,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{command.AttemptID, code}, []any{code, command.AttemptID})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`UPDATE "GPU_LEASES" SET "STATE"='recovery_required',"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "MODEL_PREPARATION_ID"=? AND "REVISION"=?`), "UPDATE gpu_leases SET state='recovery_required',updated_at=clock_timestamp() WHERE model_preparation_id=$1 AND revision=$2", `UPDATE gpu_leases SET state='recovery_required',updated_at=UTC_TIMESTAMP(6) WHERE model_preparation_id=? AND revision=?`, j.ID, command.AttemptNumber)); e != nil {
			return e
		}
		worked = true
		return preparationAuditWithin(ctx, tx, a, w, "model.preparation_physical_stop_required", j.ID, "Delivered attempt lost execution authority; exact physical reservation retained", uuid.NewString())
	})
	if e != nil {
		return false, e
	}
	return worked, nil
}

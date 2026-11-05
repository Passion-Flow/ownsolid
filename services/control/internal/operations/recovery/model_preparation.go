package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"database/sql"
	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func PreparePreparationRemoval(ctx context.Context, accounts *identity.Service, node, job, reason string) (wire.RemovalChallenge, error) {
	store, e := accounts.Repository()
	if e != nil {
		return wire.RemovalChallenge{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(accounts.Config.Policy().ModelLifecycle.TransactionSeconds))
	defer cancel()

	var c wire.RemovalChallenge
	if !datawire.ValidID(node) || !datawire.ValidID(job) || !operatorReason(reason) {
		return c, errors.New("VALIDATION_FAILED")
	}
	var tenant, workspace, expectedSHA string
	var initialRaw []byte
	e = store.QueryRow(ctx, recoverySQL(store, `SELECT j.tenant_id::text,j.workspace_id::text,a.command_sha256,t.payload FROM model_preparations j JOIN installation_state i ON i.tenant_id=j.tenant_id JOIN managed_nodes n ON n.id=j.current_node_id JOIN model_preparation_attempts a ON a.id=j.current_attempt_id JOIN node_tasks t ON t.id=a.task_id AND t.node_id=n.id WHERE i.id=1 AND i.bootstrap_complete AND j.id=$1 AND n.id=$2 AND n.execution_profile=$3 AND a.physical_stopped_at IS NULL`, `SELECT j.tenant_id,j.workspace_id,a.command_sha256,t.payload FROM model_preparations j JOIN installation_state i ON i.tenant_id=j.tenant_id JOIN managed_nodes n ON n.id=j.current_node_id JOIN model_preparation_attempts a ON a.id=j.current_attempt_id JOIN node_tasks t ON t.id=a.task_id AND t.node_id=n.id WHERE i.id=1 AND i.bootstrap_complete AND j.id=? AND n.id=? AND n.execution_profile=? AND a.physical_stopped_at IS NULL`, job, node, agentwire.DockerModelLifecycleProfile)).Scan(&tenant, &workspace, &expectedSHA, &initialRaw)
	if errors.Is(e, sql.ErrNoRows) {
		return c, errors.New("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return c, e
	}
	initial, e := wire.DecodeCommand(initialRaw)
	if e != nil {
		return c, e
	}
	digest, e := initial.Digest()
	if e != nil || digest != expectedSHA || initial.Scope != (wire.Scope{TenantID: tenant, WorkspaceID: workspace, PreparationID: job}) {
		return c, errors.New("MODEL_PREPARATION_ATTEMPT_STALE")
	}
	// Fence commits first. Any later failure retains reservations and a safely revoked credential.
	if e = FenceInferenceNode(ctx, accounts, node, reason); e != nil {
		return c, e
	}
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var lockedWorkspace string
		if e = tx.QueryRow(ctx, recoverySQL(tx, `SELECT w.id::text FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=$1 AND w.id=$2 FOR NO KEY UPDATE OF w`, `SELECT w.id FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=? AND w.id=? FOR UPDATE`, tenant, workspace)).Scan(&lockedWorkspace); e != nil {
			return e
		}
		var fenced *time.Time
		var revoked bool
		if e = tx.QueryRow(ctx, recoverySQL(tx, "SELECT status='removed' AND credential_hash IS NULL,inference_fenced_at FROM managed_nodes WHERE id=$1 FOR UPDATE", `SELECT status='removed' AND credential_hash IS NULL,inference_fenced_at FROM managed_nodes WHERE id=? FOR UPDATE`, node)).Scan(&revoked, &fenced); e != nil {
			return e
		}
		if !revoked || fenced == nil {
			return errors.New("RECOVERY_NODE_NOT_FENCED")
		}
		var raw []byte
		if e = tx.QueryRow(ctx, recoveryRuntimeSQL(tx, `SELECT t.id::text,t.payload,clock_timestamp() FROM model_preparations j JOIN model_preparation_attempts a ON a.id=j.current_attempt_id JOIN node_tasks t ON t.id=a.task_id WHERE j.tenant_id=$1 AND j.workspace_id=$2 AND j.id=$3 AND j.current_node_id=$4 AND a.node_id=$4 AND a.physical_stopped_at IS NULL AND t.operation=$5 FOR UPDATE OF j,a,t`, `SELECT t.id,t.payload,UTC_TIMESTAMP(6) FROM model_preparations j JOIN model_preparation_attempts a ON a.id=j.current_attempt_id JOIN node_tasks t ON t.id=a.task_id WHERE j.tenant_id=? AND j.workspace_id=? AND j.id=? AND j.current_node_id=? AND a.node_id=? AND a.physical_stopped_at IS NULL AND t.operation=? FOR UPDATE`, []any{tenant, workspace, job, node, agentwire.ModelPreparationRunOperation}, []any{tenant, workspace, job, node, node, agentwire.ModelPreparationRunOperation})).Scan(&c.TaskID, &raw, &c.IssuedAt); e != nil {
			return errors.New("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		c.Command, e = wire.DecodeCommand(raw)
		if e != nil {
			return e
		}
		if c.Command.Scope != (wire.Scope{TenantID: tenant, WorkspaceID: workspace, PreparationID: job}) {
			return errors.New("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		c.SchemaVersion = 1
		c.ChallengeID = uuid.NewString()
		c.NodeID = node
		c.CommandSHA256, _ = c.Command.Digest()
		var currentSHA string
		if e = tx.QueryRow(ctx, recoverySQL(tx, "SELECT command_sha256 FROM model_preparation_attempts WHERE id=$1 AND preparation_id=$2 AND task_id=$3 AND node_id=$4", `SELECT command_sha256 FROM model_preparation_attempts WHERE id=? AND preparation_id=? AND task_id=? AND node_id=?`, c.Command.AttemptID, job, c.TaskID, node)).Scan(&currentSHA); e != nil || currentSHA != c.CommandSHA256 {
			return errors.New("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		c.ExpiresAt = c.IssuedAt.Add(wire.RemovalChallengeLifetime)
		if c.Validate(time.Now()) != nil || c.IssuedAt.Before(*fenced) {
			return errors.New("MODEL_PREPARATION_REMOVAL_CHALLENGE_INVALID")
		}
		metadata, _ := json.Marshal(c)
		if _, e = tx.Exec(ctx, recoverySQL(tx, `INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES($1,clock_timestamp(),$2,$3,'operator','model.preparation_removal_challenged','success','model_preparation_attempt',$4,$5,$6,$7)`, `INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,?,'operator','model.preparation_removal_challenged','success','model_preparation_attempt',?,?,?,CAST(? AS JSON))`, c.ChallengeID, tenant, workspace, c.Command.AttemptID, reason, uuid.NewString(), metadata)); e != nil {
			return e
		}
		return nil
	})
	if e != nil {
		return wire.RemovalChallenge{}, e
	}
	return c, nil
}

// ReconcilePreparationRemoval accepts an explicit isolated local root attestation; it never accepts prepared success.
func ReconcilePreparationRemoval(ctx context.Context, accounts *identity.Service, receipt wire.RemovalReceipt, reason string) error {
	store, e := accounts.Repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(accounts.Config.Policy().ModelLifecycle.TransactionSeconds))
	defer cancel()

	if receipt.Validate(time.Now()) != nil || !operatorReason(reason) {
		return errors.New("MODEL_PREPARATION_REMOVAL_RECEIPT_INVALID")
	}
	c := receipt.Challenge
	scope := c.Command.Scope
	return store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var lockedWorkspace string
		if e = tx.QueryRow(ctx, recoverySQL(tx, `SELECT w.id::text FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=$1 AND w.id=$2 FOR NO KEY UPDATE OF w`, `SELECT w.id FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=? AND w.id=? FOR UPDATE`, scope.TenantID, scope.WorkspaceID)).Scan(&lockedWorkspace); e != nil {
			return e
		}
		var challengeRaw []byte
		if e = tx.QueryRow(ctx, recoverySQL(tx, `SELECT metadata FROM audit_events WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND audience='operator' AND action='model.preparation_removal_challenged' AND target_type='model_preparation_attempt' AND target_id=$4`, `SELECT metadata FROM audit_events WHERE id=? AND tenant_id=? AND workspace_id=? AND audience='operator' AND action='model.preparation_removal_challenged' AND target_type='model_preparation_attempt' AND target_id=?`, c.ChallengeID, scope.TenantID, scope.WorkspaceID, c.Command.AttemptID)).Scan(&challengeRaw); e != nil {
			return errors.New("MODEL_PREPARATION_REMOVAL_CHALLENGE_INVALID")
		}
		stored, e := wire.DecodeRemovalChallenge(challengeRaw, time.Now())
		if e != nil || stored.Digest() != c.Digest() {
			return errors.New("MODEL_PREPARATION_REMOVAL_CHALLENGE_INVALID")
		}
		var revoked bool
		var fenced *time.Time
		if e = tx.QueryRow(ctx, recoverySQL(tx, "SELECT status='removed' AND credential_hash IS NULL,inference_fenced_at FROM managed_nodes WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND execution_profile=$4 FOR UPDATE", `SELECT status='removed' AND credential_hash IS NULL,inference_fenced_at FROM managed_nodes WHERE id=? AND tenant_id=? AND workspace_id=? AND execution_profile=? FOR UPDATE`, c.NodeID, scope.TenantID, scope.WorkspaceID, agentwire.DockerModelLifecycleProfile)).Scan(&revoked, &fenced); e != nil {
			return e
		}
		if !revoked || fenced == nil || c.IssuedAt.Before(*fenced) {
			return errors.New("RECOVERY_NODE_NOT_FENCED")
		}
		var jobState, currentAttempt, node string
		var cancelled bool
		if e = tx.QueryRow(ctx, recoverySQL(tx, "SELECT state,current_attempt_id::text,current_node_id::text,cancel_requested_at IS NOT NULL FROM model_preparations WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 FOR UPDATE", `SELECT state,current_attempt_id,current_node_id,cancel_requested_at IS NOT NULL FROM model_preparations WHERE id=? AND tenant_id=? AND workspace_id=? FOR UPDATE`, scope.PreparationID, scope.TenantID, scope.WorkspaceID)).Scan(&jobState, &currentAttempt, &node, &cancelled); e != nil {
			return e
		}
		if currentAttempt != c.Command.AttemptID || node != c.NodeID || jobState == "approved" || jobState == "rejected" || jobState == "awaiting_review" || jobState == "quality_failed" {
			return errors.New("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		var previous *string
		var attemptNumber int
		var commandSHA, storedTask, storedNode string
		var eventCount int64
		var outcomeSHA *string
		if e = tx.QueryRow(ctx, recoverySQL(tx, "SELECT physical_stop_sha256,attempt_number,command_sha256,task_id::text,node_id::text,last_event_sequence,outcome_sha256 FROM model_preparation_attempts WHERE id=$1 AND preparation_id=$2 AND node_id=$3 AND task_id=$4 FOR UPDATE", `SELECT physical_stop_sha256,attempt_number,command_sha256,task_id,node_id,last_event_sequence,outcome_sha256 FROM model_preparation_attempts WHERE id=? AND preparation_id=? AND node_id=? AND task_id=? FOR UPDATE`, currentAttempt, scope.PreparationID, c.NodeID, c.TaskID)).Scan(&previous, &attemptNumber, &commandSHA, &storedTask, &storedNode, &eventCount, &outcomeSHA); e != nil {
			return e
		}
		if attemptNumber != c.Command.AttemptNumber || commandSHA != c.CommandSHA256 || storedTask != c.TaskID || storedNode != c.NodeID {
			return errors.New("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		if (eventCount > 0 || outcomeSHA != nil || receipt.Stop.OutcomeSHA256 != nil) && receipt.Stop.Proof != "owned_container_removed" {
			return errors.New("MODEL_PREPARATION_REMOVAL_RECEIPT_INVALID")
		}
		if outcomeSHA != nil && receipt.Stop.OutcomeSHA256 != nil && *outcomeSHA != *receipt.Stop.OutcomeSHA256 {
			return errors.New("MODEL_PREPARATION_REPORT_CONFLICT")
		}
		var taskPayload []byte
		if e = tx.QueryRow(ctx, recoverySQL(tx, "SELECT payload FROM node_tasks WHERE id=$1 AND node_id=$2 AND tenant_id=$3 AND workspace_id=$4 AND operation=$5 FOR UPDATE", `SELECT payload FROM node_tasks WHERE id=? AND node_id=? AND tenant_id=? AND workspace_id=? AND operation=? FOR UPDATE`, c.TaskID, c.NodeID, scope.TenantID, scope.WorkspaceID, wire.RunOperation)).Scan(&taskPayload); e != nil {
			return e
		}
		actualCommand, e := wire.DecodeCommand(taskPayload)
		if e != nil {
			return e
		}
		actualSHA, e := actualCommand.Digest()
		if e != nil || actualSHA != c.CommandSHA256 {
			return errors.New("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		raw, _ := json.Marshal(receipt)
		sum := sha256.Sum256(raw)
		digest := hex.EncodeToString(sum[:])
		if previous != nil {
			var matches bool
			if e = tx.QueryRow(ctx, recoverySQL(tx, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE tenant_id=$1 AND workspace_id=$2 AND audience='operator' AND action='model.preparation_removal_reconciled' AND target_id=$3 AND metadata->>'receipt_sha256'=$4)`, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE tenant_id=? AND workspace_id=? AND audience='operator' AND action='model.preparation_removal_reconciled' AND target_id=? AND JSON_UNQUOTE(JSON_EXTRACT(metadata,'$.receipt_sha256'))=?)`, scope.TenantID, scope.WorkspaceID, currentAttempt, digest)).Scan(&matches); e != nil {
				return e
			}
			if !matches {
				return errors.New("IDEMPOTENCY_CONFLICT")
			}
			return nil
		}
		var inflight bool
		if e = tx.QueryRow(ctx, recoverySQL(tx, "SELECT EXISTS(SELECT 1 FROM node_tasks WHERE node_id=$1 AND state IN ('pending','running'))", `SELECT EXISTS(SELECT 1 FROM node_tasks WHERE node_id=? AND state IN ('pending','running'))`, c.NodeID)).Scan(&inflight); e != nil {
			return e
		}
		if inflight {
			return errors.New("RECOVERY_NODE_NOT_FENCED")
		}
		var gpu, leaseNode, leaseState string
		var revision int
		if e = tx.QueryRow(ctx, recoverySQL(tx, "SELECT gpu_uuid,node_id::text,state,revision FROM gpu_leases WHERE model_preparation_id=$1 FOR UPDATE", `SELECT gpu_uuid,node_id,state,revision FROM gpu_leases WHERE model_preparation_id=? FOR UPDATE`, scope.PreparationID)).Scan(&gpu, &leaseNode, &leaseState, &revision); e != nil {
			return e
		}
		if gpu != c.Command.GPUUUID || leaseNode != c.NodeID || revision != c.Command.AttemptNumber || leaseState != "recovery_required" {
			return errors.New("MODEL_PREPARATION_ATTEMPT_STALE")
		}
		state := "failed"
		if cancelled || jobState == "cancelled" || jobState == "cancelling" {
			state = "cancelled"
		}
		proof, _ := json.Marshal(receipt.Stop)
		proofSum := sha256.Sum256(proof)
		if _, e = tx.Exec(ctx, recoveryRuntimeSQL(tx, "UPDATE model_preparation_attempts SET state=$2,error_code='MODEL_PREPARATION_OPERATOR_STOPPED',physical_stop_receipt=$3,physical_stop_sha256=$4,physical_stopped_at=$5,finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", `UPDATE model_preparation_attempts SET state=?,error_code='MODEL_PREPARATION_OPERATOR_STOPPED',physical_stop_receipt=CAST(? AS JSON),physical_stop_sha256=?,physical_stopped_at=?,finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{currentAttempt, state, proof, hex.EncodeToString(proofSum[:]), receipt.ObservedAt}, []any{state, proof, hex.EncodeToString(proofSum[:]), receipt.ObservedAt, currentAttempt})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, recoveryRuntimeSQL(tx, "UPDATE model_preparations SET state=$2,error_code='MODEL_PREPARATION_OPERATOR_STOPPED',wait_code=NULL,finished_at=clock_timestamp(),revision=revision+1,updated_at=clock_timestamp() WHERE id=$1", `UPDATE model_preparations SET state=?,error_code='MODEL_PREPARATION_OPERATOR_STOPPED',wait_code=NULL,finished_at=UTC_TIMESTAMP(6),revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{scope.PreparationID, state}, []any{state, scope.PreparationID})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, recoverySQL(tx, "UPDATE gpu_leases SET state='released',updated_at=clock_timestamp() WHERE model_preparation_id=$1 AND revision=$2 AND node_id=$3", `UPDATE gpu_leases SET state='released',updated_at=UTC_TIMESTAMP(6) WHERE model_preparation_id=? AND revision=? AND node_id=?`, scope.PreparationID, revision, c.NodeID)); e != nil {
			return e
		}
		if e = operatorAuditWithin(ctx, tx, scope.TenantID, scope.WorkspaceID, "model.preparation_removal_reconciled", "model_preparation_attempt", currentAttempt, reason, map[string]any{"receipt_sha256": digest, "receipt_id": receipt.ReceiptID, "challenge_id": c.ChallengeID, "source": "explicit_local_root_operator_attestation", "accepted_preparation_success": false}); e != nil {
			return e
		}
		return nil
	})
}

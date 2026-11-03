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
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"strings"
)

func PrepareTrainingRemoval(ctx context.Context, accounts *identity.Service, node, job, reason string) (wire.RemovalChallenge, error) {
	var c wire.RemovalChallenge
	if !datawire.ValidID(node) || !datawire.ValidID(job) || !operatorReason(reason) {
		return c, errors.New("VALIDATION_FAILED")
	}
	var tenant, workspace string
	missing := false
	e := trainingRemovalTransaction(ctx, accounts, func(ctx context.Context, tx transaction.Executor) error {
		e := tx.QueryRow(ctx, trainingRemovalStatement(tx, `SELECT j.tenant_id::text,j.workspace_id::text FROM training_jobs j JOIN installation_state i ON i.tenant_id=j.tenant_id JOIN managed_nodes n ON n.id=j.current_node_id JOIN training_attempts a ON a.id=j.current_attempt_id WHERE i.id=1 AND j.id=$1 AND n.id=$2 AND n.execution_profile=$3 AND a.physical_stopped_at IS NULL`, `SELECT j.tenant_id,j.workspace_id FROM training_jobs j JOIN installation_state i ON i.tenant_id=j.tenant_id JOIN managed_nodes n ON n.id=j.current_node_id JOIN training_attempts a ON a.id=j.current_attempt_id WHERE i.id=1 AND j.id=? AND n.id=? AND n.execution_profile=? AND a.physical_stopped_at IS NULL`, []any{job, node, agentwire.DockerAIProfile}, []any{job, node, agentwire.DockerAIProfile})).Scan(&tenant, &workspace)
		if errors.Is(e, sql.ErrNoRows) {
			missing = true
			return nil
		}
		return e
	})
	if missing && e == nil {
		return wire.RemovalChallenge{}, errors.New("RESOURCE_NOT_FOUND")
	}
	if errors.Is(e, sql.ErrNoRows) {
		return c, errors.New("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return c, e
	}
	// Fence commits first. Any later failure retains reservations and a safely revoked credential.
	if e = FenceInferenceNode(ctx, accounts, node, reason); e != nil {
		return c, e
	}
	e = trainingRemovalTransaction(ctx, accounts, func(ctx context.Context, tx transaction.Executor) error {
		var lockedWorkspace string
		if e = tx.QueryRow(ctx, trainingRemovalStatement(tx, "SELECT w.id::text FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=$1 AND w.id=$2 FOR UPDATE OF w", "SELECT w.id FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND w.tenant_id=? AND w.id=? FOR UPDATE", []any{tenant, workspace}, []any{tenant, workspace})).Scan(&lockedWorkspace); e != nil {
			return e
		}
		var fenced *time.Time
		var revoked bool
		if e = tx.QueryRow(ctx, trainingRemovalStatement(tx, "SELECT status='removed' AND credential_hash IS NULL,inference_fenced_at FROM managed_nodes WHERE id=$1 FOR UPDATE", "SELECT status='removed' AND credential_hash IS NULL,inference_fenced_at FROM managed_nodes WHERE id=? FOR UPDATE", []any{node}, []any{node})).Scan(&revoked, &fenced); e != nil {
			return e
		}
		if !revoked || fenced == nil {
			return errors.New("RECOVERY_NODE_NOT_FENCED")
		}
		var raw []byte
		if e = tx.QueryRow(ctx, trainingRemovalStatement(tx, `SELECT t.id::text,t.payload,clock_timestamp() FROM training_jobs j JOIN training_attempts a ON a.id=j.current_attempt_id JOIN node_tasks t ON t.id=a.task_id WHERE j.tenant_id=$1 AND j.workspace_id=$2 AND j.id=$3 AND j.current_node_id=$4 AND a.node_id=$4 AND a.physical_stopped_at IS NULL AND t.operation=$5 FOR UPDATE OF j,a,t`, `SELECT t.id,t.payload,UTC_TIMESTAMP(6) FROM training_jobs j JOIN training_attempts a ON a.id=j.current_attempt_id JOIN node_tasks t ON t.id=a.task_id WHERE j.tenant_id=? AND j.workspace_id=? AND j.id=? AND j.current_node_id=? AND a.node_id=? AND a.physical_stopped_at IS NULL AND t.operation=? FOR UPDATE`, []any{tenant, workspace, job, node, agentwire.TrainingRunOperation}, []any{tenant, workspace, job, node, node, agentwire.TrainingRunOperation})).Scan(&c.TaskID, &raw, &c.IssuedAt); e != nil {
			return errors.New("TRAINING_ATTEMPT_STALE")
		}
		c.Command, e = wire.DecodeCommand(raw)
		if e != nil {
			return e
		}
		if c.Command.Scope != (wire.Scope{TenantID: tenant, WorkspaceID: workspace, JobID: job}) {
			return errors.New("TRAINING_ATTEMPT_STALE")
		}
		c.SchemaVersion = 1
		c.ChallengeID = uuid.NewString()
		c.NodeID = node
		c.CommandSHA256, _ = c.Command.Digest()
		c.ExpiresAt = c.IssuedAt.Add(wire.RemovalChallengeLifetime)
		if c.Validate(time.Now()) != nil || c.IssuedAt.Before(*fenced) {
			return errors.New("TRAINING_REMOVAL_CHALLENGE_INVALID")
		}
		metadata, _ := json.Marshal(c)
		if _, e = tx.Exec(ctx, trainingRemovalStatement(tx, `INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES($1,clock_timestamp(),$2,$3,'operator','training.removal_challenged','success','training_attempt',$4,$5,$6,$7)`, `INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,?,'operator','training.removal_challenged','success','training_attempt',?,?,?,CAST(? AS JSON))`, []any{c.ChallengeID, tenant, workspace, c.Command.AttemptID, reason, uuid.NewString(), metadata}, []any{c.ChallengeID, tenant, workspace, c.Command.AttemptID, reason, uuid.NewString(), metadata})); e != nil {
			return e
		}
		return nil
	})
	if e != nil {
		return wire.RemovalChallenge{}, e
	}
	return c, nil
}

// ReconcileTrainingRemoval accepts an explicit isolated local root attestation; it never accepts trained success.
func ReconcileTrainingRemoval(ctx context.Context, accounts *identity.Service, receipt wire.RemovalReceipt, reason string) error {
	if receipt.Validate(time.Now()) != nil || !operatorReason(reason) {
		return errors.New("TRAINING_REMOVAL_RECEIPT_INVALID")
	}
	c := receipt.Challenge
	scope := c.Command.Scope
	return trainingRemovalTransaction(ctx, accounts, func(ctx context.Context, tx transaction.Executor) error {
		var e error
		var lockedWorkspace string
		if e = tx.QueryRow(ctx, trainingRemovalStatement(tx, "SELECT w.id::text FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=$1 AND w.id=$2 FOR UPDATE OF w", "SELECT w.id FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND w.tenant_id=? AND w.id=? FOR UPDATE", []any{scope.TenantID, scope.WorkspaceID}, []any{scope.TenantID, scope.WorkspaceID})).Scan(&lockedWorkspace); e != nil {
			return e
		}
		var challengeRaw []byte
		if e = tx.QueryRow(ctx, trainingRemovalStatement(tx, `SELECT metadata FROM audit_events WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND audience='operator' AND action='training.removal_challenged' AND target_type='training_attempt' AND target_id=$4`, `SELECT metadata FROM audit_events WHERE id=? AND tenant_id=? AND workspace_id=? AND audience='operator' AND action='training.removal_challenged' AND target_type='training_attempt' AND target_id=?`, []any{c.ChallengeID, scope.TenantID, scope.WorkspaceID, c.Command.AttemptID}, []any{c.ChallengeID, scope.TenantID, scope.WorkspaceID, c.Command.AttemptID})).Scan(&challengeRaw); e != nil {
			return errors.New("TRAINING_REMOVAL_CHALLENGE_INVALID")
		}
		stored, e := wire.DecodeRemovalChallenge(challengeRaw, time.Now())
		if e != nil || stored.Digest() != c.Digest() {
			return errors.New("TRAINING_REMOVAL_CHALLENGE_INVALID")
		}
		var revoked bool
		var fenced *time.Time
		if e = tx.QueryRow(ctx, trainingRemovalStatement(tx, "SELECT status='removed' AND credential_hash IS NULL,inference_fenced_at FROM managed_nodes WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND execution_profile=$4 FOR UPDATE", "SELECT status='removed' AND credential_hash IS NULL,inference_fenced_at FROM managed_nodes WHERE id=? AND tenant_id=? AND workspace_id=? AND execution_profile=? FOR UPDATE", []any{c.NodeID, scope.TenantID, scope.WorkspaceID, agentwire.DockerAIProfile}, []any{c.NodeID, scope.TenantID, scope.WorkspaceID, agentwire.DockerAIProfile})).Scan(&revoked, &fenced); e != nil {
			return e
		}
		if !revoked || fenced == nil || c.IssuedAt.Before(*fenced) {
			return errors.New("RECOVERY_NODE_NOT_FENCED")
		}
		var jobState, currentAttempt, node string
		var cancelled bool
		if e = tx.QueryRow(ctx, trainingRemovalStatement(tx, "SELECT state,current_attempt_id::text,current_node_id::text,cancel_requested_at IS NOT NULL FROM training_jobs WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 FOR UPDATE", "SELECT state,current_attempt_id,current_node_id,cancel_requested_at IS NOT NULL FROM training_jobs WHERE id=? AND tenant_id=? AND workspace_id=? FOR UPDATE", []any{scope.JobID, scope.TenantID, scope.WorkspaceID}, []any{scope.JobID, scope.TenantID, scope.WorkspaceID})).Scan(&jobState, &currentAttempt, &node, &cancelled); e != nil {
			return e
		}
		if currentAttempt != c.Command.AttemptID || node != c.NodeID || jobState == "succeeded" {
			return errors.New("TRAINING_ATTEMPT_STALE")
		}
		var previous *string
		var attemptNumber int
		if e = tx.QueryRow(ctx, trainingRemovalStatement(tx, "SELECT physical_stop_sha256,attempt_number FROM training_attempts WHERE id=$1 AND job_id=$2 AND node_id=$3 AND task_id=$4 FOR UPDATE", "SELECT physical_stop_sha256,attempt_number FROM training_attempts WHERE id=? AND job_id=? AND node_id=? AND task_id=? FOR UPDATE", []any{currentAttempt, scope.JobID, c.NodeID, c.TaskID}, []any{currentAttempt, scope.JobID, c.NodeID, c.TaskID})).Scan(&previous, &attemptNumber); e != nil {
			return e
		}
		if attemptNumber != c.Command.AttemptNumber {
			return errors.New("TRAINING_ATTEMPT_STALE")
		}
		raw, _ := json.Marshal(receipt)
		sum := sha256.Sum256(raw)
		digest := hex.EncodeToString(sum[:])
		if previous != nil {
			var matches bool
			if e = tx.QueryRow(ctx, trainingRemovalStatement(tx, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE tenant_id=$1 AND workspace_id=$2 AND audience='operator' AND action='training.removal_reconciled' AND target_id=$3 AND metadata->>'receipt_sha256'=$4)`, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE tenant_id=? AND workspace_id=? AND audience='operator' AND action='training.removal_reconciled' AND target_id=? AND JSON_UNQUOTE(JSON_EXTRACT(metadata,'$.receipt_sha256'))=?)`, []any{scope.TenantID, scope.WorkspaceID, currentAttempt, digest}, []any{scope.TenantID, scope.WorkspaceID, currentAttempt, digest})).Scan(&matches); e != nil {
				return e
			}
			if !matches {
				return errors.New("IDEMPOTENCY_CONFLICT")
			}
			return nil
		}
		var inflight bool
		if e = tx.QueryRow(ctx, trainingRemovalStatement(tx, "SELECT EXISTS(SELECT 1 FROM node_tasks WHERE node_id=$1 AND state IN ('pending','running'))", "SELECT EXISTS(SELECT 1 FROM node_tasks WHERE node_id=? AND state IN ('pending','running'))", []any{c.NodeID}, []any{c.NodeID})).Scan(&inflight); e != nil {
			return e
		}
		if inflight {
			return errors.New("RECOVERY_NODE_NOT_FENCED")
		}
		var gpu, leaseNode, leaseState string
		var revision int
		if e = tx.QueryRow(ctx, trainingRemovalStatement(tx, "SELECT gpu_uuid,node_id::text,state,revision FROM gpu_leases WHERE training_job_id=$1 FOR UPDATE", "SELECT gpu_uuid,node_id,state,revision FROM gpu_leases WHERE training_job_id=? FOR UPDATE", []any{scope.JobID}, []any{scope.JobID})).Scan(&gpu, &leaseNode, &leaseState, &revision); e != nil {
			return e
		}
		if gpu != c.Command.GPUUUID || leaseNode != c.NodeID || revision != c.Command.AttemptNumber || leaseState != "recovery_required" {
			return errors.New("TRAINING_ATTEMPT_STALE")
		}
		state := "failed"
		if cancelled || jobState == "cancelled" || jobState == "cancelling" {
			state = "cancelled"
		}
		proof, _ := json.Marshal(receipt.Stop)
		proofSum := sha256.Sum256(proof)
		if _, e = tx.Exec(ctx, trainingRemovalStatement(tx, "UPDATE training_attempts SET state=$2,error_code='TRAINING_OPERATOR_STOPPED',physical_stop_receipt=$3,physical_stop_sha256=$4,physical_stopped_at=$5,finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_attempts SET state=?,error_code='TRAINING_OPERATOR_STOPPED',physical_stop_receipt=CAST(? AS JSON),physical_stop_sha256=?,physical_stopped_at=?,finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{currentAttempt, state, proof, hex.EncodeToString(proofSum[:]), receipt.ObservedAt}, []any{state, proof, hex.EncodeToString(proofSum[:]), receipt.ObservedAt, currentAttempt})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, trainingRemovalStatement(tx, "UPDATE training_jobs SET state=$2,error_code='TRAINING_OPERATOR_STOPPED',wait_code=NULL,finished_at=clock_timestamp(),revision=revision+1,updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_jobs SET state=?,error_code='TRAINING_OPERATOR_STOPPED',wait_code=NULL,finished_at=UTC_TIMESTAMP(6),revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{scope.JobID, state}, []any{state, scope.JobID})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, trainingRemovalStatement(tx, "UPDATE gpu_leases SET state='released',updated_at=clock_timestamp() WHERE training_job_id=$1 AND revision=$2 AND node_id=$3", "UPDATE gpu_leases SET state='released',updated_at=UTC_TIMESTAMP(6) WHERE training_job_id=? AND revision=? AND node_id=?", []any{scope.JobID, revision, c.NodeID}, []any{scope.JobID, revision, c.NodeID})); e != nil {
			return e
		}
		if e = operatorAuditWithin(ctx, tx, scope.TenantID, scope.WorkspaceID, "training.removal_reconciled", "training_attempt", currentAttempt, reason, map[string]any{"receipt_sha256": digest, "receipt_id": receipt.ReceiptID, "challenge_id": c.ChallengeID, "source": "explicit_local_root_operator_attestation", "accepted_training_success": false}); e != nil {
			return e
		}
		return nil
	})
}

// These stopped-runtime operations share the identity-owned selected store.
// They never retry a possibly committed fence, challenge or stop attestation.
func trainingRemovalTransaction(ctx context.Context, accounts *identity.Service, fn func(context.Context, transaction.Executor) error) error {
	if ctx == nil || accounts == nil {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	store, e := accounts.Repository()
	if e != nil {
		return trainingRemovalError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(accounts.Config.Policy().Training.TransactionSeconds))
	defer cancel()
	return trainingRemovalError(store.Within(ctx, false, fn))
}
func trainingRemovalStatement(q transaction.Executor, pg, my string, pgArgs, myArgs []any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, myArgs...)
	}
	return transaction.SQL(transaction.PostgreSQL, pg, pgArgs...)
}
func trainingRemovalError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, context.Canceled) {
		return errors.New(string(transaction.Cancelled))
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return errors.New(string(transaction.Timeout))
	}
	code := e.Error()
	if len(code) > 120 {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	for _, c := range code {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return errors.New("STORAGE_UNAVAILABLE")
		}
	}
	for _, prefix := range []string{"TRAINING_", "RECOVERY_", "STORAGE_"} {
		if strings.HasPrefix(code, prefix) {
			return errors.New(code)
		}
	}
	switch code {
	case "ACCESS_DENIED", "RESOURCE_NOT_FOUND", "VALIDATION_FAILED", "IDEMPOTENCY_CONFLICT":
		return errors.New(code)
	}
	return errors.New("STORAGE_UNAVAILABLE")
}

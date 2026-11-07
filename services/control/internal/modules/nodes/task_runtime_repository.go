package nodes

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Runtime errors are public codes only; vendor messages and SQL never escape.
func taskRuntimeError(e error) error {
	if e == nil {
		return nil
	}
	if kind, ok := transaction.CommitOutcome(e); ok {
		return fail(string(kind))
	}
	if transaction.IsCancellationOnly(e) {
		if errors.Is(e, context.Canceled) {
			return fail(string(transaction.Cancelled))
		}
		return fail(string(transaction.Timeout))
	}
	if e.Error() != "" && (wire.KnownInferenceError(e.Error()) || wire.ComponentErrors[e.Error()] || wire.KnownMaintenanceError(e.Error())) {
		return fail(e.Error())
	}
	switch e.Error() {
	case "NODE_IDENTITY_REQUIRED", "NODE_STORAGE_MISMATCH", "NODE_BUSY", "NODE_UNAVAILABLE", "ACCESS_DENIED", "AUTH_SESSION_EXPIRED", "RESOURCE_NOT_FOUND", "VALIDATION_FAILED", "IDEMPOTENCY_CONFLICT", "AGENT_UNAUTHORIZED", "AGENT_LEASE_STALE", "AGENT_PROFILE_REJECTED", "AGENT_OPERATION_UNSUPPORTED", "COMPONENT_PROFILE_UNAVAILABLE", "COMPONENT_MAINTENANCE_REQUIRED", "TRAINING_AUTHORITY_REQUIRED", "MODEL_PREPARATION_AUTHORITY_REQUIRED", "INFERENCE_OWNERSHIP_CONFLICT", "INFERENCE_REVISION_CONFLICT", "INFERENCE_CONFIGURATION_INVALID", "INFERENCE_RECOVERY_REQUIRED", "INFERENCE_RESOURCE_BUSY", "INFERENCE_PLATFORM_UNSUPPORTED", "INFERENCE_HOST_MEMORY_LOW", "INFERENCE_PREFLIGHT_REQUIRED", "INFERENCE_DRIVER_UNSUPPORTED", "INFERENCE_GPU_UNSUPPORTED", "INFERENCE_MIG_UNSUPPORTED", "INFERENCE_GPU_MEMORY_LOW", "INFERENCE_GPU_UNAVAILABLE", "MODEL_NOT_READY", "MODEL_FILE_INVALID", "MODEL_DERIVED_CATALOG_INVALID", "MODEL_FILE_INTEGRITY_FAILED", "MODEL_FILE_CHANGED", "MODEL_IO_FAILED", "NODE_POSTGRESQL_CALLBACK_REQUIRED":
		return fail(e.Error())
	default:
		return nodeRepositoryError(e)
	}
}
func (s *Service) taskExecutor(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	if e := ctx.Err(); e != nil {
		return taskRuntimeError(e)
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	if store.Dialect() != q.Dialect() || q.Dialect() != transaction.MySQL && q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.Dameng {
		return fail("NODE_STORAGE_MISMATCH")
	}
	return nil
}
func (s *Service) taskActor(ctx context.Context, a identity.Auth, fn func(context.Context, transaction.Executor) error) error {
	if ctx == nil || !isAdmin(a) {
		return fail("ACCESS_DENIED")
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	return taskRuntimeError(store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		return fn(ctx, q)
	}))
}
func (s *Service) taskNodeWithin(ctx context.Context, q transaction.Executor, a identity.Auth, id string) (Node, error) {
	if !isAdmin(a) || !nodeID(id) {
		return Node{}, fail("ACCESS_DENIED")
	}
	// Only discover scope before the exclusive Workspace fence; publish no node yet.
	var workspace string
	e := q.QueryRow(ctx, nodeSQL(q, "SELECT n.workspace_id::text FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND n.id=$1", "SELECT n.workspace_id FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND n.id=?", `SELECT n.workspace_id FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND n.id=?`, id)).Scan(&workspace)
	if errors.Is(e, sql.ErrNoRows) {
		return Node{}, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return Node{}, e
	}
	w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
	if e != nil {
		return Node{}, e
	}
	if w.Status != "active" {
		return Node{}, fail("ACCESS_DENIED")
	}
	tenant, e := nodeTenant(ctx, q)
	if e != nil {
		return Node{}, e
	}
	if tenant != w.TenantID {
		return Node{}, fail("RESOURCE_NOT_FOUND")
	}
	n, e := s.lockedUpdateNode(ctx, q, tenant, w.ID, id)
	if e != nil {
		return Node{}, e
	}
	if n.Status != "active" {
		return Node{}, fail("ACCESS_DENIED")
	}
	return n, nil
}
func (s *Service) taskByIDWithin(ctx context.Context, q transaction.Executor, n Node, id string, lock bool) (Task, error) {
	pg, my := "SELECT "+taskFields+" FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4", "SELECT "+mysqlTaskFields+" FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?"
	dm := "SELECT " + damengTaskFields + " FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?"
	if lock {
		pg += " FOR UPDATE"
		my += " FOR UPDATE"
		dm += " FOR UPDATE"
	}
	return scanTask(q.QueryRow(ctx, nodeSQL(q, pg, my, dm, n.TenantID, n.WorkspaceID, n.ID, id)))
}
func (s *Service) taskAuditWithin(ctx context.Context, q transaction.Executor, actor, audience string, n Node, action, id, reason, request string) error {
	return s.auditTargetWithin(ctx, q, actor, audience, n, action, "node_task", id, reason, request)
}

// auditTargetWithin records one successful action against what it acted on.
func (s *Service) auditTargetWithin(ctx context.Context, q transaction.Executor, actor, audience string, n Node, action, targetType, id, reason, request string) error {
	_, e := q.Exec(ctx, nodeSQL(q, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),NULLIF($2,'')::uuid,$3,$4,$5,$6,'success',$7,$8,NULLIF($9,''),$10)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),NULLIF(?,''),?,?,?,?,'success',?,?,NULLIF(?,''),?)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),NULLIF(?,''),?,?,?,?,'success',?,?,NULLIF(?,''),?)`, uuid.NewString(), actor, n.TenantID, n.WorkspaceID, audience, action, targetType, id, reason, request))
	return e
}
func (s *Service) insertTaskWithin(ctx context.Context, q transaction.Executor, a identity.Auth, n Node, operation, key string, payload, fingerprint []byte, seconds int, epoch *int64) (Task, error) {
	id := uuid.NewString()
	if payload == nil {
		payload = []byte(`{}`)
	}
	_, e := q.Exec(ctx, nodeSQL(q, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint,inference_requested_epoch) VALUES($1,$2,$3,$4,$5,'pending',$6,$7,clock_timestamp()+make_interval(secs=>$8),$9,$10,$11)`, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint,inference_requested_epoch) VALUES(?,?,?,?,?,'pending',?,?,TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),CAST(? AS JSON),?,?)`, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint,inference_requested_epoch) VALUES(?,?,?,?,?,'pending',?,?,DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),?,?,?)`, id, n.TenantID, n.WorkspaceID, n.ID, operation, key, a.Me.User.ID, seconds, nodeJSON(q, payload), fingerprint, epoch))
	if e != nil {
		return Task{}, e
	}
	if e = s.taskWrittenWithin(ctx, q, n.ID); e != nil {
		return Task{}, e
	}
	task, e := s.taskByIDWithin(ctx, q, n, id, false)
	if e != nil {
		return Task{}, e
	}
	if s.TaskEvents != nil {
		if e = s.TaskEvents.RecordTaskQueuedWithin(ctx, q, n, task); e != nil {
			return Task{}, e
		}
	}
	return task, nil
}
func (s *Service) taskReplayWithin(ctx context.Context, q transaction.Executor, n Node, key string) (Task, error) {
	return scanTask(q.QueryRow(ctx, nodeSQL(q, "SELECT "+taskFields+" FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND idempotency_key=$4", "SELECT "+mysqlTaskFields+" FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND idempotency_key=?", `SELECT `+damengTaskFields+` FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND idempotency_key=?`, n.TenantID, n.WorkspaceID, n.ID, key)))
}
func (s *Service) taskIdleWithin(ctx context.Context, q transaction.Executor, n Node, request string) error {
	if e := s.expireTasksWithin(ctx, q, n, request); e != nil {
		return e
	}
	var busy bool
	e := q.QueryRow(ctx, nodeSQL(q, "SELECT EXISTS(SELECT 1 FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND state IN ('pending','running'))", "SELECT EXISTS(SELECT 1 FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND state IN ('pending','running'))", `SELECT CASE WHEN EXISTS(SELECT 1 FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND state IN ('pending','running')) THEN 1 ELSE 0 END FROM DUAL`, n.TenantID, n.WorkspaceID, n.ID)).Scan(&busy)
	if e != nil {
		return e
	}
	if busy {
		return fail("NODE_BUSY")
	}
	return nil
}

// expireTasksWithin ends what a claim proves over: the agent claims only when it holds no
// task, so a claim also fails a task whose lease expired on its last attempt.
func (s *Service) expireTasksWithin(ctx context.Context, q transaction.Executor, n Node, request string) error {
	return s.expireTasksBeyondWithin(ctx, q, n, request, s.Identity.Config.Policy().Nodes.MaximumTaskAttempts)
}

// noAttemptLimit keeps the sweep to task deadlines. Without a claim, an expired lease says
// nothing: its agent may be running the task through an outage (ADR 0032).
const noAttemptLimit = math.MaxInt32

// Select and mutate under the already locked node; this also works without UPDATE RETURNING.
func (s *Service) expireTasksBeyondWithin(ctx context.Context, q transaction.Executor, n Node, request string, max int) error {
	// An expired lease that was never acknowledged was never delivered: release it
	// without spending an attempt before failing tasks that ran out of attempts.
	if e := s.releaseUnacknowledgedWithin(ctx, q, n, request, true); e != nil {
		return e
	}
	rows, e := q.Query(ctx, nodeSQL(q, `SELECT id::text,deadline_at<=clock_timestamp() FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND ((state IN ('pending','running') AND deadline_at<=clock_timestamp()) OR (state='running' AND lease_expires_at<=clock_timestamp() AND attempt>=$4)) ORDER BY id FOR UPDATE`, `SELECT id,deadline_at<=UTC_TIMESTAMP(6) FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND ((state IN ('pending','running') AND deadline_at<=UTC_TIMESTAMP(6)) OR (state='running' AND lease_expires_at<=UTC_TIMESTAMP(6) AND attempt>=?)) ORDER BY id FOR UPDATE`, `SELECT id,CASE WHEN deadline_at<=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND ((state IN ('pending','running') AND deadline_at<=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) OR (state='running' AND lease_expires_at<=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) AND attempt>=?)) ORDER BY id FOR UPDATE`, n.TenantID, n.WorkspaceID, n.ID, max))
	if e != nil {
		return e
	}
	type expired struct {
		id       string
		deadline bool
	}
	items := []expired{}
	for rows.Next() {
		var v expired
		if e = rows.Scan(&v.id, &v.deadline); e != nil {
			rows.Close()
			return e
		}
		items = append(items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range items {
		code := taskDeliveriesExhausted
		if v.deadline {
			code = "TASK_DEADLINE_EXCEEDED"
		}
		changed, e := q.Exec(ctx, nodeSQL(q, `UPDATE node_tasks SET state='failed',error_code=$1,lease_token_hash=NULL,lease_expires_at=NULL,completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND node_id=$4 AND id=$5 AND state IN ('pending','running')`, `UPDATE node_tasks SET state='failed',error_code=?,lease_token_hash=NULL,lease_expires_at=NULL,completed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND state IN ('pending','running')`, `UPDATE node_tasks SET state='failed',error_code=?,lease_token_hash=NULL,lease_expires_at=NULL,completed_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND state IN ('pending','running')`, code, n.TenantID, n.WorkspaceID, n.ID, v.id))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("AGENT_LEASE_STALE")
		}
		if e = s.taskAuditWithin(ctx, q, "", "system", n, "node.task_failed", v.id, "", request); e != nil {
			return e
		}
	}
	return nil
}

// The legacy callback is accepted only on the actual selected PostgreSQL pool.
func (s *Service) postgresTaskTransaction(ctx context.Context, fn func(context.Context, pgx.Tx, transaction.Executor) error) error {
	if ctx == nil || fn == nil {
		return fail("VALIDATION_FAILED")
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	if store.Dialect() != transaction.PostgreSQL || s.DB == nil {
		return fail("NODE_POSTGRESQL_CALLBACK_REQUIRED")
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	tx, e := s.DB.BeginTx(bounded, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if e != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	defer tx.Rollback(bounded)
	if e = fn(bounded, tx, transaction.PostgreSQLTransaction(tx)); e != nil {
		return taskRuntimeError(e)
	}
	if e = bounded.Err(); e != nil {
		return taskRuntimeError(e)
	}
	if e = tx.Commit(bounded); errors.Is(e, pgx.ErrTxCommitRollback) {
		return fail(string(transaction.CommitRolledBack))
	} else if e != nil {
		return fail(string(transaction.CommitUnknown))
	}
	return nil
}

// InvalidateAuthoritiesWithin is borrowed by the operator's single restore transaction.
// It cancels protocol authority, never releases a physical GPU lease.
func InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL && q.Dialect() != transaction.Dameng {
		return fail("STORAGE_UNAVAILABLE")
	}
	statements := [][3]string{
		{`UPDATE node_join_tokens SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL AND consumed_at IS NULL`, `UPDATE node_join_tokens SET revoked_at=UTC_TIMESTAMP(6) WHERE revoked_at IS NULL AND consumed_at IS NULL`, `UPDATE node_join_tokens SET revoked_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE revoked_at IS NULL AND consumed_at IS NULL`},
		{`UPDATE managed_nodes SET status='removed',credential_hash=NULL,inference_fenced_at=CASE WHEN execution_profile IN ('docker_inference','docker_ai','docker_model_lifecycle') THEN clock_timestamp() ELSE inference_fenced_at END,version=version+1,updated_at=clock_timestamp() WHERE status<>'removed'`, `UPDATE managed_nodes SET status='removed',credential_hash=NULL,inference_fenced_at=CASE WHEN execution_profile IN ('docker_inference','docker_ai','docker_model_lifecycle') THEN UTC_TIMESTAMP(6) ELSE inference_fenced_at END,version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE status<>'removed'`, `UPDATE managed_nodes SET status='removed',credential_hash=NULL,inference_fenced_at=CASE WHEN execution_profile IN ('docker_inference','docker_ai','docker_model_lifecycle') THEN SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) ELSE inference_fenced_at END,version=version+1,updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE status<>'removed'`},
		{`UPDATE node_tasks SET state='cancelled',lease_token_hash=NULL,lease_expires_at=NULL,error_code='RESTORE_INVALIDATED',completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE state IN ('pending','running')`, `UPDATE node_tasks SET state='cancelled',lease_token_hash=NULL,lease_expires_at=NULL,error_code='RESTORE_INVALIDATED',completed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE state IN ('pending','running')`, `UPDATE node_tasks SET state='cancelled',lease_token_hash=NULL,lease_expires_at=NULL,error_code='RESTORE_INVALIDATED',completed_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE state IN ('pending','running')`},
	}
	for _, pair := range statements {
		if _, e := q.Exec(ctx, nodeSQL(q, pair[0], pair[1], pair[2])); e != nil {
			return e
		}
	}
	return nil
}

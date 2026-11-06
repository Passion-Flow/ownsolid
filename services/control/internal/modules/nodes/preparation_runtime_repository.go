package nodes

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// The caller owns current human/Workspace and node locks. Re-read persisted
// scope/profile so a stale Node value cannot authorize a task or lease.
func (s *Service) livePreparationNode(ctx context.Context, q transaction.Executor, n Node) (Node, error) {
	return s.ModelPreparationTargetWithin(ctx, q, n.TenantID, n.WorkspaceID, n.ID)
}

func (s *Service) QueuePreparationWithin(ctx context.Context, q transaction.Executor, n Node, command lifecycle.Command, actor string, deadline time.Time) (Task, error) {
	current, e := s.livePreparationNode(ctx, q, n)
	if e != nil {
		return Task{}, e
	}
	if !nodeOperationAllowed(current, wire.ModelPreparationRunOperation) || command.Validate() != nil || command.Scope.TenantID != current.TenantID || command.Scope.WorkspaceID != current.WorkspaceID || !canonicalID(actor) || deadline.IsZero() || q.Dialect() == transaction.Dameng && !nodeNativeTime(deadline) {
		return Task{}, fail("MODEL_PREPARATION_COMMAND_INVALID")
	}
	var busy bool
	if e = q.QueryRow(ctx, nodeSQL(q, `SELECT EXISTS(SELECT 1 FROM node_tasks WHERE node_id=$1 AND state IN ('pending','running'))`, `SELECT EXISTS(SELECT 1 FROM node_tasks WHERE node_id=? AND state IN ('pending','running'))`, `SELECT CASE WHEN EXISTS(SELECT 1 FROM node_tasks WHERE node_id=? AND state IN ('pending','running')) THEN 1 ELSE 0 END FROM DUAL`, current.ID)).Scan(&busy); e != nil {
		return Task{}, e
	}
	if busy {
		return Task{}, fail("NODE_BUSY")
	}
	payload, e := json.Marshal(command)
	if e != nil || len(payload) > lifecycle.MaximumCommandBytes {
		return Task{}, fail("MODEL_PREPARATION_COMMAND_INVALID")
	}
	id := uuid.NewString()
	affected, e := q.Exec(ctx, nodeSQL(q, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint) VALUES($1,$2,$3,$4,$5,'pending',$6,$7,$8,$9,$10)`, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint) VALUES(?,?,?,?,?,'pending',?,?,?,CAST(? AS JSON),?)`, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint) VALUES(?,?,?,?,?,'pending',?,?,?,?,?)`, id, current.TenantID, current.WorkspaceID, current.ID, wire.ModelPreparationRunOperation, command.AttemptID, actor, deadline.UTC(), nodeJSON(q, payload), digest("preparation-command", string(payload))))
	if e != nil {
		return Task{}, e
	}
	if affected != 1 {
		return Task{}, fail("STORAGE_UNAVAILABLE")
	}
	s.taskWritten(current.ID)
	t, _, e := s.PreparationStopTaskWithin(ctx, q, current, id)
	return t, e
}

func (s *Service) ClaimPreparationWithin(ctx context.Context, q transaction.Executor, n Node, id, request string) (wire.Claim, error) {
	var out wire.Claim
	current, e := s.livePreparationNode(ctx, q, n)
	if e != nil {
		return out, e
	}
	t, command, e := s.PreparationStopTaskWithin(ctx, q, current, id)
	if e != nil {
		return out, e
	}
	if t.State == "running" {
		return out, nil
	}
	var timely bool
	if e = q.QueryRow(ctx, nodeSQL(q, `SELECT deadline_at>clock_timestamp() FROM node_tasks WHERE id=$1`, `SELECT deadline_at>UTC_TIMESTAMP(6) FROM node_tasks WHERE id=?`, `SELECT CASE WHEN deadline_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM node_tasks WHERE id=?`, id)).Scan(&timely); e != nil {
		return out, e
	}
	if t.State != "pending" || t.Attempt != 0 || !timely {
		return out, fail("AGENT_LEASE_STALE")
	}
	raw := security.Token()
	lease := wire.Lease{TaskID: id, Operation: wire.ModelPreparationRunOperation, LeaseToken: raw, Attempt: 1, Preparation: &command}
	var affected int64
	if q.Dialect() == transaction.Dameng {
		affected, e = q.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE node_tasks SET state='running',attempt=1,lease_token_hash=?,lease_expires_at=LEAST(DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),deadline_at),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE id=? AND state='pending' AND attempt=0`, digest("node-lease", raw), wire.TaskLeaseSeconds, id))
	} else if q.Dialect() == transaction.MySQL {
		affected, e = q.Exec(ctx, transaction.SQL(transaction.MySQL, `UPDATE node_tasks SET state='running',attempt=1,lease_token_hash=?,lease_expires_at=LEAST(TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),deadline_at),updated_at=UTC_TIMESTAMP(6) WHERE id=? AND state='pending' AND attempt=0`, digest("node-lease", raw), wire.TaskLeaseSeconds, id))
	} else {
		affected, e = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE node_tasks SET state='running',attempt=1,lease_token_hash=$2,lease_expires_at=LEAST(clock_timestamp()+make_interval(secs=>$3),deadline_at),updated_at=clock_timestamp() WHERE id=$1 AND state='pending' AND attempt=0`, id, digest("node-lease", raw), wire.TaskLeaseSeconds))
	}
	if e != nil {
		return out, e
	}
	if affected != 1 {
		return out, fail("AGENT_LEASE_STALE")
	}
	if e = deliveredLeaseWithin(ctx, q, current, id, &lease); e != nil {
		return out, e
	}
	lease.LeaseExpiresAt = lease.LeaseExpiresAt.UTC()
	if e = preparationClaimAudit(ctx, q, current, id, request); e != nil {
		return out, e
	}
	out.Lease = &lease
	return out, nil
}

func (s *Service) LeasedPreparationWithin(ctx context.Context, q transaction.Executor, n Node, id, token string) (Task, lifecycle.Command, error) {
	var command lifecycle.Command
	current, e := s.livePreparationNode(ctx, q, n)
	if e != nil {
		return Task{}, command, e
	}
	t, command, e := s.PreparationStopTaskWithin(ctx, q, current, id)
	if e != nil {
		return t, command, e
	}
	if !security.ValidToken(token) {
		return t, command, fail("AGENT_LEASE_STALE")
	}
	var hash []byte
	var timely bool
	if e = q.QueryRow(ctx, nodeSQL(q, `SELECT lease_token_hash,COALESCE(lease_expires_at>clock_timestamp() AND deadline_at>clock_timestamp(),false) FROM node_tasks WHERE id=$1`, `SELECT lease_token_hash,COALESCE(lease_expires_at>UTC_TIMESTAMP(6) AND deadline_at>UTC_TIMESTAMP(6),false) FROM node_tasks WHERE id=?`, `SELECT lease_token_hash,CASE WHEN lease_expires_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) AND deadline_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM node_tasks WHERE id=?`, id)).Scan(&hash, &timely); e != nil {
		return t, command, e
	}
	if t.State != "running" || t.Attempt != 1 || !timely || subtle.ConstantTimeCompare(hash, digest("node-lease", token)) != 1 {
		return t, command, fail("AGENT_LEASE_STALE")
	}
	return t, command, nil
}

func (s *Service) RenewPreparationWithin(ctx context.Context, q transaction.Executor, n Node, id, token string) (wire.RenewedLease, lifecycle.Command, error) {
	var out wire.RenewedLease
	_, command, e := s.LeasedPreparationWithin(ctx, q, n, id, token)
	if e != nil {
		return out, command, e
	}
	if q.Dialect() == transaction.Dameng {
		_, e = q.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE node_tasks SET lease_expires_at=LEAST(DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),deadline_at),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE id=?`, wire.TaskLeaseSeconds, id))
	} else if q.Dialect() == transaction.MySQL {
		_, e = q.Exec(ctx, transaction.SQL(transaction.MySQL, `UPDATE node_tasks SET lease_expires_at=LEAST(TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),deadline_at),updated_at=UTC_TIMESTAMP(6) WHERE id=?`, wire.TaskLeaseSeconds, id))
	} else {
		_, e = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE node_tasks SET lease_expires_at=LEAST(clock_timestamp()+make_interval(secs=>$2),deadline_at),updated_at=clock_timestamp() WHERE id=$1`, id, wire.TaskLeaseSeconds))
	}
	if e != nil {
		return out, command, e
	}
	e = q.QueryRow(ctx, nodeSQL(q, `SELECT lease_expires_at,GREATEST(0,floor(extract(epoch FROM (lease_expires_at-clock_timestamp()))*1000))::integer FROM node_tasks WHERE id=$1`, `SELECT lease_expires_at,GREATEST(0,FLOOR(TIMESTAMPDIFF(MICROSECOND,UTC_TIMESTAMP(6),lease_expires_at)/1000)) FROM node_tasks WHERE id=?`, `SELECT lease_expires_at,CAST(GREATEST(0,FLOOR(BIGDATEDIFF(MICROSECOND,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),lease_expires_at)/1000)) AS BIGINT) FROM node_tasks WHERE id=?`, id)).Scan(&out.LeaseExpiresAt, &out.ValidForMilliseconds)
	if e != nil {
		return wire.RenewedLease{}, command, e
	}
	out.LeaseExpiresAt = out.LeaseExpiresAt.UTC()
	return out, command, s.noteContactWithin(ctx, q, n.ID)
}

func preparationClaimAudit(ctx context.Context, q transaction.Executor, n Node, id, request string) error {
	affected, e := q.Exec(ctx, nodeSQL(q, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),NULL,$2,$3,'system','node.task_claimed','success','node_task',$4,NULL,$5)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),NULL,?,?,'system','node.task_claimed','success','node_task',?,NULL,?)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),NULL,?,?,'system','node.task_claimed','success','node_task',?,NULL,?)`, uuid.NewString(), n.TenantID, n.WorkspaceID, id, request))
	if e != nil {
		return e
	}
	if affected != 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	return nil
}

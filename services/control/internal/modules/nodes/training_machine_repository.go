package nodes

import (
	"context"
	"crypto/subtle"
	"slices"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	training "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// CredentialWithin borrows the caller's current Workspace fence. It never grants
// execution authority or returns a node after a failed scope/credential check.
func (s *Service) CredentialWithin(ctx context.Context, q transaction.Executor, tenant, workspace, id, credential string) (Node, error) {
	if !canonicalID(id) || !security.ValidToken(credential) {
		return Node{}, fail("AGENT_UNAUTHORIZED")
	}
	n, e := s.PreparationHistoryWithin(ctx, q, tenant, workspace, id)
	if e != nil {
		if e.Error() == "RESOURCE_NOT_FOUND" {
			return Node{}, fail("AGENT_UNAUTHORIZED")
		}
		return Node{}, e
	}
	if n.Status == "removed" {
		return Node{}, fail("AGENT_UNAUTHORIZED")
	}
	var hash []byte
	if e = q.QueryRow(ctx, nodeSQL(q, "SELECT credential_hash FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "SELECT credential_hash FROM managed_nodes WHERE tenant_id=? AND workspace_id=? AND id=?", `SELECT credential_hash FROM managed_nodes WHERE tenant_id=? AND workspace_id=? AND id=?`, tenant, workspace, id)).Scan(&hash); e != nil {
		return Node{}, preparationSourceError(e)
	}
	if subtle.ConstantTimeCompare(hash, digest("node-credential", credential)) != 1 {
		return Node{}, fail("AGENT_UNAUTHORIZED")
	}
	return n, nil
}

// TrainingHistoryWithin retains stopped/removed history without authenticating
// a machine or granting execution authority.
func (s *Service) TrainingHistoryWithin(ctx context.Context, q transaction.Executor, tenant, workspace, id string) (Node, error) {
	return s.PreparationHistoryWithin(ctx, q, tenant, workspace, id)
}
func (s *Service) trainingTaskWithin(ctx context.Context, q transaction.Executor, n Node, id string, active bool) (Task, training.Command, error) {
	var zero training.Command
	if !canonicalID(id) {
		return Task{}, zero, fail("AGENT_LEASE_STALE")
	}
	current, e := s.TrainingHistoryWithin(ctx, q, n.TenantID, n.WorkspaceID, n.ID)
	if e != nil {
		return Task{}, zero, e
	}
	if active && current.Status != "active" || !wire.IsTrainingProfile(current.ExecutionProfile) || current.ProtocolVersion != wire.ProfileVersion(current.ExecutionProfile) || !slices.Contains(current.Capabilities, wire.TrainingRunOperation) {
		return Task{}, zero, fail("AGENT_PROFILE_REJECTED")
	}
	t, e := scanTask(q.QueryRow(ctx, nodeSQL(q, "SELECT "+taskFields+" FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 FOR UPDATE", "SELECT "+mysqlTaskFields+" FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? FOR UPDATE", `SELECT `+damengTaskFields+` FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? FOR UPDATE`, current.TenantID, current.WorkspaceID, current.ID, id)))
	if e != nil {
		return Task{}, zero, preparationSourceError(e)
	}
	if t.Operation != wire.TrainingRunOperation {
		return Task{}, zero, fail("AGENT_PROFILE_REJECTED")
	}
	var raw []byte
	if e = q.QueryRow(ctx, nodeSQL(q, "SELECT payload FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4", "SELECT payload FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?", `SELECT payload FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, current.TenantID, current.WorkspaceID, current.ID, id)).Scan(&raw); e != nil {
		return Task{}, zero, preparationSourceError(e)
	}
	command, e := training.DecodeCommand(raw)
	if e != nil || command.Scope.TenantID != current.TenantID || command.Scope.WorkspaceID != current.WorkspaceID || command.ModelCatalogSHA256 != wire.ModelCatalogDigest() {
		return Task{}, zero, fail("TRAINING_COMMAND_INVALID")
	}
	return t, command, nil
}
func (s *Service) TrainingStopTaskWithin(ctx context.Context, q transaction.Executor, n Node, id string) (Task, training.Command, error) {
	return s.trainingTaskWithin(ctx, q, n, id, false)
}

// ClaimTrainingWithin never reissues acknowledged work; a lease that was never
// acknowledged returns to pending first (ADR 0011). The caller publishes the
// secret only after the entire authority/GPU/audit transaction commits.
func (s *Service) ClaimTrainingWithin(ctx context.Context, q transaction.Executor, n Node, id, request string) (wire.Claim, error) {
	var out wire.Claim
	t, command, e := s.trainingTaskWithin(ctx, q, n, id, true)
	if e != nil {
		return out, e
	}
	if t.State == "running" {
		return out, nil
	}
	var timely bool
	if e = q.QueryRow(ctx, nodeSQL(q, "SELECT deadline_at>clock_timestamp() FROM node_tasks WHERE id=$1", "SELECT deadline_at>UTC_TIMESTAMP(6) FROM node_tasks WHERE id=?", `SELECT CASE WHEN deadline_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM node_tasks WHERE id=?`, id)).Scan(&timely); e != nil {
		return out, e
	}
	if t.State != "pending" || t.Attempt != 0 || !timely {
		return out, fail("AGENT_LEASE_STALE")
	}
	raw := security.Token()
	lease := wire.Lease{TaskID: id, Operation: wire.TrainingRunOperation, LeaseToken: raw, Attempt: 1, Training: &command}
	args := []any{digest("node-lease", raw), wire.TaskLeaseSeconds, id}
	var changed int64
	if q.Dialect() == transaction.Dameng {
		changed, e = q.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE node_tasks SET state='running',attempt=1,lease_token_hash=?,lease_expires_at=LEAST(DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),deadline_at),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE id=? AND state='pending' AND attempt=0`, args...))
	} else if q.Dialect() == transaction.MySQL {
		changed, e = q.Exec(ctx, transaction.SQL(transaction.MySQL, "UPDATE node_tasks SET state='running',attempt=1,lease_token_hash=?,lease_expires_at=LEAST(TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),deadline_at),updated_at=UTC_TIMESTAMP(6) WHERE id=? AND state='pending' AND attempt=0", args...))
	} else {
		changed, e = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, "UPDATE node_tasks SET state='running',attempt=1,lease_token_hash=$1,lease_expires_at=LEAST(clock_timestamp()+make_interval(secs=>$2),deadline_at),updated_at=clock_timestamp() WHERE id=$3 AND state='pending' AND attempt=0", args...))
	}
	if e != nil {
		return out, e
	}
	if changed != 1 {
		return out, fail("AGENT_LEASE_STALE")
	}
	if e = deliveredLeaseWithin(ctx, q, n, id, &lease); e != nil {
		return out, e
	}
	if _, e = q.Exec(ctx, nodeSQL(q, "INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) VALUES($1,clock_timestamp(),$2,$3,'system','node.task_claimed','success','node_task',$4,$5)", "INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,'system','node.task_claimed','success','node_task',?,?)", `INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,?,'system','node.task_claimed','success','node_task',?,?)`, uuid.NewString(), n.TenantID, n.WorkspaceID, id, request)); e != nil {
		return out, e
	}
	lease.LeaseExpiresAt = lease.LeaseExpiresAt.UTC()
	out.Lease = &lease
	return out, nil
}
func (s *Service) LeasedTrainingWithin(ctx context.Context, q transaction.Executor, n Node, id, token string) (Task, training.Command, error) {
	var zero training.Command
	t, command, e := s.trainingTaskWithin(ctx, q, n, id, true)
	if e != nil {
		return Task{}, zero, e
	}
	if !security.ValidToken(token) {
		return Task{}, zero, fail("AGENT_LEASE_STALE")
	}
	var hash []byte
	var timely bool
	if e = q.QueryRow(ctx, nodeSQL(q, "SELECT lease_token_hash,COALESCE(lease_expires_at>clock_timestamp() AND deadline_at>clock_timestamp(),false) FROM node_tasks WHERE id=$1", "SELECT lease_token_hash,COALESCE(lease_expires_at>UTC_TIMESTAMP(6) AND deadline_at>UTC_TIMESTAMP(6),false) FROM node_tasks WHERE id=?", `SELECT lease_token_hash,CASE WHEN lease_expires_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) AND deadline_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM node_tasks WHERE id=?`, id)).Scan(&hash, &timely); e != nil {
		return Task{}, zero, e
	}
	if t.State != "running" || t.Attempt != 1 || !timely || subtle.ConstantTimeCompare(hash, digest("node-lease", token)) != 1 {
		return Task{}, zero, fail("AGENT_LEASE_STALE")
	}
	return t, command, nil
}
func (s *Service) RenewTrainingWithin(ctx context.Context, q transaction.Executor, n Node, id, token string) (wire.RenewedLease, training.Command, error) {
	var out wire.RenewedLease
	var zero training.Command
	_, command, e := s.LeasedTrainingWithin(ctx, q, n, id, token)
	if e != nil {
		return out, zero, e
	}
	args := []any{wire.TaskLeaseSeconds, id}
	_, e = q.Exec(ctx, nodeSQL(q, "UPDATE node_tasks SET lease_expires_at=LEAST(clock_timestamp()+make_interval(secs=>$1),deadline_at),updated_at=clock_timestamp() WHERE id=$2", "UPDATE node_tasks SET lease_expires_at=LEAST(TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),deadline_at),updated_at=UTC_TIMESTAMP(6) WHERE id=?", `UPDATE node_tasks SET lease_expires_at=LEAST(DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),deadline_at),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE id=?`, args...))
	if e != nil {
		return out, zero, e
	}
	if e = q.QueryRow(ctx, nodeSQL(q, "SELECT lease_expires_at,GREATEST(0,floor(extract(epoch FROM (lease_expires_at-clock_timestamp()))*1000))::integer FROM node_tasks WHERE id=$1", "SELECT lease_expires_at,GREATEST(0,TIMESTAMPDIFF(MICROSECOND,UTC_TIMESTAMP(6),lease_expires_at) DIV 1000) FROM node_tasks WHERE id=?", `SELECT lease_expires_at,CAST(GREATEST(0,FLOOR(BIGDATEDIFF(MICROSECOND,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),lease_expires_at) / 1000)) AS BIGINT) FROM node_tasks WHERE id=?`, id)).Scan(&out.LeaseExpiresAt, &out.ValidForMilliseconds); e != nil {
		return wire.RenewedLease{}, zero, e
	}
	if _, e = q.Exec(ctx, nodeSQL(q, "UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", "UPDATE managed_nodes SET last_heartbeat_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?", `UPDATE managed_nodes SET last_heartbeat_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE id=?`, n.ID)); e != nil {
		return wire.RenewedLease{}, zero, e
	}
	out.LeaseExpiresAt = out.LeaseExpiresAt.UTC()
	return out, command, nil
}

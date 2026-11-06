package nodes

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	training "github.com/Passion-Flow/ownsolid/api/training/v1"
	"time"
)

// QueueTrainingWithin borrows the caller's current authority/resource transaction.
// It writes the exact immutable typed task and never commits or opens a connection.
func (s *Service) QueueTrainingWithin(ctx context.Context, q transaction.Executor, n Node, command training.Command, actor string, deadline time.Time) (Task, error) {
	if e := s.preparationSourceExecutor(ctx, q, n.TenantID, n.WorkspaceID, n.ID); e != nil {
		return Task{}, e
	}
	current, e := s.TrainingTargetWithin(ctx, q, n.TenantID, n.WorkspaceID, n.ID)
	if e != nil {
		return Task{}, e
	}
	if !wire.IsTrainingProfile(current.ExecutionProfile) || !nodeOperationAllowed(current, wire.TrainingRunOperation) || command.Validate() != nil || command.ModelCatalogSHA256 != wire.ModelCatalogDigest() || command.Scope.TenantID != current.TenantID || command.Scope.WorkspaceID != current.WorkspaceID || !canonicalID(actor) || deadline.IsZero() || q.Dialect() == transaction.Dameng && !nodeNativeTime(deadline) {
		return Task{}, fail("TRAINING_COMMAND_INVALID")
	}
	var live bool
	if e = q.QueryRow(ctx, nodeSQL(q, "SELECT $1::timestamptz>clock_timestamp()", "SELECT ?>UTC_TIMESTAMP(6)", `SELECT CASE WHEN ?>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM DUAL`, deadline)).Scan(&live); e != nil {
		return Task{}, e
	}
	if !live {
		return Task{}, fail("TRAINING_COMMAND_INVALID")
	}
	var busy bool
	if e = q.QueryRow(ctx, nodeSQL(q, "SELECT EXISTS(SELECT 1 FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND state IN ('pending','running'))", "SELECT EXISTS(SELECT 1 FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND state IN ('pending','running'))", `SELECT CASE WHEN EXISTS(SELECT 1 FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND state IN ('pending','running')) THEN 1 ELSE 0 END FROM DUAL`, current.TenantID, current.WorkspaceID, current.ID)).Scan(&busy); e != nil {
		return Task{}, e
	}
	if busy {
		return Task{}, fail("NODE_BUSY")
	}
	payload, e := json.Marshal(command)
	if e != nil || len(payload) > training.MaximumCommandBytes {
		return Task{}, fail("TRAINING_COMMAND_INVALID")
	}
	id := uuid.NewString()
	count, e := q.Exec(ctx, nodeSQL(q,
		`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint) VALUES($1,$2,$3,$4,$5,'pending',$6,$7,$8,$9,$10)`,
		`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint) VALUES(?,?,?,?,?,'pending',?,?,?,?,?)`, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint) VALUES(?,?,?,?,?,'pending',?,?,?,?,?)`, id, current.TenantID, current.WorkspaceID, current.ID, wire.TrainingRunOperation, command.AttemptID, actor, deadline, nodeJSON(q, payload), digest("training-command", string(payload))))
	if e != nil {
		return Task{}, e
	}
	if count != 1 {
		return Task{}, fail("STORAGE_UNAVAILABLE")
	}
	if e = s.taskWrittenWithin(ctx, q, current.ID); e != nil {
		return Task{}, e
	}
	out, e := scanTask(q.QueryRow(ctx, nodeSQL(q, "SELECT "+taskFields+" FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4", "SELECT "+mysqlTaskFields+" FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?", `SELECT `+damengTaskFields+` FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, current.TenantID, current.WorkspaceID, current.ID, id)))
	if e != nil {
		return Task{}, e
	}
	return out, nil
}

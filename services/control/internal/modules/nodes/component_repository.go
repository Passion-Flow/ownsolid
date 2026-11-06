package nodes

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ComponentNodeWithin repeats current actor and actual scope before locking the
// component's node. The caller owns the selected transaction and its outcome.
func (s *Service) ComponentNodeWithin(ctx context.Context, q transaction.Executor, a identity.Auth, workspace, id string) (Node, error) {
	if ctx == nil || q == nil {
		return Node{}, fail("STORAGE_UNAVAILABLE")
	}
	if _, pool := q.(transaction.Store); pool {
		return Node{}, fail("STORAGE_UNAVAILABLE")
	}
	store, e := s.repository()
	if e != nil {
		return Node{}, e
	}
	if store.Dialect() != q.Dialect() {
		return Node{}, fail("NODE_STORAGE_MISMATCH")
	}
	if e = s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
		return Node{}, e
	}
	n, e := s.getWithin(ctx, q, a, workspace, id)
	if e != nil {
		return Node{}, e
	}
	return s.lockedUpdateNode(ctx, q, n.TenantID, n.WorkspaceID, n.ID)
}

// CreateComponentTaskBoundWithin never commits or exposes an uncommitted task to
// the network. The owning caller must discard its DTO if its transaction fails.
func (s *Service) CreateComponentTaskBoundWithin(ctx context.Context, q transaction.Executor, a identity.Auth, id, operation, key, reason, request string, fingerprint []byte, build func(context.Context, transaction.Executor, Node) (wire.ComponentCommand, error), record func(context.Context, transaction.Executor, Node, Task, wire.ComponentCommand) error) (Task, error) {
	if !isAdmin(a) {
		return Task{}, fail("ACCESS_DENIED")
	}
	if !wire.IsComponentOperation(operation) || len(fingerprint) != 32 || !nodeID(key) || !nodeID(request) || !workspaces.ValidReason(reason) || build == nil || record == nil {
		return Task{}, fail("VALIDATION_FAILED")
	}
	n, e := s.ComponentNodeWithin(ctx, q, a, "", id)
	if e != nil {
		return Task{}, e
	}
	w, e := s.readScope(ctx, q, a, n.WorkspaceID)
	if e != nil {
		return Task{}, e
	}
	if n.Status != "active" || w.Status != "active" {
		return Task{}, fail("ACCESS_DENIED")
	}
	if !wire.IsManagementProfile(n.ExecutionProfile) {
		return Task{}, fail("COMPONENT_PROFILE_UNAVAILABLE")
	}
	if !nodeOperationAllowed(n, operation) {
		return Task{}, fail("COMPONENT_MAINTENANCE_REQUIRED")
	}
	previous, e := scanTask(q.QueryRow(ctx, nodeSQL(q, "SELECT "+taskFields+" FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND idempotency_key=$4 FOR UPDATE", "SELECT "+mysqlTaskFields+" FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND idempotency_key=? FOR UPDATE", `SELECT `+damengTaskFields+` FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND idempotency_key=? FOR UPDATE`, n.TenantID, n.WorkspaceID, n.ID, key)))
	if e == nil {
		if previous.Operation != operation {
			return Task{}, fail("IDEMPOTENCY_CONFLICT")
		}
		var original []byte
		if e = q.QueryRow(ctx, nodeSQL(q, "SELECT request_fingerprint FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4", "SELECT request_fingerprint FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?", `SELECT request_fingerprint FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, n.TenantID, n.WorkspaceID, n.ID, previous.ID)).Scan(&original); e != nil {
			return Task{}, e
		}
		if original == nil && n.ExecutionProfile == wire.SystemdComponentsProfile {
			c, e := s.ReadComponentCommandWithin(ctx, q, n, previous)
			if e != nil {
				return Task{}, e
			}
			raw, _ := json.Marshal(c)
			original = digest("component-intent", string(raw))
		}
		if subtle.ConstantTimeCompare(original, fingerprint) != 1 {
			return Task{}, fail("IDEMPOTENCY_CONFLICT")
		}
		return previous, nil
	}
	if e.Error() != "RESOURCE_NOT_FOUND" {
		return Task{}, e
	}
	if e = s.taskIdleWithin(ctx, q, n, request); e != nil {
		return Task{}, e
	}
	command, e := build(ctx, q, n)
	if e != nil {
		return Task{}, e
	}
	if e = command.ValidateForProfile(n.ExecutionProfile, operation); e != nil {
		return Task{}, e
	}
	raw, _ := json.Marshal(command)
	taskID := uuid.NewString()
	if _, e = q.Exec(ctx, nodeSQL(q, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint) VALUES($1,$2,$3,$4,$5,'pending',$6,$7,clock_timestamp()+make_interval(secs=>$10),$8,$9)`, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint) VALUES(?,?,?,?,?,'pending',?,?,TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),CAST(? AS JSON),?)`, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint) VALUES(?,?,?,?,?,'pending',?,?,DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),?,?)`, componentInsertArgs(q, taskID, n, operation, key, a.Me.User.ID, raw, fingerprint, s.Identity.Config.Policy().Nodes.TaskSeconds)...)); e != nil {
		return Task{}, e
	}
	if e = s.taskWrittenWithin(ctx, q, n.ID); e != nil {
		return Task{}, e
	}
	out, e := scanTask(q.QueryRow(ctx, nodeSQL(q, "SELECT "+taskFields+" FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4", "SELECT "+mysqlTaskFields+" FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?", `SELECT `+damengTaskFields+` FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, n.TenantID, n.WorkspaceID, n.ID, taskID)))
	if e != nil {
		return Task{}, e
	}
	if e = record(ctx, q, n, out, command); e != nil {
		return Task{}, e
	}
	_, e = q.Exec(ctx, nodeSQL(q, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,'admin',$5,'success','node_task',$6,$7,$8)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'admin',?,'success','node_task',?,?,?)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,?,?,'admin',?,'success','node_task',?,?,?)`, uuid.NewString(), a.Me.User.ID, n.TenantID, n.WorkspaceID, "component."+command.Action+"_requested", out.ID, reason, request))
	if e != nil {
		return Task{}, e
	}
	return out, nil
}
func componentInsertArgs(q transaction.Executor, id string, n Node, op, key, actor string, raw, fp []byte, seconds int) []any {
	if nodePositional(q) {
		return []any{id, n.TenantID, n.WorkspaceID, n.ID, op, key, actor, seconds, nodeJSON(q, raw), fp}
	}
	return []any{id, n.TenantID, n.WorkspaceID, n.ID, op, key, actor, raw, fp, seconds}
}
func (s *Service) ReadComponentCommandWithin(ctx context.Context, q transaction.Executor, n Node, t Task) (wire.ComponentCommand, error) {
	var out wire.ComponentCommand
	if e := s.preparationSourceExecutor(ctx, q, n.TenantID, n.WorkspaceID, n.ID); e != nil {
		return out, e
	}
	if _, pool := q.(transaction.Store); pool {
		return out, fail("STORAGE_UNAVAILABLE")
	}
	if t.TenantID != n.TenantID || t.WorkspaceID != n.WorkspaceID || t.NodeID != n.ID || !nodeID(t.ID) || !wire.IsComponentOperation(t.Operation) {
		return out, fail("RESOURCE_NOT_FOUND")
	}
	var raw []byte
	var operation string
	e := q.QueryRow(ctx, nodeSQL(q, "SELECT payload,operation FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4", "SELECT payload,operation FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?", `SELECT payload,operation FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, n.TenantID, n.WorkspaceID, n.ID, t.ID)).Scan(&raw, &operation)
	if e != nil {
		return out, e
	}
	if operation != t.Operation {
		return out, fail("RESOURCE_NOT_FOUND")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(new(any)) != io.EOF || out.ValidateForProfile(n.ExecutionProfile, t.Operation) != nil {
		return wire.ComponentCommand{}, fail("AGENT_OPERATION_UNSUPPORTED")
	}
	return out, nil
}

// ValidateComponentResultWithin shares the original exact maintenance contract
// with the selected machine owner; it never authenticates a lease by itself.
func (s *Service) ValidateComponentResultWithin(ctx context.Context, q transaction.Executor, n Node, t Task, input wire.Result) error {
	if !nodeOperationAllowed(n, t.Operation) {
		return fail("AGENT_PROFILE_REJECTED")
	}
	if input.Inference != nil || wire.IsInferenceOperation(t.Operation) || t.Operation == wire.ModelPreparationRunOperation {
		return fail("VALIDATION_FAILED")
	}
	if t.Operation == wire.CollectFacts || n.ExecutionProfile != wire.SystemdMaintenanceProfile {
		return validateResult(t.Operation, input)
	}
	if input.Component == nil || input.Facts != nil {
		return fail("VALIDATION_FAILED")
	}
	o := input.Component
	if e := o.ValidateMaintenance(); e != nil {
		return e
	}
	command, e := s.ReadComponentCommandWithin(ctx, q, n, t)
	if e != nil {
		return e
	}
	if input.Outcome == "failure" {
		if input.ErrorCode != "" && wire.KnownMaintenanceError(input.ErrorCode) && o.ErrorCode == input.ErrorCode && input.Logs == nil {
			return nil
		}
		return fail("VALIDATION_FAILED")
	}
	if input.Outcome != "success" || input.ErrorCode != "" || o.ErrorCode != "" {
		return fail("VALIDATION_FAILED")
	}
	if command.Action != "logs" && input.Logs != nil {
		return fail("VALIDATION_FAILED")
	}
	switch command.Action {
	case "install", "start", "restart", "configure", "upgrade", "rollback":
		if !o.Managed || o.State != "running" || o.Metrics == nil || o.Version != command.Version {
			return fail("VALIDATION_FAILED")
		}
		actual := wire.DefaultComponentConfig()
		if o.Config != nil {
			actual = *o.Config
		}
		if command.Config != nil && actual != *command.Config {
			return fail("VALIDATION_FAILED")
		}
	case "stop":
		if !o.Managed || o.State != "stopped" || o.Version != command.Version {
			return fail("VALIDATION_FAILED")
		}
	case "remove":
		if o.Managed || o.State != "not_installed" {
			return fail("VALIDATION_FAILED")
		}
	case "logs":
		if !o.Managed || o.State != "running" && o.State != "stopped" || input.Logs == nil || input.Logs.Validate() != nil {
			return fail("VALIDATION_FAILED")
		}
	}
	return nil
}

package nodes

import (
	"context"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// InferenceRegistryWithin borrows the exact owning transaction after its caller
// locks the stored delegating actor, current installed scope and whole license.
// Scope values must come from actual persisted node/task discovery, not a request.
func (s *Service) InferenceRegistryWithin(ctx context.Context, q transaction.Executor, tenant, workspace, node, credential, task, lease string) (Node, Task, wire.InferenceCommand, error) {
	if ctx == nil || q == nil {
		return Node{}, Task{}, wire.InferenceCommand{}, fail("AGENT_UNAUTHORIZED")
	}
	owner, e := s.repository()
	if e != nil || owner.Dialect() != q.Dialect() {
		return Node{}, Task{}, wire.InferenceCommand{}, fail("NODE_STORAGE_MISMATCH")
	}
	if _, store := q.(transaction.Store); store {
		return Node{}, Task{}, wire.InferenceCommand{}, fail("NODE_TRANSACTION_REQUIRED")
	}
	n, e := s.CredentialWithin(ctx, q, tenant, workspace, node, credential)
	if e != nil {
		return Node{}, Task{}, wire.InferenceCommand{}, e
	}
	if n.Status != "active" || !wire.IsInferenceProfile(n.ExecutionProfile) {
		return Node{}, Task{}, wire.InferenceCommand{}, fail("AGENT_PROFILE_REJECTED")
	}
	t, e := s.leasedTaskWithin(ctx, q, n, task, lease)
	if e != nil {
		return Node{}, Task{}, wire.InferenceCommand{}, e
	}
	c, e := s.ReadInferenceCommandWithin(ctx, q, n, t)
	if e != nil {
		return Node{}, Task{}, wire.InferenceCommand{}, e
	}
	if c.Action != "deploy" || c.DeploymentID == "" {
		return Node{}, Task{}, wire.InferenceCommand{}, fail("AGENT_OPERATION_UNSUPPORTED")
	}
	return n, t, c, nil
}

// CurrentRegistryTaskWithin is the current Admin renewal path. The original
// requesting actor cannot be replaced by another Admin when minting execution.
func (s *Service) CurrentRegistryTaskWithin(ctx context.Context, q transaction.Executor, a identity.Auth, node, task string) (Node, Task, wire.InferenceCommand, error) {
	if e := s.taskExecutor(ctx, q); e != nil {
		return Node{}, Task{}, wire.InferenceCommand{}, e
	}
	if !isAdmin(a) {
		return Node{}, Task{}, wire.InferenceCommand{}, fail("ACCESS_DENIED")
	}
	if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
		return Node{}, Task{}, wire.InferenceCommand{}, e
	}
	n, e := s.taskNodeWithin(ctx, q, a, node)
	if e != nil {
		return Node{}, Task{}, wire.InferenceCommand{}, e
	}
	t, e := s.taskByIDWithin(ctx, q, n, task, true)
	if e != nil {
		return Node{}, Task{}, wire.InferenceCommand{}, e
	}
	var live bool
	e = q.QueryRow(ctx, nodeSQL(q, `SELECT requested_by=$1 AND deadline_at>clock_timestamp() AND (state='pending' OR state='running' AND lease_expires_at>clock_timestamp()) FROM node_tasks WHERE tenant_id=$2 AND workspace_id=$3 AND node_id=$4 AND id=$5`, `SELECT requested_by=? AND deadline_at>UTC_TIMESTAMP(6) AND (state='pending' OR state='running' AND lease_expires_at>UTC_TIMESTAMP(6)) FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, `SELECT CASE WHEN requested_by=? AND deadline_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) AND (state='pending' OR state='running' AND lease_expires_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) THEN 1 ELSE 0 END FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, a.Me.User.ID, n.TenantID, n.WorkspaceID, n.ID, t.ID)).Scan(&live)
	if e != nil {
		return Node{}, Task{}, wire.InferenceCommand{}, e
	}
	if !live || n.Status != "active" {
		return Node{}, Task{}, wire.InferenceCommand{}, fail("AGENT_LEASE_STALE")
	}
	c, e := s.ReadInferenceCommandWithin(ctx, q, n, t)
	if e != nil {
		return Node{}, Task{}, wire.InferenceCommand{}, e
	}
	if c.Action != "deploy" {
		return Node{}, Task{}, wire.InferenceCommand{}, fail("AGENT_OPERATION_UNSUPPORTED")
	}
	return n, t, c, nil
}

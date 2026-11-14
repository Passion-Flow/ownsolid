package nodes

import (
	"context"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// RegistryIntentRetainedWithin is an internal retained-history witness, not a
// client pin selection. A missing reference cannot downgrade an original marker.
func (s *Service) RegistryIntentRetainedWithin(ctx context.Context, q transaction.Executor, n Node, deployment string) (bool, error) {
	if e := s.taskExecutor(ctx, q); e != nil {
		return false, e
	}
	if !nodeID(deployment) || !nodeID(n.ID) || !nodeID(n.TenantID) || !nodeID(n.WorkspaceID) {
		return false, fail("ACCESS_DENIED")
	}
	var found bool
	e := q.QueryRow(ctx, nodeSQL(q,
		`SELECT EXISTS(SELECT 1 FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND payload->>'deployment_id'=$4 AND payload ? 'image_registry_pin_id')`,
		`SELECT EXISTS(SELECT 1 FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.deployment_id'))=? AND JSON_CONTAINS_PATH(payload,'one','$.image_registry_pin_id'))`, `SELECT CASE WHEN EXISTS(SELECT 1 FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND JSON_VALUE(payload,'$.deployment_id')=? AND JSON_CONTAINS_PATH(payload,'one','$.image_registry_pin_id')=1) THEN 1 ELSE 0 END FROM DUAL`, n.TenantID, n.WorkspaceID, n.ID, deployment)).Scan(&found)
	return found, taskRuntimeError(e)
}

// RetainedInferenceTaskForActorWithin rechecks current scope and the original
// task requester. It reads a replay; it never renews a lease or creates a task.
func (s *Service) RetainedInferenceTaskForActorWithin(ctx context.Context, q transaction.Executor, a identity.Auth, node, task string) (Node, Task, wire.InferenceCommand, error) {
	if e := s.taskExecutor(ctx, q); e != nil {
		return Node{}, Task{}, wire.InferenceCommand{}, e
	}
	if !isAdmin(a) || !nodeID(task) {
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
	var actor bool
	if e = q.QueryRow(ctx, nodeSQL(q, `SELECT requested_by=$1 FROM node_tasks WHERE tenant_id=$2 AND workspace_id=$3 AND node_id=$4 AND id=$5`, `SELECT requested_by=? FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, `SELECT CASE WHEN requested_by=? THEN 1 ELSE 0 END FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, a.Me.User.ID, n.TenantID, n.WorkspaceID, n.ID, t.ID)).Scan(&actor); e != nil {
		return Node{}, Task{}, wire.InferenceCommand{}, taskRuntimeError(e)
	}
	if !actor {
		return Node{}, Task{}, wire.InferenceCommand{}, fail("ACCESS_DENIED")
	}
	c, e := s.ReadInferenceCommandWithin(ctx, q, n, t)
	return n, t, c, e
}

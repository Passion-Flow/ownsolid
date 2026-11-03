package nodes

import (
	"context"

	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func validateNodeInferenceCommand(n Node, c wire.InferenceCommand, operation string) error {
	if e := c.Validate(operation); e != nil {
		return e
	}
	if c.Derived == nil {
		return nil
	}
	if n.ExecutionProfile != wire.DockerModelLifecycleProfile || n.ProtocolVersion != wire.ModelLifecycleVersion || !nodeOperationAllowed(n, wire.DerivedInferenceOperation) {
		return fail("AGENT_PROFILE_REJECTED")
	}
	if c.Derived.Catalog.Scope.TenantID != n.TenantID || c.Derived.Catalog.Scope.WorkspaceID != n.WorkspaceID {
		return fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	return nil
}

// Machine callbacks already hold Workspace/node/task authority. A current committed
// administrator/epoch read avoids taking a user lock in the opposite order.
// Call again after callback IO; a change committed during that IO wins at this boundary.
func (s *Service) checkDerivedTaskAuthority(ctx context.Context, tx pgx.Tx, n Node, t Task) error {
	return s.checkDerivedTaskAuthorityWithin(ctx, transaction.PostgreSQLTransaction(tx), n, t)
}
func (s *Service) checkDerivedTaskAuthorityWithin(ctx context.Context, q transaction.Executor, n Node, t Task) error {
	if t.Operation != wire.DerivedInferenceOperation {
		return nil
	}
	var valid bool
	e := q.QueryRow(ctx, nodeSQL(q,
		`SELECT EXISTS(SELECT 1 FROM node_tasks t JOIN users u ON u.id=t.requested_by JOIN workspaces w ON w.tenant_id=t.tenant_id AND w.id=t.workspace_id JOIN tenants te ON te.id=w.tenant_id WHERE t.id=$1 AND t.tenant_id=$2 AND t.workspace_id=$3 AND t.node_id=$4 AND t.operation=$5 AND u.status='active' AND u.is_platform_admin AND u.auth_epoch=t.inference_requested_epoch AND w.status='active' AND te.status='active')`,
		`SELECT EXISTS(SELECT 1 FROM node_tasks t JOIN users u ON u.id=t.requested_by JOIN workspaces w ON w.tenant_id=t.tenant_id AND w.id=t.workspace_id JOIN tenants te ON te.id=w.tenant_id WHERE t.id=? AND t.tenant_id=? AND t.workspace_id=? AND t.node_id=? AND t.operation=? AND u.status='active' AND u.is_platform_admin=1 AND u.auth_epoch=t.inference_requested_epoch AND w.status='active' AND te.status='active')`, `SELECT CASE WHEN EXISTS(SELECT 1 FROM node_tasks t JOIN users u ON u.id=t.requested_by JOIN workspaces w ON w.tenant_id=t.tenant_id AND w.id=t.workspace_id JOIN tenants te ON te.id=w.tenant_id WHERE t.id=? AND t.tenant_id=? AND t.workspace_id=? AND t.node_id=? AND t.operation=? AND u.status='active' AND u.is_platform_admin=1 AND u.auth_epoch=t.inference_requested_epoch AND w.status='active' AND te.status='active') THEN 1 ELSE 0 END FROM DUAL`, t.ID, n.TenantID, n.WorkspaceID, n.ID, wire.DerivedInferenceOperation)).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return fail("ACCESS_DENIED")
	}
	return nil
}

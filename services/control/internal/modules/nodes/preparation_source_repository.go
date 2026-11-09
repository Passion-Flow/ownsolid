package nodes

import (
	"context"
	"slices"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) preparationSourceExecutor(ctx context.Context, q transaction.Executor, tenant, workspace, id string) error {
	if s == nil || ctx == nil || q == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	if e := ctx.Err(); e != nil {
		return preparationSourceError(e)
	}
	if !nodeID(tenant) || !nodeID(workspace) || !nodeID(id) {
		return fail("RESOURCE_NOT_FOUND")
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	if store.Dialect() != q.Dialect() || q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL && q.Dialect() != transaction.Dameng {
		return fail("NODE_STORAGE_MISMATCH")
	}
	return nil
}

func preparationSourceError(e error) error {
	if e == nil {
		return nil
	}
	if e == context.Canceled {
		return fail(string(transaction.Cancelled))
	}
	if e == context.DeadlineExceeded {
		return fail(string(transaction.Timeout))
	}
	switch e.Error() {
	case "NODE_UNAVAILABLE", "AGENT_PROFILE_REJECTED", "MODEL_PREPARATION_COMMAND_INVALID",
		"NODE_STORAGE_MISMATCH", "NODE_IDENTITY_REQUIRED":
		return fail(e.Error())
	default:
		return nodeRepositoryError(e)
	}
}

// PreparationHistoryWithin resolves retained scope without granting execution
// authority. The owner already holds current actor and Workspace fences.
func (s *Service) PreparationHistoryWithin(ctx context.Context, q transaction.Executor, tenant, workspace, id string) (Node, error) {
	if e := s.preparationSourceExecutor(ctx, q, tenant, workspace, id); e != nil {
		return Node{}, e
	}
	if q.Dialect() == transaction.PostgreSQL {
		n, e := s.lockedInstalledPostgresNode(ctx, q, tenant, workspace, id, nil)
		if e != nil {
			return Node{}, preparationSourceError(e)
		}
		return n, nil
	}
	installed, e := nodeTenant(ctx, q)
	if e != nil {
		return Node{}, preparationSourceError(e)
	}
	if installed != tenant {
		return Node{}, fail("RESOURCE_NOT_FOUND")
	}
	n, e := s.lockedUpdateNode(ctx, q, tenant, workspace, id)
	if e != nil {
		return Node{}, preparationSourceError(e)
	}
	return n, nil
}

// The lateral lookup depends on the locked installation row so the node
// lock cannot come first, even when a competing transaction holds the node.
func (s *Service) lockedInstalledPostgresNode(ctx context.Context, q transaction.Executor, tenant, workspace, id string, credentialHash *[]byte) (Node, error) {
	fields := pairedNodeFields
	if credentialHash != nil {
		fields += ",n.credential_hash"
	}
	p := s.Identity.Config.Policy().Nodes
	row := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `WITH installed AS MATERIALIZED (
		SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id
		WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' AND i.tenant_id=$3
		FOR SHARE OF i,t
	)
	SELECT `+fields+` FROM installed CROSS JOIN LATERAL (
		SELECT n.* FROM managed_nodes n
		WHERE n.tenant_id=installed.tenant_id AND n.workspace_id=$4 AND n.id=$5
		FOR UPDATE OF n
	) n`, p.OnlineSeconds, p.OfflineSeconds, tenant, workspace, id))
	return scanNodeRow(row, nil, credentialHash)
}

func preparationNodeProfile(n Node) bool {
	return n.ExecutionProfile == wire.DockerModelLifecycleProfile && n.ProtocolVersion == wire.ModelLifecycleVersion &&
		slices.Contains(n.Capabilities, wire.ModelPreparationRunOperation)
}

func (s *Service) ModelPreparationTargetWithin(ctx context.Context, q transaction.Executor, tenant, workspace, id string) (Node, error) {
	n, e := s.PreparationHistoryWithin(ctx, q, tenant, workspace, id)
	if e != nil {
		return Node{}, e
	}
	if n.Status != "active" {
		return Node{}, fail("NODE_UNAVAILABLE")
	}
	if !preparationNodeProfile(n) {
		return Node{}, fail("AGENT_PROFILE_REJECTED")
	}
	return n, nil
}

// PreparationStopTaskWithin rechecks persisted node capabilities rather than
// trusting a stale DTO. Removed nodes retain stopped-command history only.
func (s *Service) PreparationStopTaskWithin(ctx context.Context, q transaction.Executor, n Node, id string) (Task, lifecycle.Command, error) {
	var zero lifecycle.Command
	if !nodeID(id) {
		return Task{}, zero, fail("RESOURCE_NOT_FOUND")
	}
	current, e := s.PreparationHistoryWithin(ctx, q, n.TenantID, n.WorkspaceID, n.ID)
	if e != nil {
		return Task{}, zero, e
	}
	if !preparationNodeProfile(current) {
		return Task{}, zero, fail("AGENT_PROFILE_REJECTED")
	}
	t, e := scanTask(q.QueryRow(ctx, nodeSQL(q,
		"SELECT "+taskFields+" FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 FOR UPDATE",
		"SELECT "+mysqlTaskFields+" FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? FOR UPDATE", `SELECT `+damengTaskFields+` FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? FOR UPDATE`,
		current.TenantID, current.WorkspaceID, current.ID, id)))
	if e != nil {
		return Task{}, zero, preparationSourceError(e)
	}
	if t.Operation != wire.ModelPreparationRunOperation {
		return Task{}, zero, fail("AGENT_PROFILE_REJECTED")
	}
	var raw []byte
	e = q.QueryRow(ctx, nodeSQL(q,
		"SELECT payload FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4",
		"SELECT payload FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?", `SELECT payload FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`,
		current.TenantID, current.WorkspaceID, current.ID, id)).Scan(&raw)
	if e != nil {
		return Task{}, zero, preparationSourceError(e)
	}
	command, e := lifecycle.DecodeCommand(raw)
	if e != nil || command.Scope.TenantID != current.TenantID || command.Scope.WorkspaceID != current.WorkspaceID {
		return Task{}, zero, fail("MODEL_PREPARATION_COMMAND_INVALID")
	}
	return t, command, nil
}

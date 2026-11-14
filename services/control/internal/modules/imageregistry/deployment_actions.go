package imageregistry

import (
	"context"
	"database/sql"
	"errors"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

var _ deployments.RegistryActionOwner = (*Service)(nil)

func (s *Service) actionExecutor(ctx context.Context, q transaction.Executor, a identity.Auth) error {
	if s == nil || s.identity == nil || s.nodes == nil || s.deployments == nil || s.license == nil || q == nil || ctx == nil || a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return failure("REGISTRY_OWNER_REQUIRED")
	}
	if _, store := q.(transaction.Store); store {
		return failure("REGISTRY_OWNER_REQUIRED")
	}
	store, e := s.identity.Repository()
	if e != nil {
		return e
	}
	if store.Dialect() != q.Dialect() {
		return failure("REGISTRY_OWNER_REQUIRED")
	}
	return s.identity.RecheckOrderedWithin(ctx, q, a)
}

func (s *Service) actionPinWithin(ctx context.Context, q transaction.Executor, n nodes.Node, deployment string) (string, error) {
	var pin string
	var released *time.Time
	e := q.QueryRow(ctx, statement(q, `SELECT pin_id::text,released_at FROM image_registry_deployment_refs WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND deployment_id=$4 FOR UPDATE`, `SELECT pin_id,released_at FROM image_registry_deployment_refs WHERE tenant_id=? AND workspace_id=? AND node_id=? AND deployment_id=? FOR UPDATE`, n.TenantID, n.WorkspaceID, n.ID, deployment)).Scan(&pin, &released)
	if errors.Is(e, sql.ErrNoRows) {
		witnessed, readErr := s.nodes.RegistryIntentRetainedWithin(ctx, q, n, deployment)
		if readErr != nil {
			return "", readErr
		}
		if witnessed {
			return "", failure("REGISTRY_REFERENCE_MISSING")
		}
		return "", nil
	}
	if e != nil {
		return "", e
	}
	if !validID(pin) {
		return "", failure("REGISTRY_REFERENCE_MISSING")
	}
	if released != nil {
		return "", failure("REGISTRY_REFERENCE_RELEASED")
	}
	return pin, nil
}

func (s *Service) PrepareActionWithin(ctx context.Context, q transaction.Executor, a identity.Auth, n nodes.Node, d deployments.Deployment, c wire.InferenceCommand) (out wire.InferenceCommand, result error) {
	defer func() { result = safeError(result) }()
	if e := s.actionExecutor(ctx, q, a); e != nil {
		return out, e
	}
	if s == nil || s.nodes == nil || q == nil || n.ID != d.NodeID || n.WorkspaceID != d.WorkspaceID || c.DeploymentID != d.ID || c.Revision != d.Revision+1 {
		return out, failure("REGISTRY_OWNER_REQUIRED")
	}
	pin, e := s.actionPinWithin(ctx, q, n, d.ID)
	if e != nil {
		return out, e
	}
	if c.Action != "deploy" {
		if c.RegistryPinID != "" {
			return out, failure("REGISTRY_RECIPE_MISMATCH")
		}
		return c, nil
	}
	if c.RegistryPinID != "" && c.RegistryPinID != pin {
		return out, failure("REGISTRY_RECIPE_MISMATCH")
	}
	if pin != "" {
		if e = s.license.CheckWithin(ctx, q); e != nil {
			return out, e
		}
		p, e := pinWithin(ctx, q, n.TenantID, n.WorkspaceID, pin)
		if e != nil {
			return out, e
		}
		recipe := wire.ApprovedInferenceRecipe()
		if p.State != "ready" || p.ManifestDigest == nil || *p.ManifestDigest != recipe.ImageManifestDigest || p.ConfigDigest == nil || *p.ConfigDigest != recipe.ImageConfigDigest {
			return out, failure("REGISTRY_RECIPE_MISMATCH")
		}
	}
	c.RegistryPinID = pin
	return c, nil
}

func (s *Service) BindActionWithin(ctx context.Context, q transaction.Executor, a identity.Auth, n nodes.Node, t nodes.Task, c wire.InferenceCommand, replayed bool, reason, request string) (result error) {
	defer func() { result = safeError(result) }()
	if e := s.actionExecutor(ctx, q, a); e != nil {
		return e
	}
	if s == nil || s.nodes == nil || q == nil || t.NodeID != n.ID || t.TenantID != n.TenantID || t.WorkspaceID != n.WorkspaceID || c.Validate(t.Operation) != nil {
		return failure("REGISTRY_OWNER_REQUIRED")
	}
	pin, e := s.actionPinWithin(ctx, q, n, c.DeploymentID)
	if e != nil {
		return e
	}
	if c.Action == "deploy" && c.RegistryPinID != pin {
		return failure("REGISTRY_RECIPE_MISMATCH")
	}
	if pin == "" {
		return nil
	}
	if replayed {
		if c.Action == "deploy" {
			return s.bindDeployment(ctx, q, a, pin, n, t, c, true)
		}
		var live bool
		e = q.QueryRow(ctx, statement(q, `SELECT EXISTS(SELECT 1 FROM image_registry_grants WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND deployment_id=$4 AND revision<=$5 AND revoked_at IS NULL)`, `SELECT EXISTS(SELECT 1 FROM image_registry_grants WHERE tenant_id=? AND workspace_id=? AND node_id=? AND deployment_id=? AND revision<=? AND revoked_at IS NULL)`, n.TenantID, n.WorkspaceID, n.ID, c.DeploymentID, c.Revision)).Scan(&live)
		if e != nil {
			return e
		}
		if live {
			return failure("REGISTRY_GRANT_REVOKED")
		}
		return nil
	}
	if _, e = q.Exec(ctx, statement(q, `UPDATE image_registry_grants SET revoked_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND deployment_id=$4 AND revoked_at IS NULL`, `UPDATE image_registry_grants SET revoked_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND deployment_id=? AND revoked_at IS NULL`, n.TenantID, n.WorkspaceID, n.ID, c.DeploymentID)); e != nil {
		return e
	}
	if c.Action == "deploy" {
		if e = s.bindDeployment(ctx, q, a, pin, n, t, c, false); e != nil {
			return e
		}
	}
	return registryAudit(ctx, q, a, workspaces.Workspace{TenantID: n.TenantID, ID: n.WorkspaceID}, "deployment.action_bound", pin, Mutation{WorkspaceID: n.WorkspaceID, Reason: reason, RequestID: request})
}

// RequireEmptyWhenDisabled prevents dropping authority after retaining Registry
// state. The server calls it before serving an omitted optional component.
func RequireEmptyWhenDisabled(ctx context.Context, store transaction.Store) error {
	if store == nil || ctx == nil {
		return failure("REGISTRY_OWNER_REQUIRED")
	}
	if store.Dialect() != transaction.PostgreSQL {
		return nil
	}
	return safeError(store.Within(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		var present bool
		if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT to_regclass('image_registry_pins') IS NOT NULL`)).Scan(&present); e != nil {
			return e
		}
		if !present {
			return nil
		}
		var retained bool
		if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT EXISTS(SELECT 1 FROM image_registry_pins)`)).Scan(&retained); e != nil {
			return e
		}
		if retained {
			return failure("REGISTRY_OWNER_REQUIRED")
		}
		return nil
	}))
}

func grantBindingStatement(q transaction.Executor, d deployments.RegistryDeployment, a identity.Auth) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, `SELECT pin_id,revoked_at IS NULL AND expires_at>UTC_TIMESTAMP(6) AND revision=? AND created_by=? AND actor_epoch=? AND created_session_id=? AND created_token_hash=? FROM image_registry_grants WHERE tenant_id=? AND workspace_id=? AND node_id=? AND task_id=? AND revoked_at IS NULL FOR UPDATE`, d.Revision, a.Me.User.ID, a.Epoch, a.SessionID, a.TokenHash, d.TenantID, d.WorkspaceID, d.NodeID, d.TaskID)
	}
	return transaction.SQL(transaction.PostgreSQL, `SELECT pin_id::text,revoked_at IS NULL AND expires_at>clock_timestamp() AND revision=$5 AND created_by=$6 AND actor_epoch=$7 AND created_session_id=$8 AND created_token_hash=$9 FROM image_registry_grants WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND task_id=$4 AND revoked_at IS NULL FOR UPDATE`, d.TenantID, d.WorkspaceID, d.NodeID, d.TaskID, d.Revision, a.Me.User.ID, a.Epoch, a.SessionID, a.TokenHash)
}

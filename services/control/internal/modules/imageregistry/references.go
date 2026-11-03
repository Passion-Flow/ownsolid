package imageregistry

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// bindDeployment is invoked by the actual deployment owner after its own task
// record. It must never call a Store, start a transaction or issue remote work.
func (s *Service) bindDeployment(ctx context.Context, q transaction.Executor, a identity.Auth, pin string, n nodes.Node, t nodes.Task, c wire.InferenceCommand, replayed bool) error {
	if e := s.license.CheckWithin(ctx, q); e != nil {
		return e
	}
	_, e := s.deployments.RegistryReferenceWithin(ctx, q, n, t, c, func(ctx context.Context, q transaction.Executor, d deployments.RegistryDeployment) error {
		p, e := pinWithin(ctx, q, d.TenantID, d.WorkspaceID, pin)
		if e != nil {
			return e
		}
		if p.State != "ready" || p.ManifestDigest == nil || *p.ManifestDigest != d.ImageManifestDigest || p.ConfigDigest == nil || *p.ConfigDigest != d.ImageConfigDigest {
			return failure("REGISTRY_RECIPE_MISMATCH")
		}
		var existing string
		var released *time.Time
		e = q.QueryRow(ctx, statement(q, `SELECT pin_id::text,released_at FROM image_registry_deployment_refs WHERE tenant_id=$1 AND workspace_id=$2 AND deployment_id=$3 FOR UPDATE`, `SELECT pin_id,released_at FROM image_registry_deployment_refs WHERE tenant_id=? AND workspace_id=? AND deployment_id=? FOR UPDATE`, d.TenantID, d.WorkspaceID, d.DeploymentID)).Scan(&existing, &released)
		if e == nil {
			if existing != pin || released != nil {
				return failure("IDEMPOTENCY_CONFLICT")
			}
		} else {
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if replayed {
				return failure("REGISTRY_REFERENCE_MISSING")
			}
			var retained int
			if e = q.QueryRow(ctx, statement(q, `SELECT COUNT(*) FROM image_registry_deployment_refs WHERE tenant_id=$1`, `SELECT COUNT(*) FROM image_registry_deployment_refs WHERE tenant_id=?`, d.TenantID)).Scan(&retained); e != nil {
				return e
			}
			if retained >= s.config.MaximumReferences {
				return failure("REGISTRY_CAPACITY_REQUIRED")
			}
			_, e = q.Exec(ctx, statement(q, `INSERT INTO image_registry_deployment_refs(tenant_id,workspace_id,pin_id,node_id,deployment_id,created_revision) VALUES($1,$2,$3,$4,$5,$6)`, `INSERT INTO image_registry_deployment_refs(tenant_id,workspace_id,pin_id,node_id,deployment_id,created_revision) VALUES(?,?,?,?,?,?)`, d.TenantID, d.WorkspaceID, pin, d.NodeID, d.DeploymentID, d.Revision))
			if e != nil {
				return e
			}
		}
		var grantPin string
		var active bool
		e = q.QueryRow(ctx, grantBindingStatement(q, d, a)).Scan(&grantPin, &active)
		if e == nil {
			if grantPin != pin || !active {
				return failure("REGISTRY_GRANT_REVOKED")
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if replayed {
			return failure("REGISTRY_REFERENCE_MISSING")
		}
		var now time.Time
		if e = q.QueryRow(ctx, statement(q, "SELECT clock_timestamp()", "SELECT UTC_TIMESTAMP(6)")).Scan(&now); e != nil {
			return e
		}
		_, e = q.Exec(ctx, statement(q, `INSERT INTO image_registry_grants(id,tenant_id,workspace_id,pin_id,node_id,deployment_id,task_id,revision,created_by,actor_epoch,created_session_id,created_token_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, `INSERT INTO image_registry_grants(id,tenant_id,workspace_id,pin_id,node_id,deployment_id,task_id,revision,created_by,actor_epoch,created_session_id,created_token_hash,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, uuid.NewString(), d.TenantID, d.WorkspaceID, pin, d.NodeID, d.DeploymentID, d.TaskID, d.Revision, a.Me.User.ID, a.Epoch, a.SessionID, a.TokenHash, now.UTC().Add(time.Duration(s.config.GrantSeconds)*time.Second)))
		return e
	})
	return e
}
func (s *Service) CreateDeployment(ctx context.Context, a identity.Auth, m Mutation, pin string, input deployments.CreateRequest) (deployments.Deployment, error) {
	if m.validate() != nil || !validID(pin) || input.Reason != m.Reason {
		return deployments.Deployment{}, failure("VALIDATION_FAILED")
	}
	// The deployment owner repeats current actor/node/model/recipe/resource checks
	// and invokes this callback inside its original creation transaction.
	if _, e := s.Get(ctx, a, m.WorkspaceID, pin); e != nil {
		return deployments.Deployment{}, e
	}
	if e := s.license.Check(ctx); e != nil {
		return deployments.Deployment{}, e
	}
	out, e := s.deployments.CreateRegistryPinned(ctx, a, input, m.Key, m.RequestID, pin, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.workspaces.RecheckScopeWithin(ctx, q, a, m.WorkspaceID)
		if e != nil {
			return e
		}
		if w.Status != "active" {
			return failure("ACCESS_DENIED")
		}
		return s.license.CheckWithin(ctx, q)
	}, func(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, c wire.InferenceCommand, replayed bool) error {
		if n.WorkspaceID != m.WorkspaceID {
			return failure("ACCESS_DENIED")
		}
		return s.bindDeployment(ctx, q, a, pin, n, t, c, replayed)
	})
	if e != nil {
		return deployments.Deployment{}, safeError(e)
	}
	return out, nil
}
func (s *Service) Revoke(ctx context.Context, a identity.Auth, m Mutation, pin string) error {
	if m.validate() != nil || !validID(pin) {
		return failure("VALIDATION_FAILED")
	}
	return s.actor(ctx, a, m.WorkspaceID, true, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		_, repeat, e := mutationWithin(ctx, q, a, w, "revoke", pin, m, []string{pin, m.Reason})
		if e != nil || repeat {
			return e
		}
		if _, e = pinWithin(ctx, q, w.TenantID, w.ID, pin); e != nil {
			return e
		}
		_, e = q.Exec(ctx, statement(q, `UPDATE image_registry_grants SET revoked_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 AND revoked_at IS NULL`, `UPDATE image_registry_grants SET revoked_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND pin_id=? AND revoked_at IS NULL`, w.TenantID, w.ID, pin))
		if e != nil {
			return e
		}
		return registryAudit(ctx, q, a, w, "revoke", pin, m)
	})
}
func (s *Service) CreateOfflineReference(ctx context.Context, a identity.Auth, m Mutation, pin string) (string, error) {
	if m.validate() != nil || !validID(pin) {
		return "", failure("VALIDATION_FAILED")
	}
	reference := uuid.NewString()
	e := s.actor(ctx, a, m.WorkspaceID, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		target, repeat, e := mutationWithin(ctx, q, a, w, "offline.hold", reference, m, []string{pin, m.Reason})
		if e != nil {
			return e
		}
		reference = target
		if repeat {
			return nil
		}
		p, e := pinWithin(ctx, q, w.TenantID, w.ID, pin)
		if e != nil {
			return e
		}
		if p.State != "ready" {
			return failure("REGISTRY_PIN_UNAVAILABLE")
		}
		var count int
		if e = q.QueryRow(ctx, statement(q, `SELECT COUNT(*) FROM image_registry_offline_refs WHERE tenant_id=$1`, `SELECT COUNT(*) FROM image_registry_offline_refs WHERE tenant_id=?`, w.TenantID)).Scan(&count); e != nil {
			return e
		}
		if count >= s.config.MaximumReferences {
			return failure("REGISTRY_CAPACITY_REQUIRED")
		}
		_, e = q.Exec(ctx, statement(q, `INSERT INTO image_registry_offline_refs(id,tenant_id,workspace_id,pin_id,created_by) VALUES($1,$2,$3,$4,$5)`, `INSERT INTO image_registry_offline_refs(id,tenant_id,workspace_id,pin_id,created_by) VALUES(?,?,?,?,?)`, reference, w.TenantID, w.ID, pin, a.Me.User.ID))
		if e != nil {
			return e
		}
		return registryAudit(ctx, q, a, w, "offline.hold", pin, m)
	})
	if e != nil {
		return "", e
	}
	return reference, nil
}
func (s *Service) ReleaseOfflineReference(ctx context.Context, a identity.Auth, m Mutation, pin, reference string) error {
	if m.validate() != nil || !validID(pin) || !validID(reference) {
		return failure("VALIDATION_FAILED")
	}
	return s.actor(ctx, a, m.WorkspaceID, true, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		_, repeat, e := mutationWithin(ctx, q, a, w, "offline.release", reference, m, []string{pin, reference, m.Reason})
		if e != nil || repeat {
			return e
		}
		if _, e = pinWithin(ctx, q, w.TenantID, w.ID, pin); e != nil {
			return e
		}
		changed, e := q.Exec(ctx, statement(q, `UPDATE image_registry_offline_refs SET released_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 AND id=$4 AND released_at IS NULL`, `UPDATE image_registry_offline_refs SET released_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND pin_id=? AND id=? AND released_at IS NULL`, w.TenantID, w.ID, pin, reference))
		if e != nil {
			return e
		}
		if changed != 1 {
			return failure("RESOURCE_NOT_FOUND")
		}
		return registryAudit(ctx, q, a, w, "offline.release", pin, m)
	})
}
func (s *Service) ReleaseDeploymentReference(ctx context.Context, a identity.Auth, m Mutation, pin, deployment string) error {
	if m.validate() != nil || !validID(pin) || !validID(deployment) {
		return failure("VALIDATION_FAILED")
	}
	return s.actor(ctx, a, m.WorkspaceID, true, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		return s.deployments.RegistryRemovalWithin(ctx, q, w.TenantID, w.ID, deployment, func(ctx context.Context, q transaction.Executor) error {
			_, repeat, e := mutationWithin(ctx, q, a, w, "reference.release", deployment, m, []string{pin, deployment, m.Reason})
			if e != nil || repeat {
				return e
			}
			if _, e = pinWithin(ctx, q, w.TenantID, w.ID, pin); e != nil {
				return e
			}
			changed, e := q.Exec(ctx, statement(q, `UPDATE image_registry_deployment_refs SET released_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 AND deployment_id=$4 AND released_at IS NULL`, `UPDATE image_registry_deployment_refs SET released_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND pin_id=? AND deployment_id=? AND released_at IS NULL`, w.TenantID, w.ID, pin, deployment))
			if e != nil {
				return e
			}
			if changed != 1 {
				return failure("RESOURCE_NOT_FOUND")
			}
			return registryAudit(ctx, q, a, w, "reference.release", pin, m)
		})
	})
}

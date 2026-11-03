package imageregistry

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Grant explicitly renews only the original current task actor's delegated pull.
// Prior grants retain history and are irreversibly revoked in the same audit tx.
func (s *Service) Grant(ctx context.Context, a identity.Auth, m Mutation, pin, deployment string) (string, error) {
	if m.validate() != nil || !validID(pin) || !validID(deployment) {
		return "", failure("VALIDATION_FAILED")
	}
	grant := uuid.NewString()
	e := s.actor(ctx, a, m.WorkspaceID, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		target, repeat, e := mutationWithin(ctx, q, a, w, "grant", grant, m, []string{pin, deployment, m.Reason})
		if e != nil {
			return e
		}
		grant = target
		var node, task string
		e = q.QueryRow(ctx, statement(q, `SELECT node_id::text,COALESCE(last_task_id::text,'') FROM deployments WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3`, `SELECT node_id,COALESCE(last_task_id,'') FROM deployments WHERE tenant_id=? AND workspace_id=? AND id=?`, w.TenantID, w.ID, deployment)).Scan(&node, &task)
		if e != nil {
			return e
		}
		n, t, c, e := s.nodes.CurrentRegistryTaskWithin(ctx, q, a, node, task)
		if e != nil {
			return e
		}
		if n.TenantID != w.TenantID || n.WorkspaceID != w.ID {
			return failure("ACCESS_DENIED")
		}
		_, e = s.deployments.RegistryReferenceWithin(ctx, q, n, t, c, func(ctx context.Context, q transaction.Executor, d deployments.RegistryDeployment) error {
			p, e := pinWithin(ctx, q, w.TenantID, w.ID, pin)
			if e != nil {
				return e
			}
			if p.State != "ready" || p.ManifestDigest == nil || *p.ManifestDigest != d.ImageManifestDigest || p.ConfigDigest == nil || *p.ConfigDigest != d.ImageConfigDigest {
				return failure("REGISTRY_RECIPE_MISMATCH")
			}
			var held bool
			if e = q.QueryRow(ctx, statement(q, `SELECT released_at IS NULL FROM image_registry_deployment_refs WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 AND node_id=$4 AND deployment_id=$5 FOR UPDATE`, `SELECT released_at IS NULL FROM image_registry_deployment_refs WHERE tenant_id=? AND workspace_id=? AND pin_id=? AND node_id=? AND deployment_id=? FOR UPDATE`, w.TenantID, w.ID, pin, node, deployment)).Scan(&held); e != nil {
				return e
			}
			if !held {
				return failure("REGISTRY_REFERENCE_RELEASED")
			}
			if repeat {
				var live bool
				e = q.QueryRow(ctx, statement(q, `SELECT revoked_at IS NULL AND expires_at>clock_timestamp() AND task_id=$1 AND revision=$2 AND created_by=$3 AND actor_epoch=$4 AND created_session_id=$5 AND created_token_hash=$6 FROM image_registry_grants WHERE id=$7 AND tenant_id=$8 AND workspace_id=$9 AND pin_id=$10`, `SELECT revoked_at IS NULL AND expires_at>UTC_TIMESTAMP(6) AND task_id=? AND revision=? AND created_by=? AND actor_epoch=? AND created_session_id=? AND created_token_hash=? FROM image_registry_grants WHERE id=? AND tenant_id=? AND workspace_id=? AND pin_id=?`, task, d.Revision, a.Me.User.ID, a.Epoch, a.SessionID, a.TokenHash, grant, w.TenantID, w.ID, pin)).Scan(&live)
				if e != nil {
					return e
				}
				if !live {
					return failure("REGISTRY_GRANT_REVOKED")
				}
				return nil
			}
			_, e = q.Exec(ctx, statement(q, `UPDATE image_registry_grants SET revoked_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND task_id=$4 AND revoked_at IS NULL`, `UPDATE image_registry_grants SET revoked_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND task_id=? AND revoked_at IS NULL`, w.TenantID, w.ID, node, task))
			if e != nil {
				return e
			}
			var now time.Time
			if e = q.QueryRow(ctx, statement(q, "SELECT clock_timestamp()", "SELECT UTC_TIMESTAMP(6)")).Scan(&now); e != nil {
				return e
			}
			_, e = q.Exec(ctx, statement(q, `INSERT INTO image_registry_grants(id,tenant_id,workspace_id,pin_id,node_id,deployment_id,task_id,revision,created_by,actor_epoch,created_session_id,created_token_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, `INSERT INTO image_registry_grants(id,tenant_id,workspace_id,pin_id,node_id,deployment_id,task_id,revision,created_by,actor_epoch,created_session_id,created_token_hash,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, grant, w.TenantID, w.ID, pin, node, deployment, task, d.Revision, a.Me.User.ID, a.Epoch, a.SessionID, a.TokenHash, now.UTC().Add(time.Duration(s.config.GrantSeconds)*time.Second)))
			if e != nil {
				return e
			}
			return registryAudit(ctx, q, a, w, "grant.renewed", pin, m)
		})
		return e
	})
	if e != nil {
		return "", e
	}
	return grant, nil
}

package plugins

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const grantFields = `g.id::text,g.workspace_id::text,g.plugin_version_id::text,g.enabled,g.revision,g.granted_by::text,g.created_at,g.updated_at,g.authority_id::text`

func scanGrant(row transaction.Row) (grantRecord, error) {
	var out grantRecord
	e := row.Scan(&out.ID, &out.WorkspaceID, &out.PluginVersionID, &out.Enabled, &out.Revision, &out.GrantedBy, &out.CreatedAt, &out.UpdatedAt, &out.authority)
	if errors.Is(e, sql.ErrNoRows) {
		return out, failure("PLUGIN_GRANT_NOT_FOUND")
	}
	if e != nil {
		return grantRecord{}, pluginError(e)
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}
func (s *Service) workspace(ctx context.Context, tx transaction.Executor, a identity.Auth, tenant, workspace string, active bool) (workspaces.Workspace, error) {
	w, e := s.Workspaces.RecheckScopeWithin(ctx, tx, a, workspace)
	if e != nil {
		return w, e
	}
	if w.TenantID != tenant || active && w.Status != "active" {
		return w, failure("ACCESS_DENIED")
	}
	return w, nil
}
func (s *Service) grantVersion(ctx context.Context, tx transaction.Executor, tenant string, g *grantRecord) error {
	v, e := scanVersion(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+versionFields+" FROM plugin_versions WHERE tenant_id=$1 AND id=$2", "SELECT "+mysqlVersionFields+" FROM plugin_versions WHERE tenant_id=? AND id=?", nativePluginVersionByID, tenant, g.PluginVersionID)))
	if e != nil {
		return e
	}
	g.Version = s.present(v)
	return nil
}
func (s *Service) Grant(ctx context.Context, a identity.Auth, workspace string, input GrantRequest, key, request string) (Grant, error) {
	var out Grant
	if !validID(workspace) || !validID(input.PluginVersionID) || !validReason(input.Reason) {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.adminTransaction(ctx, a, true, func(ctx context.Context, tx transaction.Executor, tenant string) error {
		if _, e := s.workspace(ctx, tx, a, tenant, workspace, true); e != nil {
			return e
		}
		purpose := "grant:" + workspace
		previous, digest, e := s.intent(ctx, tx, a, tenant, purpose, key, input)
		if e != nil {
			return e
		}
		if previous != "" {
			g, e := scanGrant(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+grantFields+" FROM plugin_grants g WHERE g.tenant_id=$1 AND g.workspace_id=$2 AND g.id=$3", "SELECT "+mysqlGrantFields+" FROM plugin_grants g WHERE g.tenant_id=? AND g.workspace_id=? AND g.id=?", nativePluginGrantScoped, tenant, workspace, previous)))
			if e != nil {
				return e
			}
			if e = s.grantVersion(ctx, tx, tenant, &g); e != nil {
				return e
			}
			out = g.Grant
			return nil
		}
		v, e := scanVersion(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+versionFields+" FROM plugin_versions WHERE tenant_id=$1 AND id=$2 FOR SHARE", "SELECT "+mysqlVersionFields+" FROM plugin_versions WHERE tenant_id=? AND id=? FOR SHARE", nativePluginVersionLocked, tenant, input.PluginVersionID)))
		if e != nil {
			return e
		}
		if v.State != InstalledState {
			return failure("PLUGIN_NOT_INSTALLED")
		}
		if _, e = s.verified(v); e != nil {
			return e
		}
		var exists bool
		if e = tx.QueryRow(ctx, pluginSQL(tx, "SELECT EXISTS(SELECT 1 FROM plugin_grants WHERE tenant_id=$1 AND workspace_id=$2 AND plugin_version_id=$3)", "SELECT EXISTS(SELECT 1 FROM plugin_grants WHERE tenant_id=? AND workspace_id=? AND plugin_version_id=?)", nativePluginGrantExists, tenant, workspace, v.ID)).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return failure("PLUGIN_GRANT_EXISTS")
		}
		var count int
		if e = tx.QueryRow(ctx, pluginSQL(tx, "SELECT count(*) FROM plugin_grants WHERE tenant_id=$1", "SELECT count(*) FROM plugin_grants WHERE tenant_id=?", nativePluginGrantCount, tenant)).Scan(&count); e != nil {
			return e
		}
		if count >= s.policy().MaximumGrants {
			return failure("PLUGIN_LIMIT_EXCEEDED")
		}
		id := uuid.NewString()
		if e = pluginExecOne(ctx, tx, pluginSQL(tx, `INSERT INTO plugin_grants(id,tenant_id,workspace_id,plugin_version_id,authority_id,granted_by) VALUES($1,$2,$3,$4,$5,$6)`, `INSERT INTO plugin_grants(id,tenant_id,workspace_id,plugin_version_id,authority_id,granted_by) VALUES(?,?,?,?,?,?)`, nativePluginGrantCreate, id, tenant, workspace, v.ID, uuid.NewString(), a.Me.User.ID)); e != nil {
			return e
		}
		if e = saveIntent(ctx, tx, a, tenant, purpose, key, id, digest); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, tenant, workspace, "plugin.granted", "plugin_grant", id, input.Reason, request); e != nil {
			return e
		}
		g, e := scanGrant(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+grantFields+" FROM plugin_grants g WHERE g.id=$1", "SELECT "+mysqlGrantFields+" FROM plugin_grants g WHERE g.id=?", nativePluginGrantByID, id)))
		if e != nil {
			return e
		}
		g.Version = s.present(v)
		out = g.Grant
		return nil
	})
	if e != nil {
		return Grant{}, e
	}
	return out, nil
}
func (s *Service) UpdateGrant(ctx context.Context, a identity.Auth, workspace, id string, input GrantUpdate, key, request string) (Grant, error) {
	var out Grant
	if !validID(workspace) || !validID(id) || !validReason(input.Reason) || input.ExpectedRevision < 1 || input.ExpectedRevision >= MaximumRevision {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.adminTransaction(ctx, a, input.Enabled, func(ctx context.Context, tx transaction.Executor, tenant string) error {
		if _, e := s.workspace(ctx, tx, a, tenant, workspace, input.Enabled); e != nil {
			return e
		}
		purpose := "grant_update:" + id
		previous, digest, e := s.intent(ctx, tx, a, tenant, purpose, key, input)
		if e != nil {
			return e
		}
		var version string
		if e = tx.QueryRow(ctx, pluginSQL(tx, "SELECT plugin_version_id::text FROM plugin_grants WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "SELECT plugin_version_id FROM plugin_grants WHERE tenant_id=? AND workspace_id=? AND id=?", nativePluginGrantVersionID, tenant, workspace, id)).Scan(&version); errors.Is(e, sql.ErrNoRows) {
			return failure("PLUGIN_GRANT_NOT_FOUND")
		} else if e != nil {
			return e
		}
		v, e := scanVersion(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+versionFields+" FROM plugin_versions WHERE tenant_id=$1 AND id=$2 FOR SHARE", "SELECT "+mysqlVersionFields+" FROM plugin_versions WHERE tenant_id=? AND id=? FOR SHARE", nativePluginVersionLocked, tenant, version)))
		if e != nil {
			return e
		}
		g, e := scanGrant(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+grantFields+" FROM plugin_grants g WHERE g.tenant_id=$1 AND g.workspace_id=$2 AND g.id=$3 FOR UPDATE", "SELECT "+mysqlGrantFields+" FROM plugin_grants g WHERE g.tenant_id=? AND g.workspace_id=? AND g.id=? FOR UPDATE", nativePluginGrantLocked, tenant, workspace, id)))
		if e != nil {
			return e
		}
		if previous != "" {
			if previous != id {
				return failure("IDEMPOTENCY_CONFLICT")
			}
			g.Version = s.present(v)
			out = g.Grant
			return nil
		}
		if g.Revision != input.ExpectedRevision {
			return failure("PLUGIN_REVISION_CONFLICT")
		}
		if input.Enabled {
			if v.State != InstalledState {
				return failure("PLUGIN_NOT_INSTALLED")
			}
			if _, e = s.verified(v); e != nil {
				return e
			}
		}
		if e = pluginExecOne(ctx, tx, pluginStatement(tx, "UPDATE plugin_grants SET enabled=$2,revision=revision+1,authority_id=$3,updated_at=clock_timestamp() WHERE tenant_id=$4 AND workspace_id=$5 AND id=$1", "UPDATE plugin_grants SET enabled=?,revision=revision+1,authority_id=?,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?", nativePluginGrantUpdate, []any{id, input.Enabled, uuid.NewString(), tenant, workspace}, []any{input.Enabled, uuid.NewString(), tenant, workspace, id})); e != nil {
			return e
		}
		if e = saveIntent(ctx, tx, a, tenant, purpose, key, id, digest); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, tenant, workspace, "plugin.grant_updated", "plugin_grant", id, input.Reason, request); e != nil {
			return e
		}
		g, e = scanGrant(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+grantFields+" FROM plugin_grants g WHERE g.id=$1", "SELECT "+mysqlGrantFields+" FROM plugin_grants g WHERE g.id=?", nativePluginGrantByID, id)))
		if e != nil {
			return e
		}
		g.Version = s.present(v)
		out = g.Grant
		return nil
	})
	if e != nil {
		return Grant{}, e
	}
	return out, nil
}

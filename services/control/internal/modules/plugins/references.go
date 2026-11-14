package plugins

import (
	"context"
	"encoding/json"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

func (s *Service) references(ctx context.Context, tx transaction.Executor, tenant, id string) (References, error) {
	out := References{Items: []Reference{}}
	rows, e := tx.Query(ctx, pluginStatement(tx, `SELECT kind,workspace_id,workflow_id,entity_id,graph_sha256 FROM (
      SELECT 'draft'::text kind,f.workspace_id::text workspace_id,f.id::text workflow_id,f.id::text entity_id,f.draft_sha256::text graph_sha256 FROM workflows f WHERE f.tenant_id=$1 AND EXISTS(SELECT 1 FROM jsonb_array_elements(f.draft_graph->'nodes') n WHERE n->>'kind'='tool' AND n->'configuration'->>'plugin_version_id'=$2)
      UNION ALL SELECT 'publication',v.workspace_id::text,v.workflow_id::text,v.id::text,v.graph_sha256::text FROM workflow_versions v WHERE v.tenant_id=$1 AND EXISTS(SELECT 1 FROM jsonb_array_elements(v.graph->'nodes') n WHERE n->>'kind'='tool' AND n->'configuration'->>'plugin_version_id'=$2)
      UNION ALL SELECT 'conversation',c.workspace_id::text,c.workflow_id::text,c.id::text,v.graph_sha256::text FROM workflow_conversations c JOIN workflow_versions v ON v.tenant_id=c.tenant_id AND v.workspace_id=c.workspace_id AND v.workflow_id=c.workflow_id AND v.id=c.version_id WHERE c.tenant_id=$1 AND c.deleted_at IS NULL AND EXISTS(SELECT 1 FROM workflow_plugin_references r WHERE r.workflow_version_id=c.version_id AND r.plugin_version_id::text=$2)
      UNION ALL SELECT 'active_run',r.workspace_id::text,r.workflow_id::text,r.id::text,r.graph_sha256::text FROM workflow_runs r WHERE r.tenant_id=$1 AND r.state IN ('pending','running') AND EXISTS(SELECT 1 FROM jsonb_array_elements(r.graph_snapshot->'nodes') n WHERE n->>'kind'='tool' AND n->'configuration'->>'plugin_version_id'=$2)
     ) refs ORDER BY kind,workspace_id,workflow_id,entity_id LIMIT $3`, `SELECT kind,workspace_id,workflow_id,entity_id,graph_sha256 FROM (
 SELECT 'draft' kind,f.workspace_id,f.id workflow_id,f.id entity_id,f.draft_sha256 graph_sha256 FROM workflows f WHERE f.tenant_id=? AND EXISTS(SELECT 1 FROM JSON_TABLE(f.draft_graph,'$.nodes[*]' COLUMNS(kind VARCHAR(16) PATH '$.kind',plugin_id VARCHAR(36) PATH '$.configuration.plugin_version_id' NULL ON EMPTY ERROR ON ERROR)) n WHERE n.kind='tool' AND n.plugin_id=?)
 UNION ALL SELECT 'publication',v.workspace_id,v.workflow_id,v.id,v.graph_sha256 FROM workflow_versions v WHERE v.tenant_id=? AND EXISTS(SELECT 1 FROM JSON_TABLE(v.graph,'$.nodes[*]' COLUMNS(kind VARCHAR(16) PATH '$.kind',plugin_id VARCHAR(36) PATH '$.configuration.plugin_version_id' NULL ON EMPTY ERROR ON ERROR)) n WHERE n.kind='tool' AND n.plugin_id=?)
 UNION ALL SELECT 'conversation',c.workspace_id,c.workflow_id,c.id,v.graph_sha256 FROM workflow_conversations c JOIN workflow_versions v ON v.tenant_id=c.tenant_id AND v.workspace_id=c.workspace_id AND v.workflow_id=c.workflow_id AND v.id=c.version_id WHERE c.tenant_id=? AND c.deleted_at IS NULL AND EXISTS(SELECT 1 FROM workflow_plugin_references r WHERE r.workflow_version_id=c.version_id AND r.plugin_version_id=?)
 UNION ALL SELECT 'active_run',r.workspace_id,r.workflow_id,r.id,r.graph_sha256 FROM workflow_runs r WHERE r.tenant_id=? AND r.state IN ('pending','running') AND EXISTS(SELECT 1 FROM JSON_TABLE(r.graph_snapshot,'$.nodes[*]' COLUMNS(kind VARCHAR(16) PATH '$.kind',plugin_id VARCHAR(36) PATH '$.configuration.plugin_version_id' NULL ON EMPTY ERROR ON ERROR)) n WHERE n.kind='tool' AND n.plugin_id=?)
 ) refs ORDER BY kind,workspace_id,workflow_id,entity_id LIMIT ?`, nativePluginReferences, []any{tenant, id, s.policy().MaximumReferences + 1}, []any{tenant, id, tenant, id, tenant, id, tenant, id, s.policy().MaximumReferences + 1}))
	if e != nil {
		return References{}, pluginError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var ref Reference
		if e = rows.Scan(&ref.Kind, &ref.WorkspaceID, &ref.WorkflowID, &ref.EntityID, &ref.GraphSHA256); e != nil {
			return References{}, pluginError(e)
		}
		out.Items = append(out.Items, ref)
	}
	if e = rows.Err(); e != nil {
		return References{}, pluginError(e)
	}
	if len(out.Items) > s.policy().MaximumReferences {
		return References{}, failure("PLUGIN_REFERENCE_LIMIT_EXCEEDED")
	}
	out.Count = len(out.Items)
	raw, _ := json.Marshal(struct {
		VersionID string      `json:"plugin_version_id"`
		Items     []Reference `json:"items"`
	}{id, out.Items})
	out.SHA256 = wire.Digest(raw)
	return out, nil
}
func (s *Service) References(ctx context.Context, a identity.Auth, id string) (References, error) {
	var out References
	if !validID(id) {
		return out, failure("PLUGIN_VERSION_NOT_FOUND")
	}
	e := s.adminTransaction(ctx, a, false, func(ctx context.Context, tx transaction.Executor, tenant string) error {
		if _, e := scanVersion(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+versionFields+" FROM plugin_versions WHERE tenant_id=$1 AND id=$2 FOR SHARE", "SELECT "+mysqlVersionFields+" FROM plugin_versions WHERE tenant_id=? AND id=? FOR SHARE", nativePluginVersionLocked, tenant, id))); e != nil {
			return e
		}
		var e error
		out, e = s.references(ctx, tx, tenant, id)
		return e
	})
	if e != nil {
		return References{}, e
	}
	return out, nil
}
func (s *Service) Uninstall(ctx context.Context, a identity.Auth, id string, input UninstallRequest, key, request string) (Version, error) {
	var out Version
	if !validID(id) || !validReason(input.Reason) || input.ExpectedRevision < 1 || input.ExpectedRevision >= MaximumRevision || len(input.ReferencesSHA256) != 64 {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.adminTransaction(ctx, a, false, func(ctx context.Context, tx transaction.Executor, tenant string) error {
		previous, digest, e := s.intent(ctx, tx, a, tenant, "uninstall:"+id, key, input)
		if e != nil {
			return e
		}
		v, e := scanVersion(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+versionFields+" FROM plugin_versions WHERE tenant_id=$1 AND id=$2 FOR UPDATE", "SELECT "+mysqlVersionFields+" FROM plugin_versions WHERE tenant_id=? AND id=? FOR UPDATE", nativePluginVersionLocked, tenant, id)))
		if e != nil {
			return e
		}
		if previous != "" {
			if previous != id {
				return failure("IDEMPOTENCY_CONFLICT")
			}
			out = s.present(v)
			return nil
		}
		if v.Revision != input.ExpectedRevision {
			return failure("PLUGIN_REVISION_CONFLICT")
		}
		refs, e := s.references(ctx, tx, tenant, id)
		if e != nil {
			return e
		}
		if refs.SHA256 != input.ReferencesSHA256 {
			return failure("PLUGIN_REFERENCES_CHANGED")
		}
		if refs.Count > 0 && !input.ConfirmReferencedUninstall {
			return failure("PLUGIN_REFERENCED")
		}
		if v.State != UninstalledState {
			if e = pluginExecOne(ctx, tx, pluginSQL(tx, "UPDATE plugin_versions SET state='uninstalled',revision=revision+1,uninstalled_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2", "UPDATE plugin_versions SET state='uninstalled',revision=revision+1,uninstalled_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND id=?", nativePluginVersionUninstall, tenant, id)); e != nil {
				return e
			}
			if tx.Dialect() == transaction.Dameng {
				if e = invalidateNativeGrants(ctx, tx, tenant, id); e != nil {
					return e
				}
			} else {
				if _, e = tx.Exec(ctx, pluginSQL(tx, "UPDATE plugin_grants SET enabled=false,authority_id=gen_random_uuid(),revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND plugin_version_id=$2", "UPDATE plugin_grants SET enabled=false,authority_id=LOWER(UUID()),revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND plugin_version_id=?", pluginNativeQuery{}, tenant, id)); e != nil {
					return e
				}
			}
			if e = s.audit(ctx, tx, a, tenant, "", "plugin.uninstalled", "plugin_version", id, input.Reason, request); e != nil {
				return e
			}
		}
		if e = saveIntent(ctx, tx, a, tenant, "uninstall:"+id, key, id, digest); e != nil {
			return e
		}
		v, e = scanVersion(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+versionFields+" FROM plugin_versions WHERE tenant_id=$1 AND id=$2", "SELECT "+mysqlVersionFields+" FROM plugin_versions WHERE tenant_id=? AND id=?", nativePluginVersionByID, tenant, id)))
		if e != nil {
			return e
		}
		out = s.present(v)
		return nil
	})
	if e != nil {
		return Version{}, e
	}
	return out, nil
}

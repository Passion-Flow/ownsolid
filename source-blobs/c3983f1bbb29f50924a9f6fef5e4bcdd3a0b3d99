package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	"strconv"
	"time"
)

type pluginNativeQuery struct {
	text                                 string
	jsonArgument, limitArgument, maximum int
	suffix                               string
	order                                []int
}

func (query pluginNativeQuery) statement(input ...any) transaction.Statement {
	invalid := func() transaction.Statement { return transaction.SQL(transaction.Dameng, "") }
	if query.text == "" {
		return invalid()
	}
	args := append([]any(nil), input...)
	if query.jsonArgument != 0 {
		i := query.jsonArgument - 1
		if i < 0 || i >= len(args) {
			return invalid()
		}
		var raw []byte
		switch value := args[i].(type) {
		case []byte:
			raw = value
		case json.RawMessage:
			raw = value
		case string:
			raw = []byte(value)
		default:
			return invalid()
		}
		if !json.Valid(raw) {
			return invalid()
		}
		args[i] = string(raw)
	}
	text := query.text
	if query.limitArgument != 0 {
		i := query.limitArgument - 1
		if i < 0 || i >= len(args) {
			return invalid()
		}
		limit, ok := args[i].(int)
		if !ok || limit < 1 || limit > query.maximum {
			return invalid()
		}
		text += " LIMIT " + strconv.Itoa(limit) + query.suffix
		args = append(args[:i], args[i+1:]...)
	}
	if len(query.order) != 0 {
		ordered := make([]any, len(query.order))
		for i, position := range query.order {
			if position < 1 || position > len(args) {
				return invalid()
			}
			ordered[i] = args[position-1]
		}
		args = ordered
	}
	return transaction.SQL(transaction.Dameng, text, args...)
}

const nativeVersionFields = `id,tenant_id,plugin_id,release_version,abi,executor,capsule_sha256,manifest_sha256,key_id,state,revision,imported_by,created_at,installed_at,uninstalled_at,capsule,manifest`
const nativeGrantFields = `g.id,g.workspace_id,g.plugin_version_id,g.enabled,g.revision,g.granted_by,g.created_at,g.updated_at,g.authority_id`

var nativePluginVersionByID = pluginNativeQuery{text: `SELECT id,tenant_id,plugin_id,release_version,abi,executor,capsule_sha256,manifest_sha256,key_id,state,revision,imported_by,created_at,installed_at,uninstalled_at,capsule,manifest FROM plugin_versions WHERE tenant_id=? AND id=?`}
var nativePluginVersionLocked = pluginNativeQuery{text: `SELECT id,tenant_id,plugin_id,release_version,abi,executor,capsule_sha256,manifest_sha256,key_id,state,revision,imported_by,created_at,installed_at,uninstalled_at,capsule,manifest FROM plugin_versions WHERE tenant_id=? AND id=? FOR UPDATE`}
var nativePluginVersionRelease = pluginNativeQuery{text: `SELECT id,tenant_id,plugin_id,release_version,abi,executor,capsule_sha256,manifest_sha256,key_id,state,revision,imported_by,created_at,installed_at,uninstalled_at,capsule,manifest FROM plugin_versions WHERE tenant_id=? AND plugin_id=? AND release_version=?`}
var nativePluginGrantScoped = pluginNativeQuery{text: `SELECT g.id,g.workspace_id,g.plugin_version_id,g.enabled,g.revision,g.granted_by,g.created_at,g.updated_at,g.authority_id FROM plugin_grants g WHERE g.tenant_id=? AND g.workspace_id=? AND g.id=?`}
var nativePluginGrantLocked = pluginNativeQuery{text: `SELECT g.id,g.workspace_id,g.plugin_version_id,g.enabled,g.revision,g.granted_by,g.created_at,g.updated_at,g.authority_id FROM plugin_grants g WHERE g.tenant_id=? AND g.workspace_id=? AND g.id=? FOR UPDATE`}
var nativePluginGrantByID = pluginNativeQuery{text: `SELECT g.id,g.workspace_id,g.plugin_version_id,g.enabled,g.revision,g.granted_by,g.created_at,g.updated_at,g.authority_id FROM plugin_grants g WHERE g.id=?`}
var nativePluginGrantVersionLocked = pluginNativeQuery{text: `SELECT g.id,g.workspace_id,g.plugin_version_id,g.enabled,g.revision,g.granted_by,g.created_at,g.updated_at,g.authority_id FROM plugin_grants g WHERE g.tenant_id=? AND g.workspace_id=? AND g.plugin_version_id=? FOR UPDATE`}
var nativePluginGrantVersionID = pluginNativeQuery{text: `SELECT plugin_version_id FROM plugin_grants WHERE tenant_id=? AND workspace_id=? AND id=?`}
var nativePluginGrantExists = pluginNativeQuery{text: `SELECT CASE WHEN EXISTS(SELECT 1 FROM plugin_grants WHERE tenant_id=? AND workspace_id=? AND plugin_version_id=?) THEN 1 ELSE 0 END FROM DUAL`}
var nativePluginGrantCount = pluginNativeQuery{text: `SELECT count(*) FROM plugin_grants WHERE tenant_id=?`}
var nativePluginVersionCount = pluginNativeQuery{text: `SELECT count(*) FROM plugin_versions WHERE tenant_id=?`}
var nativePluginGrantCreate = pluginNativeQuery{text: `INSERT INTO plugin_grants(id,tenant_id,workspace_id,plugin_version_id,authority_id,granted_by) VALUES(?,?,?,?,?,?)`}
var nativePluginGrantUpdate = pluginNativeQuery{text: `UPDATE plugin_grants SET enabled=?,revision=revision+1,authority_id=?,updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND workspace_id=? AND id=?`}
var nativePluginVersionCreate = pluginNativeQuery{text: `INSERT INTO plugin_versions(id,tenant_id,plugin_id,release_version,version_major,version_minor,version_patch,abi,executor,capsule,capsule_sha256,manifest,manifest_sha256,key_id,state,imported_by) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'imported',?)`, jsonArgument: 12}
var nativePluginVersionInstall = pluginNativeQuery{text: `UPDATE plugin_versions SET state='installed',revision=revision+1,installed_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),uninstalled_at=NULL WHERE tenant_id=? AND id=?`}
var nativePluginVersionUninstall = pluginNativeQuery{text: `UPDATE plugin_versions SET state='uninstalled',revision=revision+1,uninstalled_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND id=?`}
var nativePluginAudit = pluginNativeQuery{text: `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,?,NULLIF(?,''),?,?,'success',?,?,?,?)`}
var nativePluginIntentRead = pluginNativeQuery{text: `SELECT entity_id,purpose,request_hash FROM plugin_mutation_intents WHERE tenant_id=? AND actor_id=? AND idempotency_key=? FOR UPDATE`}
var nativePluginIntentCreate = pluginNativeQuery{text: `INSERT INTO plugin_mutation_intents(tenant_id,actor_id,idempotency_key,purpose,request_hash,entity_id) VALUES(?,?,?,?,?,?)`}
var nativePluginIntentDelete = pluginNativeQuery{text: `DELETE FROM plugin_mutation_intents WHERE tenant_id=? AND actor_id=? AND idempotency_key=?`}
var nativePluginIntentPage = pluginNativeQuery{text: `SELECT actor_id,idempotency_key FROM plugin_mutation_intents WHERE tenant_id=? AND created_at<TIMESTAMPADD(SQL_TSI_SECOND,0-?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) ORDER BY created_at,idempotency_key`, limitArgument: 3, maximum: 1000, suffix: " FOR UPDATE SKIP LOCKED"}
var nativePluginRetentionAudit = pluginNativeQuery{text: `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,'system','plugin.intent_retention','success','installation',?,'Expired bounded plugin mutation intents',?)`}
var nativePluginPublication = pluginNativeQuery{text: `INSERT INTO workflow_plugin_references(tenant_id,workspace_id,workflow_id,workflow_version_id,plugin_version_id,grant_id,publication_grant_authority) VALUES(?,?,?,?,?,?,?)`}
var nativePluginInstallation = pluginNativeQuery{text: `SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' FOR UPDATE OF i.tenant_id`}
var nativePluginInstalledTenant = pluginNativeQuery{text: `SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1`}

// Schema2 is the versioned three-node start/tool/end contract. Drafts are sorted
// by ID, so every permitted tool position is checked. A future graph family must
// extend this query and its acceptance cases together; no JSON text matching.
var nativePluginReferences = pluginNativeQuery{text: `SELECT kind,workspace_id,workflow_id,entity_id,graph_sha256 FROM (SELECT 'draft' kind,f.workspace_id,f.id workflow_id,f.id entity_id,f.draft_sha256 graph_sha256 FROM workflows f WHERE f.tenant_id=? AND ((JSON_VALUE(f.draft_graph,'$.nodes[0].kind' NULL ON EMPTY ERROR ON ERROR)='tool' AND JSON_VALUE(f.draft_graph,'$.nodes[0].configuration.plugin_version_id' NULL ON EMPTY ERROR ON ERROR)=?) OR (JSON_VALUE(f.draft_graph,'$.nodes[1].kind' NULL ON EMPTY ERROR ON ERROR)='tool' AND JSON_VALUE(f.draft_graph,'$.nodes[1].configuration.plugin_version_id' NULL ON EMPTY ERROR ON ERROR)=?) OR (JSON_VALUE(f.draft_graph,'$.nodes[2].kind' NULL ON EMPTY ERROR ON ERROR)='tool' AND JSON_VALUE(f.draft_graph,'$.nodes[2].configuration.plugin_version_id' NULL ON EMPTY ERROR ON ERROR)=?)) UNION ALL SELECT 'publication',v.workspace_id,v.workflow_id,v.id,v.graph_sha256 FROM workflow_versions v WHERE v.tenant_id=? AND ((JSON_VALUE(v.graph,'$.nodes[0].kind' NULL ON EMPTY ERROR ON ERROR)='tool' AND JSON_VALUE(v.graph,'$.nodes[0].configuration.plugin_version_id' NULL ON EMPTY ERROR ON ERROR)=?) OR (JSON_VALUE(v.graph,'$.nodes[1].kind' NULL ON EMPTY ERROR ON ERROR)='tool' AND JSON_VALUE(v.graph,'$.nodes[1].configuration.plugin_version_id' NULL ON EMPTY ERROR ON ERROR)=?) OR (JSON_VALUE(v.graph,'$.nodes[2].kind' NULL ON EMPTY ERROR ON ERROR)='tool' AND JSON_VALUE(v.graph,'$.nodes[2].configuration.plugin_version_id' NULL ON EMPTY ERROR ON ERROR)=?)) UNION ALL SELECT 'conversation',c.workspace_id,c.workflow_id,c.id,v.graph_sha256 FROM workflow_conversations c JOIN workflow_versions v ON v.tenant_id=c.tenant_id AND v.workspace_id=c.workspace_id AND v.workflow_id=c.workflow_id AND v.id=c.version_id WHERE c.tenant_id=? AND c.deleted_at IS NULL AND EXISTS(SELECT 1 FROM workflow_plugin_references r WHERE r.tenant_id=c.tenant_id AND r.workspace_id=c.workspace_id AND r.workflow_id=c.workflow_id AND r.workflow_version_id=c.version_id AND r.plugin_version_id=?) UNION ALL SELECT 'active_run',r.workspace_id,r.workflow_id,r.id,r.graph_sha256 FROM workflow_runs r WHERE r.tenant_id=? AND r.state IN ('pending','running') AND ((JSON_VALUE(r.graph_snapshot,'$.nodes[0].kind' NULL ON EMPTY ERROR ON ERROR)='tool' AND JSON_VALUE(r.graph_snapshot,'$.nodes[0].configuration.plugin_version_id' NULL ON EMPTY ERROR ON ERROR)=?) OR (JSON_VALUE(r.graph_snapshot,'$.nodes[1].kind' NULL ON EMPTY ERROR ON ERROR)='tool' AND JSON_VALUE(r.graph_snapshot,'$.nodes[1].configuration.plugin_version_id' NULL ON EMPTY ERROR ON ERROR)=?) OR (JSON_VALUE(r.graph_snapshot,'$.nodes[2].kind' NULL ON EMPTY ERROR ON ERROR)='tool' AND JSON_VALUE(r.graph_snapshot,'$.nodes[2].configuration.plugin_version_id' NULL ON EMPTY ERROR ON ERROR)=?))) refs ORDER BY kind,workspace_id,workflow_id,entity_id`, limitArgument: 9, maximum: 10001, order: []int{1, 2, 2, 2, 3, 4, 4, 4, 5, 6, 7, 8, 8, 8}}

// Called only inside the caller's locked transaction. Each grant receives its
// own UUID; after any native SQL error there is no subsequent query or replay.
func invalidateNativeGrants(ctx context.Context, q transaction.Executor, tenant, version string) error {
	var after any
	for {
		text := "SELECT id FROM plugin_grants WHERE (? IS NULL OR id>?)"
		args := []any{after, after}
		if tenant != "" {
			text += " AND tenant_id=? AND plugin_version_id=?"
			args = append(args, tenant, version)
		}
		var id string
		e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, text+" ORDER BY id LIMIT 1 FOR UPDATE", args...)).Scan(&id)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		n, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, "UPDATE plugin_grants SET enabled=0,authority_id=?,revision=revision+1,updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE id=?", uuid.NewString(), id))
		if e != nil {
			return e
		}
		if n != 1 {
			return failure("PLUGIN_REVISION_CONFLICT")
		}
		after = id
	}
}

func pluginNativePage(base, timestamp, id string, args []any, cursor pagination.Cursor, limit int) (transaction.Statement, error) {
	if cursor.ID != "" && (cursor.Time.Location() != time.UTC || cursor.Time.Year() < 1970 || cursor.Time.Year() > 9999 || cursor.Time.Nanosecond()%1000 != 0) {
		return transaction.Statement{}, failure("INVALID_CURSOR")
	}
	if limit < 1 || limit > pagination.MaximumLimit {
		return transaction.Statement{}, failure("VALIDATION_FAILED")
	}
	args = append([]any(nil), args...)
	if cursor.ID != "" {
		base += " AND (" + timestamp + "<? OR (" + timestamp + "=? AND " + id + "<?))"
		args = append(args, cursor.Time, cursor.Time, cursor.ID)
	}
	return transaction.SQL(transaction.Dameng, base+" ORDER BY "+timestamp+" DESC,"+id+" DESC LIMIT "+strconv.Itoa(limit+1), args...), nil
}

var nativePluginBootstrapReady = pluginNativeQuery{text: `SELECT CASE WHEN EXISTS(SELECT 1 FROM installation_state WHERE id=1 AND bootstrap_complete=1) THEN 1 ELSE 0 END FROM DUAL`}
var nativePluginRelationsPresent = pluginNativeQuery{text: `SELECT CASE WHEN EXISTS(SELECT 1 FROM plugin_versions) THEN 1 ELSE 0 END,CASE WHEN EXISTS(SELECT 1 FROM plugin_grants) THEN 1 ELSE 0 END,CASE WHEN EXISTS(SELECT 1 FROM plugin_mutation_intents) THEN 1 ELSE 0 END,CASE WHEN EXISTS(SELECT 1 FROM workflow_plugin_references) THEN 1 ELSE 0 END FROM DUAL`}

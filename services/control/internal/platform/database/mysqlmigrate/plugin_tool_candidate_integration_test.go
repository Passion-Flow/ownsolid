//go:build mysql_integration

package mysqlmigrate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	candidate "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/plugin-tool"
)

func pluginCandidateOrder2Probe(t *testing.T, s *mysqlstore.Store, item kwpCandidateStep, source string) {
	t.Helper()
	var name, table, timing, event, orientation, statement, modes, definer, charset, collation, databaseCollation, principal string
	var order int
	e := s.Pool().QueryRowContext(t.Context(), `SELECT trigger_name,event_object_table,action_timing,event_manipulation,action_orientation,action_order,action_statement,sql_mode,definer,character_set_client,collation_connection,database_collation,CURRENT_USER() FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name=?`, item.Object).Scan(&name, &table, &timing, &event, &orientation, &order, &statement, &modes, &definer, &charset, &collation, &databaseCollation, &principal)
	if e != nil || name != "workflow_run_result_identity" || table != "workflow_runs" || timing != "BEFORE" || event != "UPDATE" || orientation != "ROW" || order != 2 || definer != principal || kwpModeSet(modes) != kwpModeSet(mysqlstore.SessionSQLModes) || charset != mysqlstore.ConnectionCharset || collation != mysqlstore.ConnectionCollation || databaseCollation != mysqlstore.ConnectionCollation || !strings.HasSuffix(source, statement+";\n") {
		t.Fatal("actual second workflow identity guard metadata", mysqlstore.SafeError(e))
	}
	raw, _ := json.Marshal(map[string]any{"format": "projectone.mysql.schema-source-candidate-probe.v1", "activation": false, "step": item.Step, "source_sha256": item.SourceSHA256, "trigger_metadata": map[string]any{"name": name, "table": table, "timing": timing, "event": event, "orientation": orientation, "order": order, "statement": statement, "sql_modes": strings.Split(modes, ","), "definer": definer, "character_set_client": charset, "collation_connection": collation, "database_collation": databaseCollation}})
	t.Log("MYSQL_CANDIDATE_PROBE=" + string(raw))
}

func TestActualMySQLPluginToolSchemaCandidate(t *testing.T) {
	s, _, r, full := kwpFixture(t)
	d := pluginCandidateLoad(t)
	for _, item := range d.Steps {
		if item.Step == 192 {
			var count int
			e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND constraint_type='CHECK' AND ((table_name='workflow_runs' AND constraint_name IN('workflow_answer_bytes','workflow_non_success_result_absent','workflow_success_result_required')) OR (table_name='workflow_node_traces' AND constraint_name='workflow_node_traces_kind_check'))`).Scan(&count)
			if e != nil || count != 4 {
				t.Fatal("exact named input check metadata required; never guess/drop another check", mysqlstore.SafeError(e))
			}
		}
		raw, e := candidate.Files.ReadFile(item.File)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Pool().ExecContext(t.Context(), string(raw)); e != nil {
			t.Fatal("actual candidate plugin DDL failed; retain prefix and do not replay", item.Step, mysqlstore.SafeError(e))
		}
		if item.Step == 194 {
			pluginCandidateOrder2Probe(t, s, item, string(raw))
		} else {
			kwpProbeMetadata(t, s, item, string(raw))
		}
	}
	full.Steps = append(append([]kwpCandidateStep{}, full.Steps...), d.Steps...)
	plugin, grant := uuid.NewString(), uuid.NewString()
	capsule := []byte("Explicit synthetic schema capsule envelope, not a signed installed runtime capsule")
	migrationExec(t, s, `INSERT INTO plugin_versions(id,tenant_id,plugin_id,release_version,version_major,version_minor,version_patch,abi,executor,capsule,capsule_sha256,manifest,manifest_sha256,key_id,state,imported_by,installed_at) VALUES(?,?,'synthetic.schema','1.0.0',1,0,0,'synthetic.schema.v1','synthetic.schema',?,?,JSON_OBJECT(),?,?,'installed',?,UTC_TIMESTAMP(6))`, plugin, r.tenant, capsule, hash(string(capsule)), hash("{}"), hash("synthetic-schema-key-envelope"), r.user)
	migrationExec(t, s, `INSERT INTO plugin_grants(id,tenant_id,workspace_id,plugin_version_id,authority_id,granted_by) VALUES(?,?,?,?,?,?)`, grant, r.tenant, r.workspace, plugin, uuid.NewString(), r.user)
	w := kwpWorkflowSeed(t, s, r)
	migrationExec(t, s, `INSERT INTO workflow_plugin_references(tenant_id,workspace_id,workflow_id,workflow_version_id,plugin_version_id,grant_id,publication_grant_authority) SELECT ?,?,?,?,?,id,authority_id FROM plugin_grants WHERE id=?`, r.tenant, r.workspace, w.workflow, w.version, plugin, grant)
	t.Run("immutable_scoped_plugin_and_publication_reference", func(t *testing.T) {
		for _, query := range []string{`UPDATE plugin_versions SET capsule=X'01' WHERE id=?`, `UPDATE plugin_versions SET manifest=JSON_OBJECT('changed',1) WHERE id=?`, `DELETE FROM plugin_versions WHERE id=?`} {
			kwpRefuse(t, s, full, 1644, query, plugin)
		}
		kwpRefuse(t, s, full, 1644, `UPDATE workflow_plugin_references SET publication_grant_authority=? WHERE workflow_version_id=?`, uuid.NewString(), w.version)
		kwpRefuse(t, s, full, 1644, `DELETE FROM workflow_plugin_references WHERE workflow_version_id=?`, w.version)
		kwpRefuse(t, s, full, 1452, `INSERT INTO plugin_grants(id,tenant_id,workspace_id,plugin_version_id,authority_id,granted_by) VALUES(?,?,?,?,?,?)`, uuid.NewString(), uuid.NewString(), r.workspace, plugin, uuid.NewString(), r.user)
		other := kwpWorkflowSeed(t, s, r)
		kwpRefuse(t, s, full, 1452, `INSERT INTO workflow_plugin_references(tenant_id,workspace_id,workflow_id,workflow_version_id,plugin_version_id,grant_id,publication_grant_authority) VALUES(?,?,?,?,?,?,?)`, r.tenant, r.workspace, w.workflow, other.version, plugin, grant, uuid.NewString())
		kwpRefuse(t, s, full, 3819, `INSERT INTO plugin_versions(id,tenant_id,plugin_id,release_version,version_major,version_minor,version_patch,abi,executor,capsule,capsule_sha256,manifest,manifest_sha256,key_id,state,imported_by) VALUES(?,?,'synthetic.bad.version','1.0.1',1,0,0,'synthetic','synthetic',X'01',?,JSON_OBJECT(),?,?,'imported',?)`, uuid.NewString(), r.tenant, hash("bad-version"), hash("{}"), hash("synthetic-key"), r.user)
	})
	t.Run("tool_result_shape_identity_trace_and_atomic_notice", func(t *testing.T) {
		id := uuid.NewString()
		migrationExec(t, s, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at,result_kind) VALUES(?,?,?,?,?,1,?,?,'admin',JSON_OBJECT('schema_version',2),?,'pending','Synthetic schema tool question',TIMESTAMPADD(MINUTE,5,UTC_TIMESTAMP(6)),'model_list')`, id, r.tenant, r.workspace, w.workflow, r.user, uuid.NewString(), uuid.NewString(), hash(`{"schema_version":2}`))
		kwpRefuse(t, s, full, 1644, `UPDATE workflow_runs SET result_kind='answer' WHERE id=?`, id)
		kwpRefuse(t, s, full, 3819, `UPDATE workflow_runs SET tool_result=JSON_OBJECT() WHERE id=?`, id)
		kwpRefuse(t, s, full, 3819, `UPDATE workflow_runs SET state='succeeded',tool_result=JSON_OBJECT(),finished_at=UTC_TIMESTAMP(6) WHERE id=?`, id)
		migrationExec(t, s, `INSERT INTO workflow_node_traces(tenant_id,workspace_id,workflow_id,run_id,node_id,kind,state) VALUES(?,?,?,?,?,'tool','not_started')`, r.tenant, r.workspace, w.workflow, id, uuid.NewString())
		// This tests DDL shape only. Real signed completion proof and model directory
		// contents are independently verified by the plugin/workflow runtime gates.
		migrationExec(t, s, `UPDATE workflow_runs SET state='succeeded',tool_result=JSON_OBJECT('synthetic_schema',true),tool_proof=JSON_OBJECT('synthetic_schema',true),finished_at=UTC_TIMESTAMP(6) WHERE id=?`, id)
		var exact bool
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT answer IS NULL AND evidence IS NULL AND alias_snapshot IS NULL AND `usage` IS NULL AND tool_result IS NOT NULL AND tool_proof IS NOT NULL FROM workflow_runs WHERE id=?", id).Scan(&exact); e != nil || !exact {
			t.Fatal("tool shape manufactured answer/evidence/usage", mysqlstore.SafeError(e))
		}
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM terminal_notifications WHERE run_id=? AND recipient_id=? AND outcome='succeeded' AND error_code IS NULL`, id, r.user).Scan(&count); e != nil || count != 1 {
			t.Fatal("actual tool terminal notice missing/duplicated", mysqlstore.SafeError(e))
		}
		kwpRefuse(t, s, full, 3819, `UPDATE workflow_runs SET answer='forged answer' WHERE id=?`, id)
	})
	t.Log("MYSQL_CANDIDATE_SYNTHETIC_ROWS_SHA256=" + kwpDigest(t, s, full))
}

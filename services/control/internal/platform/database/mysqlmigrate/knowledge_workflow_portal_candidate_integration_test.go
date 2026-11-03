//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	candidate "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/knowledge-workflow-portal"
)

// The fixture uses the actual immutable149 catalog and every candidate SQL file.
// It deliberately does not journal or activate an unmeasured schema candidate.
// All rows are synthetic schema envelopes, never encoder/model qualification.
type kwpRows struct{ tenant, workspace, user, otherUser string }
type kwpKnowledge struct{ base, generation, document, source, attempt string }
type kwpWorkflow struct{ workflow, version, conversation string }

func kwpSeedUser(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	id := uuid.NewString()
	email := id + "@example.invalid"
	migrationExec(t, s, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Synthetic candidate schema owner','en','active',0)`, id, email, email)
	return id
}

func kwpModeSet(raw string) string {
	parts := strings.Split(raw, ",")
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

func kwpProbeMetadata(t *testing.T, s *mysqlstore.Store, item kwpCandidateStep, source string) {
	t.Helper()
	ctx := t.Context()
	record := map[string]any{"format": "projectone.mysql.schema-source-candidate-probe.v1", "activation": false, "step": item.Step, "object": item.Object, "kind": item.Kind, "source_sha256": item.SourceSHA256}
	var version, principal, modes, zone, charset, collation, databaseCollation string
	e := s.Pool().QueryRowContext(ctx, `SELECT VERSION(),CURRENT_USER(),@@SESSION.sql_mode,@@SESSION.time_zone,@@SESSION.character_set_client,@@SESSION.collation_connection,@@collation_database`).Scan(&version, &principal, &modes, &zone, &charset, &collation, &databaseCollation)
	if e != nil || !strings.HasPrefix(version, "8.4.") || zone != "+00:00" || !authenticatedDefiner.MatchString(principal) || kwpModeSet(modes) != kwpModeSet(mysqlstore.SessionSQLModes) || charset != mysqlstore.ConnectionCharset || collation != mysqlstore.ConnectionCollation || databaseCollation != mysqlstore.ConnectionCollation {
		t.Fatal("actual candidate metadata context", mysqlstore.SafeError(e))
	}
	record["server_version"] = version
	record["authenticated_definer"] = principal
	record["session_sql_modes"] = modes
	record["session_time_zone"] = zone
	record["character_set_client"] = charset
	record["collation_connection"] = collation
	record["database_collation"] = databaseCollation
	switch item.Kind {
	case "create_table", "alter_table":
		var name, definition string
		if e = s.Pool().QueryRowContext(ctx, "SHOW CREATE TABLE `"+item.Object+"`").Scan(&name, &definition); e != nil || name != item.Object || len(definition) > maximumStatementBytes {
			t.Fatal("actual candidate SHOW CREATE TABLE", item.Step, mysqlstore.SafeError(e))
		}
		record["show_create_table"] = definition
		record["canonical_table_sha256"] = hash(canonicalDefinition(definition))
	case "create_trigger":
		var name, table, timing, event, orientation, statement, definer, triggerModes, triggerCharset, triggerCollation, triggerDatabaseCollation string
		var order int
		e = s.Pool().QueryRowContext(ctx, `SELECT trigger_name,event_object_table,action_timing,event_manipulation,action_orientation,action_order,action_statement,sql_mode,definer,character_set_client,collation_connection,database_collation FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name=?`, item.Object).Scan(&name, &table, &timing, &event, &orientation, &order, &statement, &triggerModes, &definer, &triggerCharset, &triggerCollation, &triggerDatabaseCollation)
		if e != nil || name != item.Object || definer != principal || kwpModeSet(triggerModes) != kwpModeSet(modes) || orientation != "ROW" || order != 1 || triggerCharset != charset || triggerCollation != collation || triggerDatabaseCollation != databaseCollation || !strings.HasPrefix(source, "CREATE TRIGGER "+name+" "+timing+" "+event+" ON "+table+" FOR EACH ROW\n") || !strings.HasSuffix(source, statement+";\n") {
			t.Fatal("actual candidate trigger context/source/order", item.Step, mysqlstore.SafeError(e))
		}
		record["trigger_metadata"] = map[string]any{"name": name, "table": table, "timing": timing, "event": event, "orientation": orientation, "order": order, "statement": statement, "sql_modes": strings.Split(triggerModes, ","), "definer": definer, "character_set_client": triggerCharset, "collation_connection": triggerCollation, "database_collation": triggerDatabaseCollation}
	case "seed":
		var count int
		if e = s.Pool().QueryRowContext(ctx, `SELECT COUNT(*) FROM users u LEFT JOIN user_preferences p ON p.user_id=u.id WHERE p.user_id IS NULL`).Scan(&count); e != nil || count != 0 {
			t.Fatal("actual existing-user seed", mysqlstore.SafeError(e))
		}
		record["missing_preferences"] = count
	default:
		t.Fatal("unowned candidate metadata kind")
	}
	raw, e := json.Marshal(record)
	if e != nil {
		t.Fatal(e)
	}
	t.Log("MYSQL_CANDIDATE_PROBE=" + string(raw))
}

func kwpFixture(t *testing.T) (*mysqlstore.Store, Catalog, kwpRows, kwpCandidateDocument) {
	t.Helper()
	s, c, _ := actualMigration(t)
	c = candidatePrefix(c, 149)
	if c.Steps() != 149 {
		t.Fatal("candidate fixture requires exact reviewed149 prefix")
	}
	migrationRun(t, s, c, Apply)
	r := kwpRows{tenant: uuid.NewString(), workspace: uuid.NewString()}
	migrationExec(t, s, `INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic candidate tenant','active')`, r.tenant)
	migrationExec(t, s, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic candidate Workspace','active')`, r.workspace, r.tenant)
	r.user = kwpSeedUser(t, s)
	r.otherUser = kwpSeedUser(t, s)
	d, e := kwpCandidateLoad(candidate.Files)
	if e != nil {
		t.Fatal(e)
	}
	before := kwpJournalDigest(t, s)
	for _, item := range d.Steps {
		raw, e := candidate.Files.ReadFile(item.File)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Pool().ExecContext(t.Context(), string(raw)); e != nil {
			t.Fatal("candidate actual ordered DDL failed; retained prefix must not be auto-replayed", item.Step, mysqlstore.SafeError(e))
		}
		kwpProbeMetadata(t, s, item, string(raw))
	}
	if before != kwpJournalDigest(t, s) {
		t.Fatal("source-only candidate altered active migration journal")
	}
	return s, c, r, d
}
func kwpJournalDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	return trainingSchemaColumnDigest(t, s, "schema_migrations", holdoutSchemaDigestColumns(t, s, "schema_migrations"))
}
func kwpDigest(t *testing.T, s *mysqlstore.Store, d kwpCandidateDocument) string {
	t.Helper()
	out := kwpJournalDigest(t, s)
	for _, item := range d.Steps {
		if item.Kind == "create_table" {
			out += trainingSchemaColumnDigest(t, s, item.Object, holdoutSchemaDigestColumns(t, s, item.Object))
		}
	}
	return hash(out)
}
func kwpRefuse(t *testing.T, s *mysqlstore.Store, d kwpCandidateDocument, number uint16, query string, args ...any) {
	t.Helper()
	before := kwpDigest(t, s, d)
	_, e := s.Pool().ExecContext(t.Context(), query, args...)
	var safe *mysqlstore.Error
	if e == nil || !errors.As(mysqlstore.SafeError(e), &safe) || safe.Number == 0 || number != 0 && safe.Number != number {
		t.Fatalf("candidate refusal category expected=%d actual=%v", number, mysqlstore.SafeError(e))
	}
	if kwpDigest(t, s, d) != before {
		t.Fatal("refused candidate statement changed complete candidate graph or149 journal")
	}
}

type kwpExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func kwpCreateBase(t *testing.T, q kwpExecutor, r kwpRows) kwpKnowledge {
	t.Helper()
	k := kwpKnowledge{base: uuid.NewString(), generation: uuid.NewString(), document: uuid.NewString(), source: uuid.NewString(), attempt: uuid.NewString()}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO knowledge_bases(id,tenant_id,workspace_id,name,name_key,created_by,creation_key,creation_fingerprint) VALUES(?,?,?,? ,?,?,?,?)`, []any{k.base, r.tenant, r.workspace, "Synthetic base \u4e16\u754c", k.base, r.user, uuid.NewString(), []byte(strings.Repeat("b", 32))}},
		{`INSERT INTO knowledge_generations(id,tenant_id,workspace_id,base_id,catalog_sha256,dimension,chunk_tokens,overlap_tokens,state,requested_by,requested_audience,requested_epoch,expected_documents,activated_at) VALUES(?,?,?,?,?,384,32,8,'active',?,'admin',1,0,UTC_TIMESTAMP(6))`, []any{k.generation, r.tenant, r.workspace, k.base, hash("synthetic-encoder-envelope"), r.user}},
		{`UPDATE knowledge_bases SET active_generation_id=?,initialization_state='ready' WHERE id=?`, []any{k.generation, k.base}},
	} {
		if _, e := q.ExecContext(t.Context(), statement.sql, statement.args...); e != nil {
			t.Fatal("actual cyclic initialization fixture", mysqlstore.SafeError(e))
		}
	}
	return k
}
func kwpKnowledgeSeed(t *testing.T, s *mysqlstore.Store, r kwpRows) kwpKnowledge {
	t.Helper()
	tx, e := s.Pool().BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer tx.Rollback()
	k := kwpCreateBase(t, tx, r)
	if e = tx.Commit(); e != nil {
		t.Fatal("fixture initialization commit unknown; do not replay", mysqlstore.SafeError(e))
	}
	migrationExec(t, s, `INSERT INTO knowledge_documents(id,tenant_id,workspace_id,base_id,name,name_key,state,created_by) VALUES(?,?,?,?,'Synthetic UTF-8 source',?,'pending',?)`, k.document, r.tenant, r.workspace, k.base, k.document, r.user)
	content := []byte("Synthetic knowledge \u4e16\u754c🙂\nsecond line")
	migrationExec(t, s, `INSERT INTO knowledge_source_versions(id,tenant_id,workspace_id,base_id,document_id,source_version,content,sha256,created_by) VALUES(?,?,?,?,?,1,?,?,?)`, k.source, r.tenant, r.workspace, k.base, k.document, content, hash(string(content)), r.user)
	migrationExec(t, s, `INSERT INTO knowledge_attempts(id,tenant_id,workspace_id,base_id,document_id,source_id,generation_id,kind,attempt_number,expected_document_revision,requested_by,requested_audience,requested_epoch,state) VALUES(?,?,?,?,?,?,?,'ingest',1,1,?,'admin',1,'pending')`, k.attempt, r.tenant, r.workspace, k.base, k.document, k.source, k.generation, r.user)
	migrationExec(t, s, `UPDATE knowledge_documents SET pending_source_id=?,current_attempt_id=? WHERE id=?`, k.source, k.attempt, k.document)
	return k
}
func kwpVector(value float64) string {
	v := make([]float64, 384)
	v[0] = value
	raw, _ := json.Marshal(v)
	return string(raw)
}

const kwpChunkInsert = `INSERT INTO knowledge_chunks(tenant_id,workspace_id,base_id,document_id,generation_id,source_id,attempt_id,ordinal,start_byte,end_byte,token_count,embedding) VALUES(?,?,?,?,?,?,?,0,0,9,3,CAST(? AS JSON))`

func kwpChunkArguments(r kwpRows, k kwpKnowledge, vector string) []any {
	return []any{r.tenant, r.workspace, k.base, k.document, k.generation, k.source, k.attempt, vector}
}

func kwpWorkflowSeed(t *testing.T, s *mysqlstore.Store, r kwpRows) kwpWorkflow {
	t.Helper()
	w := kwpWorkflow{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	migrationExec(t, s, `INSERT INTO workflows(id,tenant_id,workspace_id,name,name_key,execution_authority,draft_graph,draft_sha256,created_by) VALUES(?,?,?,'Synthetic workflow',?,?,JSON_OBJECT(),?,?)`, w.workflow, r.tenant, r.workspace, w.workflow, uuid.NewString(), hash("{}"), r.user)
	migrationExec(t, s, `INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) VALUES(?,?,?,?,1,1,JSON_OBJECT(),?,?)`, w.version, r.tenant, r.workspace, w.workflow, hash("{}"), r.user)
	migrationExec(t, s, `UPDATE workflows SET current_version_id=? WHERE id=?`, w.version, w.workflow)
	migrationExec(t, s, `INSERT INTO workflow_conversations(id,tenant_id,workspace_id,workflow_id,version_id,owner_id) VALUES(?,?,?,?,?,?)`, w.conversation, r.tenant, r.workspace, w.workflow, w.version, r.user)
	return w
}
func kwpPendingRun(t *testing.T, s *mysqlstore.Store, r kwpRows, w kwpWorkflow) string {
	t.Helper()
	id := uuid.NewString()
	migrationExec(t, s, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at) VALUES(?,?,?,?,?,1,?,?,'admin',JSON_OBJECT(),?,'pending','Synthetic question',TIMESTAMPADD(MINUTE,5,UTC_TIMESTAMP(6)))`, id, r.tenant, r.workspace, w.workflow, r.user, uuid.NewString(), uuid.NewString(), hash("{}"))
	return id
}
func kwpFinishRun(t *testing.T, q kwpExecutor, id, state string) {
	t.Helper()
	var answer, errorCode any
	if state == "succeeded" {
		answer = "Synthetic schema answer"
	} else {
		errorCode = "SYNTHETIC_" + strings.ToUpper(state)
	}
	if _, e := q.ExecContext(t.Context(), "UPDATE workflow_runs SET state=?,answer=?,evidence=JSON_OBJECT(),alias_snapshot=JSON_OBJECT(),`usage`=JSON_OBJECT(),error_code=?,process_owner=NULL,lease_id=NULL,lease_expires_at=NULL,finished_at=UTC_TIMESTAMP(6) WHERE id=?", state, answer, errorCode, id); e != nil {
		t.Fatal("actual terminal transition and immediate provenance", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLKnowledgeWorkflowPortalCandidate(t *testing.T) {
	s, c, r, d := kwpFixture(t)
	t.Run("candidate_objects_remain_unjournaled", func(t *testing.T) {
		before := kwpDigest(t, s, d)
		report, e := Run(t.Context(), s, c, testOptions(), Readiness, nil)
		if e == nil || e.Error() != CodeUnjournaledObject || report.Ready || !report.LockReleased || before != kwpDigest(t, s, d) {
			t.Fatal("unactivated candidate was treated as migration readiness", report, e)
		}
	})
	t.Run("initialization_ready_guard_rollback_and_both_scoped_references", func(t *testing.T) {
		k := kwpKnowledgeSeed(t, s, r)
		other := kwpKnowledgeSeed(t, s, r)
		kwpRefuse(t, s, d, 1644, `UPDATE knowledge_bases SET initialization_state='initializing',active_generation_id=NULL WHERE id=?`, k.base)
		kwpRefuse(t, s, d, 3819, `UPDATE knowledge_bases SET active_generation_id=NULL WHERE id=?`, k.base)
		kwpRefuse(t, s, d, 1452, `UPDATE knowledge_bases SET active_generation_id=? WHERE id=?`, other.generation, k.base)
		kwpRefuse(t, s, d, 1452, `UPDATE knowledge_bases SET building_generation_id=? WHERE id=?`, other.generation, k.base)
		for _, foreignTenant := range []bool{false, true} {
			foreign := r
			foreign.workspace = uuid.NewString()
			if foreignTenant {
				foreign.tenant = uuid.NewString()
				migrationExec(t, s, `INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic foreign scope','active')`, foreign.tenant)
			}
			migrationExec(t, s, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic foreign Workspace','active')`, foreign.workspace, foreign.tenant)
			scoped := kwpKnowledgeSeed(t, s, foreign)
			kwpRefuse(t, s, d, 1452, `UPDATE knowledge_bases SET active_generation_id=? WHERE id=?`, scoped.generation, k.base)
			kwpRefuse(t, s, d, 1452, `UPDATE knowledge_documents SET visible_source_id=? WHERE id=?`, scoped.source, k.document)
			kwpRefuse(t, s, d, 1452, `UPDATE knowledge_documents SET current_attempt_id=? WHERE id=?`, scoped.attempt, k.document)
			args := kwpChunkArguments(r, k, kwpVector(1))
			args[4] = scoped.generation
			kwpRefuse(t, s, d, 1452, kwpChunkInsert, args...)
		}

		before := kwpDigest(t, s, d)
		tx, e := s.Pool().BeginTx(t.Context(), nil)
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		defer tx.Rollback()
		rolled := kwpCreateBase(t, tx, r)
		if e = tx.Rollback(); e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		if before != kwpDigest(t, s, d) {
			t.Fatal("initializing/generation/ready rollback leaked graph")
		}
		var count int
		if e = s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM knowledge_bases WHERE id=?`, rolled.base).Scan(&count); e != nil || count != 0 {
			t.Fatal("rolled initialization retained", mysqlstore.SafeError(e))
		}
	})
	t.Run("knowledge_scope_sources_retirement_and_partial_keys", func(t *testing.T) {
		k := kwpKnowledgeSeed(t, s, r)
		other := kwpKnowledgeSeed(t, s, r)
		kwpRefuse(t, s, d, 1452, `UPDATE knowledge_documents SET visible_source_id=? WHERE id=?`, other.source, k.document)
		kwpRefuse(t, s, d, 1452, `UPDATE knowledge_documents SET current_attempt_id=? WHERE id=?`, other.attempt, k.document)
		kwpRefuse(t, s, d, 1644, `UPDATE knowledge_generations SET catalog_sha256=? WHERE id=?`, hash("changed"), k.generation)
		kwpRefuse(t, s, d, 1644, `UPDATE knowledge_attempts SET source_id=? WHERE id=?`, other.source, k.attempt)
		for _, content := range [][]byte{nil, {0}, {0xff}, []byte("digest-mismatch")} {
			digest := hash(string(content))
			if string(content) == "digest-mismatch" {
				digest = hash("wrong")
			}
			kwpRefuse(t, s, d, 0, `INSERT INTO knowledge_source_versions(id,tenant_id,workspace_id,base_id,document_id,source_version,content,sha256,created_by) VALUES(?,?,?,?,?,2,?,?,?)`, uuid.NewString(), r.tenant, r.workspace, k.base, k.document, content, digest, r.user)
		}

		var content []byte
		var size int
		var checksum string
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT content,size_bytes,sha256 FROM knowledge_source_versions WHERE id=?`, k.source).Scan(&content, &size, &checksum); e != nil || string(content) != "Synthetic knowledge \u4e16\u754c🙂\nsecond line" || size != len(content) || checksum != hash(string(content)) {
			t.Fatal("exact UTF-8 BLOB/size/hash readback", mysqlstore.SafeError(e))
		}
		kwpRefuse(t, s, d, 3819, `UPDATE knowledge_attempts SET state='running' WHERE id=?`, k.attempt)
		kwpRefuse(t, s, d, 3819, `UPDATE knowledge_attempts SET state='failed',finished_at=UTC_TIMESTAMP(6) WHERE id=?`, k.attempt)
		kwpRefuse(t, s, d, 1644, `UPDATE knowledge_source_versions SET content='forbidden' WHERE id=?`, k.source)
		migrationExec(t, s, `UPDATE knowledge_source_versions SET superseded_at=UTC_TIMESTAMP(6) WHERE id=?`, k.source)
		kwpRefuse(t, s, d, 1644, `UPDATE knowledge_source_versions SET superseded_at=TIMESTAMPADD(SECOND,1,superseded_at) WHERE id=?`, k.source)
		kwpRefuse(t, s, d, 1062, `INSERT INTO knowledge_documents(id,tenant_id,workspace_id,base_id,name,name_key,state,created_by) VALUES(?,?,?,?,'duplicate',?,'pending',?)`, uuid.NewString(), r.tenant, r.workspace, k.base, k.document, r.user)
		migrationExec(t, s, `UPDATE knowledge_documents SET state='deleted',deleted_at=UTC_TIMESTAMP(6),delete_reason='Synthetic deletion' WHERE id=?`, k.document)
		migrationExec(t, s, `INSERT INTO knowledge_documents(id,tenant_id,workspace_id,base_id,name,name_key,state,created_by) VALUES(?,?,?,?,'reused name',?,'pending',?)`, uuid.NewString(), r.tenant, r.workspace, k.base, k.document, r.user)
		kwpRefuse(t, s, d, 1062, `INSERT INTO knowledge_generations(id,tenant_id,workspace_id,base_id,catalog_sha256,dimension,chunk_tokens,overlap_tokens,state,requested_by,requested_audience,requested_epoch,expected_documents,activated_at) SELECT ?,tenant_id,workspace_id,base_id,catalog_sha256,dimension,chunk_tokens,overlap_tokens,'active',requested_by,requested_audience,requested_epoch,0,UTC_TIMESTAMP(6) FROM knowledge_generations WHERE id=?`, uuid.NewString(), k.generation)
		kwpRefuse(t, s, d, 1062, `INSERT INTO knowledge_attempts(id,tenant_id,workspace_id,base_id,document_id,source_id,generation_id,kind,attempt_number,expected_document_revision,requested_by,requested_audience,requested_epoch,state) SELECT ?,tenant_id,workspace_id,base_id,document_id,source_id,generation_id,kind,2,1,requested_by,requested_audience,requested_epoch,'pending' FROM knowledge_attempts WHERE id=?`, uuid.NewString(), k.attempt)
		migrationExec(t, s, `UPDATE knowledge_attempts SET state='ready',finished_at=UTC_TIMESTAMP(6) WHERE id=?`, k.attempt)
		kwpRefuse(t, s, d, 1062, `INSERT INTO knowledge_attempts(id,tenant_id,workspace_id,base_id,document_id,source_id,generation_id,kind,attempt_number,expected_document_revision,requested_by,requested_audience,requested_epoch,state,finished_at) SELECT ?,tenant_id,workspace_id,base_id,document_id,source_id,generation_id,kind,2,1,requested_by,requested_audience,requested_epoch,'ready',UTC_TIMESTAMP(6) FROM knowledge_attempts WHERE id=?`, uuid.NewString(), k.attempt)
		building := uuid.NewString()
		buildingInsert := `INSERT INTO knowledge_generations(id,tenant_id,workspace_id,base_id,catalog_sha256,dimension,chunk_tokens,overlap_tokens,state,requested_by,requested_audience,requested_epoch,expected_documents) SELECT ?,tenant_id,workspace_id,base_id,catalog_sha256,dimension,chunk_tokens,overlap_tokens,'building',requested_by,requested_audience,requested_epoch,0 FROM knowledge_generations WHERE id=?`
		migrationExec(t, s, buildingInsert, building, k.generation)
		migrationExec(t, s, `UPDATE knowledge_bases SET building_generation_id=? WHERE id=?`, building, k.base)
		kwpRefuse(t, s, d, 1062, buildingInsert, uuid.NewString(), k.generation)

	})
	t.Run("json_embedding_dimensions_types_finite_norm_and_immutable", func(t *testing.T) {
		k := kwpKnowledgeSeed(t, s, r)
		normalized := kwpVector(1)
		for _, vector := range []string{`[]`, `null`, `{}`, `[1]`, strings.TrimSuffix(normalized, ",0]") + "]", strings.TrimSuffix(normalized, "]") + ",0]", strings.Replace(normalized, "1,", "true,", 1), strings.Replace(normalized, "1,", "null,", 1), strings.Replace(normalized, "1,", "\"NaN\",", 1), strings.Replace(normalized, "1,", "1e999,", 1), kwpVector(0), kwpVector(0.5), kwpVector(1.002)} {
			kwpRefuse(t, s, d, 0, kwpChunkInsert, kwpChunkArguments(r, k, vector)...)
		}
		args := kwpChunkArguments(r, k, normalized)
		args[4] = uuid.NewString()
		kwpRefuse(t, s, d, 1452, kwpChunkInsert, args...)
		migrationExec(t, s, kwpChunkInsert, kwpChunkArguments(r, k, normalized)...)
		kwpRefuse(t, s, d, 1644, `UPDATE knowledge_chunks SET token_count=4 WHERE attempt_id=?`, k.attempt)
		var length int
		var first float64
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT JSON_LENGTH(embedding),CAST(JSON_UNQUOTE(JSON_EXTRACT(embedding,'$[0]')) AS DOUBLE) FROM knowledge_chunks WHERE attempt_id=?`, k.attempt).Scan(&length, &first); e != nil || length != 384 || first != 1 {
			t.Fatal("normalized actual JSON readback", mysqlstore.SafeError(e))
		}
	})
	t.Run("workflow_pinned_owner_version_identity_trace_and_cascade", func(t *testing.T) {
		w := kwpWorkflowSeed(t, s, r)
		other := kwpWorkflowSeed(t, s, r)
		kwpRefuse(t, s, d, 1452, `UPDATE workflows SET current_version_id=? WHERE id=?`, other.version, w.workflow)
		kwpRefuse(t, s, d, 1644, `UPDATE workflow_versions SET graph=JSON_OBJECT('changed',1) WHERE id=?`, w.version)
		kwpRefuse(t, s, d, 1644, `DELETE FROM workflow_versions WHERE id=?`, w.version)
		insert := `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,conversation_id,turn_number,version_id,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at) VALUES(?,?,?,?,?,?,1,?,?,?,'portal',JSON_OBJECT(),?,'pending','Synthetic pinned question',TIMESTAMPADD(MINUTE,5,UTC_TIMESTAMP(6)))`
		args := []any{uuid.NewString(), r.tenant, r.workspace, w.workflow, r.otherUser, w.conversation, w.version, uuid.NewString(), uuid.NewString(), hash("{}")}
		kwpRefuse(t, s, d, 1452, insert, args...)
		id := uuid.NewString()
		args[0] = id
		args[4] = r.user
		migrationExec(t, s, insert, args...)
		args[0] = uuid.NewString()
		kwpRefuse(t, s, d, 1062, strings.Replace(insert, ",1,", ",2,", 1), args...)
		kwpRefuse(t, s, d, 1644, `UPDATE workflow_runs SET question='forged question' WHERE id=?`, id)
		kwpRefuse(t, s, d, 3819, `UPDATE workflow_runs SET state='running' WHERE id=?`, id)
		trace := uuid.NewString()
		migrationExec(t, s, `INSERT INTO workflow_node_traces(tenant_id,workspace_id,workflow_id,run_id,node_id,kind,state) VALUES(?,?,?,?,?,'start','not_started')`, r.tenant, r.workspace, w.workflow, id, trace)
		kwpRefuse(t, s, d, 3819, `UPDATE workflow_node_traces SET state='running' WHERE run_id=?`, id)
		kwpRefuse(t, s, d, 1062, `INSERT INTO workflow_node_traces(tenant_id,workspace_id,workflow_id,run_id,node_id,kind,state) VALUES(?,?,?,?,?,'start','not_started')`, r.tenant, r.workspace, w.workflow, id, uuid.NewString())
		kwpFinishRun(t, s.Pool(), id, "cancelled")
		migrationExec(t, s, `DELETE FROM workflow_runs WHERE id=?`, id)
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM workflow_node_traces WHERE run_id=?)+(SELECT COUNT(*) FROM terminal_notifications WHERE run_id=?)`, id, id).Scan(&count); e != nil || count != 0 {
			t.Fatal("trace/notice full run-owner cascade", mysqlstore.SafeError(e))
		}
	})
	t.Run("preference_existing_seed_new_user_revision_and_owner", func(t *testing.T) {
		for _, user := range []string{r.user, r.otherUser, kwpSeedUser(t, s)} {
			var theme string
			var revision int
			if e := s.Pool().QueryRowContext(t.Context(), `SELECT theme,revision FROM user_preferences WHERE user_id=?`, user).Scan(&theme, &revision); e != nil || theme != "light" || revision != 1 {
				t.Fatal("real seed/new-user trigger", mysqlstore.SafeError(e))
			}
		}
		kwpRefuse(t, s, d, 1644, `UPDATE user_preferences SET theme='dark' WHERE user_id=?`, r.user)
		kwpRefuse(t, s, d, 1644, `UPDATE user_preferences SET user_id=?,revision=revision+1 WHERE user_id=?`, uuid.NewString(), r.user)
		kwpRefuse(t, s, d, 3819, `UPDATE user_preferences SET theme='invalid',revision=revision+1 WHERE user_id=?`, r.user)
		migrationExec(t, s, `UPDATE user_preferences SET theme='dark',revision=revision+1 WHERE user_id=?`, r.user)
		disposable := kwpSeedUser(t, s)
		migrationExec(t, s, `DELETE FROM users WHERE id=?`, disposable)
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM user_preferences WHERE user_id=?`, disposable).Scan(&count); e != nil || count != 0 {
			t.Fatal("preference user cascade", mysqlstore.SafeError(e))
		}
	})
	t.Run("all_terminal_notifications_once_read_provenance_and_atomic_rollback", func(t *testing.T) {
		w := kwpWorkflowSeed(t, s, r)
		for _, running := range []bool{false, true} {
			for _, state := range []string{"succeeded", "failed", "cancelled", "interrupted"} {
				id := kwpPendingRun(t, s, r, w)
				if running {
					migrationExec(t, s, `UPDATE workflow_runs SET state='running',process_owner=?,lease_id=?,lease_expires_at=TIMESTAMPADD(MINUTE,1,UTC_TIMESTAMP(6)),started_at=UTC_TIMESTAMP(6) WHERE id=?`, uuid.NewString(), uuid.NewString(), id)
				}
				kwpFinishRun(t, s.Pool(), id, state)
				var notice, outcome, owner string
				var occurred time.Time
				if e := s.Pool().QueryRowContext(t.Context(), `SELECT id,outcome,recipient_id,occurred_at FROM terminal_notifications WHERE run_id=?`, id).Scan(&notice, &outcome, &owner, &occurred); e != nil || outcome != state || owner != r.user || occurred.Location() != time.UTC {
					t.Fatal("exact terminal notification readback", state, mysqlstore.SafeError(e))
				}
				migrationExec(t, s, `UPDATE workflow_runs SET state=state WHERE id=?`, id)
				var count int
				if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM terminal_notifications WHERE run_id=?`, id).Scan(&count); e != nil || count != 1 {
					t.Fatal("terminal replay created another notice", mysqlstore.SafeError(e))
				}
				kwpRefuse(t, s, d, 1644, `UPDATE terminal_notifications SET recipient_id=? WHERE id=?`, r.otherUser, notice)
				kwpRefuse(t, s, d, 1644, `UPDATE terminal_notifications SET occurred_at=TIMESTAMPADD(SECOND,1,occurred_at) WHERE id=?`, notice)
				kwpRefuse(t, s, d, 3819, `UPDATE terminal_notifications SET read_at=TIMESTAMPADD(SECOND,-1,occurred_at) WHERE id=?`, notice)
				migrationExec(t, s, `UPDATE terminal_notifications SET read_at=TIMESTAMPADD(SECOND,1,occurred_at) WHERE id=?`, notice)
				kwpRefuse(t, s, d, 1644, `UPDATE terminal_notifications SET read_at=NULL WHERE id=?`, notice)
				kwpRefuse(t, s, d, 1644, `UPDATE terminal_notifications SET read_at=TIMESTAMPADD(SECOND,1,read_at) WHERE id=?`, notice)
			}
		}
		pending := kwpPendingRun(t, s, r, w)
		kwpRefuse(t, s, d, 1644, `INSERT INTO terminal_notifications(tenant_id,workspace_id,workflow_id,run_id,recipient_id,outcome,error_code) VALUES(?,?,?,?,?,'failed','SYNTHETIC_FAILED')`, r.tenant, r.workspace, w.workflow, pending, r.user)
		before := kwpDigest(t, s, d)
		tx, e := s.Pool().BeginTx(t.Context(), nil)
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		defer tx.Rollback()
		kwpFinishRun(t, tx, pending, "failed")
		_, e = tx.ExecContext(t.Context(), `INSERT INTO audit_events(id,occurred_at,actor_user_id,audience,action,outcome,request_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,'operator','candidate.synthetic_failure','accepted',?,CAST('[]' AS JSON))`, uuid.NewString(), r.user, uuid.NewString())
		var auditError *mysqlstore.Error
		if e == nil || !errors.As(mysqlstore.SafeError(e), &auditError) || auditError.Number != 3819 {
			t.Fatal("intentional invalid audit JSON did not produce CHECK refusal", mysqlstore.SafeError(e))
		}
		if e = tx.Rollback(); e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		if before != kwpDigest(t, s, d) {
			t.Fatal("failed audit did not roll terminal run and notice back together")
		}
	})
	t.Log(fmt.Sprintf("MYSQL_CANDIDATE_SYNTHETIC_ROWS_SHA256=%s", kwpDigest(t, s, d)))
}

//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
	baseCandidate "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/knowledge-workflow-portal"
	toolCandidate "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/plugin-tool"
)

// Actual metadata is supplied only by a retained centrally executed measurement.
// All MapFS bytes below are actual owned SQL files, never toy tables/definitions.
func candidateMeasuredCatalog(t *testing.T) Catalog {
	t.Helper()
	path := os.Getenv("PROJECT_ONE_MYSQL_CANDIDATE_EVOLUTION")
	expected := os.Getenv("PROJECT_ONE_MYSQL_CANDIDATE_EVOLUTION_SHA256")
	resolved, e := filepath.EvalSymlinks(path)
	info, se := os.Lstat(path)
	if e != nil || se != nil || !filepath.IsAbs(path) || resolved != path || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maximumCatalogBytes || !checksum.MatchString(expected) {
		t.Fatal("exact private actual candidate metadata input required")
	}
	raw, e := os.ReadFile(path)
	if e != nil || hash(string(raw)) != expected {
		t.Fatal("actual candidate metadata fingerprint changed")
	}
	canonical, e := CanonicalizeMeasuredEvolution(raw)
	if e != nil {
		t.Fatal("actual candidate canonicalization refused", e)
	}
	var measured, old evolutionDocument
	oldRaw, e := baseCandidate.Files.ReadFile("prefix-evolution149.json")
	if e != nil || json.Unmarshal(oldRaw, &old) != nil || json.Unmarshal(canonical, &measured) != nil || len(measured.Steps) != 179 || len(old.Steps) != 134 {
		t.Fatal("actual exact149 plus45 measured steps required")
	}
	for n, value := range old.Steps {
		if measured.Steps[n] != value {
			t.Fatal("actual old149 reference entry changed", n)
		}
	}
	source := fstest.MapFS{}
	for _, files := range []fs.FS{assets.Files, baseCandidate.Files, toolCandidate.Files} {
		names, e := fs.Glob(files, "*.sql")
		if e != nil {
			t.Fatal(e)
		}
		for _, name := range names {
			raw, e := fs.ReadFile(files, name)
			if e != nil {
				t.Fatal("actual candidate source read failed", e)
			}
			if previous := source[name]; previous != nil {
				if string(previous.Data) != string(raw) {
					t.Fatal("conflicting installed and historical candidate source")
				}
				continue
			}
			source[name] = &fstest.MapFile{Data: raw, Mode: 0444}
		}
	}
	reference, e := assets.Files.ReadFile("reference.json")
	if e != nil {
		t.Fatal(e)
	}
	var original referenceDocument
	if json.Unmarshal(reference, &original) != nil {
		t.Fatal("actual original reference decode")
	}
	allowed := map[string]bool{"journal.sql": true}
	for _, item := range append(original.Steps, measured.Steps...) {
		allowed[item.File] = true
	}
	for name := range source {
		if !allowed[name] {
			delete(source, name)
		}
	}
	catalog, e := loadEvolution(reference, canonical, source)
	if e != nil || catalog.Steps() != 194 {
		t.Fatal("actual candidate-only catalog load refused", e)
	}
	active, e := DefaultCatalog()
	if e != nil || active.Steps() != 265 {
		t.Fatal("current complete catalog required alongside historical194 fixture", e)
	}
	return catalog
}
func candidateActual(t *testing.T) (*mysqlstore.Store, Catalog) {
	t.Helper()
	c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
	if e != nil || c.Host != "127.0.0.1" || !strings.HasPrefix(c.Database, "m20_repository_") || !strings.HasSuffix(c.Database, "_test") {
		t.Fatal("actual isolated candidate repository fixture required")
	}
	s, _, e := mysqlstore.Open(t.Context(), c)
	if e != nil {
		t.Fatal("actual candidate kernel", e)
	}
	t.Cleanup(func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	})
	return s, candidateMeasuredCatalog(t)
}
func candidateOptions(c Catalog) Options {
	o := testOptions()
	o.MaximumSteps = c.Steps()
	o.MaximumMetadataBytes = maximumStatementBytes
	return o
}
func candidateRun(t *testing.T, s *mysqlstore.Store, c Catalog, mode Mode) Report {
	t.Helper()
	r, e := Run(t.Context(), s, c, candidateOptions(c), mode, nil)
	if e != nil || !r.Ready || !r.LockReleased || r.AppliedSteps != c.Steps() {
		t.Fatal("actual candidate owning migration", r, e)
	}
	return r
}
func candidatePrefix(c Catalog, count int) Catalog { c.steps = c.steps[:count]; return c }
func candidateRows(t *testing.T, s *mysqlstore.Store) kwpRows {
	t.Helper()
	r := kwpRows{tenant: uuid.NewString(), workspace: uuid.NewString()}
	migrationExec(t, s, `INSERT INTO tenants(id,name,status) VALUES(?,'Actual candidate schema envelope','active')`, r.tenant)
	migrationExec(t, s, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Actual candidate Workspace envelope','active')`, r.workspace, r.tenant)
	r.user = kwpSeedUser(t, s)
	r.otherUser = kwpSeedUser(t, s)
	migrationExec(t, s, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')`, r.user, r.tenant, r.workspace)
	return r
}
func candidateData(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	return publicationSchemaDigest(t, s.Pool(), publicationSchemaLayout(t, s.Pool()), false)
}
func candidateBusiness(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	layout := publicationSchemaLayout(t, s.Pool())
	var business []publicationSchemaTable
	for _, v := range layout {
		if v.name != journalObject {
			business = append(business, v)
		}
	}
	return publicationSchemaDigest(t, s.Pool(), business, false)
}
func candidateChecksActual(t *testing.T, s *mysqlstore.Store, name string) map[string]string {
	t.Helper()
	var actual, definition string
	if e := s.Pool().QueryRowContext(t.Context(), "SHOW CREATE TABLE `"+name+"`").Scan(&actual, &definition); e != nil || actual != name {
		t.Fatal("actual CHECK metadata", mysqlstore.SafeError(e))
	}
	values, ok := workflowChecks(definition)
	if !ok {
		t.Fatal("actual exact CHECK line shape required")
	}
	return values
}
func candidateRefuse(t *testing.T, s *mysqlstore.Store, number uint16, query string, args ...any) {
	t.Helper()
	before := candidateData(t, s)
	_, e := s.Pool().ExecContext(t.Context(), query, args...)
	var safe *mysqlstore.Error
	if e == nil || !errors.As(mysqlstore.SafeError(e), &safe) || safe.Number != number {
		t.Fatal("actual candidate constraint/guard category", number, mysqlstore.SafeError(e))
	}
	if candidateData(t, s) != before {
		t.Fatal("refused candidate statement mutated complete graph/journal")
	}
}
func candidatePending(t *testing.T, s *mysqlstore.Store, r kwpRows, w kwpWorkflow) string {
	t.Helper()
	id := uuid.NewString()
	migrationExec(t, s, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at) VALUES(?,?,?,?,?,1,?,?,'admin',JSON_OBJECT('schema_version',1),?,'pending','Explicit candidate schema question',TIMESTAMPADD(MINUTE,5,UTC_TIMESTAMP(6)))`, id, r.tenant, r.workspace, w.workflow, r.user, uuid.NewString(), uuid.NewString(), hash(`{"schema_version":1}`))
	return id
}
func candidateAssertGuards(t *testing.T, s *mysqlstore.Store, r kwpRows) {
	t.Helper()
	w := kwpWorkflowSeed(t, s, r)
	id := candidatePending(t, s, r, w)
	candidateRefuse(t, s, 1644, `UPDATE workflow_runs SET question='forged private question' WHERE id=?`, id)
	candidateRefuse(t, s, 1644, `UPDATE workflow_runs SET result_kind='model_list' WHERE id=?`, id)
	candidateRefuse(t, s, 3819, "UPDATE workflow_runs SET state='succeeded',answer=REPEAT('x',1048577),evidence=JSON_OBJECT(),alias_snapshot=JSON_OBJECT(),`usage`=JSON_OBJECT(),finished_at=UTC_TIMESTAMP(6) WHERE id=?", id)
	candidateRefuse(t, s, 3819, `INSERT INTO workflow_node_traces(tenant_id,workspace_id,workflow_id,run_id,node_id,kind,state) VALUES(?,?,?,?,?,'unowned','not_started')`, r.tenant, r.workspace, w.workflow, id, uuid.NewString())
	migrationExec(t, s, `INSERT INTO workflow_node_traces(tenant_id,workspace_id,workflow_id,run_id,node_id,kind,state) VALUES(?,?,?,?,?,'tool','not_started')`, r.tenant, r.workspace, w.workflow, id, uuid.NewString())
	before := candidateData(t, s)
	tx, e := s.Pool().BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer tx.Rollback()
	kwpFinishRun(t, tx, id, "failed")
	_, e = tx.ExecContext(t.Context(), `INSERT INTO audit_events(id,occurred_at,actor_user_id,audience,action,outcome,request_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,'operator','candidate.rollback','accepted',?,CAST('[]' AS JSON))`, uuid.NewString(), r.user, uuid.NewString())
	if e == nil {
		t.Fatal("actual invalid audit did not refuse")
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	if before != candidateData(t, s) {
		t.Fatal("terminal/notice/audit graph rollback failed")
	}
	kwpFinishRun(t, s.Pool(), id, "failed")
}

// Each leaf owns one fresh real database. All row envelopes are schema-only:
// no encoder, signed model result, private provider call or hardware claim.
func TestActualMySQLCandidateEvolution(t *testing.T) {
	t.Run("fresh194_apply_repeat_readiness", func(t *testing.T) {
		s, c := candidateActual(t)
		candidateRun(t, s, c, Apply)
		before := candidateData(t, s)
		events := 0
		r, e := Run(t.Context(), s, c, candidateOptions(c), Apply, func(Event) { events++ })
		if e != nil || !r.Ready || events != 0 || candidateData(t, s) != before {
			t.Fatal("actual repeat changed graph/journal", r, e)
		}
		candidateRun(t, s, c, Readiness)
	})
	t.Run("populated149_upgrade_seed_checks_guards_and_downgrade_refusal", func(t *testing.T) {
		s, c := candidateActual(t)
		candidateRun(t, s, candidatePrefix(c, 149), Apply)
		r := candidateRows(t, s)
		layout := publicationSchemaLayout(t, s.Pool())
		var old []publicationSchemaTable
		for _, v := range layout {
			if v.name != journalObject {
				old = append(old, v)
			}
		}
		before := publicationSchemaDigest(t, s.Pool(), old, false)
		candidateRun(t, s, candidatePrefix(c, 191), Apply)
		runChecks, traceChecks := candidateChecksActual(t, s, "workflow_runs"), candidateChecksActual(t, s, "workflow_node_traces")
		candidateRun(t, s, c, Apply)
		if publicationSchemaDigest(t, s.Pool(), old, false) != before {
			t.Fatal("populated149 business changed")
		}
		after := candidateChecksActual(t, s, "workflow_runs")
		delete(runChecks, "workflow_non_success_result_absent")
		delete(runChecks, "workflow_success_result_required")
		for name, text := range runChecks {
			if after[name] != text {
				t.Fatal("actual unrelated run CHECK changed", name)
			}
		}
		after = candidateChecksActual(t, s, "workflow_node_traces")
		delete(traceChecks, "workflow_node_traces_kind_check")
		for name, text := range traceChecks {
			if after[name] != text {
				t.Fatal("actual unrelated trace CHECK changed", name)
			}
		}
		var missing int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM users u LEFT JOIN user_preferences p ON p.user_id=u.id WHERE p.user_id IS NULL`).Scan(&missing); e != nil || missing != 0 {
			t.Fatal("actual existing-user seed incomplete", mysqlstore.SafeError(e))
		}
		candidateAssertGuards(t, s, r)
		saved := candidateData(t, s)
		if report, e := Run(t.Context(), s, candidatePrefix(c, 149), candidateOptions(c), Apply, nil); e == nil || report.Ready || candidateData(t, s) != saved {
			t.Fatal("older catalog adopted/downgraded used194", report, e)
		}
	})
	t.Run("dirty192_before_ddl_requires_explicit_reconcile", func(t *testing.T) {
		s, c := candidateActual(t)
		candidateRun(t, s, candidatePrefix(c, 191), Apply)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		report, e := Run(ctx, s, c, candidateOptions(c), Apply, func(v Event) {
			if v.Step == 192 && v.Stage == Applying {
				cancel()
			}
		})
		if e == nil || report.DirtyStep != 192 || !report.LockReleased {
			t.Fatal("before actual DDL failure lost dirty intent", report, e)
		}
		if _, e = Run(t.Context(), s, c, candidateOptions(c), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
			t.Fatal("dirty CHECK source automatically replayed", e)
		}
		candidateRun(t, s, c, Reconcile)
		candidateRun(t, s, c, Readiness)
	})
	t.Run("dirty192_after_check_ddl_reconcile_without_drop_replay", func(t *testing.T) {
		s, c := candidateActual(t)
		candidateRun(t, s, candidatePrefix(c, 191), Apply)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		report, e := Run(ctx, s, c, candidateOptions(c), Apply, func(v Event) {
			if v.Step == 192 && v.Stage == Verified {
				cancel()
			}
		})
		if e == nil || report.DirtyStep != 192 || !report.LockReleased {
			t.Fatal("post-CHECK DDL unknown journal intent lost", report, e)
		}
		if _, e = Run(t.Context(), s, c, candidateOptions(c), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
			t.Fatal("post-CHECK unknown source replayed", e)
		}
		candidateRun(t, s, c, Reconcile)
		candidateRun(t, s, c, Readiness)
	})
	t.Run("dirty176_after_seed_reconcile_without_duplicate_insert", func(t *testing.T) {
		s, c := candidateActual(t)
		candidateRun(t, s, candidatePrefix(c, 175), Apply)
		candidateRows(t, s)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		report, e := Run(ctx, s, c, candidateOptions(c), Apply, func(v Event) {
			if v.Step == 176 && v.Stage == Verified {
				cancel()
			}
		})
		if e == nil || report.DirtyStep != 176 || !report.LockReleased {
			t.Fatal("post-seed unknown journal intent lost", report, e)
		}
		if _, e = Run(t.Context(), s, c, candidateOptions(c), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
			t.Fatal("post-seed unknown data replayed", e)
		}
		candidateRun(t, s, c, Reconcile)
		candidateRun(t, s, c, Readiness)
	})
	t.Run("dirty194_after_ddl_requires_explicit_reconcile", func(t *testing.T) {
		s, c := candidateActual(t)
		candidateRun(t, s, candidatePrefix(c, 193), Apply)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		report, e := Run(ctx, s, c, candidateOptions(c), Apply, func(v Event) {
			if v.Step == 194 && v.Stage == Verified {
				cancel()
			}
		})
		if e == nil || report.DirtyStep != 194 || !report.LockReleased {
			t.Fatal("after actual DDL failure lost dirty intent", report, e)
		}
		if _, e = Run(t.Context(), s, c, candidateOptions(c), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
			t.Fatal("created trigger automatically replayed", e)
		}
		candidateRun(t, s, c, Reconcile)
		candidateRun(t, s, c, Readiness)
	})
	t.Run("dirty_third_state_and_applied_original_guard_drift_refuse", func(t *testing.T) {
		s, c := candidateActual(t)
		candidateRun(t, s, candidatePrefix(c, 191), Apply)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		_, e := Run(ctx, s, c, candidateOptions(c), Apply, func(v Event) {
			if v.Step == 192 && v.Stage == Verified {
				cancel()
			}
		})
		if e == nil {
			t.Fatal("dirty fixture did not interrupt")
		}
		migrationExec(t, s, "ALTER TABLE workflow_runs ADD COLUMN candidate_drift INT NULL")
		if _, e = Run(t.Context(), s, c, candidateOptions(c), Reconcile, nil); e == nil || e.Error() != CodeDefinitionDrift {
			t.Fatal("third table state adopted", e)
		}
		migrationExec(t, s, "ALTER TABLE workflow_runs DROP COLUMN candidate_drift")
		candidateRun(t, s, c, Reconcile)
		migrationExec(t, s, "DROP TRIGGER workflow_run_identity_guard")
		if _, e = Run(t.Context(), s, c, candidateOptions(c), Readiness, nil); e == nil || e.Error() != CodeDefinitionDrift {
			t.Fatal("second guard concealed missing original", e)
		}
	})
	t.Run("prefilled_unjournaled_seed_refuses_without_adoption", func(t *testing.T) {
		s, c := candidateActual(t)
		candidateRun(t, s, candidatePrefix(c, 175), Apply)
		r := candidateRows(t, s)
		migrationExec(t, s, userPreferencesSeedSQL)
		before := candidateData(t, s)
		business := candidateBusiness(t, s)
		_, e := Run(t.Context(), s, c, candidateOptions(c), Apply, nil)
		if e == nil || e.Error() != CodeUnjournaledObject {
			t.Fatal("nonempty unjournaled seed adopted", e)
		}
		var count int
		if e = s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM user_preferences WHERE user_id IN(?,?)`, r.user, r.otherUser).Scan(&count); e != nil || count != 2 {
			t.Fatal("refusal changed existing user seed", mysqlstore.SafeError(e))
		}
		if candidateBusiness(t, s) != business {
			t.Fatal("refused prefilled seed altered business rows")
		}
		// Only a dirty journal intent may have been appended; no old data is rewritten.
		if before == candidateData(t, s) {
			t.Fatal("new dirty seed intent missing")
		}
	})
}

func candidateRecoverySeed(t *testing.T, s *mysqlstore.Store) {
	t.Helper()
	step := 0
	seed := func(expected int64, query string, args ...any) {
		t.Helper()
		step++
		result, e := s.Pool().ExecContext(t.Context(), query, args...)
		if e != nil {
			safe := mysqlstore.SafeError(e)
			var vendor *mysqlstore.Error
			if errors.As(safe, &vendor) {
				t.Fatalf("actual recovery fixture step=%d code=%s number=%d state=%s", step, vendor.Error(), vendor.Number, vendor.SQLState)
			}
			t.Fatal("actual recovery fixture", step, safe)
		}
		changed, e := result.RowsAffected()
		if e != nil || changed != expected {
			t.Fatal("actual recovery fixture affected rows", step, changed, expected, mysqlstore.SafeError(e))
		}
	}
	r := candidateRows(t, s)
	// Migration177 initializes preferences for these newly inserted users.
	// Verify the actual trigger output without repeating the migration176 seed.
	var preferences int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM user_preferences WHERE user_id IN(?,?) AND theme='light' AND revision=1`, r.user, r.otherUser).Scan(&preferences); e != nil || preferences != 2 {
		t.Fatal("actual new-user preference initialization", preferences, mysqlstore.SafeError(e))
	}
	t.Log("actual recovery fixture stage=knowledge")
	k := kwpKnowledgeSeed(t, s, r)
	t.Log("actual recovery fixture stage=knowledge_ready")
	for _, invalid := range []string{"[]", kwpVector(0), kwpVector(2), strings.Replace(kwpVector(1), "[1,", "[\"1\",", 1)} {
		candidateRefuse(t, s, 1644, kwpChunkInsert, kwpChunkArguments(r, k, invalid)...)
	}
	seed(1, kwpChunkInsert, kwpChunkArguments(r, k, kwpVector(1))...)
	candidateAssertGuards(t, s, r)
	plugin, grant := uuid.NewString(), uuid.NewString()
	capsule := []byte("Explicit schema-only capsule envelope, not a signed plugin result")
	seed(1, `INSERT INTO plugin_versions(id,tenant_id,plugin_id,release_version,version_major,version_minor,version_patch,abi,executor,capsule,capsule_sha256,manifest,manifest_sha256,key_id,state,imported_by,installed_at) VALUES(?,?,'schema.candidate','1.0.0',1,0,0,'schema.v1','schema',?,?,JSON_OBJECT(),?,?,'installed',?,UTC_TIMESTAMP(6))`, plugin, r.tenant, capsule, hash(string(capsule)), hash("{}"), hash("explicit-schema-key"), r.user)
	seed(1, `INSERT INTO plugin_grants(id,tenant_id,workspace_id,plugin_version_id,authority_id,granted_by) VALUES(?,?,?,?,?,?)`, grant, r.tenant, r.workspace, plugin, uuid.NewString(), r.user)
	w := kwpWorkflowSeed(t, s, r)
	seed(1, `INSERT INTO workflow_plugin_references(tenant_id,workspace_id,workflow_id,workflow_version_id,plugin_version_id,grant_id,publication_grant_authority) SELECT ?,?,?,?,?,id,authority_id FROM plugin_grants WHERE id=?`, r.tenant, r.workspace, w.workflow, w.version, plugin, grant)
	seed(1, `INSERT INTO plugin_mutation_intents(tenant_id,actor_id,idempotency_key,purpose,request_hash,entity_id) VALUES(?,?,?,'import',?,?)`, r.tenant, r.user, uuid.NewString(), make([]byte, 32), plugin)
	seed(1, `INSERT INTO workflow_mutation_intents(tenant_id,workspace_id,actor_id,idempotency_key,purpose,request_hash,entity_id) VALUES(?,?,?,?,'create_workflow',?,?)`, r.tenant, r.workspace, r.user, uuid.NewString(), make([]byte, 32), w.workflow)
	seed(1, `INSERT INTO knowledge_mutation_intents(tenant_id,workspace_id,actor_id,idempotency_key,purpose,request_hash,entity_id) VALUES(?,?,?,?,'create_base',?,?)`, r.tenant, r.workspace, r.user, uuid.NewString(), make([]byte, 32), k.base)
	// Persist a nonempty exact preference; actual ownership/revision trigger applies.
	seed(1, `UPDATE user_preferences SET theme='dark',revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE user_id=?`, r.user)
}
func candidateReadback(t *testing.T, s *mysqlstore.Store, c Catalog) {
	t.Helper()
	candidateRun(t, s, c, Readiness)
	for _, table := range []string{"knowledge_bases", "knowledge_documents", "knowledge_source_versions", "knowledge_attempts", "knowledge_chunks", "workflow_versions", "workflow_runs", "workflow_node_traces", "terminal_notifications", "user_preferences", "plugin_versions", "plugin_grants", "workflow_plugin_references", "plugin_mutation_intents", "workflow_mutation_intents", "knowledge_mutation_intents"} {
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); e != nil || count < 1 {
			t.Fatal("nonempty actual candidate retained graph", table, mysqlstore.SafeError(e))
		}
	}
	t.Log("MYSQL_CANDIDATE_EVOLUTION_DATA_SHA256=" + candidateData(t, s))
}

// The caller owns real logical export/fresh restore. Readback never creates a
// missing graph, edits a journal or adopts an unknown previous mutation.
func TestActualMySQLCandidateEvolutionRecovery(t *testing.T) {
	for _, leaf := range []string{"prepare_complete_candidate_graph", "after_restart_readback", "fresh_restore_readback", "restored_missing_guard_refusal", "fresh_restore_after_drift_readback", "restored_unrelated_check_refusal", "fresh_restore_after_check_drift_readback"} {
		t.Run(leaf, func(t *testing.T) {
			s, c := candidateActual(t)
			if leaf == "prepare_complete_candidate_graph" {
				candidateRun(t, s, c, Apply)
				candidateRecoverySeed(t, s)
			}
			if leaf == "restored_missing_guard_refusal" {
				before := candidateData(t, s)
				migrationExec(t, s, "DROP TRIGGER workflow_run_identity_guard")
				if _, e := Run(t.Context(), s, c, candidateOptions(c), Readiness, nil); e == nil || e.Error() != CodeDefinitionDrift {
					t.Fatal("restored missing original guard remained ready", e)
				}
				source := ""
				for _, item := range c.steps {
					if item.Object == "workflow_run_identity_guard" {
						source = item.sql
					}
				}
				// Recreating order1 after order2 changes actual order. Source-only repair is
				// insufficient and must fail; the recovery runner restores the genuine dump.
				if source == "" {
					t.Fatal("original immutable source missing")
				}
				migrationExec(t, s, source)
				if _, e := Run(t.Context(), s, c, candidateOptions(c), Readiness, nil); e == nil {
					t.Fatal("recreated wrong trigger order silently adopted")
				}
				if candidateData(t, s) != before {
					t.Fatal("refused trigger drift altered data/journal")
				}
				t.Log("MYSQL_CANDIDATE_EVOLUTION_DATA_SHA256=" + before)
				return
			}
			if leaf == "restored_unrelated_check_refusal" {
				before := candidateData(t, s)
				migrationExec(t, s, "ALTER TABLE workflow_runs DROP CHECK workflow_answer_bytes")
				if _, e := Run(t.Context(), s, c, candidateOptions(c), Readiness, nil); e == nil || e.Error() != CodeDefinitionDrift {
					t.Fatal("restored unrelated CHECK removal remained ready", e)
				}
				if candidateData(t, s) != before {
					t.Fatal("refused CHECK drift changed rows/journal")
				}
				t.Log("MYSQL_CANDIDATE_EVOLUTION_DATA_SHA256=" + before)
				return
			}
			candidateReadback(t, s, c)
		})
	}
}

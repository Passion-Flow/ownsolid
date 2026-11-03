//go:build mysql_integration

package mysqlmigrate

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// Explicit synthetic metadata only: real SQL/TLS/schema/constraints, not actual
// uploaded files, Python materialization, rights approval or training execution.
type datasetSchemaRows struct {
	modelInferenceSchemaRows
	dataset, draft, row, job, attempt, version string
}

func datasetSchemaPrefixDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	return hash(modelInferenceDataDigest(t, s) + licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step BETWEEN 46 AND 51 ORDER BY step`))
}
func datasetSchemaDataDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	result := datasetSchemaPrefixDigest(t, s)
	for _, query := range []string{
		`SELECT JSON_ARRAY(id,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,revision,next_version,created_at,updated_at,retired_at,retire_reason,creation_key,HEX(creation_fingerprint),active_name_key) FROM datasets ORDER BY id`,
		`SELECT JSON_ARRAY(id,tenant_id,workspace_id,dataset_id,created_by,requested_audience,requested_epoch,membership_authority,parent_version_id,source_object_id,source_name,source_sha256,expected_bytes,source_received_at,upload_attempt_id,upload_expires_at,state,revision,total_rows,invalid_rows,validation_completed_at,fatal_source_error,validation_errors,errors_truncated,error_code,error_line_number,created_at,updated_at,creation_key,HEX(creation_fingerprint),current_job_id,published_version_id) FROM dataset_drafts ORDER BY id`,
		`SELECT JSON_ARRAY(id,tenant_id,workspace_id,dataset_id,draft_id,line_number,revision,source_id,prompt,completion,error_code,raw_preview,preview_replaced,deleted,created_at,updated_at) FROM dataset_rows ORDER BY id`,
		`SELECT JSON_ARRAY(tenant_id,workspace_id,dataset_id,draft_id,row_id,revision,actor_user_id,action,content_sha256,reason,occurred_at) FROM dataset_row_history ORDER BY tenant_id,workspace_id,draft_id,row_id,revision`,
		`SELECT JSON_ARRAY(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,membership_authority,reason,expected_revision,state,version_id,input_sha256,input_policy,split_seed,validation_basis_points,attempt_count,attempt_id,lease_expires_at,cancel_requested_at,cancel_requested_by,error_code,error_line_number,retryable,retry_of,not_before,created_at,updated_at,completed_at,creation_key,HEX(creation_fingerprint)) FROM dataset_jobs ORDER BY id`,
		`SELECT JSON_ARRAY(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state,started_at,finished_at,error_code,memory_enforcement) FROM dataset_job_attempts ORDER BY tenant_id,workspace_id,job_id,id`,
		`SELECT JSON_ARRAY(id,tenant_id,workspace_id,dataset_id,draft_id,job_id,attempt_id,ordinal,parent_version_id,published_by,published_at,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256,retired_at,retire_reason) FROM dataset_versions ORDER BY id`,
	} {
		result += licenseSchemaQueryDigest(t, s, query)
	}
	return hash(result)
}
func datasetSchemaRecoveryDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	return hash(datasetSchemaDataDigest(t, s) + licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step>=52 ORDER BY step`))
}

func datasetSchemaSeed(t *testing.T, s *mysqlstore.Store, old modelInferenceSchemaRows) datasetSchemaRows {
	t.Helper()
	r := datasetSchemaRows{old, uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fingerprint := make([]byte, 32)
	fingerprint[0] = 255
	migrationExec(t, s, "INSERT INTO datasets(id,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,next_version,created_at,updated_at,creation_key,creation_fingerprint) VALUES(?,?,?,'Synthetic dataset \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629','synthetic dataset \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629','Synthetic metadata origin only','Synthetic fixture; not an actual rights approval',1,?,2,?,?,?,?)", r.dataset, r.tenant, r.workspace, r.user, r.heartbeat, r.heartbeat, uuid.NewString(), fingerprint)
	migrationExec(t, s, `INSERT INTO dataset_drafts(id,tenant_id,workspace_id,dataset_id,created_by,requested_audience,requested_epoch,source_object_id,source_name,source_sha256,expected_bytes,source_received_at,state,revision,total_rows,validation_completed_at,created_at,updated_at,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,'admin',1,?,'synthetic.jsonl',?,64,?,'published',1,1,?,?,?,?,?)`, r.draft, r.tenant, r.workspace, r.dataset, r.user, uuid.NewString(), digest, r.heartbeat, r.heartbeat, r.heartbeat, r.heartbeat, uuid.NewString(), fingerprint)
	migrationExec(t, s, "INSERT INTO dataset_rows(id,tenant_id,workspace_id,dataset_id,draft_id,line_number,source_id,prompt,completion,created_at,updated_at) VALUES(?,?,?,?,?,1,'synthetic-source','Prompt \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629','Completion \u0627\u0644\u0639\u0631\u0628\u064a\u0629 \u4e16\u754c',?,?)", r.row, r.tenant, r.workspace, r.dataset, r.draft, r.heartbeat, r.heartbeat)
	migrationExec(t, s, `INSERT INTO dataset_row_history(tenant_id,workspace_id,dataset_id,draft_id,row_id,revision,actor_user_id,action,content_sha256,reason,occurred_at) VALUES(?,?,?,?,?,1,?,'imported',?,'Synthetic schema metadata only',?)`, r.tenant, r.workspace, r.dataset, r.draft, r.row, r.user, digest, r.heartbeat)
	migrationExec(t, s, `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,reason,expected_revision,state,version_id,input_sha256,input_policy,split_seed,validation_basis_points,attempt_count,attempt_id,completed_at,not_before,created_at,updated_at,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,'publish',?,'admin',1,'Synthetic schema publication',1,'succeeded',?,?,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?,1000,1,?,?,?,?,?,?,?)`, r.job, r.tenant, r.workspace, r.dataset, r.draft, r.user, r.version, digest, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", r.attempt, r.heartbeat, r.heartbeat, r.heartbeat, r.heartbeat, uuid.NewString(), fingerprint)
	migrationExec(t, s, `INSERT INTO dataset_job_attempts(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state,started_at,finished_at,memory_enforcement) VALUES(?,?,?,?,?,?,1,'succeeded',?,?,'Synthetic schema metadata only')`, r.tenant, r.workspace, r.dataset, r.draft, r.job, r.attempt, r.heartbeat, r.heartbeat)
	migrationExec(t, s, "INSERT INTO dataset_versions(id,tenant_id,workspace_id,dataset_id,draft_id,job_id,attempt_id,ordinal,published_by,published_at,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256) VALUES(?,?,?,?,?,?,?,1,?,?,?,?,?,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON),'label','\u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629'),?)", r.version, r.tenant, r.workspace, r.dataset, r.draft, r.job, r.attempt, r.user, r.heartbeat, digest, digest, digest, digest)
	migrationExec(t, s, `UPDATE dataset_drafts SET current_job_id=?,published_version_id=? WHERE id=?`, r.job, r.version, r.draft)
	return r
}
func datasetSchemaReady(t *testing.T) (*mysqlstore.Store, Catalog, datasetSchemaRows) {
	t.Helper()
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Apply)
	old := nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s))
	return s, c, datasetSchemaSeed(t, s, modelInferenceSeed(t, s, old))
}
func datasetSchemaAssertRows(t *testing.T, s *mysqlstore.Store) {
	t.Helper()
	for _, table := range []string{"datasets", "dataset_drafts", "dataset_rows", "dataset_row_history", "dataset_jobs", "dataset_job_attempts", "dataset_versions"} {
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); e != nil || count != 1 {
			t.Fatal("actual dataset graph row count", table, mysqlstore.SafeError(e))
		}
	}
	var name, active string
	var rights bool
	var created time.Time
	var fingerprint []byte
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT name,active_name_key,rights_asserted,created_at,creation_fingerprint FROM datasets`).Scan(&name, &active, &rights, &created, &fingerprint); e != nil || name != "Synthetic dataset \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || active != "synthetic dataset \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || !rights || len(fingerprint) != 32 || fingerprint[0] != 255 || created.Location() != time.UTC || created.Nanosecond() != 123456000 {
		t.Fatal("actual dataset Unicode/boolean/binary/generated/UTC microsecond round trip", mysqlstore.SafeError(e))
	}
	var raw []byte
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT manifest FROM dataset_versions`).Scan(&raw); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	var manifest map[string]any
	if json.Unmarshal(raw, &manifest) != nil || manifest["synthetic_schema"] != true || manifest["label"] != "\u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" {
		t.Fatal("actual dataset manifest Unicode object round trip")
	}
	var count int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM dataset_drafts d JOIN dataset_jobs j ON j.id=d.current_job_id AND j.tenant_id=d.tenant_id AND j.workspace_id=d.workspace_id AND j.dataset_id=d.dataset_id AND j.draft_id=d.id JOIN dataset_versions v ON v.id=d.published_version_id AND v.job_id=j.id JOIN dataset_job_attempts a ON a.id=v.attempt_id AND a.job_id=j.id`).Scan(&count); e != nil || count != 1 {
		t.Fatal("actual immediate-FK dataset publication graph", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLDatasetSchemaFresh(t *testing.T) {
	s, c, _ := datasetSchemaReady(t)
	datasetSchemaAssertRows(t, s)
	before, events := datasetSchemaRecoveryDigest(t, s), 0
	r, e := Run(t.Context(), s, c, testOptions(), Apply, func(Event) { events++ })
	if e != nil || !r.Ready || events != 0 || datasetSchemaRecoveryDigest(t, s) != before {
		t.Fatal("actual full dataset migration no-op changed state", e)
	}
	migrationRun(t, s, c, Readiness)
}
func TestActualMySQLDatasetSchemaUpgrade(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:51]
	migrationRun(t, s, prefix, Apply)
	old := modelInferenceSeed(t, s, nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s)))
	before := datasetSchemaPrefixDigest(t, s)
	migrationRun(t, s, c, Apply)
	if datasetSchemaPrefixDigest(t, s) != before {
		t.Fatal("51-to59 changed accepted identity/node/model/inference/journal data")
	}
	datasetSchemaSeed(t, s, old)
	datasetSchemaAssertRows(t, s)
	migrationRun(t, s, c, Readiness)
}

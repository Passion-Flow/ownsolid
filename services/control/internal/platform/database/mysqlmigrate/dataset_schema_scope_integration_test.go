//go:build mysql_integration

package mysqlmigrate

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

const datasetSchemaInsertDataset = `INSERT INTO datasets(id,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,creation_key,creation_fingerprint) VALUES(?,?,?,'Synthetic reservation',?,'Synthetic metadata only','Synthetic; no rights approval',1,?,?,?)`
const datasetSchemaInsertDraft = `INSERT INTO dataset_drafts(id,tenant_id,workspace_id,dataset_id,created_by,requested_audience,requested_epoch,source_object_id,source_name,source_sha256,expected_bytes,state,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,'admin',1,?,'synthetic.jsonl',?,1,'uploading',?,?)`
const datasetSchemaInsertRow = `INSERT INTO dataset_rows(id,tenant_id,workspace_id,dataset_id,draft_id,line_number,source_id,prompt,completion) VALUES(?,?,?,?,?,?,'synthetic','Synthetic prompt','Synthetic completion')`
const datasetSchemaInsertJob = `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,reason,expected_revision,state,input_policy,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,'import',?,'admin',1,'Synthetic schema job',1,'pending',JSON_OBJECT(),?,?)`
const datasetSchemaInsertVersion = `INSERT INTO dataset_versions(id,tenant_id,workspace_id,dataset_id,draft_id,job_id,attempt_id,ordinal,published_by,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,JSON_OBJECT(),?)`

func datasetSchemaVersionArgs(r datasetSchemaRows, id, job, attempt string, ordinal int) []any {
	digest := strings.Repeat("a", 64)
	return []any{id, r.tenant, r.workspace, r.dataset, r.draft, job, attempt, ordinal, r.user, digest, digest, digest, digest}
}

func datasetSchemaPublicationOwner(t *testing.T, s *mysqlstore.Store, r datasetSchemaRows) (job, attempt, version string) {
	t.Helper()
	job, attempt, version = uuid.NewString(), uuid.NewString(), uuid.NewString()
	migrationExec(t, s, `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,reason,expected_revision,state,version_id,input_policy,split_seed,validation_basis_points,attempt_count,attempt_id,completed_at,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,'publish',?,'admin',1,'Synthetic publication owner',1,'succeeded',?,JSON_OBJECT(),?,1000,1,?,UTC_TIMESTAMP(6),?,?)`, job, r.tenant, r.workspace, r.dataset, r.draft, r.user, version, strings.Repeat("a", 32), attempt, uuid.NewString(), make([]byte, 32))
	migrationExec(t, s, `INSERT INTO dataset_job_attempts(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state,finished_at) VALUES(?,?,?,?,?,?,1,'succeeded',UTC_TIMESTAMP(6))`, r.tenant, r.workspace, r.dataset, r.draft, job, attempt)
	return
}

func TestActualMySQLDatasetSchemaForeignScopes(t *testing.T) {
	s, c, r := datasetSchemaReady(t)
	// Give the same-Workspace peer its own active name without weakening the
	// real uniqueness constraint or sharing a row between dataset identities.
	migrationExec(t, s, `UPDATE datasets SET name_key=CONCAT(name_key,'/',id) WHERE id=?`, r.dataset)
	for _, kind := range []string{"dataset", "workspace", "tenant"} {
		old := r.modelInferenceSchemaRows
		if kind != "dataset" {
			old.workspace = uuid.NewString()
			if kind == "tenant" {
				old.tenant = uuid.NewString()
				migrationExec(t, s, `INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic foreign tenant','active')`, old.tenant)
			}
			migrationExec(t, s, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,?,'active')`, old.workspace, old.tenant, "Synthetic dataset scope "+old.workspace)
		}
		f := datasetSchemaSeed(t, s, old)
		// Use an unpublished real owner so job uniqueness cannot mask the scoped FK.
		foreignJob, foreignAttempt, _ := datasetSchemaPublicationOwner(t, s, f)
		for _, item := range []struct {
			name, query string
			args        []any
		}{
			{"draft_dataset", datasetSchemaInsertDraft, []any{uuid.NewString(), r.tenant, r.workspace, f.dataset, r.user, uuid.NewString(), strings.Repeat("a", 64), uuid.NewString(), make([]byte, 32)}},
			{"row_draft", datasetSchemaInsertRow, []any{uuid.NewString(), r.tenant, r.workspace, r.dataset, f.draft, 2}},
			{"history_row", `INSERT INTO dataset_row_history(tenant_id,workspace_id,dataset_id,draft_id,row_id,revision,action,reason) VALUES(?,?,?,?,?,2,'replaced','Synthetic scope')`, []any{r.tenant, r.workspace, r.dataset, r.draft, f.row}},
			{"job_draft", datasetSchemaInsertJob, []any{uuid.NewString(), r.tenant, r.workspace, r.dataset, f.draft, r.user, uuid.NewString(), make([]byte, 32)}},
			{"attempt_job", `INSERT INTO dataset_job_attempts(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state) VALUES(?,?,?,?,?,?,2,'running')`, []any{r.tenant, r.workspace, r.dataset, r.draft, f.job, uuid.NewString()}},
			{"version_job", datasetSchemaInsertVersion, datasetSchemaVersionArgs(r, uuid.NewString(), foreignJob, foreignAttempt, 2)},
			{"version_attempt", `UPDATE dataset_versions SET attempt_id=? WHERE id=?`, []any{f.attempt, r.version}},
			{"version_parent", `UPDATE dataset_versions SET parent_version_id=? WHERE id=?`, []any{f.version, r.version}},
			{"draft_current_job", `UPDATE dataset_drafts SET current_job_id=? WHERE id=?`, []any{f.job, r.draft}},
			{"draft_published_version", `UPDATE dataset_drafts SET published_version_id=? WHERE id=?`, []any{f.version, r.draft}},
			{"draft_parent_version", `UPDATE dataset_drafts SET parent_version_id=? WHERE id=?`, []any{f.version, r.draft}},
		} {
			// A different dataset in the SAME Workspace is a valid draft owner;
			// its rows/jobs/versions are still invalid children of r.dataset.
			if kind == "dataset" && item.name == "draft_dataset" {
				continue
			}
			t.Run(kind+"_"+item.name, func(t *testing.T) { datasetSchemaRefuses(t, s, 1452, item.query, item.args...) })
		}
		if kind != "dataset" {
			t.Run(kind+"_retry_scope", func(t *testing.T) {
				datasetSchemaRefuses(t, s, 1452, `UPDATE dataset_jobs SET retry_of=? WHERE id=?`, f.job, r.job)
			})
			if kind == "tenant" {
				t.Run(kind+"_workspace_owner", func(t *testing.T) {
					datasetSchemaRefuses(t, s, 1452, datasetSchemaInsertDataset, uuid.NewString(), r.tenant, f.workspace, uuid.NewString(), r.user, uuid.NewString(), make([]byte, 32))
				})
			}
		}
	}
	for _, item := range []struct{ table, key, id string }{
		{"datasets", "id", r.dataset}, {"dataset_drafts", "id", r.draft}, {"dataset_rows", "id", r.row}, {"dataset_jobs", "id", r.job}, {"dataset_job_attempts", "id", r.attempt}, {"dataset_versions", "id", r.version},
	} {
		t.Run("referenced_"+item.table+"_delete", func(t *testing.T) {
			datasetSchemaRefuses(t, s, 1451, "DELETE FROM "+item.table+" WHERE "+item.key+"=?", item.id)
		})
	}
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLDatasetSchemaReservations(t *testing.T) {
	s, c, r := datasetSchemaReady(t)
	t.Run("active_name_retired_reuse_and_intent", func(t *testing.T) {
		args := []any{uuid.NewString(), r.tenant, r.workspace, "synthetic dataset \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", r.user, uuid.NewString(), make([]byte, 32)}
		datasetSchemaRefuses(t, s, 1062, datasetSchemaInsertDataset, args...)
		migrationExec(t, s, `UPDATE datasets SET retired_at=UTC_TIMESTAMP(6),retire_reason='Synthetic retirement' WHERE id=?`, r.dataset)
		migrationExec(t, s, datasetSchemaInsertDataset, args...)
		var active any
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT active_name_key FROM datasets WHERE id=?`, r.dataset).Scan(&active); e != nil || active != nil {
			t.Fatal("retirement retained generated name reservation", mysqlstore.SafeError(e))
		}
		args[0], args[3] = uuid.NewString(), uuid.NewString()
		datasetSchemaRefuses(t, s, 1062, datasetSchemaInsertDataset, args...)
	})
	t.Run("draft_creation_intent", func(t *testing.T) {
		datasetSchemaRefuses(t, s, 1062, `INSERT INTO dataset_drafts(id,tenant_id,workspace_id,dataset_id,created_by,requested_audience,requested_epoch,source_object_id,source_name,source_sha256,expected_bytes,state,creation_key,creation_fingerprint) SELECT ?,tenant_id,workspace_id,dataset_id,created_by,'admin',1,?,'synthetic.jsonl',source_sha256,1,'uploading',creation_key,creation_fingerprint FROM dataset_drafts WHERE id=?`, uuid.NewString(), uuid.NewString(), r.draft)
	})
	t.Run("row_line_and_history_identity", func(t *testing.T) {
		datasetSchemaRefuses(t, s, 1062, datasetSchemaInsertRow, uuid.NewString(), r.tenant, r.workspace, r.dataset, r.draft, 1)
		datasetSchemaRefuses(t, s, 1062, `INSERT INTO dataset_row_history SELECT * FROM dataset_row_history WHERE row_id=?`, r.row)
	})
	t.Run("job_intent_and_attempt_number", func(t *testing.T) {
		datasetSchemaRefuses(t, s, 1062, `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,reason,expected_revision,state,input_policy,creation_key,creation_fingerprint) SELECT ?,tenant_id,workspace_id,dataset_id,draft_id,'import',requested_by,'admin',1,'Synthetic duplicate',1,'pending',JSON_OBJECT(),creation_key,creation_fingerprint FROM dataset_jobs WHERE id=?`, uuid.NewString(), r.job)
		datasetSchemaRefuses(t, s, 1062, `INSERT INTO dataset_job_attempts(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state) VALUES(?,?,?,?,?,?,1,'running')`, r.tenant, r.workspace, r.dataset, r.draft, r.job, uuid.NewString())
	})
	t.Run("version_ordinal_and_job", func(t *testing.T) {
		job, attempt, version := datasetSchemaPublicationOwner(t, s, r)
		datasetSchemaRefuses(t, s, 1062, datasetSchemaInsertVersion, datasetSchemaVersionArgs(r, version, job, attempt, 1)...)
		datasetSchemaRefuses(t, s, 1062, datasetSchemaInsertVersion, datasetSchemaVersionArgs(r, uuid.NewString(), r.job, r.attempt, 2)...)
	})
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLDatasetSchemaConcurrentReservations(t *testing.T) {
	s, c, r := datasetSchemaReady(t)
	t.Run("active_dataset_name", func(t *testing.T) {
		name := "Synthetic concurrent " + uuid.NewString()
		args := func() []any {
			return []any{uuid.NewString(), r.tenant, r.workspace, name, r.user, uuid.NewString(), make([]byte, 32)}
		}
		modelInferenceOneWinner(t, s, datasetSchemaInsertDataset, args(), args())
	})
	t.Run("published_ordinal", func(t *testing.T) {
		a, aa, av := datasetSchemaPublicationOwner(t, s, r)
		b, ba, bv := datasetSchemaPublicationOwner(t, s, r)
		modelInferenceOneWinner(t, s, datasetSchemaInsertVersion, datasetSchemaVersionArgs(r, av, a, aa, 2), datasetSchemaVersionArgs(r, bv, b, ba, 2))
	})
	migrationRun(t, s, c, Readiness)
}

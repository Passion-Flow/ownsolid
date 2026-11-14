//go:build mysql_integration

package mysqlmigrate

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func TestActualMySQLDatasetSchemaBounds(t *testing.T) {
	s, c, r := datasetSchemaReady(t)
	for _, item := range []struct {
		name, query string
		args        []any
		number      uint16
	}{
		{"dataset_name_characters", `UPDATE datasets SET name=? WHERE id=?`, []any{strings.Repeat("\u4e16", 101), r.dataset}, 1406},
		{"dataset_origin_null", `UPDATE datasets SET origin=NULL WHERE id=?`, []any{r.dataset}, 1048},
		{"dataset_license_empty", `UPDATE datasets SET license='' WHERE id=?`, []any{r.dataset}, 3819},
		{"draft_expected_null", `UPDATE dataset_drafts SET expected_bytes=NULL WHERE id=?`, []any{r.draft}, 1048},
		{"draft_epoch_negative", `UPDATE dataset_drafts SET requested_epoch=-1 WHERE id=?`, []any{r.draft}, 3819},
		{"draft_revision_overflow", `UPDATE dataset_drafts SET revision=9007199254740992 WHERE id=?`, []any{r.draft}, 3819},
		{"draft_total_rows_overflow", `UPDATE dataset_drafts SET total_rows=10001 WHERE id=?`, []any{r.draft}, 3819},
		{"draft_invalid_rows_overflow", `UPDATE dataset_drafts SET invalid_rows=10002 WHERE id=?`, []any{r.draft}, 3819},
		{"draft_invalid_validation_JSON", `UPDATE dataset_drafts SET validation_errors=? WHERE id=?`, []any{"[", r.draft}, 3140},
		{"draft_invalid_membership_uuid", `UPDATE dataset_drafts SET membership_authority='invalid' WHERE id=?`, []any{r.draft}, 3819},
		{"row_completion_bytes", `UPDATE dataset_rows SET completion=? WHERE id=?`, []any{strings.Repeat("x", 32769), r.row}, 3819},
		{"row_line_zero", `UPDATE dataset_rows SET line_number=0 WHERE id=?`, []any{r.row}, 3819},
		{"row_line_overflow", `UPDATE dataset_rows SET line_number=10001 WHERE id=?`, []any{r.row}, 3819},
		{"row_preview_nonboolean", `UPDATE dataset_rows SET preview_replaced=2 WHERE id=?`, []any{r.row}, 3819},
		{"history_digest", `UPDATE dataset_row_history SET content_sha256='invalid' WHERE row_id=?`, []any{r.row}, 3819},
		{"history_reason_null", `UPDATE dataset_row_history SET reason=NULL WHERE row_id=?`, []any{r.row}, 1048},
		{"job_missing_publish_version", `UPDATE dataset_jobs SET version_id=NULL WHERE id=?`, []any{r.job}, 3819},
		{"job_missing_publish_seed", `UPDATE dataset_jobs SET split_seed=NULL WHERE id=?`, []any{r.job}, 3819},
		{"job_import_with_publish_intent", `UPDATE dataset_jobs SET kind='import' WHERE id=?`, []any{r.job}, 3819},
		{"job_portal_missing_authority", `UPDATE dataset_jobs SET requested_audience='portal',membership_authority=NULL WHERE id=?`, []any{r.job}, 3819},
		{"job_policy_null", `UPDATE dataset_jobs SET input_policy=NULL WHERE id=?`, []any{r.job}, 1048},
		{"job_policy_JSON_null", `UPDATE dataset_jobs SET input_policy=CAST('null' AS JSON) WHERE id=?`, []any{r.job}, 3819},
		{"job_retryable_nonboolean", `UPDATE dataset_jobs SET retryable=2 WHERE id=?`, []any{r.job}, 3819},
		{"job_fraction_lower", `UPDATE dataset_jobs SET validation_basis_points=99 WHERE id=?`, []any{r.job}, 3819},
		{"job_fraction_upper", `UPDATE dataset_jobs SET validation_basis_points=5001 WHERE id=?`, []any{r.job}, 3819},
		{"attempt_terminal_without_finish", `UPDATE dataset_job_attempts SET finished_at=NULL WHERE id=?`, []any{r.attempt}, 3819},
		{"attempt_unknown_state", `UPDATE dataset_job_attempts SET state='unknown' WHERE id=?`, []any{r.attempt}, 3819},
		{"version_manifest_JSON_null", `UPDATE dataset_versions SET manifest=CAST('null' AS JSON) WHERE id=?`, []any{r.version}, 3819},
		{"version_digest", `UPDATE dataset_versions SET raw_source_sha256='invalid' WHERE id=?`, []any{r.version}, 3819},
		{"version_publisher_null", `UPDATE dataset_versions SET published_by=NULL WHERE id=?`, []any{r.version}, 1048},
	} {
		t.Run(item.name, func(t *testing.T) { datasetSchemaRefuses(t, s, item.number, item.query, item.args...) })
	}
	t.Run("exact_JSON_byte_and_packet_profile", func(t *testing.T) {
		var packet int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT @@session.max_allowed_packet`).Scan(&packet); e != nil || packet < 16*1024*1024 {
			t.Fatal("declared 8MiB metadata profile requires at least16MiB packet", mysqlstore.SafeError(e))
		}
		for _, item := range []struct {
			table, column, constructor, id string
			maximum, overhead              int
		}{
			{"dataset_drafts", "validation_errors", "JSON_ARRAY(?)", r.draft, 8388608, len(`[""]`)},
			{"dataset_jobs", "input_policy", "JSON_OBJECT('v',?)", r.job, 16384, len(`{"v": ""}`)},
			{"dataset_versions", "manifest", "JSON_OBJECT('v',?)", r.version, 8388608, len(`{"v": ""}`)},
		} {
			value := strings.Repeat("x", item.maximum-item.overhead)
			query := "UPDATE " + item.table + " SET " + item.column + "=" + item.constructor + " WHERE id=?"
			migrationExec(t, s, query, value, item.id)
			var size int
			if e := s.Pool().QueryRowContext(t.Context(), "SELECT OCTET_LENGTH(CAST("+item.column+" AS CHAR CHARACTER SET utf8mb4)) FROM "+item.table+" WHERE id=?", item.id).Scan(&size); e != nil || size != item.maximum {
				t.Fatal("actual dataset JSON byte ceiling", item.table, size, mysqlstore.SafeError(e))
			}
			datasetSchemaRefuses(t, s, 3819, query, value+"x", item.id)
			// Keep each bounded8MiB exercise sequential, including digest memory.
			recovery := "JSON_OBJECT()"
			if item.table == "dataset_drafts" {
				recovery = "JSON_ARRAY()"
			}
			migrationExec(t, s, "UPDATE "+item.table+" SET "+item.column+"="+recovery+" WHERE id=?", item.id)
		}
	})
	t.Run("Unicode_binary_trailing_nullable_and_safe_limits", func(t *testing.T) {
		for _, name := range []string{"Synthetic \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", "synthetic \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", "Synthetic \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629 ", strings.Repeat("\u4e16", 100)} {
			migrationExec(t, s, `UPDATE datasets SET name=? WHERE id=?`, name, r.dataset)
			var got string
			if e := s.Pool().QueryRowContext(t.Context(), `SELECT name FROM datasets WHERE id=?`, r.dataset).Scan(&got); e != nil || got != name {
				t.Fatal("actual binary Unicode name round trip", mysqlstore.SafeError(e))
			}
		}
		migrationExec(t, s, `UPDATE datasets SET revision=9007199254740991,next_version=9007199254740991 WHERE id=?`, r.dataset)
		migrationExec(t, s, `UPDATE dataset_drafts SET revision=9007199254740991,expected_bytes=67108864,total_rows=10000,invalid_rows=10001,error_line_number=10001,requested_audience='portal',membership_authority=?,upload_attempt_id=?,upload_expires_at=UTC_TIMESTAMP(6) WHERE id=?`, uuid.NewString(), uuid.NewString(), r.draft)
		migrationExec(t, s, `UPDATE dataset_rows SET line_number=10000,revision=9007199254740991,source_id=?,prompt=?,completion=?,raw_preview=?,preview_replaced=1,deleted=1 WHERE id=?`, strings.Repeat("x", 512), strings.Repeat("\u4e16", 10922)+"xx", strings.Repeat("x", 32768), strings.Repeat("x", 4096), r.row)
		var size int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT OCTET_LENGTH(prompt) FROM dataset_rows WHERE id=?`, r.row).Scan(&size); e != nil || size != 32768 {
			t.Fatal("exact multibyte prompt ceiling", mysqlstore.SafeError(e))
		}
		migrationExec(t, s, `UPDATE dataset_row_history SET actor_user_id=NULL,content_sha256=NULL,revision=9007199254740991 WHERE row_id=?`, r.row)
		migrationExec(t, s, `UPDATE dataset_jobs SET requested_audience='portal',membership_authority=?,attempt_count=5,expected_revision=9007199254740991,input_sha256=NULL,error_line_number=10001,retryable=1 WHERE id=?`, uuid.NewString(), r.job)
		migrationExec(t, s, `UPDATE dataset_versions SET ordinal=9007199254740991,retired_at=UTC_TIMESTAMP(6),retire_reason='Synthetic retained artifact' WHERE id=?`, r.version)
	})
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLDatasetSchemaLifecycle(t *testing.T) {
	s, c, r := datasetSchemaReady(t)
	t.Run("upload_lease_receive_lifecycle", func(t *testing.T) {
		draft := uuid.NewString()
		migrationExec(t, s, datasetSchemaInsertDraft, draft, r.tenant, r.workspace, r.dataset, r.user, uuid.NewString(), strings.Repeat("a", 64), uuid.NewString(), make([]byte, 32))
		migrationExec(t, s, `UPDATE dataset_drafts SET upload_attempt_id=?,upload_expires_at=UTC_TIMESTAMP(6) WHERE id=?`, uuid.NewString(), draft)
		migrationExec(t, s, `UPDATE dataset_drafts SET source_received_at=UTC_TIMESTAMP(6),upload_attempt_id=NULL,upload_expires_at=NULL,state='validating' WHERE id=?`, draft)
		for _, state := range []string{"editing", "publishing", "published", "failed", "cancelled"} {
			migrationExec(t, s, `UPDATE dataset_drafts SET state=? WHERE id=?`, state, draft)
		}
		migrationExec(t, s, `UPDATE dataset_rows SET source_id=NULL,prompt=NULL,completion=NULL,error_code='SYNTHETIC_INVALID_ROW',deleted=1 WHERE id=?`, r.row)
		migrationExec(t, s, `UPDATE dataset_rows SET source_id='synthetic repaired',prompt='Synthetic prompt',completion='Synthetic completion',error_code=NULL,deleted=0 WHERE id=?`, r.row)
	})
	t.Run("import_attempt_retry_cancel_lifecycle", func(t *testing.T) {
		job, attempt := uuid.NewString(), uuid.NewString()
		migrationExec(t, s, datasetSchemaInsertJob, job, r.tenant, r.workspace, r.dataset, r.draft, r.user, uuid.NewString(), make([]byte, 32))
		migrationExec(t, s, `UPDATE dataset_jobs SET state='running',attempt_id=?,attempt_count=1,lease_expires_at=TIMESTAMPADD(MINUTE,1,UTC_TIMESTAMP(6)) WHERE id=?`, attempt, job)
		migrationExec(t, s, `INSERT INTO dataset_job_attempts(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state) VALUES(?,?,?,?,?,?,1,'running')`, r.tenant, r.workspace, r.dataset, r.draft, job, attempt)
		migrationExec(t, s, `UPDATE dataset_job_attempts SET state='expired',finished_at=UTC_TIMESTAMP(6),error_code='SYNTHETIC_LEASE_EXPIRED' WHERE id=?`, attempt)
		migrationExec(t, s, `UPDATE dataset_jobs SET state='failed',completed_at=UTC_TIMESTAMP(6),lease_expires_at=NULL,retryable=1 WHERE id=?`, job)
		retry := uuid.NewString()
		migrationExec(t, s, datasetSchemaInsertJob, retry, r.tenant, r.workspace, r.dataset, r.draft, r.user, uuid.NewString(), make([]byte, 32))
		migrationExec(t, s, `UPDATE dataset_jobs SET retry_of=?,cancel_requested_at=UTC_TIMESTAMP(6),cancel_requested_by=?,state='cancelled',completed_at=UTC_TIMESTAMP(6) WHERE id=?`, job, r.user, retry)
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM dataset_jobs j JOIN dataset_jobs old ON old.id=j.retry_of AND old.tenant_id=j.tenant_id AND old.workspace_id=j.workspace_id WHERE j.id=? AND j.state='cancelled' AND j.lease_expires_at IS NULL AND old.state='failed' AND old.retryable=1`, retry).Scan(&count); e != nil || count != 1 {
			t.Fatal("actual retained scoped retry/cancel lifecycle", mysqlstore.SafeError(e))
		}
	})
	t.Run("publication_intent_precedes_version", func(t *testing.T) {
		job, attempt, version := datasetSchemaPublicationOwner(t, s, r)
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM dataset_versions WHERE id=?`, version).Scan(&count); e != nil || count != 0 {
			t.Fatal("reserved intent created phantom version", mysqlstore.SafeError(e))
		}
		migrationExec(t, s, datasetSchemaInsertVersion, datasetSchemaVersionArgs(r, version, job, attempt, 2)...)
		migrationExec(t, s, `UPDATE dataset_drafts SET current_job_id=?,published_version_id=?,parent_version_id=? WHERE id=?`, job, version, r.version, r.draft)
		migrationExec(t, s, `UPDATE dataset_versions SET parent_version_id=? WHERE id=?`, r.version, version)
	})
	migrationRun(t, s, c, Readiness)
}

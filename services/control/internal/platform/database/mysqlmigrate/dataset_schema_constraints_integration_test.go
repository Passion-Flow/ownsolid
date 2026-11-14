//go:build mysql_integration

package mysqlmigrate

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func datasetSchemaRefuses(t *testing.T, s *mysqlstore.Store, number uint16, query string, args ...any) {
	t.Helper()
	before := datasetSchemaRecoveryDigest(t, s)
	_, e := s.Pool().ExecContext(t.Context(), query, args...)
	var safe *mysqlstore.Error
	if !errors.As(mysqlstore.SafeError(e), &safe) || safe.Number != number {
		t.Fatal("actual dataset constraint refusal category", mysqlstore.SafeError(e))
	}
	if datasetSchemaRecoveryDigest(t, s) != before {
		t.Fatal("refused dataset write changed accepted data or journal")
	}
}

func TestActualMySQLDatasetSchemaConstraints(t *testing.T) {
	s, c, r := datasetSchemaReady(t)
	for _, item := range []struct {
		name, query string
		args        []any
		number      uint16
	}{
		{"dataset_name_empty", `UPDATE datasets SET name='' WHERE id=?`, []any{r.dataset}, 3819},
		{"dataset_rights_false", `UPDATE datasets SET rights_asserted=0 WHERE id=?`, []any{r.dataset}, 3819},
		{"dataset_rights_nonboolean", `UPDATE datasets SET rights_asserted=2 WHERE id=?`, []any{r.dataset}, 3819},
		{"dataset_revision_zero", `UPDATE datasets SET revision=0 WHERE id=?`, []any{r.dataset}, 3819},
		{"dataset_revision_overflow", `UPDATE datasets SET revision=9007199254740992 WHERE id=?`, []any{r.dataset}, 3819},
		{"dataset_next_version_zero", `UPDATE datasets SET next_version=0 WHERE id=?`, []any{r.dataset}, 3819},
		{"dataset_fingerprint_short", `UPDATE datasets SET creation_fingerprint=? WHERE id=?`, []any{[]byte{1}, r.dataset}, 3819},
		{"dataset_fingerprint_null", `UPDATE datasets SET creation_fingerprint=NULL WHERE id=?`, []any{r.dataset}, 1048},
		{"dataset_retirement_missing_reason", `UPDATE datasets SET retired_at=UTC_TIMESTAMP(6),retire_reason=NULL WHERE id=?`, []any{r.dataset}, 3819},
		{"dataset_reason_without_retirement", `UPDATE datasets SET retire_reason='Synthetic reason' WHERE id=?`, []any{r.dataset}, 3819},
		{"dataset_creation_uuid", `UPDATE datasets SET creation_key='invalid' WHERE id=?`, []any{r.dataset}, 3819},
		{"draft_audience", `UPDATE dataset_drafts SET requested_audience='foreign' WHERE id=?`, []any{r.draft}, 3819},
		{"draft_portal_missing_authority", `UPDATE dataset_drafts SET requested_audience='portal',membership_authority=NULL WHERE id=?`, []any{r.draft}, 3819},
		{"draft_expected_zero", `UPDATE dataset_drafts SET expected_bytes=0 WHERE id=?`, []any{r.draft}, 3819},
		{"draft_expected_overflow", `UPDATE dataset_drafts SET expected_bytes=67108865 WHERE id=?`, []any{r.draft}, 3819},
		{"draft_source_digest", `UPDATE dataset_drafts SET source_sha256='invalid' WHERE id=?`, []any{r.draft}, 3819},
		{"draft_validation_object", `UPDATE dataset_drafts SET validation_errors=JSON_OBJECT() WHERE id=?`, []any{r.draft}, 3819},
		{"draft_validation_null", `UPDATE dataset_drafts SET validation_errors=NULL WHERE id=?`, []any{r.draft}, 1048},
		{"draft_validation_JSON_null", `UPDATE dataset_drafts SET validation_errors=CAST('null' AS JSON) WHERE id=?`, []any{r.draft}, 3819},
		{"draft_fatal_nonboolean", `UPDATE dataset_drafts SET fatal_source_error=2 WHERE id=?`, []any{r.draft}, 3819},
		{"draft_truncation_nonboolean", `UPDATE dataset_drafts SET errors_truncated=-1 WHERE id=?`, []any{r.draft}, 3819},
		{"draft_unpaired_upload", `UPDATE dataset_drafts SET upload_attempt_id=?,upload_expires_at=NULL WHERE id=?`, []any{uuid.NewString(), r.draft}, 3819},
		{"draft_unreceived_published_source", `UPDATE dataset_drafts SET source_received_at=NULL WHERE id=?`, []any{r.draft}, 3819},
		{"row_error_with_valid_text", `UPDATE dataset_rows SET error_code='SYNTHETIC_ROW_ERROR' WHERE id=?`, []any{r.row}, 3819},
		{"row_missing_valid_source", `UPDATE dataset_rows SET source_id=NULL WHERE id=?`, []any{r.row}, 3819},
		{"row_source_bytes", `UPDATE dataset_rows SET source_id=? WHERE id=?`, []any{strings.Repeat("\u4e16", 171), r.row}, 3819},
		{"row_prompt_bytes", `UPDATE dataset_rows SET prompt=? WHERE id=?`, []any{strings.Repeat("x", 32769), r.row}, 3819},
		{"row_preview_bytes", `UPDATE dataset_rows SET raw_preview=? WHERE id=?`, []any{strings.Repeat("x", 4097), r.row}, 3819},
		{"row_deleted_nonboolean", `UPDATE dataset_rows SET deleted=2 WHERE id=?`, []any{r.row}, 3819},
		{"history_revision_zero", `UPDATE dataset_row_history SET revision=0 WHERE row_id=?`, []any{r.row}, 3819},
		{"history_action", `UPDATE dataset_row_history SET action='foreign' WHERE row_id=?`, []any{r.row}, 3819},
		{"job_missing_publish_fraction", `UPDATE dataset_jobs SET validation_basis_points=NULL WHERE id=?`, []any{r.job}, 3819},
		{"job_input_policy_array", `UPDATE dataset_jobs SET input_policy=JSON_ARRAY() WHERE id=?`, []any{r.job}, 3819},
		{"job_state", `UPDATE dataset_jobs SET state='foreign' WHERE id=?`, []any{r.job}, 3819},
		{"job_running_without_lease", `UPDATE dataset_jobs SET state='running',completed_at=NULL,lease_expires_at=NULL WHERE id=?`, []any{r.job}, 3819},
		{"job_pending_with_completion", `UPDATE dataset_jobs SET state='pending' WHERE id=?`, []any{r.job}, 3819},
		{"job_unpaired_cancel", `UPDATE dataset_jobs SET cancel_requested_at=NULL,cancel_requested_by=? WHERE id=?`, []any{r.user, r.job}, 3819},
		{"job_attempt_overflow", `UPDATE dataset_jobs SET attempt_count=6 WHERE id=?`, []any{r.job}, 3819},
		{"job_reserved_version_uuid", `UPDATE dataset_jobs SET version_id='invalid' WHERE id=?`, []any{r.job}, 3819},
		{"job_split_seed", `UPDATE dataset_jobs SET split_seed=REPEAT('A',32) WHERE id=?`, []any{r.job}, 3819},
		{"attempt_running_with_finish", `UPDATE dataset_job_attempts SET state='running' WHERE id=?`, []any{r.attempt}, 3819},
		{"attempt_number_zero", `UPDATE dataset_job_attempts SET attempt_number=0 WHERE id=?`, []any{r.attempt}, 3819},
		{"attempt_number_overflow", `UPDATE dataset_job_attempts SET attempt_number=6 WHERE id=?`, []any{r.attempt}, 3819},
		{"version_manifest_array", `UPDATE dataset_versions SET manifest=JSON_ARRAY() WHERE id=?`, []any{r.version}, 3819},
		{"version_manifest_null", `UPDATE dataset_versions SET manifest=NULL WHERE id=?`, []any{r.version}, 1048},
		{"version_manifest_invalid", `UPDATE dataset_versions SET manifest=? WHERE id=?`, []any{"{", r.version}, 3140},
		{"version_ordinal_zero", `UPDATE dataset_versions SET ordinal=0 WHERE id=?`, []any{r.version}, 3819},
		{"version_ordinal_overflow", `UPDATE dataset_versions SET ordinal=9007199254740992 WHERE id=?`, []any{r.version}, 3819},
		{"version_retirement_missing_reason", `UPDATE dataset_versions SET retired_at=UTC_TIMESTAMP(6),retire_reason=NULL WHERE id=?`, []any{r.version}, 3819},
	} {
		t.Run(item.name, func(t *testing.T) { datasetSchemaRefuses(t, s, item.number, item.query, item.args...) })
	}
	migrationRun(t, s, c, Readiness)
}

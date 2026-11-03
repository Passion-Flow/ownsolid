//go:build mysql_integration

package mysqlmigrate

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// Exercise the actual72 table/reference layer in a fresh disposable database so
// an immutable-input trigger cannot mask a missing CHECK/FK. Fresh/Upgrade and
// Immutability separately exercise the complete current85 combination. No
// current or customer database is downgraded and no guard is disabled.
func trainingSchemaConstraintLayer(t *testing.T) (*mysqlstore.Store, Catalog, trainingSchemaRows) {
	t.Helper()
	s, c, _ := actualMigration(t)
	c.steps = c.steps[:72]
	migrationRun(t, s, c, Apply)
	old := datasetSchemaSeed(t, s, modelInferenceSeed(t, s, nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s))))
	return s, c, trainingSchemaSeed(t, s, old)
}

func TestActualMySQLTrainingSchemaConstraints(t *testing.T) {
	s, c, r := trainingSchemaConstraintLayer(t)
	for _, item := range []struct {
		name, statement string
		args            []any
		number          uint16
	}{
		{"job_state", `UPDATE training_jobs SET state='foreign' WHERE id=?`, []any{r.training}, 3819},
		{"job_revision_zero", `UPDATE training_jobs SET revision=0 WHERE id=?`, []any{r.training}, 3819},
		{"job_revision_overflow", `UPDATE training_jobs SET revision=9007199254740992 WHERE id=?`, []any{r.training}, 3819},
		{"job_attempt_negative", `UPDATE training_jobs SET attempt_number=-1 WHERE id=?`, []any{r.training}, 3819},
		{"job_attempt_overflow", `UPDATE training_jobs SET attempt_number=1001 WHERE id=?`, []any{r.training}, 3819},
		{"job_audience", `UPDATE training_jobs SET requested_audience='foreign' WHERE id=?`, []any{r.training}, 3819},
		{"job_epoch_negative", `UPDATE training_jobs SET requested_epoch=-1 WHERE id=?`, []any{r.training}, 3819},
		{"job_portal_without_authority", `UPDATE training_jobs SET requested_audience='portal',membership_authority=NULL WHERE id=?`, []any{r.training}, 3819},
		{"job_configuration_array", `UPDATE training_jobs SET configuration=JSON_ARRAY() WHERE id=?`, []any{r.training}, 3819},
		{"job_configuration_JSON_null", `UPDATE training_jobs SET configuration=CAST('null' AS JSON) WHERE id=?`, []any{r.training}, 3819},
		{"job_recipe_array", `UPDATE training_jobs SET execution_recipe=JSON_ARRAY() WHERE id=?`, []any{r.training}, 3819},
		{"job_digest_uppercase", `UPDATE training_jobs SET configuration_sha256=REPEAT('A',64) WHERE id=?`, []any{r.training}, 3819},
		{"job_fingerprint_short", `UPDATE training_jobs SET creation_fingerprint=? WHERE id=?`, []any{[]byte{1}, r.training}, 3819},
		{"job_reason_blank", `UPDATE training_jobs SET reason=' ' WHERE id=?`, []any{r.training}, 3819},
		{"job_creation_key_noncanonical", `UPDATE training_jobs SET creation_key='foreign' WHERE id=?`, []any{r.training}, 3819},
		{"job_GPU_noncanonical", `UPDATE training_jobs SET gpu_uuid='GPU-invalid' WHERE id=?`, []any{r.training}, 3819},
		{"attempt_number_zero", `UPDATE training_attempts SET attempt_number=0 WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"attempt_number_overflow", `UPDATE training_attempts SET attempt_number=1001 WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"attempt_state", `UPDATE training_attempts SET state='foreign' WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"attempt_inventory_JSON_null", `UPDATE training_attempts SET input_inventory=CAST('null' AS JSON) WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"attempt_sequence_negative", `UPDATE training_attempts SET last_event_sequence=-1 WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"attempt_sequence_overflow", `UPDATE training_attempts SET last_event_sequence=1000001 WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"attempt_step_negative", `UPDATE training_attempts SET last_global_step=-1 WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"attempt_step_overflow", `UPDATE training_attempts SET last_global_step=1001 WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"attempt_short_stop_token", `UPDATE training_attempts SET stop_token_hash=? WHERE id=?`, []any{[]byte{1}, r.trainingAttempt}, 3819},
		{"attempt_outcome_without_digest", `UPDATE training_attempts SET outcome=JSON_OBJECT() WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"attempt_digest_without_outcome", `UPDATE training_attempts SET outcome_sha256=REPEAT('a',64) WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"attempt_JSON_null_outcome", `UPDATE training_attempts SET outcome=CAST('null' AS JSON),outcome_sha256=REPEAT('a',64) WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"attempt_array_stop_receipt", `UPDATE training_attempts SET physical_stop_receipt=JSON_ARRAY(),physical_stop_sha256=REPEAT('a',64),physical_stopped_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"attempt_unpaired_stop_receipt", `UPDATE training_attempts SET physical_stop_receipt=JSON_OBJECT() WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"attempt_unpaired_stop_time", `UPDATE training_attempts SET physical_stopped_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{r.trainingAttempt}, 3819},
		{"event_sequence_zero", `UPDATE training_events SET sequence=0 WHERE attempt_id=?`, []any{r.trainingAttempt}, 3819},
		{"event_kind", `UPDATE training_events SET kind='foreign' WHERE attempt_id=?`, []any{r.trainingAttempt}, 3819},
		{"event_payload_array", `UPDATE training_events SET payload=JSON_ARRAY() WHERE attempt_id=?`, []any{r.trainingAttempt}, 3819},
		{"checkpoint_step_zero", `UPDATE training_checkpoints SET global_step=0 WHERE id=?`, []any{r.checkpoint}, 3819},
		{"checkpoint_receipt_array", `UPDATE training_checkpoints SET receipt=JSON_ARRAY() WHERE id=?`, []any{r.checkpoint}, 3819},
		{"checkpoint_path_traversal", `UPDATE training_checkpoints SET storage_key='../foreign' WHERE id=?`, []any{r.checkpoint}, 3819},
		{"artifact_manifest_JSON_null", `UPDATE training_artifacts SET manifest=CAST('null' AS JSON) WHERE id=?`, []any{r.artifact}, 3819},
		{"artifact_inventory_array", `UPDATE training_artifacts SET file_inventory=JSON_ARRAY() WHERE id=?`, []any{r.artifact}, 3819},
		{"artifact_path_traversal", `UPDATE training_artifacts SET storage_key='../foreign' WHERE id=?`, []any{r.artifact}, 3819},
		{"recovery_mode", `UPDATE training_recoveries SET mode='foreign' WHERE id=?`, []any{r.recovery}, 3819},
		{"recovery_checkpoint_missing", `UPDATE training_recoveries SET checkpoint_id=NULL WHERE id=?`, []any{r.recovery}, 3819},
		{"recovery_restart_with_checkpoint", `UPDATE training_recoveries SET mode='restart' WHERE id=?`, []any{r.recovery}, 3819},
		{"recovery_reason_blank", `UPDATE training_recoveries SET reason=' ' WHERE id=?`, []any{r.recovery}, 3819},
		{"upload_kind", `UPDATE training_uploads SET kind='foreign' WHERE id=?`, []any{r.checkpoint}, 3819},
		{"upload_state", `UPDATE training_uploads SET state='foreign' WHERE id=?`, []any{r.checkpoint}, 3819},
		{"upload_manifest_array", `UPDATE training_uploads SET manifest=JSON_ARRAY() WHERE id=?`, []any{r.checkpoint}, 3819},
		{"upload_manifest_short", `UPDATE training_uploads SET manifest_bytes=? WHERE id=?`, []any{[]byte{'{'}, r.checkpoint}, 3819},
		{"upload_chunk_partial", `UPDATE training_uploads SET active_chunk_id=? WHERE id=?`, []any{uuid.NewString(), r.checkpoint}, 3819},
		{"upload_chunk_misaligned", `UPDATE training_uploads SET active_chunk_id=?,active_asset='safe.bin',active_offset=1,active_expires_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{uuid.NewString(), r.checkpoint}, 3819},
		{"upload_chunk_unsafe_asset", `UPDATE training_uploads SET active_chunk_id=?,active_asset='../unsafe',active_offset=0,active_expires_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{uuid.NewString(), r.checkpoint}, 3819},
		{"chunk_size_zero", `UPDATE training_upload_chunks SET size_bytes=0 WHERE upload_id=?`, []any{r.checkpoint}, 3819},
		{"chunk_size_overflow", `UPDATE training_upload_chunks SET size_bytes=8388609 WHERE upload_id=?`, []any{r.checkpoint}, 3819},
		{"chunk_offset_misaligned", `UPDATE training_upload_chunks SET byte_offset=1 WHERE upload_id=?`, []any{r.checkpoint}, 3819},
		{"chunk_asset_traversal", `UPDATE training_upload_chunks SET asset='../unsafe' WHERE upload_id=?`, []any{r.checkpoint}, 3819},
		{"GPU_no_owner", `UPDATE gpu_leases SET training_job_id=NULL,deployment_id=NULL WHERE deployment_id=?`, []any{r.deployment}, 3819},
		{"GPU_two_owners", `UPDATE gpu_leases SET training_job_id=? WHERE deployment_id=?`, []any{r.training, r.deployment}, 3819},
		{"GPU_id_noncanonical", `UPDATE gpu_leases SET id=? WHERE deployment_id=?`, []any{strings.Repeat("A", 36), r.deployment}, 3819},
	} {
		t.Run(item.name, func(t *testing.T) { trainingSchemaRefuses(t, s, item.number, item.statement, item.args...) })
	}
	migrationRun(t, s, c, Readiness)
}

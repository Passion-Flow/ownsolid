//go:build mysql_integration

package mysqlmigrate

import (
	"testing"

	"github.com/google/uuid"
)

func TestActualMySQLTrainingSchemaImmutability(t *testing.T) {
	s, c, r := trainingSchemaReady(t)
	for _, item := range []struct {
		name, statement string
		args            []any
	}{
		{"job_frozen_configuration", `UPDATE training_jobs SET configuration=JSON_OBJECT('changed',1) WHERE id=?`, []any{r.training}},
		{"job_frozen_creation_identity", `UPDATE training_jobs SET creation_key=? WHERE id=?`, []any{uuid.NewString(), r.training}},
		{"attempt_frozen_inventory", `UPDATE training_attempts SET input_inventory=JSON_OBJECT('changed',1) WHERE id=?`, []any{r.trainingAttempt}},
		{"attempt_frozen_task", `UPDATE training_attempts SET task_id=? WHERE id=?`, []any{r.task, r.trainingAttempt}},
		{"checkpoint_update", `UPDATE training_checkpoints SET file_inventory=JSON_OBJECT('changed',1) WHERE id=?`, []any{r.checkpoint}},
		{"checkpoint_delete", `DELETE FROM training_checkpoints WHERE id=?`, []any{r.checkpoint}},
		{"artifact_update", `UPDATE training_artifacts SET manifest=JSON_OBJECT('changed',1) WHERE id=?`, []any{r.artifact}},
		{"artifact_delete", `DELETE FROM training_artifacts WHERE id=?`, []any{r.artifact}},
		{"event_update", `UPDATE training_events SET payload=JSON_OBJECT('changed',1) WHERE attempt_id=?`, []any{r.trainingAttempt}},
		{"event_delete", `DELETE FROM training_events WHERE attempt_id=?`, []any{r.trainingAttempt}},
		{"chunk_update", `UPDATE training_upload_chunks SET sha256=REPEAT('b',64) WHERE upload_id=?`, []any{r.checkpoint}},
		{"chunk_delete", `DELETE FROM training_upload_chunks WHERE upload_id=?`, []any{r.checkpoint}},
		{"upload_frozen_manifest", `UPDATE training_uploads SET manifest=JSON_OBJECT('changed',1) WHERE id=?`, []any{r.checkpoint}},
		{"upload_accepted_state", `UPDATE training_uploads SET state='failed' WHERE id=?`, []any{r.checkpoint}},
		{"recovery_frozen_reason", `UPDATE training_recoveries SET reason='Changed synthetic reason' WHERE id=?`, []any{r.recovery}},
		{"recovery_delete", `DELETE FROM training_recoveries WHERE id=?`, []any{r.recovery}},
	} {
		t.Run(item.name, func(t *testing.T) { trainingSchemaRefuses(t, s, 1644, item.statement, item.args...) })
	}
	t.Run("attempt_outcome_once_and_exact_replay", func(t *testing.T) {
		statement := `UPDATE training_attempts SET outcome=JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),outcome_sha256=REPEAT('a',64) WHERE id=?`
		migrationExec(t, s, statement, r.trainingAttempt)
		before := trainingSchemaDataDigest(t, s)
		migrationExec(t, s, statement, r.trainingAttempt)
		if trainingSchemaDataDigest(t, s) != before {
			t.Fatal("exact outcome replay rewrote accepted metadata")
		}
		trainingSchemaRefuses(t, s, 1644, `UPDATE training_attempts SET outcome=JSON_OBJECT('changed',1),outcome_sha256=REPEAT('b',64) WHERE id=?`, r.trainingAttempt)
	})
	t.Run("stop_token_once", func(t *testing.T) {
		migrationExec(t, s, `UPDATE training_attempts SET stop_token_hash=REPEAT('a',32) WHERE id=?`, r.trainingAttempt)
		trainingSchemaRefuses(t, s, 1644, `UPDATE training_attempts SET stop_token_hash=REPEAT('b',32) WHERE id=?`, r.trainingAttempt)
		trainingSchemaRefuses(t, s, 1644, `UPDATE training_attempts SET stop_token_hash=NULL WHERE id=?`, r.trainingAttempt)
	})
	t.Run("physical_stop_tuple_once", func(t *testing.T) {
		// Real SQL tuple protection; this synthetic object is not GPU-stop evidence.
		migrationExec(t, s, `UPDATE training_attempts SET physical_stop_receipt=JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),physical_stop_sha256=REPEAT('a',64),physical_stopped_at=? WHERE id=?`, r.heartbeat, r.trainingAttempt)
		trainingSchemaRefuses(t, s, 1644, `UPDATE training_attempts SET physical_stop_sha256=REPEAT('b',64) WHERE id=?`, r.trainingAttempt)
		trainingSchemaRefuses(t, s, 1644, `UPDATE training_attempts SET physical_stop_receipt=NULL,physical_stop_sha256=NULL,physical_stopped_at=NULL WHERE id=?`, r.trainingAttempt)
	})
	t.Run("recovery_next_attempt_once", func(t *testing.T) {
		id := uuid.NewString()
		migrationExec(t, s, `INSERT INTO training_attempts(id,tenant_id,workspace_id,job_id,node_id,attempt_number,state,requested_by,requested_audience,requested_epoch,input_inventory) VALUES(?,?,?,?,?,2,'pending',?,'admin',1,JSON_OBJECT())`, id, r.tenant, r.workspace, r.training, r.node, r.user)
		migrationExec(t, s, `UPDATE training_recoveries SET next_attempt_id=? WHERE id=?`, id, r.recovery)
		trainingSchemaRefuses(t, s, 1644, `UPDATE training_recoveries SET next_attempt_id=NULL WHERE id=?`, r.recovery)
	})
	t.Run("new_job_progress_and_upload_chunk_tuple_remain_mutable", func(t *testing.T) {
		job := trainingSchemaJobPeer(t, s, r)
		for _, state := range []string{"preparing", "running", "cancelling", "recovery_required", "cancelled"} {
			migrationExec(t, s, `UPDATE training_jobs SET state=?,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, state, job)
		}
		migrationExec(t, s, `UPDATE training_uploads SET active_chunk_id=?,active_asset='safe.bin',active_offset=0,active_expires_at=? WHERE id=?`, uuid.NewString(), r.heartbeat, r.checkpoint)
		migrationExec(t, s, `UPDATE training_uploads SET active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL WHERE id=?`, r.checkpoint)
	})
	migrationRun(t, s, c, Readiness)
}

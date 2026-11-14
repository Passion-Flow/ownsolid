//go:build mysql_integration

package mysqlmigrate

import (
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func TestActualMySQLTrainingSchemaBounds(t *testing.T) {
	s, c, r := trainingSchemaConstraintLayer(t)
	var serverPacket, overhead int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT @@max_allowed_packet,OCTET_LENGTH(CAST(JSON_OBJECT('v','') AS CHAR CHARACTER SET utf8mb4))`).Scan(&serverPacket, &overhead); e != nil || serverPacket < 33554432 || overhead < 1 {
		t.Fatal("actual32MiB server packet profile and canonical JSON overhead required", mysqlstore.SafeError(e))
	}
	// The runner explicitly selects the documented32MiB driver profile. The16MiB
	// outcome plus SQL/protocol overhead must not depend on the old16MiB default.
	for _, item := range []struct {
		table, field, key, id, paired, reset string
		maximum                              int
	}{
		{"training_jobs", "configuration", "id", r.training, "", "", 16384},
		{"training_jobs", "execution_recipe", "id", r.training, "", "", 32768},
		{"training_attempts", "input_inventory", "id", r.trainingAttempt, "", "", 65536},
		{"training_attempts", "outcome", "id", r.trainingAttempt, ",outcome_sha256=REPEAT('a',64)", ",outcome_sha256=NULL", 16777216},
		{"training_attempts", "physical_stop_receipt", "id", r.trainingAttempt, ",physical_stop_sha256=REPEAT('a',64),physical_stopped_at=UTC_TIMESTAMP(6)", ",physical_stop_sha256=NULL,physical_stopped_at=NULL", 32768},
		{"training_events", "payload", "attempt_id", r.trainingAttempt, "", "", 16384},
		{"training_checkpoints", "receipt", "id", r.checkpoint, "", "", 1048576},
		{"training_checkpoints", "file_inventory", "id", r.checkpoint, "", "", 1048576},
		{"training_artifacts", "manifest", "id", r.artifact, "", "", 1048576},
		{"training_artifacts", "file_inventory", "id", r.artifact, "", "", 1048576},
		{"training_uploads", "manifest", "id", r.checkpoint, "", "", 1048576},
	} {
		t.Run(item.table+"_"+item.field+"_exact_byte_boundary", func(t *testing.T) {
			statement := "UPDATE " + item.table + " SET " + item.field + "=JSON_OBJECT('v',?)" + item.paired + " WHERE " + item.key + "=?"
			migrationExec(t, s, statement, strings.Repeat("x", item.maximum-overhead), item.id)
			var actual int
			if e := s.Pool().QueryRowContext(t.Context(), "SELECT OCTET_LENGTH(CAST("+item.field+" AS CHAR CHARACTER SET utf8mb4)) FROM "+item.table+" WHERE "+item.key+"=?", item.id).Scan(&actual); e != nil || actual != item.maximum {
				t.Fatal("exact persisted JSON byte boundary", mysqlstore.SafeError(e))
			}
			trainingSchemaRefuses(t, s, 3819, statement, strings.Repeat("x", item.maximum-overhead+1), item.id)
			value := "JSON_OBJECT()"
			if item.reset != "" {
				value = "NULL"
			}
			migrationExec(t, s, "UPDATE "+item.table+" SET "+item.field+"="+value+item.reset+" WHERE "+item.key+"=?", item.id)
		})
	}
	t.Run("manifest_raw_bytes_exact_boundary", func(t *testing.T) {
		migrationExec(t, s, `UPDATE training_uploads SET manifest_bytes=? WHERE id=?`, []byte(strings.Repeat("x", 1048576)), r.checkpoint)
		trainingSchemaRefuses(t, s, 3819, `UPDATE training_uploads SET manifest_bytes=? WHERE id=?`, []byte(strings.Repeat("x", 1048577)), r.checkpoint)
		migrationExec(t, s, `UPDATE training_uploads SET manifest_bytes=? WHERE id=?`, []byte("{}"), r.checkpoint)
	})
	t.Run("SQL_null_and_invalid_JSON_refusal", func(t *testing.T) {
		trainingSchemaRefuses(t, s, 1048, `UPDATE training_jobs SET configuration=NULL WHERE id=?`, r.training)
		trainingSchemaRefuses(t, s, 3140, `UPDATE training_jobs SET configuration=? WHERE id=?`, "{", r.training)
	})
	t.Run("safe_numeric_and_nullable_limits", func(t *testing.T) {
		migrationExec(t, s, `UPDATE training_jobs SET revision=9007199254740991,attempt_number=1000,wait_code=NULL,error_code=NULL WHERE id=?`, r.training)
		migrationExec(t, s, `UPDATE training_attempts SET last_event_sequence=1000000,last_global_step=1000,requested_epoch=0 WHERE id=?`, r.trainingAttempt)
		migrationExec(t, s, `UPDATE training_events SET sequence=1000000,global_step=1000 WHERE attempt_id=?`, r.trainingAttempt)
		migrationExec(t, s, `UPDATE training_upload_chunks SET size_bytes=8388608,byte_offset=8388608 WHERE upload_id=?`, r.checkpoint)
		trainingSchemaRefuses(t, s, 3819, `UPDATE training_attempts SET requested_epoch=-1 WHERE id=?`, r.trainingAttempt)
	})
	migrationRun(t, s, c, Readiness)
}

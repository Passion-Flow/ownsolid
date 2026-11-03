//go:build mysql_integration

package mysqlmigrate

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func preparationSchemaRowID(t *testing.T, r preparationSchemaRows, table string) string {
	t.Helper()
	switch table {
	case "model_preparations":
		return r.preparation
	case "model_preparation_attempts":
		return r.preparationAttempt
	case "model_preparation_recoveries":
		return r.preparationRecovery
	case "model_preparation_uploads", "model_preparation_artifacts":
		return r.preparationUpload
	}
	t.Fatal("owned preparation single-row identity required")
	return ""
}

func preparationSchemaChangedValue(r preparationSchemaRows, field string) any {
	switch field {
	case "reason":
		return "Synthetic immutable-input mutation"
	case "creation_audience", "requested_audience":
		return "portal"
	case "requested_epoch":
		return int64(2)
	case "attempt_number":
		return int64(2)
	case "next_attempt_number":
		return int64(3)
	case "gpu_uuid":
		return "GPU-" + uuid.NewString()
	case "created_at":
		return r.heartbeat.Add(time.Second)
	case "input_snapshot", "configuration", "execution_recipe", "manifest", "descriptor":
		return `{"changed":true}`
	case "manifest_bytes":
		return []byte(`{"changed":true}`)
	case "creation_fingerprint":
		return bytes.Repeat([]byte{'z'}, 32)
	}
	if strings.HasSuffix(field, "sha256") {
		return strings.Repeat("b", 64)
	}
	return uuid.NewString()
}

func TestActualMySQLPreparationSchemaImmutability(t *testing.T) {
	s, c, r := preparationSchemaLayer(t, preparationSchemaStepCount)
	// Install synthetic first writes once. Refusal leaves independently select
	// these same existing proofs, including when only one leaf is requested.
	migrationExec(t, s, `UPDATE model_preparation_attempts SET stop_token_hash=? WHERE id=?`, bytes.Repeat([]byte{'t'}, 32), r.preparationAttempt)
	migrationExec(t, s, `UPDATE model_preparation_recoveries SET dispatched_attempt_id=?,applied_at=? WHERE id=?`, r.preparationAttempt, r.heartbeat, r.preparationRecovery)
	for _, item := range []struct {
		table  string
		fields []string
	}{
		{"model_preparations", []string{"id", "tenant_id", "workspace_id", "node_id", "model_id", "training_job_id", "training_attempt_id", "training_artifact_id", "heldout_id", "gpu_uuid", "input_snapshot", "input_sha256", "configuration", "configuration_sha256", "execution_recipe", "created_by", "creation_audience", "reason", "creation_key", "creation_fingerprint", "created_at"}},
		{"model_preparation_attempts", []string{"id", "tenant_id", "workspace_id", "preparation_id", "node_id", "task_id", "attempt_number", "requested_by", "requested_audience", "requested_epoch", "membership_authority", "command_sha256", "created_at"}},
		{"model_preparation_recoveries", []string{"id", "tenant_id", "workspace_id", "preparation_id", "previous_attempt_id", "previous_node_id", "next_node_id", "next_attempt_number", "requested_by", "requested_audience", "requested_epoch", "membership_authority", "reason", "creation_key", "creation_fingerprint", "created_at"}},
		{"model_preparation_uploads", []string{"id", "tenant_id", "workspace_id", "preparation_id", "attempt_id", "manifest_sha256", "manifest", "manifest_bytes", "descriptor", "created_at"}},
	} {
		for _, field := range item.fields {
			t.Run(item.table+"__"+field, func(t *testing.T) {
				preparationSchemaRefuses(t, s, 1644, "UPDATE "+item.table+" SET `"+field+"`=? WHERE id=?", preparationSchemaChangedValue(r, field), preparationSchemaRowID(t, r, item.table))
			})
		}
	}
	for _, table := range preparationSchemaTables {
		t.Run(table+"__delete", func(t *testing.T) {
			preparationSchemaRefuses(t, s, 1644, "DELETE FROM "+table)
		})
	}
	for _, item := range []struct {
		table, assignment, predicate string
		arguments                    []any
	}{
		{"model_preparation_events", "kind='evaluation_progress'", "attempt_id=? AND sequence=1", []any{r.preparationAttempt}},
		{"model_preparation_upload_chunks", "size_bytes=3", "upload_id=? AND asset='synthetic.bin' AND byte_offset=0", []any{r.preparationUpload}},
		{"model_preparation_artifacts", "descriptor=JSON_OBJECT('changed',CAST('true' AS JSON))", "id=?", []any{r.preparationUpload}},
	} {
		t.Run(item.table+"__update", func(t *testing.T) {
			preparationSchemaRefuses(t, s, 1644, "UPDATE "+item.table+" SET "+item.assignment+" WHERE "+item.predicate, item.arguments...)
		})
	}
	for _, item := range []struct {
		name, table, assignment, id string
		arguments                   []any
	}{
		{"attempt_stop_token__clear", "model_preparation_attempts", "stop_token_hash=NULL", r.preparationAttempt, nil},
		{"attempt_stop_token__rewrite", "model_preparation_attempts", "stop_token_hash=?", r.preparationAttempt, []any{bytes.Repeat([]byte{'z'}, 32)}},
		{"attempt_stop_proof__clear", "model_preparation_attempts", "physical_stop_receipt=NULL,physical_stop_sha256=NULL,physical_stopped_at=NULL", r.preparationAttempt, nil},
		{"attempt_stop_proof__rewrite", "model_preparation_attempts", "physical_stop_receipt=JSON_OBJECT('changed',CAST('true' AS JSON)),physical_stop_sha256=?,physical_stopped_at=?", r.preparationAttempt, []any{hash(`{"changed":true}`), r.heartbeat.Add(time.Second)}},
		{"attempt_outcome__clear", "model_preparation_attempts", "outcome=NULL,outcome_sha256=NULL,outcome_bytes=NULL", r.preparationAttempt, nil},
		{"attempt_outcome__rewrite", "model_preparation_attempts", "outcome=JSON_OBJECT('changed',CAST('true' AS JSON)),outcome_sha256=?,outcome_bytes=?", r.preparationAttempt, []any{hash(`{"changed":true}`), []byte(`{"changed":true}`)}},
		{"recovery_applied__clear", "model_preparation_recoveries", "dispatched_attempt_id=NULL,applied_at=NULL", r.preparationRecovery, nil},
		{"recovery_applied__rewrite", "model_preparation_recoveries", "applied_at=?", r.preparationRecovery, []any{r.heartbeat.Add(time.Second)}},
		{"upload_accepted__clear", "model_preparation_uploads", "state='uploading',accepted_at=NULL", r.preparationUpload, nil},
		{"upload_accepted__rewrite", "model_preparation_uploads", "accepted_at=?", r.preparationUpload, []any{r.heartbeat.Add(time.Second)}},
	} {
		t.Run(item.name, func(t *testing.T) {
			preparationSchemaRefuses(t, s, 1644, "UPDATE "+item.table+" SET "+item.assignment+" WHERE id=?", append(item.arguments, item.id)...)
		})
	}
	migrationRun(t, s, c, Readiness)
}

func preparationSchemaEmptyAttempt(t *testing.T, s *mysqlstore.Store, r preparationSchemaRows) string {
	t.Helper()
	values := preparationSchemaCopyValues(t, s, r, "model_preparation_attempts")
	values["outcome"], values["outcome_sha256"], values["outcome_bytes"] = nil, nil, nil
	values["physical_stop_receipt"], values["physical_stop_sha256"], values["physical_stopped_at"] = nil, nil, nil
	values["stop_token_hash"], values["last_event_sequence"], values["event_bytes"] = nil, int64(0), int64(0)
	preparationSchemaInsertCopy(t, s, r, "model_preparation_attempts", values)
	return values["id"].(string)
}

func TestActualMySQLPreparationSchemaMutableTransitions(t *testing.T) {
	s, c, r := preparationSchemaLayer(t, preparationSchemaStepCount)
	t.Run("preparation_lifecycle", func(t *testing.T) {
		for index, state := range []string{"queued", "preparing", "running", "cancelling", "cancelled"} {
			migrationExec(t, s, `UPDATE model_preparations SET state=?,revision=revision+1,requested_epoch=2,wait_code='SYNTHETIC_WAIT',error_code='SYNTHETIC_ERROR',cancel_requested_at=?,cancel_requested_by=?,next_dispatch_at=?,updated_at=?,finished_at=? WHERE id=?`, state, r.heartbeat, r.user, r.heartbeat.Add(time.Minute), r.heartbeat.Add(time.Second), r.heartbeat.Add(time.Second), r.preparation)
			var actual string
			var revision int64
			if e := s.Pool().QueryRowContext(t.Context(), `SELECT state,revision FROM model_preparations WHERE id=?`, r.preparation).Scan(&actual, &revision); e != nil || actual != state || revision != int64(index+3) {
				t.Fatal("preparation lifecycle/revision did not persist", mysqlstore.SafeError(e))
			}
		}
	})
	t.Run("attempt_event_progress", func(t *testing.T) {
		id := preparationSchemaEmptyAttempt(t, s, r)
		tx, e := s.Pool().BeginTx(t.Context(), nil)
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		defer tx.Rollback()
		_, e = tx.ExecContext(t.Context(), `INSERT INTO model_preparation_events(tenant_id,workspace_id,preparation_id,attempt_id,sequence,kind,payload,canonical_sha256) VALUES(?,?,?,?,1,'evaluation_progress',JSON_OBJECT(),?)`, r.tenant, r.workspace, r.preparation, id, hash("{}"))
		if e == nil {
			_, e = tx.ExecContext(t.Context(), `UPDATE model_preparation_attempts SET state='running',last_event_sequence=1,event_bytes=2,started_at=?,updated_at=? WHERE id=?`, r.heartbeat, r.heartbeat, id)
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			t.Fatal("atomic attempt event progress", mysqlstore.SafeError(e))
		}
		var count int
		if e = s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_preparation_attempts a JOIN model_preparation_events e ON e.attempt_id=a.id AND e.preparation_id=a.preparation_id AND e.tenant_id=a.tenant_id AND e.workspace_id=a.workspace_id AND e.sequence=a.last_event_sequence WHERE a.id=? AND a.state='running' AND a.event_bytes=2 AND a.started_at=?`, id, r.heartbeat).Scan(&count); e != nil || count != 1 {
			t.Fatal("atomic event/counter readback", mysqlstore.SafeError(e))
		}
	})
	t.Run("attempt_outcome_first_write", func(t *testing.T) {
		id := preparationSchemaEmptyAttempt(t, s, r)
		raw := []byte(`{"synthetic_outcome":true}`)
		checksum := hash(string(raw))
		query := `UPDATE model_preparation_attempts SET outcome=?,outcome_bytes=?,outcome_sha256=? WHERE id=?`
		migrationExec(t, s, query, string(raw), raw, checksum, id)
		// Repeating exactly the same proof is permitted; changing it is covered
		// by the separate immutable clear/rewrite controls.
		migrationExec(t, s, query, string(raw), raw, checksum, id)
		var actual []byte
		var actualHash string
		var nativeMatches bool
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT outcome_bytes,outcome_sha256,JSON_EXTRACT(outcome,'$.synthetic_outcome')=CAST('true' AS JSON) FROM model_preparation_attempts WHERE id=?`, id).Scan(&actual, &actualHash, &nativeMatches); e != nil || !bytes.Equal(actual, raw) || actualHash != checksum || !nativeMatches {
			t.Fatal("exact first outcome proof readback", mysqlstore.SafeError(e))
		}
	})
	t.Run("attempt_stop_token_first_write", func(t *testing.T) {
		id := preparationSchemaEmptyAttempt(t, s, r)
		expected := bytes.Repeat([]byte{'t'}, 32)
		migrationExec(t, s, `UPDATE model_preparation_attempts SET stop_token_hash=? WHERE id=?`, expected, id)
		var actual []byte
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT stop_token_hash FROM model_preparation_attempts WHERE id=?`, id).Scan(&actual); e != nil || !bytes.Equal(actual, expected) {
			t.Fatal("exact first stop authority readback", mysqlstore.SafeError(e))
		}
	})
	t.Run("attempt_stop_proof_first_write", func(t *testing.T) {
		id := preparationSchemaEmptyAttempt(t, s, r)
		checksum := hash(`{"synthetic_stop":true}`)
		migrationExec(t, s, `UPDATE model_preparation_attempts SET physical_stop_receipt=JSON_OBJECT('synthetic_stop',CAST('true' AS JSON)),physical_stop_sha256=?,physical_stopped_at=? WHERE id=?`, checksum, r.heartbeat, id)
		var actualHash string
		var stamp time.Time
		var nativeMatches bool
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT physical_stop_sha256,physical_stopped_at,JSON_EXTRACT(physical_stop_receipt,'$.synthetic_stop')=CAST('true' AS JSON) FROM model_preparation_attempts WHERE id=?`, id).Scan(&actualHash, &stamp, &nativeMatches); e != nil || actualHash != checksum || !stamp.Equal(r.heartbeat) || !nativeMatches {
			t.Fatal("exact first synthetic stop proof readback", mysqlstore.SafeError(e))
		}
	})
	t.Run("recovery_first_dispatch", func(t *testing.T) {
		values := preparationSchemaCopyValues(t, s, r, "model_preparation_recoveries")
		preparationSchemaInsertCopy(t, s, r, "model_preparation_recoveries", values)
		id := values["id"]
		migrationExec(t, s, `UPDATE model_preparation_recoveries SET dispatched_attempt_id=?,applied_at=? WHERE id=?`, r.preparationAttempt, r.heartbeat, id)
		var actual string
		var stamp time.Time
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT dispatched_attempt_id,applied_at FROM model_preparation_recoveries WHERE id=?`, id).Scan(&actual, &stamp); e != nil || actual != r.preparationAttempt || !stamp.Equal(r.heartbeat) {
			t.Fatal("first recovery dispatch did not persist", mysqlstore.SafeError(e))
		}
	})
	newUpload := func(t *testing.T) string {
		values := preparationSchemaCopyValues(t, s, r, "model_preparation_uploads")
		values["state"], values["accepted_at"] = "uploading", nil
		preparationSchemaInsertCopy(t, s, r, "model_preparation_uploads", values)
		return values["id"].(string)
	}
	t.Run("upload_chunk_claim_release", func(t *testing.T) {
		id, chunk := newUpload(t), uuid.NewString()
		expires := r.heartbeat.Add(time.Minute)
		migrationExec(t, s, `UPDATE model_preparation_uploads SET active_chunk_id=?,active_asset='synthetic.bin',active_offset=0,active_expires_at=? WHERE id=?`, chunk, expires, id)
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_preparation_uploads WHERE id=? AND active_chunk_id=? AND active_asset='synthetic.bin' AND active_offset=0 AND active_expires_at=?`, id, chunk, expires).Scan(&count); e != nil || count != 1 {
			t.Fatal("complete active chunk claim required", mysqlstore.SafeError(e))
		}
		migrationExec(t, s, `UPDATE model_preparation_uploads SET active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL WHERE id=?`, id)
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_preparation_uploads WHERE id=? AND active_chunk_id IS NULL AND active_asset IS NULL AND active_offset IS NULL AND active_expires_at IS NULL`, id).Scan(&count); e != nil || count != 1 {
			t.Fatal("complete chunk release required", mysqlstore.SafeError(e))
		}
	})
	t.Run("upload_first_acceptance", func(t *testing.T) {
		id := newUpload(t)
		migrationExec(t, s, `UPDATE model_preparation_uploads SET state='accepted',accepted_at=?,updated_at=? WHERE id=?`, r.heartbeat, r.heartbeat, id)
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_preparation_uploads WHERE id=? AND state='accepted' AND accepted_at=? AND updated_at=?`, id, r.heartbeat, r.heartbeat).Scan(&count); e != nil || count != 1 {
			t.Fatal("first upload acceptance did not persist", mysqlstore.SafeError(e))
		}
	})
	migrationRun(t, s, c, Readiness)
}

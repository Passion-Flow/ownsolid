//go:build mysql_integration

package mysqlmigrate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

var preparationSchemaTables = []string{"model_preparations", "model_preparation_attempts", "model_preparation_events", "model_preparation_recoveries", "model_preparation_uploads", "model_preparation_upload_chunks", "model_preparation_artifacts"}

type preparationSchemaRows struct {
	holdoutSchemaRows
	preparation, preparationAttempt, preparationRecovery, preparationUpload string
	preparationNode, preparationTask, preparationGPU                        string
}

// Synthetic SQL metadata only. These records are not source analysis, completed
// preparation, real model files, physical stop proof or human review evidence.
func preparationSchemaSeed(t *testing.T, s *mysqlstore.Store, old holdoutSchemaRows) preparationSchemaRows {
	t.Helper()
	r := preparationSchemaRows{holdoutSchemaRows: old, preparation: uuid.NewString(), preparationAttempt: uuid.NewString(), preparationRecovery: uuid.NewString(), preparationUpload: uuid.NewString(), preparationNode: uuid.NewString(), preparationTask: uuid.NewString(), preparationGPU: "GPU-" + uuid.NewString()}
	raw := []byte(`{"synthetic_schema":true}`)
	digest := hash(string(raw))
	fingerprint := []byte(strings.Repeat("x", 32))
	credential := sha256.Sum256([]byte(r.preparationNode))
	migrationExec(t, s, nodeSchemaInsertNode, r.preparationNode, r.tenant, r.workspace, "Synthetic preparation node "+r.preparationNode, credential[:])
	migrationExec(t, s, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint) VALUES(?,?,?,?,'model.prepare.v1','pending',?,?,TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)),JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?)`, r.preparationTask, r.tenant, r.workspace, r.preparationNode, uuid.NewString(), r.user, fingerprint)
	migrationExec(t, s, "INSERT INTO model_preparations(id,tenant_id,workspace_id,node_id,current_node_id,model_id,training_job_id,training_attempt_id,training_artifact_id,heldout_id,gpu_uuid,input_snapshot,input_sha256,configuration,configuration_sha256,execution_recipe,created_by,requested_by,creation_audience,requested_audience,requested_epoch,reason,creation_key,creation_fingerprint,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?,JSON_OBJECT(),?,JSON_OBJECT(),?,?,'admin','admin',1,'Synthetic preparation metadata \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629',?,?,'queued',?,?)", r.preparation, r.tenant, r.workspace, r.preparationNode, r.preparationNode, r.model, r.training, r.trainingAttempt, r.artifact, r.holdout, r.preparationGPU, digest, hash("{}"), r.user, r.user, uuid.NewString(), fingerprint, r.heartbeat, r.heartbeat)
	migrationExec(t, s, `INSERT INTO model_preparation_attempts(id,tenant_id,workspace_id,preparation_id,node_id,task_id,attempt_number,state,requested_by,requested_audience,requested_epoch,command_sha256,last_event_sequence,event_bytes,outcome_sha256,outcome,outcome_bytes,physical_stop_receipt,physical_stop_sha256,physical_stopped_at,created_at,updated_at) VALUES(?,?,?,?,?,?,1,'prepared',?,'admin',1,?,1,1,?,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?,?,?,?)`, r.preparationAttempt, r.tenant, r.workspace, r.preparation, r.preparationNode, r.preparationTask, r.user, digest, digest, raw, digest, r.heartbeat, r.heartbeat, r.heartbeat)
	migrationExec(t, s, `INSERT INTO model_preparation_events(tenant_id,workspace_id,preparation_id,attempt_id,sequence,kind,payload,canonical_sha256,created_at) VALUES(?,?,?,?,1,'phase',JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?,?)`, r.tenant, r.workspace, r.preparation, r.preparationAttempt, digest, r.heartbeat)
	migrationExec(t, s, `INSERT INTO model_preparation_recoveries(id,tenant_id,workspace_id,preparation_id,previous_attempt_id,previous_node_id,next_node_id,next_attempt_number,requested_by,requested_audience,requested_epoch,reason,creation_key,creation_fingerprint,created_at) VALUES(?,?,?,?,?,?,?,2,?,'admin',1,'Synthetic recovery metadata',?,?,?)`, r.preparationRecovery, r.tenant, r.workspace, r.preparation, r.preparationAttempt, r.preparationNode, r.preparationNode, r.user, uuid.NewString(), fingerprint, r.heartbeat)
	migrationExec(t, s, `INSERT INTO model_preparation_uploads(id,tenant_id,workspace_id,preparation_id,attempt_id,manifest_sha256,manifest,manifest_bytes,descriptor,state,accepted_at,created_at,updated_at) VALUES(?,?,?,?,?,?,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?,JSON_OBJECT(),'accepted',?,?,?)`, r.preparationUpload, r.tenant, r.workspace, r.preparation, r.preparationAttempt, digest, raw, r.heartbeat, r.heartbeat, r.heartbeat)
	migrationExec(t, s, `INSERT INTO model_preparation_upload_chunks(upload_id,asset,byte_offset,size_bytes,sha256,created_at) VALUES(?,'synthetic.bin',0,2,?,?)`, r.preparationUpload, hash("{}"), r.heartbeat)
	migrationExec(t, s, `INSERT INTO model_preparation_artifacts(id,tenant_id,workspace_id,preparation_id,attempt_id,upload_id,manifest_sha256,descriptor,report,report_bytes,storage_path,created_at) VALUES(?,?,?,?,?,?,?,JSON_OBJECT(),JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?,?,?)`, r.preparationUpload, r.tenant, r.workspace, r.preparation, r.preparationAttempt, r.preparationUpload, digest, raw, "artifacts/"+r.preparation+"/"+r.preparationAttempt+"/"+r.preparationUpload, r.heartbeat)
	migrationExec(t, s, `UPDATE model_preparations SET state='awaiting_review',attempt_number=1,current_attempt_id=?,pending_recovery_id=?,revision=2 WHERE id=?`, r.preparationAttempt, r.preparationRecovery, r.preparation)
	return r
}

func preparationSchemaLayer(t *testing.T, count int) (*mysqlstore.Store, Catalog, preparationSchemaRows) {
	t.Helper()
	s, c, _ := actualMigration(t)
	if len(c.steps) < preparationSchemaStepCount || count != 103 && count != preparationSchemaStepCount {
		t.Fatal("complete measured preparation catalog/layer required")
	}
	c.steps = c.steps[:count]
	migrationRun(t, s, c, Apply)
	old := holdoutSchemaSeed(t, s, holdoutSchemaOldRows(t, s))
	return s, c, preparationSchemaSeed(t, s, old)
}

func preparationSchemaDataDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	result := holdoutSchemaDataDigest(t, s)
	for _, table := range preparationSchemaTables {
		result += trainingSchemaColumnDigest(t, s, table, holdoutSchemaDigestColumns(t, s, table))
	}
	result += licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(id,model_preparation_id) FROM gpu_leases ORDER BY id`)
	result += licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step BETWEEN 95 AND 117 ORDER BY step`)
	return hash(result)
}

func preparationSchemaRefuses(t *testing.T, s *mysqlstore.Store, number uint16, statement string, arguments ...any) {
	t.Helper()
	before := preparationSchemaDataDigest(t, s)
	_, e := s.Pool().ExecContext(t.Context(), statement, arguments...)
	var safe *mysqlstore.Error
	if !errors.As(mysqlstore.SafeError(e), &safe) {
		t.Fatal("preparation refusal missing safe numeric category")
	}
	if safe.Number != number {
		t.Fatalf("PREPARATION_REFUSAL_CATEGORY_MISMATCH expected_number=%d actual_number=%d state_hex=%x code=%s", number, safe.Number, safe.SQLState, safe.Error())
	}
	if preparationSchemaDataDigest(t, s) != before {
		t.Fatal("refused preparation statement changed complete persisted data/journal")
	}
}

func preparationSchemaAssertRows(t *testing.T, s *mysqlstore.Store, r preparationSchemaRows) {
	t.Helper()
	for _, table := range preparationSchemaTables {
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); e != nil || count != 1 {
			t.Fatal("complete actual preparation graph required", table, mysqlstore.SafeError(e))
		}
	}
	var state, attempt, recovery, reason string
	var stamp time.Time
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT state,current_attempt_id,pending_recovery_id,reason,created_at FROM model_preparations WHERE id=? AND tenant_id=? AND workspace_id=?`, r.preparation, r.tenant, r.workspace).Scan(&state, &attempt, &recovery, &reason, &stamp); e != nil || state != "awaiting_review" || attempt != r.preparationAttempt || recovery != r.preparationRecovery || reason != "Synthetic preparation metadata \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || !stamp.Equal(r.heartbeat) || stamp.Location() != time.UTC {
		t.Fatal("preparation scope/backreference/Unicode/UTC readback", mysqlstore.SafeError(e))
	}
	var outcome, canonical []byte
	var checksum string
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT outcome,outcome_bytes,outcome_sha256 FROM model_preparation_attempts WHERE id=? AND preparation_id=? AND tenant_id=? AND workspace_id=?`, r.preparationAttempt, r.preparation, r.tenant, r.workspace).Scan(&outcome, &canonical, &checksum); e != nil {
		t.Fatal("preparation outcome actual readback", mysqlstore.SafeError(e))
	}
	var value map[string]bool
	if json.Unmarshal(outcome, &value) != nil || !value["synthetic_schema"] || string(canonical) != `{"synthetic_schema":true}` || hash(string(canonical)) != checksum {
		t.Fatal("native JSON/canonical bytes/digest changed")
	}
	var id, upload, path string
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT id,upload_id,storage_path FROM model_preparation_artifacts WHERE preparation_id=? AND attempt_id=? AND tenant_id=? AND workspace_id=?`, r.preparation, r.preparationAttempt, r.tenant, r.workspace).Scan(&id, &upload, &path); e != nil || id != r.preparationUpload || id != upload || path != "artifacts/"+r.preparation+"/"+r.preparationAttempt+"/"+r.preparationUpload {
		t.Fatal("retained artifact scoped upload/storage identity", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLPreparationSchemaFresh(t *testing.T) {
	s, c, r := preparationSchemaLayer(t, preparationSchemaStepCount)
	preparationSchemaAssertRows(t, s, r)
	before := preparationSchemaDataDigest(t, s)
	migrationRun(t, s, c, Apply)
	migrationRun(t, s, c, Readiness)
	if preparationSchemaDataDigest(t, s) != before {
		t.Fatal("current preparation no-op changed complete persisted graph/journal")
	}
}

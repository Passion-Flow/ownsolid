//go:build mysql_integration

package mysqlmigrate

import (
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func preparationSchemaForeignGraph(t *testing.T, s *mysqlstore.Store, r preparationSchemaRows, tenant string) preparationSchemaRows {
	t.Helper()
	f := modelInferenceForeign(t, s, r.modelInferenceSchemaRows, tenant)
	base := r.modelInferenceSchemaRows
	base.tenant, base.workspace, base.node, base.model, base.task = f.tenant, f.workspace, f.node, f.model, f.task
	return preparationSchemaSeed(t, s, holdoutSchemaSeed(t, s, trainingSchemaSeed(t, s, datasetSchemaSeed(t, s, base))))
}

func preparationSchemaArtifactPath(r preparationSchemaRows, values map[string]any) {
	preparation, attempt, id := r.preparation, r.preparationAttempt, r.preparationUpload
	if value, found := values["preparation_id"]; found {
		preparation = value.(string)
	}
	if value, found := values["attempt_id"]; found {
		attempt = value.(string)
	}
	if value, found := values["id"]; found {
		id = value.(string)
	}
	values["storage_path"] = "artifacts/" + preparation + "/" + attempt + "/" + id
}

func TestActualMySQLPreparationSchemaForeignScopes(t *testing.T) {
	s, c, r := preparationSchemaLayer(t, 103)
	same := preparationSchemaForeignGraph(t, s, r, r.tenant)
	tenant := uuid.NewString()
	migrationExec(t, s, `INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic preparation foreign tenant','active')`, tenant)
	foreign := preparationSchemaForeignGraph(t, s, r, tenant)
	for _, scope := range []struct {
		name string
		rows preparationSchemaRows
	}{{"foreign_Workspace", same}, {"foreign_tenant", foreign}} {
		peer := scope.rows
		unusedAttempt := preparationSchemaUnusedAttempt(t, s, peer)
		unusedUpload, uploadAttempt := uuid.NewString(), preparationSchemaUnusedAttempt(t, s, peer)
		preparationSchemaInsertCopy(t, s, peer, "model_preparation_uploads", map[string]any{"id": unusedUpload, "attempt_id": uploadAttempt})
		unusedTask := preparationSchemaCopyValues(t, s, peer, "model_preparation_attempts")["task_id"].(string)
		for _, item := range []struct{ name, table, field, value string }{
			{"preparation_node", "model_preparations", "node_id", peer.preparationNode},
			{"preparation_current_node", "model_preparations", "current_node_id", peer.preparationNode},
			{"preparation_model", "model_preparations", "model_id", peer.model},
			{"preparation_training_job", "model_preparations", "training_job_id", peer.training},
			{"preparation_training_attempt", "model_preparations", "training_attempt_id", peer.trainingAttempt},
			{"preparation_training_artifact", "model_preparations", "training_artifact_id", peer.artifact},
			{"preparation_heldout", "model_preparations", "heldout_id", peer.holdout},
			{"preparation_current_attempt", "model_preparations", "current_attempt_id", unusedAttempt},
			{"preparation_pending_recovery", "model_preparations", "pending_recovery_id", peer.preparationRecovery},
			{"attempt_preparation", "model_preparation_attempts", "preparation_id", peer.preparation},
			{"attempt_node", "model_preparation_attempts", "node_id", peer.preparationNode},
			{"attempt_node_task", "model_preparation_attempts", "task_id", unusedTask},
			{"event_attempt", "model_preparation_events", "attempt_id", unusedAttempt},
			{"recovery_preparation", "model_preparation_recoveries", "preparation_id", peer.preparation},
			{"recovery_previous_attempt", "model_preparation_recoveries", "previous_attempt_id", unusedAttempt},
			{"recovery_previous_node", "model_preparation_recoveries", "previous_node_id", peer.preparationNode},
			{"recovery_next_node", "model_preparation_recoveries", "next_node_id", peer.preparationNode},
			{"recovery_dispatched_attempt", "model_preparation_recoveries", "dispatched_attempt_id", unusedAttempt},
			{"upload_attempt", "model_preparation_uploads", "attempt_id", unusedAttempt},
			{"artifact_attempt", "model_preparation_artifacts", "attempt_id", unusedAttempt},
			{"artifact_upload", "model_preparation_artifacts", "upload_id", unusedUpload},
		} {
			t.Run(item.name+"__"+scope.name, func(t *testing.T) {
				if item.name == "preparation_current_attempt" || item.name == "preparation_pending_recovery" {
					preparationSchemaRefuses(t, s, 1452, "UPDATE model_preparations SET "+item.field+"=? WHERE id=?", item.value, r.preparation)
					return
				}
				values := preparationSchemaCopyValues(t, s, r, item.table)
				values[item.field] = item.value
				if item.field == "dispatched_attempt_id" {
					values["applied_at"] = r.heartbeat
				}
				if item.name == "artifact_upload" {
					values["id"] = item.value
				}
				if item.table == "model_preparation_artifacts" {
					preparationSchemaArtifactPath(r, values)
				}
				query, arguments := preparationSchemaCopySQL(t, s, r, item.table, values)
				preparationSchemaRefuses(t, s, 1452, query, arguments...)
			})
		}
		t.Run("GPU_preparation_owner__"+scope.name, func(t *testing.T) {
			preparationSchemaRefuses(t, s, 1452, `UPDATE gpu_leases SET deployment_id=NULL,training_job_id=NULL,model_preparation_id=? WHERE deployment_id=?`, peer.preparation, r.deployment)
		})
	}
	// A distinct actual graph inside the same Workspace isolates parent-pair
	// integrity from the independently exercised tenant and Workspace boundaries.
	peer := preparationSchemaSeed(t, s, holdoutSchemaSeed(t, s, trainingSchemaSeed(t, s, r.datasetSchemaRows)))
	unusedAttempt := preparationSchemaUnusedAttempt(t, s, peer)
	unusedUpload := uuid.NewString()
	preparationSchemaInsertCopy(t, s, peer, "model_preparation_uploads", map[string]any{"id": unusedUpload, "attempt_id": unusedAttempt})
	unusedTask := preparationSchemaCopyValues(t, s, peer, "model_preparation_attempts")["task_id"].(string)
	for _, item := range []struct{ name, table, field, value string }{
		{"preparation__training_attempt_job_pair", "model_preparations", "training_attempt_id", peer.trainingAttempt},
		{"preparation__training_artifact_job_pair", "model_preparations", "training_artifact_id", peer.artifact},
		{"preparation__current_attempt_preparation_pair", "model_preparations", "current_attempt_id", unusedAttempt},
		{"preparation__pending_recovery_preparation_pair", "model_preparations", "pending_recovery_id", peer.preparationRecovery},
		{"attempt__task_node_pair", "model_preparation_attempts", "task_id", unusedTask},
		{"event__attempt_preparation_pair", "model_preparation_events", "attempt_id", unusedAttempt},
		{"recovery__previous_attempt_preparation_pair", "model_preparation_recoveries", "previous_attempt_id", unusedAttempt},
		{"recovery__dispatched_attempt_preparation_pair", "model_preparation_recoveries", "dispatched_attempt_id", unusedAttempt},
		{"upload__attempt_preparation_pair", "model_preparation_uploads", "attempt_id", preparationSchemaUnusedAttempt(t, s, peer)},
		{"artifact__upload_attempt_pair", "model_preparation_artifacts", "upload_id", unusedUpload},
	} {
		t.Run(item.name, func(t *testing.T) {
			if item.field == "current_attempt_id" || item.field == "pending_recovery_id" {
				preparationSchemaRefuses(t, s, 1452, "UPDATE model_preparations SET "+item.field+"=? WHERE id=?", item.value, r.preparation)
				return
			}
			values := preparationSchemaCopyValues(t, s, r, item.table)
			values[item.field] = item.value
			if item.field == "dispatched_attempt_id" {
				values["applied_at"] = r.heartbeat
			}
			if item.table == "model_preparation_artifacts" {
				values["id"] = item.value
				preparationSchemaArtifactPath(r, values)
			}
			query, arguments := preparationSchemaCopySQL(t, s, r, item.table, values)
			preparationSchemaRefuses(t, s, 1452, query, arguments...)
		})
	}
	for _, item := range []struct {
		table, id string
		number    uint16
	}{
		{"model_preparations", r.preparation, 1451}, {"model_preparation_attempts", r.preparationAttempt, 1451},
		{"model_preparation_uploads", r.preparationUpload, 1451}, {"model_holdouts", r.holdout, 1644},
		{"training_jobs", r.training, 1451}, {"training_attempts", r.trainingAttempt, 1451},
		{"training_artifacts", r.artifact, 1644}, {"managed_nodes", r.preparationNode, 1451},
	} {
		t.Run(item.table+"__referenced_delete", func(t *testing.T) {
			// Existing immutable held-out/training artifacts refuse before FK
			// evaluation. Preserve those guards rather than disabling triggers.
			preparationSchemaRefuses(t, s, item.number, "DELETE FROM "+item.table+" WHERE id=?", item.id)
		})
	}
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLPreparationSchemaUniqueness(t *testing.T) {
	s, c, r := preparationSchemaLayer(t, 103)
	var preparationKey, recoveryKey string
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT creation_key FROM model_preparations WHERE id=?`, r.preparation).Scan(&preparationKey); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT creation_key FROM model_preparation_recoveries WHERE id=?`, r.preparationRecovery).Scan(&recoveryKey); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	for _, item := range []struct {
		name, table, field string
		value              any
	}{
		{"preparation_creation", "model_preparations", "creation_key", preparationKey},
		{"attempt_task", "model_preparation_attempts", "task_id", r.preparationTask},
		{"attempt_number", "model_preparation_attempts", "attempt_number", int64(1)},
		{"event_sequence", "model_preparation_events", "sequence", int64(1)},
		{"recovery_creation", "model_preparation_recoveries", "creation_key", recoveryKey},
		{"upload_attempt", "model_preparation_uploads", "attempt_id", r.preparationAttempt},
		{"artifact_attempt", "model_preparation_artifacts", "attempt_id", r.preparationAttempt},
		{"artifact_upload", "model_preparation_artifacts", "upload_id", r.preparationUpload},
		{"chunk_primary", "model_preparation_upload_chunks", "asset", "synthetic.bin"},
	} {
		t.Run(item.name, func(t *testing.T) {
			values := preparationSchemaCopyValues(t, s, r, item.table)
			values[item.field] = item.value
			if item.table == "model_preparation_artifacts" {
				// id=upload_id intentionally makes the upload key redundant with
				// the primary key; exact measured metadata also owns both indexes.
				if item.field == "upload_id" {
					values["id"] = item.value
				}
				preparationSchemaArtifactPath(r, values)
			}
			query, arguments := preparationSchemaCopySQL(t, s, r, item.table, values)
			preparationSchemaRefuses(t, s, 1062, query, arguments...)
		})
	}
	t.Run("distinct_Workspace_creation_allowed", func(t *testing.T) {
		peer := preparationSchemaForeignGraph(t, s, r, r.tenant)
		values := preparationSchemaCopyValues(t, s, peer, "model_preparations")
		values["creation_key"] = preparationKey
		preparationSchemaInsertCopy(t, s, peer, "model_preparations", values)
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_preparations WHERE tenant_id=? AND created_by=? AND creation_audience='admin' AND creation_key=? AND workspace_id IN (?,?)`, r.tenant, r.user, preparationKey, r.workspace, peer.workspace).Scan(&count); e != nil || count != 2 {
			t.Fatal("distinct Workspace preparation creation did not persist", mysqlstore.SafeError(e))
		}
	})
	migrationRun(t, s, c, Readiness)
}

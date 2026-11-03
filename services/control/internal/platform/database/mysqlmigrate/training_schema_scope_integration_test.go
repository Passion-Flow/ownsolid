//go:build mysql_integration

package mysqlmigrate

import (
	"testing"

	"github.com/google/uuid"
)

func TestActualMySQLTrainingSchemaForeignScopes(t *testing.T) {
	s, c, r := trainingSchemaConstraintLayer(t)
	for _, kind := range []string{"job", "workspace", "tenant"} {
		old := r.datasetSchemaRows
		if kind != "job" {
			tenant := r.tenant
			if kind == "tenant" {
				tenant = uuid.NewString()
				migrationExec(t, s, `INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic training tenant','active')`, tenant)
			}
			f := modelInferenceForeign(t, s, r.modelInferenceSchemaRows, tenant)
			base := r.modelInferenceSchemaRows
			base.tenant, base.workspace, base.node, base.model, base.task = f.tenant, f.workspace, f.node, f.model, f.task
			old = datasetSchemaSeed(t, s, base)
		}
		peer := trainingSchemaSeed(t, s, old)
		// An unused actual peer attempt keeps uniqueness from masking scoped FKs.
		peerAttempt := uuid.NewString()
		migrationExec(t, s, `INSERT INTO training_attempts(id,tenant_id,workspace_id,job_id,node_id,attempt_number,state,requested_by,requested_audience,requested_epoch,input_inventory) VALUES(?,?,?,?,?,2,'succeeded',?,'admin',1,JSON_OBJECT())`, peerAttempt, peer.tenant, peer.workspace, peer.training, peer.node, peer.user)
		for _, item := range []struct {
			name, statement string
			args            []any
			sameJobAllowed  bool
		}{
			{"job_current_attempt", `UPDATE training_jobs SET current_attempt_id=? WHERE id=?`, []any{peerAttempt, r.training}, false},
			{"job_accepted_artifact", `UPDATE training_jobs SET accepted_artifact_id=? WHERE id=?`, []any{peer.artifact, r.training}, false},
			{"job_pending_recovery", `UPDATE training_jobs SET pending_recovery_id=? WHERE id=?`, []any{peer.recovery, r.training}, false},
			{"attempt_resume_checkpoint", `UPDATE training_attempts SET resume_checkpoint_id=? WHERE id=?`, []any{peer.checkpoint, r.trainingAttempt}, false},
			{"event_attempt", `UPDATE training_events SET attempt_id=? WHERE attempt_id=?`, []any{peerAttempt, r.trainingAttempt}, false},
			{"checkpoint_attempt", `UPDATE training_checkpoints SET attempt_id=? WHERE id=?`, []any{peerAttempt, r.checkpoint}, false},
			{"artifact_attempt", `UPDATE training_artifacts SET attempt_id=? WHERE id=?`, []any{peerAttempt, r.artifact}, false},
			{"recovery_previous_attempt", `UPDATE training_recoveries SET previous_attempt_id=? WHERE id=?`, []any{peerAttempt, r.recovery}, false},
			{"recovery_next_attempt", `UPDATE training_recoveries SET next_attempt_id=? WHERE id=?`, []any{peerAttempt, r.recovery}, false},
			{"recovery_checkpoint", `UPDATE training_recoveries SET checkpoint_id=? WHERE id=?`, []any{peer.checkpoint, r.recovery}, false},
			{"upload_attempt", `INSERT INTO training_uploads(id,tenant_id,workspace_id,job_id,attempt_id,kind,global_step,manifest_sha256,manifest,state,manifest_bytes) VALUES(?,?,?,?,?,'checkpoint',2,REPEAT('a',64),JSON_OBJECT(),'uploading',?)`, []any{uuid.NewString(), r.tenant, r.workspace, r.training, peerAttempt, []byte("{}")}, false},
			{"job_dataset_version", `UPDATE training_jobs SET dataset_version_id=? WHERE id=?`, []any{peer.version, r.training}, true},
			{"job_original_node", `UPDATE training_jobs SET node_id=? WHERE id=?`, []any{peer.node, r.training}, true},
			{"job_current_node", `UPDATE training_jobs SET current_node_id=? WHERE id=?`, []any{peer.node, r.training}, true},
			{"job_model", `UPDATE training_jobs SET model_id=? WHERE id=?`, []any{peer.model, r.training}, true},
			{"attempt_task", `UPDATE training_attempts SET task_id=? WHERE id=?`, []any{peer.task, r.trainingAttempt}, true},
			{"recovery_previous_node", `UPDATE training_recoveries SET previous_node_id=? WHERE id=?`, []any{peer.node, r.recovery}, true},
			{"recovery_next_node", `UPDATE training_recoveries SET next_node_id=? WHERE id=?`, []any{peer.node, r.recovery}, true},
			{"GPU_training_owner", `UPDATE gpu_leases SET deployment_id=NULL,training_job_id=? WHERE deployment_id=?`, []any{peer.training, r.deployment}, true},
		} {
			if kind == "job" && item.sameJobAllowed {
				continue
			}
			t.Run(kind+"_"+item.name, func(t *testing.T) { trainingSchemaRefuses(t, s, 1452, item.statement, item.args...) })
		}
	}
	migrationExec(t, s, `UPDATE training_jobs SET pending_recovery_id=? WHERE id=?`, r.recovery, r.training)
	for _, item := range []struct{ table, id string }{
		{"training_jobs", r.training}, {"training_attempts", r.trainingAttempt},
		{"training_uploads", r.checkpoint}, {"training_checkpoints", r.checkpoint},
		{"training_artifacts", r.artifact}, {"training_recoveries", r.recovery},
		{"dataset_versions", r.version}, {"managed_nodes", r.node},
	} {
		t.Run("referenced_"+item.table+"_delete", func(t *testing.T) {
			trainingSchemaRefuses(t, s, 1451, "DELETE FROM "+item.table+" WHERE id=?", item.id)
		})
	}
	migrationRun(t, s, c, Readiness)
}

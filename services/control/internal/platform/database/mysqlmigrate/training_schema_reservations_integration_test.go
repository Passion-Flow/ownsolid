//go:build mysql_integration

package mysqlmigrate

import (
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

const trainingSchemaInsertLease = `INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,training_job_id,revision,state) VALUES(?,?,?,?,?,?,1,?)`

func trainingSchemaJobPeer(t *testing.T, s *mysqlstore.Store, r trainingSchemaRows) string {
	t.Helper()
	id := uuid.NewString()
	migrationExec(t, s, `INSERT INTO training_jobs(id,tenant_id,workspace_id,node_id,current_node_id,model_id,dataset_id,dataset_version_id,gpu_uuid,configuration,configuration_sha256,execution_recipe,dataset_manifest_sha256,model_catalog_sha256,created_by,requested_by,requested_audience,requested_epoch,reason,creation_key,creation_fingerprint,state) SELECT ?,tenant_id,workspace_id,node_id,current_node_id,model_id,dataset_id,dataset_version_id,gpu_uuid,configuration,configuration_sha256,execution_recipe,dataset_manifest_sha256,model_catalog_sha256,created_by,requested_by,requested_audience,requested_epoch,'Synthetic competing training metadata',?,creation_fingerprint,'queued' FROM training_jobs WHERE id=?`, id, uuid.NewString(), r.training)
	return id
}

func trainingSchemaLeaseArgs(r trainingSchemaRows, job, node, gpu, state string) []any {
	return []any{uuid.NewString(), r.tenant, r.workspace, node, gpu, job, state}
}

func TestActualMySQLTrainingSchemaReservations(t *testing.T) {
	s, c, r := trainingSchemaReady(t)
	otherNode := uuid.NewString()
	migrationExec(t, s, nodeSchemaInsertNode, otherNode, r.tenant, r.workspace, "Synthetic training peer "+otherNode, []byte(uuid.NewString())[:32])
	t.Run("held_inference_excludes_training_node", func(t *testing.T) {
		trainingSchemaRefuses(t, s, 1062, trainingSchemaInsertLease, trainingSchemaLeaseArgs(r, r.training, r.node, "GPU-"+uuid.NewString(), "held")...)
	})
	t.Run("held_inference_excludes_training_GPU", func(t *testing.T) {
		trainingSchemaRefuses(t, s, 1062, trainingSchemaInsertLease, trainingSchemaLeaseArgs(r, r.training, otherNode, r.gpu, "held")...)
	})
	t.Run("uncertain_inference_retains_reservation", func(t *testing.T) {
		migrationExec(t, s, `UPDATE gpu_leases SET state='recovery_required' WHERE deployment_id=?`, r.deployment)
		trainingSchemaRefuses(t, s, 1062, trainingSchemaInsertLease, trainingSchemaLeaseArgs(r, r.training, r.node, r.gpu, "held")...)
	})
	t.Run("released_inference_allows_explicit_training_owner", func(t *testing.T) {
		migrationExec(t, s, `UPDATE gpu_leases SET state='released' WHERE deployment_id=?`, r.deployment)
		args := trainingSchemaLeaseArgs(r, r.training, r.node, r.gpu, "held")
		migrationExec(t, s, trainingSchemaInsertLease, args...)
		var id string
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT id FROM gpu_leases WHERE training_job_id=?`, r.training).Scan(&id); e != nil || id != args[0] {
			t.Fatal("training lease did not retain explicit independent identity", mysqlstore.SafeError(e))
		}
	})
	t.Run("uncertain_training_excludes_inference", func(t *testing.T) {
		migrationExec(t, s, `UPDATE gpu_leases SET state='recovery_required' WHERE training_job_id=?`, r.training)
		trainingSchemaRefuses(t, s, 1062, `UPDATE gpu_leases SET state='held' WHERE deployment_id=?`, r.deployment)
	})
	t.Run("one_retained_lease_per_training_job", func(t *testing.T) {
		trainingSchemaRefuses(t, s, 1062, trainingSchemaInsertLease, trainingSchemaLeaseArgs(r, r.training, otherNode, "GPU-"+uuid.NewString(), "released")...)
	})
	t.Run("released_training_allows_inference", func(t *testing.T) {
		migrationExec(t, s, `UPDATE gpu_leases SET state='released' WHERE training_job_id=?`, r.training)
		migrationExec(t, s, `UPDATE gpu_leases SET state='held' WHERE deployment_id=?`, r.deployment)
	})
	t.Run("one_uncleared_attempt_and_explicit_stopped_tuple", func(t *testing.T) {
		statement := `INSERT INTO training_attempts(id,tenant_id,workspace_id,job_id,node_id,attempt_number,state,requested_by,requested_audience,requested_epoch,input_inventory) VALUES(?,?,?,?,?,?,'pending',?,'admin',1,JSON_OBJECT())`
		id := uuid.NewString()
		migrationExec(t, s, statement, id, r.tenant, r.workspace, r.training, r.node, 2, r.user)
		trainingSchemaRefuses(t, s, 1062, statement, uuid.NewString(), r.tenant, r.workspace, r.training, r.node, 3, r.user)
		migrationExec(t, s, `UPDATE training_attempts SET state='recovery_required' WHERE id=?`, id)
		trainingSchemaRefuses(t, s, 1062, statement, uuid.NewString(), r.tenant, r.workspace, r.training, r.node, 3, r.user)
		// Synthetic SQL receipt only, never evidence that a real GPU process stopped.
		migrationExec(t, s, `UPDATE training_attempts SET physical_stop_receipt=JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),physical_stop_sha256=REPEAT('a',64),physical_stopped_at=UTC_TIMESTAMP(6) WHERE id=?`, id)
		migrationExec(t, s, statement, uuid.NewString(), r.tenant, r.workspace, r.training, r.node, 3, r.user)
	})
	t.Run("checkpoint_step_unique_phase_step_reusable", func(t *testing.T) {
		statement := `INSERT INTO training_events(tenant_id,workspace_id,job_id,attempt_id,sequence,kind,global_step,payload,canonical_sha256) VALUES(?,?,?,?,2,?,1,JSON_OBJECT(),REPEAT('a',64))`
		trainingSchemaRefuses(t, s, 1062, statement, r.tenant, r.workspace, r.training, r.trainingAttempt, "checkpoint")
		migrationExec(t, s, statement, r.tenant, r.workspace, r.training, r.trainingAttempt, "phase")
	})
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLTrainingSchemaConcurrentReservations(t *testing.T) {
	s, c, r := trainingSchemaReady(t)
	migrationExec(t, s, `UPDATE gpu_leases SET state='released' WHERE deployment_id=?`, r.deployment)
	t.Run("same_node_different_GPU", func(t *testing.T) {
		left, right := trainingSchemaJobPeer(t, s, r), trainingSchemaJobPeer(t, s, r)
		modelInferenceOneWinner(t, s, trainingSchemaInsertLease,
			trainingSchemaLeaseArgs(r, left, r.node, "GPU-"+uuid.NewString(), "held"),
			trainingSchemaLeaseArgs(r, right, r.node, "GPU-"+uuid.NewString(), "held"))
	})
	migrationExec(t, s, `UPDATE gpu_leases SET state='released' WHERE training_job_id IS NOT NULL`)
	t.Run("same_GPU_different_node", func(t *testing.T) {
		otherNode := uuid.NewString()
		migrationExec(t, s, nodeSchemaInsertNode, otherNode, r.tenant, r.workspace, "Synthetic concurrent node "+otherNode, []byte(uuid.NewString())[:32])
		left, right, gpu := trainingSchemaJobPeer(t, s, r), trainingSchemaJobPeer(t, s, r), "GPU-"+uuid.NewString()
		modelInferenceOneWinner(t, s, trainingSchemaInsertLease,
			trainingSchemaLeaseArgs(r, left, r.node, gpu, "held"),
			trainingSchemaLeaseArgs(r, right, otherNode, gpu, "held"))
	})
	migrationRun(t, s, c, Readiness)
}

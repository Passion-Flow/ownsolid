//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"testing"
)

// Real cancellation at the applying/verified journal boundary. These tests do
// not claim operating-system process death or lost COMMIT acknowledgement.
func trainingSchemaInterrupted(t *testing.T, step int, stage string) {
	t.Helper()
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:59]
	migrationRun(t, s, prefix, Apply)
	old := datasetSchemaSeed(t, s, modelInferenceSeed(t, s, nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s))))
	before, identity := trainingSchemaPrefixDigest(t, s), licenseSchemaDeployment(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, e := Run(ctx, s, c, testOptions(), Apply, func(event Event) {
		if event.Step == step && event.Stage == stage {
			cancel()
		}
	})
	if e == nil || r.Ready || r.DirtyStep != step || !r.LockReleased {
		t.Fatal("interrupted training DDL lost dirty journal or lock", r, e)
	}
	var state string
	if e = s.Pool().QueryRowContext(t.Context(), `SELECT state FROM schema_migrations WHERE step=?`, step).Scan(&state); e != nil || state != Applying {
		t.Fatal("interrupted training journal must remain applying", mysqlstore.SafeError(e))
	}
	if _, e = Run(t.Context(), s, c, testOptions(), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
		t.Fatal("dirty training Apply silently repeated", e)
	}
	if r, e = Run(t.Context(), s, c, testOptions(), Readiness, nil); e == nil || r.Ready || !r.LockReleased {
		t.Fatal("dirty training schema served ready or retained lock", r, e)
	}
	if trainingSchemaPrefixDigest(t, s) != before {
		t.Fatal("dirty refusal changed accepted1-59 prefix")
	}
	migrationRun(t, s, c, Reconcile)
	if trainingSchemaPrefixDigest(t, s) != before || licenseSchemaDeployment(t, s) != identity {
		t.Fatal("explicit training reconciliation changed accepted data/journal/identity")
	}
	rows := trainingSchemaSeed(t, s, old)
	trainingSchemaAssertRows(t, s, rows)
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLTrainingSchemaBefore60(t *testing.T) { trainingSchemaInterrupted(t, 60, Applying) }

func TestActualMySQLTrainingSchemaAfter60(t *testing.T) { trainingSchemaInterrupted(t, 60, Verified) }

func TestActualMySQLTrainingSchemaBefore61(t *testing.T) { trainingSchemaInterrupted(t, 61, Applying) }

func TestActualMySQLTrainingSchemaAfter61(t *testing.T) { trainingSchemaInterrupted(t, 61, Verified) }

func TestActualMySQLTrainingSchemaBefore62(t *testing.T) { trainingSchemaInterrupted(t, 62, Applying) }

func TestActualMySQLTrainingSchemaAfter62(t *testing.T) { trainingSchemaInterrupted(t, 62, Verified) }

func TestActualMySQLTrainingSchemaBefore63(t *testing.T) { trainingSchemaInterrupted(t, 63, Applying) }

func TestActualMySQLTrainingSchemaAfter63(t *testing.T) { trainingSchemaInterrupted(t, 63, Verified) }

func TestActualMySQLTrainingSchemaBefore64(t *testing.T) { trainingSchemaInterrupted(t, 64, Applying) }

func TestActualMySQLTrainingSchemaAfter64(t *testing.T) { trainingSchemaInterrupted(t, 64, Verified) }

func TestActualMySQLTrainingSchemaBefore65(t *testing.T) { trainingSchemaInterrupted(t, 65, Applying) }

func TestActualMySQLTrainingSchemaAfter65(t *testing.T) { trainingSchemaInterrupted(t, 65, Verified) }

func TestActualMySQLTrainingSchemaBefore66(t *testing.T) { trainingSchemaInterrupted(t, 66, Applying) }

func TestActualMySQLTrainingSchemaAfter66(t *testing.T) { trainingSchemaInterrupted(t, 66, Verified) }

func TestActualMySQLTrainingSchemaBefore67(t *testing.T) { trainingSchemaInterrupted(t, 67, Applying) }

func TestActualMySQLTrainingSchemaAfter67(t *testing.T) { trainingSchemaInterrupted(t, 67, Verified) }

func TestActualMySQLTrainingSchemaBefore68(t *testing.T) { trainingSchemaInterrupted(t, 68, Applying) }

func TestActualMySQLTrainingSchemaAfter68(t *testing.T) { trainingSchemaInterrupted(t, 68, Verified) }

func TestActualMySQLTrainingSchemaBefore69(t *testing.T) { trainingSchemaInterrupted(t, 69, Applying) }

func TestActualMySQLTrainingSchemaAfter69(t *testing.T) { trainingSchemaInterrupted(t, 69, Verified) }

func TestActualMySQLTrainingSchemaBefore70(t *testing.T) { trainingSchemaInterrupted(t, 70, Applying) }

func TestActualMySQLTrainingSchemaAfter70(t *testing.T) { trainingSchemaInterrupted(t, 70, Verified) }

func TestActualMySQLTrainingSchemaBefore71(t *testing.T) { trainingSchemaInterrupted(t, 71, Applying) }

func TestActualMySQLTrainingSchemaAfter71(t *testing.T) { trainingSchemaInterrupted(t, 71, Verified) }

func TestActualMySQLTrainingSchemaBefore72(t *testing.T) { trainingSchemaInterrupted(t, 72, Applying) }

func TestActualMySQLTrainingSchemaAfter72(t *testing.T) { trainingSchemaInterrupted(t, 72, Verified) }

func TestActualMySQLTrainingSchemaBefore73(t *testing.T) { trainingSchemaInterrupted(t, 73, Applying) }

func TestActualMySQLTrainingSchemaAfter73(t *testing.T) { trainingSchemaInterrupted(t, 73, Verified) }

func TestActualMySQLTrainingSchemaBefore74(t *testing.T) { trainingSchemaInterrupted(t, 74, Applying) }

func TestActualMySQLTrainingSchemaAfter74(t *testing.T) { trainingSchemaInterrupted(t, 74, Verified) }

func TestActualMySQLTrainingSchemaBefore75(t *testing.T) { trainingSchemaInterrupted(t, 75, Applying) }

func TestActualMySQLTrainingSchemaAfter75(t *testing.T) { trainingSchemaInterrupted(t, 75, Verified) }

func TestActualMySQLTrainingSchemaBefore76(t *testing.T) { trainingSchemaInterrupted(t, 76, Applying) }

func TestActualMySQLTrainingSchemaAfter76(t *testing.T) { trainingSchemaInterrupted(t, 76, Verified) }

func TestActualMySQLTrainingSchemaBefore77(t *testing.T) { trainingSchemaInterrupted(t, 77, Applying) }

func TestActualMySQLTrainingSchemaAfter77(t *testing.T) { trainingSchemaInterrupted(t, 77, Verified) }

func TestActualMySQLTrainingSchemaBefore78(t *testing.T) { trainingSchemaInterrupted(t, 78, Applying) }

func TestActualMySQLTrainingSchemaAfter78(t *testing.T) { trainingSchemaInterrupted(t, 78, Verified) }

func TestActualMySQLTrainingSchemaBefore79(t *testing.T) { trainingSchemaInterrupted(t, 79, Applying) }

func TestActualMySQLTrainingSchemaAfter79(t *testing.T) { trainingSchemaInterrupted(t, 79, Verified) }

func TestActualMySQLTrainingSchemaBefore80(t *testing.T) { trainingSchemaInterrupted(t, 80, Applying) }

func TestActualMySQLTrainingSchemaAfter80(t *testing.T) { trainingSchemaInterrupted(t, 80, Verified) }

func TestActualMySQLTrainingSchemaBefore81(t *testing.T) { trainingSchemaInterrupted(t, 81, Applying) }

func TestActualMySQLTrainingSchemaAfter81(t *testing.T) { trainingSchemaInterrupted(t, 81, Verified) }

func TestActualMySQLTrainingSchemaBefore82(t *testing.T) { trainingSchemaInterrupted(t, 82, Applying) }

func TestActualMySQLTrainingSchemaAfter82(t *testing.T) { trainingSchemaInterrupted(t, 82, Verified) }

func TestActualMySQLTrainingSchemaBefore83(t *testing.T) { trainingSchemaInterrupted(t, 83, Applying) }

func TestActualMySQLTrainingSchemaAfter83(t *testing.T) { trainingSchemaInterrupted(t, 83, Verified) }

func TestActualMySQLTrainingSchemaBefore84(t *testing.T) { trainingSchemaInterrupted(t, 84, Applying) }

func TestActualMySQLTrainingSchemaAfter84(t *testing.T) { trainingSchemaInterrupted(t, 84, Verified) }

func TestActualMySQLTrainingSchemaBefore85(t *testing.T) { trainingSchemaInterrupted(t, 85, Applying) }

func TestActualMySQLTrainingSchemaAfter85(t *testing.T) { trainingSchemaInterrupted(t, 85, Verified) }

func trainingSchemaRefuseModes(t *testing.T, s *mysqlstore.Store, c Catalog, code string, digest func() string) {
	t.Helper()
	before, identity := digest(), licenseSchemaDeployment(t, s)
	for _, mode := range []Mode{Readiness, Apply, Reconcile} {
		r, e := Run(t.Context(), s, c, testOptions(), mode, nil)
		if e == nil || e.Error() != code || r.Ready || !r.LockReleased || digest() != before || licenseSchemaDeployment(t, s) != identity {
			t.Fatal("training drift mode altered state or accepted unsafe metadata", mode, r, e)
		}
	}
}

func TestActualMySQLTrainingSchemaUnjournaledJob(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:59]
	migrationRun(t, s, prefix, Apply)
	datasetSchemaSeed(t, s, modelInferenceSeed(t, s, nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s))))
	migrationExec(t, s, c.steps[59].sql)
	trainingSchemaRefuseModes(t, s, c, CodeUnjournaledObject, func() string { return trainingSchemaPrefixDigest(t, s) })
}

func TestActualMySQLTrainingSchemaMissingAppliedEvents(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Apply)
	datasetSchemaSeed(t, s, modelInferenceSeed(t, s, nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s))))
	// Empty owned leaf only; no customer or accepted training content is deleted.
	migrationExec(t, s, `DROP TABLE training_events`)
	trainingSchemaRefuseModes(t, s, c, CodeDefinitionDrift, func() string {
		return hash(trainingSchemaPrefixDigest(t, s) + licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step>=60 ORDER BY step`))
	})
}

func TestActualMySQLTrainingSchemaWrongDefinition(t *testing.T) {
	s, c, _ := trainingSchemaReady(t)
	migrationExec(t, s, `ALTER TABLE training_jobs ADD INDEX unexpected_training_index(created_at)`)
	trainingSchemaRefuseModes(t, s, c, CodeDefinitionDrift, func() string { return trainingSchemaDataDigest(t, s) })
}

func TestActualMySQLTrainingSchemaWrongSourceChecksum(t *testing.T) {
	s, c, _ := trainingSchemaReady(t)
	migrationExec(t, s, `UPDATE schema_migrations SET source_sha256=REPEAT('b',64) WHERE step=60`)
	trainingSchemaRefuseModes(t, s, c, CodeChecksumMismatch, func() string { return trainingSchemaDataDigest(t, s) })
}

func TestActualMySQLTrainingSchemaWrongTrigger(t *testing.T) {
	s, c, _ := trainingSchemaReady(t)
	migrationExec(t, s, `DROP TRIGGER training_event_immutable_update`)
	migrationExec(t, s, "CREATE TRIGGER training_event_immutable_update BEFORE UPDATE ON training_events FOR EACH ROW\nBEGIN\n    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SYNTHETIC_CHANGED_TRIGGER';\nEND;")
	trainingSchemaRefuseModes(t, s, c, CodeDefinitionDrift, func() string { return trainingSchemaDataDigest(t, s) })
}

func TestActualMySQLTrainingSchemaPrepareRecovery(t *testing.T) {
	s, c, r := trainingSchemaReady(t)
	migrationExec(t, s, `UPDATE gpu_leases SET state='released' WHERE deployment_id=?`, r.deployment)
	migrationExec(t, s, trainingSchemaInsertLease, trainingSchemaLeaseArgs(r, r.training, r.node, r.gpu, "recovery_required")...)
	migrationExec(t, s, `UPDATE training_jobs SET state='recovery_required',finished_at=NULL,error_code='SYNTHETIC_SCHEMA_RECOVERY' WHERE id=?`, r.training)
	migrationExec(t, s, `UPDATE training_attempts SET state='recovery_required',finished_at=NULL,error_code='SYNTHETIC_SCHEMA_RECOVERY' WHERE id=?`, r.trainingAttempt)
	migrationRun(t, s, c, Readiness)
	t.Log("MYSQL_TRAINING_DATA_SHA256=" + trainingSchemaDataDigest(t, s))
}

func TestActualMySQLTrainingSchemaReadback(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Readiness)
	for _, table := range trainingSchemaTables {
		var count int
		want := 1
		if table == "training_uploads" {
			want = 2
		}
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); e != nil || count != want {
			t.Fatal("restored complete training graph", table, mysqlstore.SafeError(e))
		}
	}
	var job, attempt, reservedNode, reservedGPU, node, gpu, jobState, attemptState string
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT j.id,a.id,j.current_node_id,j.gpu_uuid,g.reserved_node_id,g.reserved_gpu_uuid,j.state,a.state FROM training_jobs j JOIN training_attempts a ON a.id=j.current_attempt_id AND a.job_id=j.id JOIN gpu_leases g ON g.training_job_id=j.id AND g.node_id=j.current_node_id WHERE g.state='recovery_required'`).Scan(&job, &attempt, &node, &gpu, &reservedNode, &reservedGPU, &jobState, &attemptState); e != nil || node != reservedNode || gpu != reservedGPU || jobState != "recovery_required" || attemptState != "recovery_required" {
		t.Fatal("restored training recovery lost physical reservation", mysqlstore.SafeError(e))
	}
	trainingSchemaRefuses(t, s, 3819, `UPDATE training_jobs SET state='foreign' WHERE id=?`, job)
	trainingSchemaRefuses(t, s, 1644, `UPDATE training_attempts SET input_inventory=JSON_OBJECT('changed',1) WHERE id=?`, attempt)
	t.Log("MYSQL_TRAINING_DATA_SHA256=" + trainingSchemaDataDigest(t, s))
}

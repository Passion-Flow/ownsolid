//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// Cancellation observes real applying/verified DDL journal boundaries. It is
// not process death, power loss or uncertain COMMIT acknowledgement evidence.
func preparationSchemaInterrupted(t *testing.T, step int, stage string) {
	t.Helper()
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:94]
	migrationRun(t, s, prefix, Apply)
	old := holdoutSchemaSeed(t, s, holdoutSchemaReservationSeed(t, s))
	before, identity := holdoutSchemaDataDigest(t, s), licenseSchemaDeployment(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result, e := Run(ctx, s, c, testOptions(), Apply, func(event Event) {
		if event.Step == step && event.Stage == stage {
			cancel()
		}
	})
	if e == nil || result.Ready || result.DirtyStep != step || !result.LockReleased {
		t.Fatal("interrupted preparation migration lost dirty journal/lock", result, e)
	}
	var state string
	if e = s.Pool().QueryRowContext(t.Context(), `SELECT state FROM schema_migrations WHERE step=?`, step).Scan(&state); e != nil || state != Applying {
		t.Fatal("preparation dirty journal must remain applying", mysqlstore.SafeError(e))
	}
	if _, e = Run(t.Context(), s, c, testOptions(), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
		t.Fatal("dirty preparation Apply retried implicitly", e)
	}
	if result, e = Run(t.Context(), s, c, testOptions(), Readiness, nil); e == nil || result.Ready || !result.LockReleased {
		t.Fatal("dirty preparation schema served ready", result, e)
	}
	if holdoutSchemaDataDigest(t, s) != before {
		t.Fatal("dirty refusal changed complete old94 graph/journal")
	}
	migrationRun(t, s, c, Reconcile)
	if holdoutSchemaDataDigest(t, s) != before || licenseSchemaDeployment(t, s) != identity {
		t.Fatal("reconciliation changed old94 graph/journal/identity")
	}
	holdoutSchemaAssertReservations(t, s)
	holdoutSchemaAssertRows(t, s, old)
	preparationSchemaAssertRows(t, s, preparationSchemaSeed(t, s, old))
	migrationRun(t, s, c, Readiness)
}

func preparationSchemaRefuseModes(t *testing.T, s *mysqlstore.Store, c Catalog, code string, digest func() string) {
	t.Helper()
	before, identity := digest(), licenseSchemaDeployment(t, s)
	for _, mode := range []Mode{Readiness, Apply, Reconcile} {
		result, e := Run(t.Context(), s, c, testOptions(), mode, nil)
		if e == nil || e.Error() != code || result.Ready || !result.LockReleased || digest() != before || licenseSchemaDeployment(t, s) != identity {
			t.Fatal("preparation drift accepted or altered state", mode, result, e)
		}
	}
}

func TestActualMySQLPreparationSchemaUnjournaledPreparation(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:94]
	migrationRun(t, s, prefix, Apply)
	holdoutSchemaSeed(t, s, holdoutSchemaReservationSeed(t, s))
	migrationExec(t, s, c.steps[94].sql)
	preparationSchemaRefuseModes(t, s, c, CodeUnjournaledObject, func() string {
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='model_preparations'`).Scan(&count); e != nil || count != 1 {
			t.Fatal("unjournaled preparation table was changed", mysqlstore.SafeError(e))
		}
		return holdoutSchemaDataDigest(t, s) + trainingSchemaColumnDigest(t, s, "model_preparations", holdoutSchemaDigestColumns(t, s, "model_preparations"))
	})
}

func TestActualMySQLPreparationSchemaMissingAppliedArtifacts(t *testing.T) {
	s, c, r := preparationSchemaLayer(t, preparationSchemaStepCount)
	// Drop only the explicitly disposable leaf table. Every other owned row,
	// owner identity, backreference and journal entry remains in the digest.
	migrationExec(t, s, `DROP TABLE model_preparation_artifacts`)
	preparationSchemaRefuseModes(t, s, c, CodeDefinitionDrift, func() string {
		result := holdoutSchemaDataDigest(t, s)
		for _, table := range preparationSchemaTables {
			if table != "model_preparation_artifacts" {
				result += trainingSchemaColumnDigest(t, s, table, holdoutSchemaDigestColumns(t, s, table))
			}
		}
		result += licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(id,model_preparation_id) FROM gpu_leases ORDER BY id`)
		return hash(result + licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step>=95 ORDER BY step`))
	})
	var count int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_preparations p JOIN model_preparation_uploads u ON u.preparation_id=p.id AND u.tenant_id=p.tenant_id AND u.workspace_id=p.workspace_id WHERE p.id=?`, r.preparation).Scan(&count); e != nil || count != 1 {
		t.Fatal("artifact drift lost retained input/upload", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLPreparationSchemaWrongDefinition(t *testing.T) {
	s, c, _ := preparationSchemaLayer(t, preparationSchemaStepCount)
	migrationExec(t, s, `ALTER TABLE model_preparations ADD INDEX unexpected_preparation_index(created_at)`)
	preparationSchemaRefuseModes(t, s, c, CodeDefinitionDrift, func() string { return preparationSchemaDataDigest(t, s) })
}
func TestActualMySQLPreparationSchemaWrongSourceChecksum(t *testing.T) {
	s, c, _ := preparationSchemaLayer(t, preparationSchemaStepCount)
	migrationExec(t, s, `UPDATE schema_migrations SET source_sha256=REPEAT('b',64) WHERE step=95`)
	preparationSchemaRefuseModes(t, s, c, CodeChecksumMismatch, func() string { return preparationSchemaDataDigest(t, s) })
}
func TestActualMySQLPreparationSchemaWrongTrigger(t *testing.T) {
	s, c, _ := preparationSchemaLayer(t, preparationSchemaStepCount)
	migrationExec(t, s, `DROP TRIGGER model_preparation_artifact_immutable_update`)
	migrationExec(t, s, "CREATE TRIGGER model_preparation_artifact_immutable_update BEFORE UPDATE ON model_preparation_artifacts FOR EACH ROW\nBEGIN\n    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SYNTHETIC_CHANGED_TRIGGER';\nEND;")
	preparationSchemaRefuseModes(t, s, c, CodeDefinitionDrift, func() string { return preparationSchemaDataDigest(t, s) })
}

func preparationSchemaAssertThreeReservations(t *testing.T, s *mysqlstore.Store) {
	t.Helper()
	var inference, training, preparation, nodes, GPUs, validOwners int
	query := `SELECT SUM(deployment_id IS NOT NULL AND training_job_id IS NULL AND model_preparation_id IS NULL AND state='held' AND id=deployment_id),SUM(deployment_id IS NULL AND training_job_id IS NOT NULL AND model_preparation_id IS NULL AND state='recovery_required' AND id<>training_job_id),SUM(deployment_id IS NULL AND training_job_id IS NULL AND model_preparation_id IS NOT NULL AND state='held' AND id<>model_preparation_id),COUNT(DISTINCT reserved_node_id),COUNT(DISTINCT reserved_gpu_uuid) FROM gpu_leases`
	if e := s.Pool().QueryRowContext(t.Context(), query).Scan(&inference, &training, &preparation, &nodes, &GPUs); e != nil || inference != 1 || training != 1 || preparation != 1 || nodes != 3 || GPUs != 3 {
		t.Fatal("three independent retained reservation owners required", mysqlstore.SafeError(e))
	}
	query = `SELECT COUNT(*) FROM gpu_leases g LEFT JOIN deployments d ON d.id=g.deployment_id AND d.tenant_id=g.tenant_id AND d.workspace_id=g.workspace_id AND d.node_id=g.node_id AND d.gpu_uuid=g.gpu_uuid LEFT JOIN training_jobs j ON j.id=g.training_job_id AND j.tenant_id=g.tenant_id AND j.workspace_id=g.workspace_id AND j.current_node_id=g.node_id AND j.gpu_uuid=g.gpu_uuid LEFT JOIN model_preparations p ON p.id=g.model_preparation_id AND p.tenant_id=g.tenant_id AND p.workspace_id=g.workspace_id AND p.current_node_id=g.node_id AND p.gpu_uuid=g.gpu_uuid WHERE g.reserved_node_id=g.node_id AND g.reserved_gpu_uuid=g.gpu_uuid AND (d.id IS NOT NULL OR j.id IS NOT NULL OR p.id IS NOT NULL)`
	if e := s.Pool().QueryRowContext(t.Context(), query).Scan(&validOwners); e != nil || validOwners != 3 {
		t.Fatal("scoped node/GPU reservation graph changed", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLPreparationSchemaPrepareRecovery(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Apply)
	old := holdoutSchemaSeed(t, s, holdoutSchemaReservationSeed(t, s))
	holdoutSchemaAssertRows(t, s, old)
	holdoutSchemaAssertReservations(t, s)
	r := preparationSchemaSeed(t, s, old)
	preparationSchemaAssertRows(t, s, r)
	// Explicit third-owner SQL metadata, never actual GPU execution evidence.
	migrationExec(t, s, preparationSchemaInsertLease, preparationSchemaLeaseValues(t, r, "preparation", r.preparation, r.preparationNode, r.preparationGPU, "held")...)
	preparationSchemaAssertThreeReservations(t, s)
	migrationRun(t, s, c, Readiness)
	t.Log("MYSQL_PREPARATION_DATA_SHA256=" + preparationSchemaDataDigest(t, s))
}

func TestActualMySQLPreparationSchemaReadback(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Readiness)
	for _, table := range append(append([]string{}, trainingSchemaTables...), holdoutSchemaTables...) {
		want := 1
		if table == "training_uploads" {
			want = 2
		}
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); e != nil || count != want {
			t.Fatal("restored complete old94 graph required", table, mysqlstore.SafeError(e))
		}
	}
	var r preparationSchemaRows
	query := `SELECT p.id,p.tenant_id,p.workspace_id,p.current_attempt_id,p.pending_recovery_id,p.created_at,p.node_id,p.gpu_uuid,p.heldout_id,u.id FROM model_preparations p JOIN model_preparation_uploads u ON u.preparation_id=p.id AND u.attempt_id=p.current_attempt_id AND u.tenant_id=p.tenant_id AND u.workspace_id=p.workspace_id`
	if e := s.Pool().QueryRowContext(t.Context(), query).Scan(&r.preparation, &r.tenant, &r.workspace, &r.preparationAttempt, &r.preparationRecovery, &r.heartbeat, &r.preparationNode, &r.preparationGPU, &r.holdout, &r.preparationUpload); e != nil {
		t.Fatal("restored preparation identity", mysqlstore.SafeError(e))
	}
	preparationSchemaAssertRows(t, s, r)
	holdoutSchemaAssertRows(t, s, r.holdoutSchemaRows)
	preparationSchemaAssertThreeReservations(t, s)
	preparationSchemaRefuses(t, s, 1644, `UPDATE model_preparations SET reason='Synthetic forbidden restored change' WHERE id=?`, r.preparation)
	preparationSchemaRefuses(t, s, 1644, `UPDATE model_preparation_attempts SET outcome=NULL,outcome_bytes=NULL,outcome_sha256=NULL WHERE id=?`, r.preparationAttempt)
	preparationSchemaRefuses(t, s, 1644, `UPDATE model_preparation_artifacts SET descriptor=JSON_OBJECT('changed',CAST('true' AS JSON)) WHERE id=?`, r.preparationUpload)
	t.Log("MYSQL_PREPARATION_DATA_SHA256=" + preparationSchemaDataDigest(t, s))
}

func TestActualMySQLPreparationSchemaBefore95(t *testing.T) {
	preparationSchemaInterrupted(t, 95, Applying)
}

func TestActualMySQLPreparationSchemaAfter95(t *testing.T) {
	preparationSchemaInterrupted(t, 95, Verified)
}

func TestActualMySQLPreparationSchemaBefore96(t *testing.T) {
	preparationSchemaInterrupted(t, 96, Applying)
}

func TestActualMySQLPreparationSchemaAfter96(t *testing.T) {
	preparationSchemaInterrupted(t, 96, Verified)
}

func TestActualMySQLPreparationSchemaBefore97(t *testing.T) {
	preparationSchemaInterrupted(t, 97, Applying)
}

func TestActualMySQLPreparationSchemaAfter97(t *testing.T) {
	preparationSchemaInterrupted(t, 97, Verified)
}

func TestActualMySQLPreparationSchemaBefore98(t *testing.T) {
	preparationSchemaInterrupted(t, 98, Applying)
}

func TestActualMySQLPreparationSchemaAfter98(t *testing.T) {
	preparationSchemaInterrupted(t, 98, Verified)
}

func TestActualMySQLPreparationSchemaBefore99(t *testing.T) {
	preparationSchemaInterrupted(t, 99, Applying)
}

func TestActualMySQLPreparationSchemaAfter99(t *testing.T) {
	preparationSchemaInterrupted(t, 99, Verified)
}

func TestActualMySQLPreparationSchemaBefore100(t *testing.T) {
	preparationSchemaInterrupted(t, 100, Applying)
}

func TestActualMySQLPreparationSchemaAfter100(t *testing.T) {
	preparationSchemaInterrupted(t, 100, Verified)
}

func TestActualMySQLPreparationSchemaBefore101(t *testing.T) {
	preparationSchemaInterrupted(t, 101, Applying)
}

func TestActualMySQLPreparationSchemaAfter101(t *testing.T) {
	preparationSchemaInterrupted(t, 101, Verified)
}

func TestActualMySQLPreparationSchemaBefore102(t *testing.T) {
	preparationSchemaInterrupted(t, 102, Applying)
}

func TestActualMySQLPreparationSchemaAfter102(t *testing.T) {
	preparationSchemaInterrupted(t, 102, Verified)
}

func TestActualMySQLPreparationSchemaBefore103(t *testing.T) {
	preparationSchemaInterrupted(t, 103, Applying)
}

func TestActualMySQLPreparationSchemaAfter103(t *testing.T) {
	preparationSchemaInterrupted(t, 103, Verified)
}

func TestActualMySQLPreparationSchemaBefore104(t *testing.T) {
	preparationSchemaInterrupted(t, 104, Applying)
}

func TestActualMySQLPreparationSchemaAfter104(t *testing.T) {
	preparationSchemaInterrupted(t, 104, Verified)
}

func TestActualMySQLPreparationSchemaBefore105(t *testing.T) {
	preparationSchemaInterrupted(t, 105, Applying)
}

func TestActualMySQLPreparationSchemaAfter105(t *testing.T) {
	preparationSchemaInterrupted(t, 105, Verified)
}

func TestActualMySQLPreparationSchemaBefore106(t *testing.T) {
	preparationSchemaInterrupted(t, 106, Applying)
}

func TestActualMySQLPreparationSchemaAfter106(t *testing.T) {
	preparationSchemaInterrupted(t, 106, Verified)
}

func TestActualMySQLPreparationSchemaBefore107(t *testing.T) {
	preparationSchemaInterrupted(t, 107, Applying)
}

func TestActualMySQLPreparationSchemaAfter107(t *testing.T) {
	preparationSchemaInterrupted(t, 107, Verified)
}

func TestActualMySQLPreparationSchemaBefore108(t *testing.T) {
	preparationSchemaInterrupted(t, 108, Applying)
}

func TestActualMySQLPreparationSchemaAfter108(t *testing.T) {
	preparationSchemaInterrupted(t, 108, Verified)
}

func TestActualMySQLPreparationSchemaBefore109(t *testing.T) {
	preparationSchemaInterrupted(t, 109, Applying)
}

func TestActualMySQLPreparationSchemaAfter109(t *testing.T) {
	preparationSchemaInterrupted(t, 109, Verified)
}

func TestActualMySQLPreparationSchemaBefore110(t *testing.T) {
	preparationSchemaInterrupted(t, 110, Applying)
}

func TestActualMySQLPreparationSchemaAfter110(t *testing.T) {
	preparationSchemaInterrupted(t, 110, Verified)
}

func TestActualMySQLPreparationSchemaBefore111(t *testing.T) {
	preparationSchemaInterrupted(t, 111, Applying)
}

func TestActualMySQLPreparationSchemaAfter111(t *testing.T) {
	preparationSchemaInterrupted(t, 111, Verified)
}

func TestActualMySQLPreparationSchemaBefore112(t *testing.T) {
	preparationSchemaInterrupted(t, 112, Applying)
}

func TestActualMySQLPreparationSchemaAfter112(t *testing.T) {
	preparationSchemaInterrupted(t, 112, Verified)
}

func TestActualMySQLPreparationSchemaBefore113(t *testing.T) {
	preparationSchemaInterrupted(t, 113, Applying)
}

func TestActualMySQLPreparationSchemaAfter113(t *testing.T) {
	preparationSchemaInterrupted(t, 113, Verified)
}

func TestActualMySQLPreparationSchemaBefore114(t *testing.T) {
	preparationSchemaInterrupted(t, 114, Applying)
}

func TestActualMySQLPreparationSchemaAfter114(t *testing.T) {
	preparationSchemaInterrupted(t, 114, Verified)
}

func TestActualMySQLPreparationSchemaBefore115(t *testing.T) {
	preparationSchemaInterrupted(t, 115, Applying)
}

func TestActualMySQLPreparationSchemaAfter115(t *testing.T) {
	preparationSchemaInterrupted(t, 115, Verified)
}

func TestActualMySQLPreparationSchemaBefore116(t *testing.T) {
	preparationSchemaInterrupted(t, 116, Applying)
}

func TestActualMySQLPreparationSchemaAfter116(t *testing.T) {
	preparationSchemaInterrupted(t, 116, Verified)
}

func TestActualMySQLPreparationSchemaBefore117(t *testing.T) {
	preparationSchemaInterrupted(t, 117, Applying)
}

func TestActualMySQLPreparationSchemaAfter117(t *testing.T) {
	preparationSchemaInterrupted(t, 117, Verified)
}

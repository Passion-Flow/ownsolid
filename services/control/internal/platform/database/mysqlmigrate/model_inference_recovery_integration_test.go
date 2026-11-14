//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func modelInferenceRecoveryDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	return hash(modelInferenceDataDigest(t, s) + licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step>45 ORDER BY step`))
}

// These are real journal/DDL observer-boundary cancellations. They do not stand
// for process death, lost COMMIT acknowledgement or native target qualification.
func modelInferenceInterrupted(t *testing.T, step int, stage string) {
	t.Helper()
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:45]
	migrationRun(t, s, prefix, Apply)
	nodes := nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s))
	before, identity := modelInferencePrefixDigest(t, s), licenseSchemaDeployment(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, e := Run(ctx, s, c, testOptions(), Apply, func(event Event) {
		if event.Step == step && event.Stage == stage {
			cancel()
		}
	})
	if e == nil || r.Ready || r.DirtyStep != step || !r.LockReleased {
		t.Fatal("interrupted model/inference DDL must retain dirty step and release lock", r, e)
	}
	var state string
	if e = s.Pool().QueryRowContext(t.Context(), `SELECT state FROM schema_migrations WHERE step=?`, step).Scan(&state); e != nil || state != Applying {
		t.Fatal("interrupted model/inference journal must remain applying", mysqlstore.SafeError(e))
	}
	if _, e = Run(t.Context(), s, c, testOptions(), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
		t.Fatal("dirty model/inference Apply silently repeated", e)
	}
	if r, e = Run(t.Context(), s, c, testOptions(), Readiness, nil); e == nil || r.Ready || !r.LockReleased {
		t.Fatal("dirty model/inference schema served ready or retained lock", r, e)
	}
	if modelInferencePrefixDigest(t, s) != before {
		t.Fatal("dirty migration refusal changed retained prefix")
	}
	migrationRun(t, s, c, Reconcile)
	if modelInferencePrefixDigest(t, s) != before || licenseSchemaDeployment(t, s) != identity {
		t.Fatal("explicit reconciliation changed prior business/journal/deployment identity")
	}
	modelInferenceSeed(t, s, nodes)
	modelInferenceAssertRows(t, s)
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLModelInferenceSchemaBefore46(t *testing.T) {
	modelInferenceInterrupted(t, 46, Applying)
}

func TestActualMySQLModelInferenceSchemaAfter46(t *testing.T) {
	modelInferenceInterrupted(t, 46, Verified)
}

func TestActualMySQLModelInferenceSchemaBefore47(t *testing.T) {
	modelInferenceInterrupted(t, 47, Applying)
}

func TestActualMySQLModelInferenceSchemaAfter47(t *testing.T) {
	modelInferenceInterrupted(t, 47, Verified)
}

func TestActualMySQLModelInferenceSchemaBefore48(t *testing.T) {
	modelInferenceInterrupted(t, 48, Applying)
}

func TestActualMySQLModelInferenceSchemaAfter48(t *testing.T) {
	modelInferenceInterrupted(t, 48, Verified)
}

func TestActualMySQLModelInferenceSchemaBefore49(t *testing.T) {
	modelInferenceInterrupted(t, 49, Applying)
}

func TestActualMySQLModelInferenceSchemaAfter49(t *testing.T) {
	modelInferenceInterrupted(t, 49, Verified)
}

func TestActualMySQLModelInferenceSchemaBefore50(t *testing.T) {
	modelInferenceInterrupted(t, 50, Applying)
}

func TestActualMySQLModelInferenceSchemaAfter50(t *testing.T) {
	modelInferenceInterrupted(t, 50, Verified)
}

func TestActualMySQLModelInferenceSchemaBefore51(t *testing.T) {
	modelInferenceInterrupted(t, 51, Applying)
}

func TestActualMySQLModelInferenceSchemaAfter51(t *testing.T) {
	modelInferenceInterrupted(t, 51, Verified)
}

func modelInferenceRefuseModes(t *testing.T, s *mysqlstore.Store, c Catalog, code string, digest func() string) {
	t.Helper()
	before, identity := digest(), licenseSchemaDeployment(t, s)
	for _, mode := range []Mode{Readiness, Apply, Reconcile} {
		r, e := Run(t.Context(), s, c, testOptions(), mode, nil)
		if e == nil || e.Error() != code || r.Ready || !r.LockReleased || digest() != before || licenseSchemaDeployment(t, s) != identity {
			t.Fatal("unsafe model/inference migration mode changed accepted state or accepted drift", mode, r, e)
		}
	}
}

func TestActualMySQLModelInferenceSchemaUnjournaledModel(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:45]
	migrationRun(t, s, prefix, Apply)
	nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s))
	migrationExec(t, s, c.steps[45].sql)
	modelInferenceRefuseModes(t, s, c, CodeUnjournaledObject, func() string { return modelInferencePrefixDigest(t, s) })
	var count int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='model_versions'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("unjournaled table silently changed", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLModelInferenceSchemaMissingAppliedLease(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Apply)
	nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s))
	// Deliberately remove only the empty leaf table inside the disposable scope.
	// Readiness/Apply/Reconcile must refuse; none may recreate it automatically.
	migrationExec(t, s, `DROP TABLE gpu_leases`)
	digest := func() string {
		return hash(modelInferencePrefixDigest(t, s) + licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step>45 ORDER BY step`))
	}
	modelInferenceRefuseModes(t, s, c, CodeDefinitionDrift, digest)
	for _, table := range []string{"model_versions", "model_imports", "model_import_chunks", "inference_nodes", "deployments"} {
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); e != nil || count != 0 {
			t.Fatal("missing-leaf refusal changed another new table", table, mysqlstore.SafeError(e))
		}
	}
	var count int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='gpu_leases'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("missing applied lease table silently recreated", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLModelInferenceSchemaWrongDefinition(t *testing.T) {
	s, c, _ := modelInferenceReady(t)
	migrationExec(t, s, `ALTER TABLE model_versions ADD INDEX unexpected_model_index(created_at)`)
	modelInferenceRefuseModes(t, s, c, CodeDefinitionDrift, func() string { return modelInferenceRecoveryDigest(t, s) })
}

func TestActualMySQLModelInferenceSchemaWrongSourceChecksum(t *testing.T) {
	s, c, _ := modelInferenceReady(t)
	migrationExec(t, s, `UPDATE schema_migrations SET source_sha256=REPEAT('b',64) WHERE step=46`)
	modelInferenceRefuseModes(t, s, c, CodeChecksumMismatch, func() string { return modelInferenceRecoveryDigest(t, s) })
}

func TestActualMySQLModelInferenceSchemaPrepareRecovery(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:45]
	migrationRun(t, s, prefix, Apply)
	nodes := nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s))
	before := modelInferencePrefixDigest(t, s)
	migrationRun(t, s, c, Apply)
	if modelInferencePrefixDigest(t, s) != before {
		t.Fatal("recovery fixture setup changed retained prefix")
	}
	modelInferenceSeed(t, s, nodes)
	modelInferenceAssertRows(t, s)
	migrationRun(t, s, c, Readiness)
	t.Log("MYSQL_MODEL_INFERENCE_DATA_SHA256=" + modelInferenceRecoveryDigest(t, s))
}

func TestActualMySQLModelInferenceSchemaReadback(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Readiness)
	modelInferenceAssertRows(t, s)
	// Exercise a real restored check/FK/index rather than accepting row counts alone.
	var model string
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT id FROM model_versions`).Scan(&model); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	modelInferenceRefuses(t, s, 3819, `UPDATE model_versions SET catalog_digest='invalid' WHERE id=?`, model)
	t.Log("MYSQL_MODEL_INFERENCE_DATA_SHA256=" + modelInferenceRecoveryDigest(t, s))
}

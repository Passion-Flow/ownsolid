//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"testing"
)

// Real cancellation at the applying/verified journal boundary. These tests do
// not claim operating-system process death or lost COMMIT acknowledgement.
func datasetSchemaInterrupted(t *testing.T, step int, stage string) {
	t.Helper()
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:51]
	migrationRun(t, s, prefix, Apply)
	old := modelInferenceSeed(t, s, nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s)))
	before, identity := datasetSchemaPrefixDigest(t, s), licenseSchemaDeployment(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, e := Run(ctx, s, c, testOptions(), Apply, func(event Event) {
		if event.Step == step && event.Stage == stage {
			cancel()
		}
	})
	if e == nil || r.Ready || r.DirtyStep != step || !r.LockReleased {
		t.Fatal("interrupted dataset DDL lost dirty journal or lock", r, e)
	}
	var state string
	if e = s.Pool().QueryRowContext(t.Context(), `SELECT state FROM schema_migrations WHERE step=?`, step).Scan(&state); e != nil || state != Applying {
		t.Fatal("interrupted dataset journal must remain applying", mysqlstore.SafeError(e))
	}
	if _, e = Run(t.Context(), s, c, testOptions(), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
		t.Fatal("dirty dataset Apply silently repeated", e)
	}
	if r, e = Run(t.Context(), s, c, testOptions(), Readiness, nil); e == nil || r.Ready || !r.LockReleased {
		t.Fatal("dirty dataset schema served ready or retained lock", r, e)
	}
	if datasetSchemaPrefixDigest(t, s) != before {
		t.Fatal("dirty refusal changed accepted1–51 prefix")
	}
	migrationRun(t, s, c, Reconcile)
	if datasetSchemaPrefixDigest(t, s) != before || licenseSchemaDeployment(t, s) != identity {
		t.Fatal("explicit dataset reconciliation changed accepted data/journal/identity")
	}
	datasetSchemaSeed(t, s, old)
	datasetSchemaAssertRows(t, s)
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLDatasetSchemaBefore52(t *testing.T) { datasetSchemaInterrupted(t, 52, Applying) }

func TestActualMySQLDatasetSchemaAfter52(t *testing.T) { datasetSchemaInterrupted(t, 52, Verified) }

func TestActualMySQLDatasetSchemaBefore53(t *testing.T) { datasetSchemaInterrupted(t, 53, Applying) }

func TestActualMySQLDatasetSchemaAfter53(t *testing.T) { datasetSchemaInterrupted(t, 53, Verified) }

func TestActualMySQLDatasetSchemaBefore54(t *testing.T) { datasetSchemaInterrupted(t, 54, Applying) }

func TestActualMySQLDatasetSchemaAfter54(t *testing.T) { datasetSchemaInterrupted(t, 54, Verified) }

func TestActualMySQLDatasetSchemaBefore55(t *testing.T) { datasetSchemaInterrupted(t, 55, Applying) }

func TestActualMySQLDatasetSchemaAfter55(t *testing.T) { datasetSchemaInterrupted(t, 55, Verified) }

func TestActualMySQLDatasetSchemaBefore56(t *testing.T) { datasetSchemaInterrupted(t, 56, Applying) }

func TestActualMySQLDatasetSchemaAfter56(t *testing.T) { datasetSchemaInterrupted(t, 56, Verified) }

func TestActualMySQLDatasetSchemaBefore57(t *testing.T) { datasetSchemaInterrupted(t, 57, Applying) }

func TestActualMySQLDatasetSchemaAfter57(t *testing.T) { datasetSchemaInterrupted(t, 57, Verified) }

func TestActualMySQLDatasetSchemaBefore58(t *testing.T) { datasetSchemaInterrupted(t, 58, Applying) }

func TestActualMySQLDatasetSchemaAfter58(t *testing.T) { datasetSchemaInterrupted(t, 58, Verified) }

func TestActualMySQLDatasetSchemaBefore59(t *testing.T) { datasetSchemaInterrupted(t, 59, Applying) }

func TestActualMySQLDatasetSchemaAfter59(t *testing.T) { datasetSchemaInterrupted(t, 59, Verified) }

func datasetSchemaRefuseModes(t *testing.T, s *mysqlstore.Store, c Catalog, code string, digest func() string) {
	t.Helper()
	before, identity := digest(), licenseSchemaDeployment(t, s)
	for _, mode := range []Mode{Readiness, Apply, Reconcile} {
		r, e := Run(t.Context(), s, c, testOptions(), mode, nil)
		if e == nil || e.Error() != code || r.Ready || !r.LockReleased || digest() != before || licenseSchemaDeployment(t, s) != identity {
			t.Fatal("dataset drift mode altered state or accepted unsafe metadata", mode, r, e)
		}
	}
}
func TestActualMySQLDatasetSchemaUnjournaledDataset(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:51]
	migrationRun(t, s, prefix, Apply)
	modelInferenceSeed(t, s, nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s)))
	migrationExec(t, s, c.steps[51].sql)
	datasetSchemaRefuseModes(t, s, c, CodeUnjournaledObject, func() string { return datasetSchemaPrefixDigest(t, s) })
	var count int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='datasets'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("unjournaled dataset silently altered", mysqlstore.SafeError(e))
	}
}
func TestActualMySQLDatasetSchemaMissingAppliedHistory(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Apply)
	modelInferenceSeed(t, s, nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s)))
	// Remove only the empty leaf in the exclusive synthetic scope.
	migrationExec(t, s, `DROP TABLE dataset_row_history`)
	digest := func() string {
		return hash(datasetSchemaPrefixDigest(t, s) + licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step>=52 ORDER BY step`))
	}
	datasetSchemaRefuseModes(t, s, c, CodeDefinitionDrift, digest)
	for _, table := range []string{"datasets", "dataset_drafts", "dataset_rows", "dataset_jobs", "dataset_job_attempts", "dataset_versions"} {
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); e != nil || count != 0 {
			t.Fatal("missing history refusal changed another table", table, mysqlstore.SafeError(e))
		}
	}
	var count int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='dataset_row_history'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("missing applied history silently recreated", mysqlstore.SafeError(e))
	}
}
func TestActualMySQLDatasetSchemaWrongDefinition(t *testing.T) {
	s, c, _ := datasetSchemaReady(t)
	migrationExec(t, s, `ALTER TABLE datasets ADD INDEX unexpected_dataset_index(created_at)`)
	datasetSchemaRefuseModes(t, s, c, CodeDefinitionDrift, func() string { return datasetSchemaRecoveryDigest(t, s) })
}
func TestActualMySQLDatasetSchemaWrongSourceChecksum(t *testing.T) {
	s, c, _ := datasetSchemaReady(t)
	migrationExec(t, s, `UPDATE schema_migrations SET source_sha256=REPEAT('b',64) WHERE step=52`)
	datasetSchemaRefuseModes(t, s, c, CodeChecksumMismatch, func() string { return datasetSchemaRecoveryDigest(t, s) })
}
func TestActualMySQLDatasetSchemaPrepareRecovery(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:51]
	migrationRun(t, s, prefix, Apply)
	old := modelInferenceSeed(t, s, nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s)))
	before := datasetSchemaPrefixDigest(t, s)
	migrationRun(t, s, c, Apply)
	if datasetSchemaPrefixDigest(t, s) != before {
		t.Fatal("recovery setup changed accepted1–51 prefix")
	}
	datasetSchemaSeed(t, s, old)
	datasetSchemaAssertRows(t, s)
	migrationRun(t, s, c, Readiness)
	t.Log("MYSQL_DATASET_DATA_SHA256=" + datasetSchemaRecoveryDigest(t, s))
}
func TestActualMySQLDatasetSchemaReadback(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Readiness)
	datasetSchemaAssertRows(t, s)
	var dataset string
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT id FROM datasets`).Scan(&dataset); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	datasetSchemaRefuses(t, s, 3819, `UPDATE datasets SET rights_asserted=0 WHERE id=?`, dataset)
	t.Log("MYSQL_DATASET_DATA_SHA256=" + datasetSchemaRecoveryDigest(t, s))
}

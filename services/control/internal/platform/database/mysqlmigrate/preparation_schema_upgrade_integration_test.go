//go:build mysql_integration

package mysqlmigrate

import "testing"

func TestActualMySQLPreparationSchemaUpgrade(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:94]
	migrationRun(t, s, prefix, Apply)
	old := holdoutSchemaSeed(t, s, holdoutSchemaReservationSeed(t, s))
	holdoutSchemaAssertRows(t, s, old)
	holdoutSchemaAssertReservations(t, s)
	before, identity := holdoutSchemaDataDigest(t, s), licenseSchemaDeployment(t, s)
	migrationRun(t, s, c, Apply)
	if holdoutSchemaDataDigest(t, s) != before || licenseSchemaDeployment(t, s) != identity {
		t.Fatal("94-to117 changed complete old business/journal/identity")
	}
	holdoutSchemaAssertReservations(t, s)
	var owners int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM gpu_leases WHERE model_preparation_id IS NULL AND (deployment_id IS NOT NULL OR training_job_id IS NOT NULL)`).Scan(&owners); e != nil || owners != 2 {
		t.Fatal("new preparation owner column changed existing inference/training identity")
	}
	r := preparationSchemaSeed(t, s, old)
	preparationSchemaAssertRows(t, s, r)
	after := preparationSchemaDataDigest(t, s)
	var events int
	result, e := Run(t.Context(), s, c, testOptions(), Apply, func(Event) { events++ })
	if e != nil || !result.Ready || !result.LockReleased || events != 0 || preparationSchemaDataDigest(t, s) != after {
		t.Fatal("upgraded preparation no-op changed complete graph or lock", result, e)
	}
	migrationRun(t, s, c, Readiness)
	t.Log("PREPARATION_SCHEMA_POPULATED94_UPGRADE_PRESERVED old_business_journal_sha256=" + before)
}

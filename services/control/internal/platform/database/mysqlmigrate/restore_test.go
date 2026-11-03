//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"testing"
)

func TestActualMySQLMigrationRestoredSemanticDriftRefusal(t *testing.T) {
	s, catalog, _ := actualMigration(t)
	migrationRun(t, s, catalog, Readiness)
	migrationExec(t, s, "ALTER TABLE tenants MODIFY name VARCHAR(100) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL")
	if _, e := Run(context.Background(), s, catalog, testOptions(), Readiness, nil); e == nil || e.Error() != CodeDefinitionDrift {
		t.Fatal("real collation drift hidden by metadata normalization", e)
	}
}

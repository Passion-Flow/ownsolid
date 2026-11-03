//go:build mysql_integration

package mysqlmigrate

import "testing"

// The quality gate retains the upgraded exclusive database,then runs this in
// independent processes before/after a real fixture restart and fresh restore.
func TestActualMySQLSessionForeignKeyIndexReadback(t *testing.T) {
	s, catalog, _ := actualMigration(t)
	migrationRun(t, s, catalog, Readiness)
	sessionFKDefinition(t, s, catalog)
	var sessions int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sessions`).Scan(&sessions); e != nil || sessions < 1 {
		t.Fatal("retained real session data required")
	}
	t.Log("MYSQL_SESSION_FK_DATA_SHA256=" + hash(sessionFKDataDigest(t, s)))
}

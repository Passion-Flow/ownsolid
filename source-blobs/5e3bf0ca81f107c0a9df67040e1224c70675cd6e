package database_test

import (
	"os"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAuditCandidateProductionMigratorAfterRestore(t *testing.T) {
	raw, err := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_DSN_FILE"))
	if err != nil {
		t.Fatal("owned PostgreSQL fixture input required")
	}
	prefix := os.Getenv("AUDIT_FIXTURE_PREFIX")
	if prefix != "baseline" && prefix != "integrated" {
		t.Fatal("owned PostgreSQL fixture prefix required")
	}
	suffixes := []string{"fresh_test", "55_test", "56_test", "57_test"}
	if database.SchemaVersion == 58 {
		suffixes = append(suffixes, "58_test")
	}
	for _, suffix := range suffixes {
		name := prefix + "_" + suffix
		t.Run(name, func(t *testing.T) {
			config, err := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
			if err != nil {
				t.Fatal("invalid PostgreSQL fixture input")
			}
			config.ConnConfig.Database = name
			db, err := pgxpool.NewWithConfig(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(db.Close)
			if err := database.Migrate(t.Context(), db); err != nil {
				t.Fatalf("migration: %v", err)
			}
			if err := database.Migrate(t.Context(), db); err != nil {
				t.Fatalf("migration replay: %v", err)
			}
			if err := database.Check(t.Context(), db); err != nil {
				t.Fatalf("admission: %v", err)
			}
		})
	}
}

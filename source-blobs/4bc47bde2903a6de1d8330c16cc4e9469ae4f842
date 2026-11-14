package audit_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func newAuditFixture(t *testing.T, version int) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PROJECT_ONE_TEST_DSN")
	if file := os.Getenv("PROJECT_ONE_TEST_DSN_FILE"); file != "" {
		if dsn != "" {
			t.Fatal("specify only one PostgreSQL fixture input")
		}
		raw, err := config.Secret(file)
		if err != nil {
			t.Fatal("could not read the PostgreSQL fixture input")
		}
		dsn = strings.TrimSpace(string(raw))
	}
	if dsn == "" {
		t.Skip("PROJECT_ONE_TEST_DSN is required for real PostgreSQL integration")
	}
	pc, err := pgxpool.ParseConfig(dsn)
	if err != nil || pc.ConnConfig.Host != "127.0.0.1" || !strings.HasSuffix(pc.ConnConfig.Database, "_test") {
		t.Fatal("an isolated loopback test database is required")
	}
	ctx := t.Context()
	operator, err := pgxpool.NewWithConfig(ctx, pc.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(operator.Close)
	schema := "audit_fixture_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := operator.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := operator.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("drop owned audit fixture schema: %v", err)
		}
	})
	pc.MaxConns = 8
	pc.ConnConfig.RuntimeParams["search_path"] = schema
	pc.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pc.ConnConfig.RuntimeParams["lock_timeout"] = "3000"
	db, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if version == 0 {
		if err := database.Migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	} else {
		seedAuditMigrationPrefix(t, db, version)
	}
	return db
}

// The upgrade tests load the published prefix, then use the production runner
// to validate its ledger and append the remaining migrations.
func seedAuditMigrationPrefix(t *testing.T, db *pgxpool.Pool, version int) {
	t.Helper()
	var lineage struct {
		Entries []struct {
			Version int    `json:"version"`
			Path    string `json:"source_path"`
			SHA256  string `json:"sha256"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(release.MVPPostgreSQLCPULineage(), &lineage); err != nil || version < 1 || version >= len(lineage.Entries) {
		t.Fatal("invalid published PostgreSQL fixture prefix", err)
	}
	ctx := t.Context()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `CREATE TABLE schema_migrations(version integer PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for _, entry := range lineage.Entries[:version] {
		path := strings.TrimPrefix(entry.Path, "services/control/migrations/")
		sql, err := migrations.Files.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatal(entry.Version, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)", entry.Version, entry.SHA256); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

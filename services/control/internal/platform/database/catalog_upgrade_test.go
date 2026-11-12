package database_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCatalogAdmissionAfterLogicalRestore(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_DSN_FILE") == "" && os.Getenv("PROJECT_ONE_TEST_DSN") == "" {
		t.Skip("requires an owned PostgreSQL restore fixture")
	}
	for _, version := range []int{55, 56, 57, 58, database.SchemaVersion} {
		t.Run(fmt.Sprintf("restore_at_%d", version), func(t *testing.T) {
			ctx := t.Context()
			fixture.PreflightOwnedPostgres(t)
			source := fixture.NewAtVersion(t, version)
			owned := fixture.OwnedPostgres(t, source.DB)
			archive := owned.DumpSchema(t, ctx)
			restored := owned.RestoreFresh(t, ctx, source.DB, archive)

			if version == 55 {
				testCatalogDriftRollsBack(t, restored)
			}

			if err := database.Migrate(ctx, restored); err != nil {
				t.Fatalf("upgrade restored schema %d: %v", version, err)
			}
			settled := catalogUpgradeSnapshot(t, restored)
			if err := database.Migrate(ctx, restored); err != nil {
				t.Fatalf("repeat migration: %v", err)
			}
			if got := catalogUpgradeSnapshot(t, restored); got != settled {
				t.Fatal("repeat migration changed the catalog or ledger")
			}
			if err := database.Check(ctx, restored); err != nil {
				t.Fatalf("check restored and upgraded schema: %v", err)
			}
		})
	}
}

func testCatalogDriftRollsBack(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := t.Context()
	var original string
	if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conrelid = 'tenants'::regclass AND conname = 'tenants_status_check'`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	const replaceConstraint = `ALTER TABLE tenants DROP CONSTRAINT tenants_status_check,
		ADD CONSTRAINT tenants_status_check `
	before := catalogUpgradeSnapshot(t, pool)
	if _, err := pool.Exec(ctx, replaceConstraint+`CHECK (status IS NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	drifted := catalogUpgradeSnapshot(t, pool)
	for range 2 {
		if err := database.Migrate(ctx, pool); err == nil || err.Error() != "SCHEMA_CATALOG_INCOMPATIBLE" {
			t.Fatalf("catalog drift must prevent migration: %v", err)
		}
		if got := catalogUpgradeSnapshot(t, pool); got != drifted {
			t.Fatal("failed migration changed the catalog or ledger")
		}
	}
	if _, err := pool.Exec(ctx, replaceConstraint+original); err != nil {
		t.Fatal(err)
	}
	if got := catalogUpgradeSnapshot(t, pool); got != before {
		t.Fatal("constraint repair did not restore the original catalog and ledger")
	}
}

type upgradeSnapshot struct {
	catalog string
	ledger  string
}

func catalogUpgradeSnapshot(t *testing.T, pool *pgxpool.Pool) upgradeSnapshot {
	t.Helper()
	var snapshot upgradeSnapshot
	if err := pool.QueryRow(t.Context(), database.CatalogSQL).Scan(&snapshot.catalog); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT jsonb_agg(to_jsonb(m) ORDER BY version)::text
		FROM schema_migrations AS m`).Scan(&snapshot.ledger); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

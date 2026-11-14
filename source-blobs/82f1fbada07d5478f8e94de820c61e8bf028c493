package database_test

import (
	"context"
	"sync"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM02ActualUpgradeFailureRecoveryAndChecksums(t *testing.T) {
	s := fixture.NewM01(t)
	fixture.Bootstrap(t, s)
	ctx := context.Background()
	if e := database.Check(ctx, s.DB); e == nil || e.Error() != "SCHEMA_INCOMPATIBLE" {
		t.Fatal("new server accepted old schema", e)
	}
	// A synthetic conflicting table forces the actual embedded additive migration to fail.
	if _, e := s.DB.Exec(ctx, "CREATE TABLE workspace_notes(id integer)"); e != nil {
		t.Fatal(e)
	}
	if e := database.Migrate(ctx, s.DB); e == nil {
		t.Fatal("failed migration reported success")
	}
	var versions, columns, users int
	if e := s.DB.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&versions); e != nil || versions != 1 {
		t.Fatal("partial migration metadata", e)
	}
	if e := s.DB.QueryRow(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='workspaces' AND column_name='version'").Scan(&columns); e != nil || columns != 0 {
		t.Fatal("partial DDL survived rollback", e)
	}
	if _, e := s.DB.Exec(ctx, "DROP TABLE workspace_notes"); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- database.Migrate(ctx, s.DB) }()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatal("concurrent migration failed", e)
		}
	}
	if e := database.Check(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	if e := s.DB.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&users); e != nil || users != 1 {
		t.Fatal("upgrade lost account", e)
	}
	var checksum string
	if e := s.DB.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE version=1").Scan(&checksum); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(ctx, "UPDATE schema_migrations SET checksum='tampered' WHERE version=1"); e != nil {
		t.Fatal(e)
	}
	if e := database.Check(ctx, s.DB); e == nil || e.Error() != "SCHEMA_INCOMPATIBLE" {
		t.Fatal("old migration tampering not rejected", e)
	}
	if e := database.Migrate(ctx, s.DB); e == nil || e.Error() != "MIGRATION_CHECKSUM_MISMATCH" {
		t.Fatal("operator ignored old checksum", e)
	}
	if _, e := s.DB.Exec(ctx, "UPDATE schema_migrations SET checksum=$1 WHERE version=1", checksum); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(ctx, "INSERT INTO schema_migrations(version,checksum) SELECT max(version)+1,'future' FROM schema_migrations"); e != nil {
		t.Fatal(e)
	}
	if e := database.Check(ctx, s.DB); e == nil || e.Error() != "SCHEMA_INCOMPATIBLE" {
		t.Fatal("future schema accepted", e)
	}
	if e := database.Migrate(ctx, s.DB); e == nil || e.Error() != "SCHEMA_INCOMPATIBLE" {
		t.Fatal("operator accepted future schema", e)
	}
}

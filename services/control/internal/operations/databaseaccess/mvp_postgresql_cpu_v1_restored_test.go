package databaseaccess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestMVPPostgreSQLCPUV1RestoredAcceptedAccount22Upgrades(t *testing.T) {
	f := mvpPopulatedRoleFixture(t, identityfixture.NewM16WithSetup(t, identityfixture.SetupOptions{TimeoutSeconds: 180, StatementTimeoutSeconds: 30}))
	ctx := t.Context()
	if status, e := Prepare(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	identityfixture.Bootstrap(t, f.service)
	owned := identityfixture.OwnedPostgres(t, f.service.DB)
	dump := owned.DumpSchema(t, ctx)
	restored := owned.RestoreFresh(t, ctx, f.service.DB, dump, true)
	lease, e := restored.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, e := lease.Exec(cleanup, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
			t.Error(e)
		}
		lease.Release()
	})
	var locked bool
	if e = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || !locked {
		t.Fatal("new restored target original lease", e)
	}
	if _, e = lease.Exec(ctx, "SET statement_timeout=30000"); e != nil {
		t.Fatal(e)
	}
	old := f.input
	old.connection = f.input.connection.Copy()
	old.connection.ConnConfig.Database = restored.Config().ConnConfig.Database
	// The maintained restore fixture deliberately omits ACLs. Reapply exactly the
	// original accepted grant set for the same cluster principal before upgrade;
	// neither its OID nor SCRAM verifier is recreated or rekeyed.
	role := pgx.Identifier{f.role}.Sanitize()
	schema := restored.Config().ConnConfig.RuntimeParams["search_path"]
	statements := []string{"GRANT USAGE ON SCHEMA " + pgx.Identifier{schema}.Sanitize() + " TO " + role, "GRANT SELECT ON TABLE " + pgx.Identifier{schema, "schema_migrations"}.Sanitize() + " TO " + role}
	for _, table := range runtimeTables(old) {
		statements = append(statements, "GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE "+pgx.Identifier{schema, table}.Sanitize()+" TO "+role)
	}
	for _, sql := range statements {
		if _, e = lease.Exec(ctx, sql); e != nil {
			t.Fatal(e)
		}
	}
	if state, e := Check(ctx, restored, lease, old); e != nil || state != Verified {
		t.Fatal("actual restored accepted precursor", state, e)
	}
	targetFixture := f
	service := *f.service
	service.DB = restored
	targetFixture.service = &service
	targetFixture.lease = lease
	targetFixture.input = old
	before := mvpCommonDataFingerprint(t, targetFixture)
	principal, e := readMVPPrincipal(ctx, lease, old)
	if e != nil {
		t.Fatal(e)
	}
	next := mvpRuntimeInput(t, targetFixture)
	for i := 0; i < 2; i++ {
		if state, e := UpgradeMVPPostgreSQLCPUV1(ctx, restored, lease, next); e != nil || state != MVPPostgreSQLCPUV1UpgradeComplete {
			t.Fatal("restored precursor upgrade/noop", state, e)
		}
	}
	after, e := readMVPPrincipal(ctx, lease, next)
	if e != nil || after != principal || mvpCommonDataFingerprint(t, targetFixture) != before {
		t.Fatal("restored precursor identity/data changed", e)
	}
	var raw string
	if e = lease.QueryRow(ctx, database.MVPPostgreSQLCPUV1CatalogSQL).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	digest := sha256.Sum256([]byte(raw))
	if hex.EncodeToString(digest[:]) != "b62e804f80505293af0bb218b1e1cce2a056339e07c78f289d455e1ee51b173e" {
		t.Fatal("unexpected upgraded native reparse representation")
	}
	// Exact representation admission must still reject changed semantics.
	tx, e := lease.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, e = tx.Exec(ctx, "ALTER TABLE users ALTER COLUMN display_name TYPE varchar(100) COLLATE \"C\""); e != nil {
		t.Fatal(e)
	}
	if e = database.CheckMVPPostgreSQLCPUV1Within(ctx, transaction.PostgreSQLTransaction(tx)); e == nil {
		t.Fatal("restored precursor pin bypassed column integrity")
	}
}

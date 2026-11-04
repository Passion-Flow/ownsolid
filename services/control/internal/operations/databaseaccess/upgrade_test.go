package databaseaccess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// upgradeFixture is an accepted installation at an earlier lineage version
// whose runtime principal holds DML on exactly the tables of that version.
func upgradeFixture(t *testing.T, version int) actualFixture {
	t.Helper()
	f := actualFixtureFor(t, identityfixture.NewAtVersion(t, version))
	identityfixture.Bootstrap(t, f.service)
	ctx := t.Context()
	if _, e := f.lease.Exec(ctx, "SET statement_timeout=30000"); e != nil {
		t.Fatal(e)
	}
	role := pgx.Identifier{f.role}.Sanitize()
	verifier, e := newVerifier(f.input.connection.ConnConfig.Password, 4096)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.lease.Exec(ctx, "CREATE ROLE "+role+" LOGIN NOINHERIT CONNECTION LIMIT "+strconv.Itoa(int(f.input.configuration.Database.MaxOpenConnections))+" PASSWORD '"+verifier+"'"); e != nil {
		t.Fatal(e)
	}
	var schema string
	if e = f.lease.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); e != nil {
		t.Fatal(e)
	}
	statements := []string{"GRANT USAGE ON SCHEMA " + pgx.Identifier{schema}.Sanitize() + " TO " + role, "GRANT SELECT ON TABLE " + pgx.Identifier{schema, "schema_migrations"}.Sanitize() + " TO " + role}
	rows, e := f.lease.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename<>'schema_migrations' ORDER BY 1")
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var table string
		if e = rows.Scan(&table); e != nil {
			t.Fatal(e)
		}
		statements = append(statements, "GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE "+pgx.Identifier{schema, table}.Sanitize()+" TO "+role)
	}
	rows.Close()
	for _, statement := range statements {
		if _, e = f.lease.Exec(ctx, statement); e != nil {
			t.Fatal(e)
		}
	}
	return f
}

func upgradeDataFingerprint(t *testing.T, f actualFixture, tables []string) string {
	t.Helper()
	h := sha256.New()
	for _, table := range tables {
		var raw string
		if e := f.lease.QueryRow(t.Context(), "SELECT COALESCE(string_agg((to_jsonb(r)-'site_id'-'cell_id')::text,E'\\n' ORDER BY (to_jsonb(r)-'site_id'-'cell_id')::text),'') FROM "+pgx.Identifier{table}.Sanitize()+" r").Scan(&raw); e != nil {
			t.Fatal(table, e)
		}
		h.Write([]byte(table + "\x00" + raw + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func existingTables(t *testing.T, f actualFixture) []string {
	t.Helper()
	rows, e := f.lease.Query(t.Context(), "SELECT tablename FROM pg_tables WHERE schemaname=current_schema() AND tablename<>'schema_migrations' ORDER BY 1")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var table string
		if e = rows.Scan(&table); e != nil {
			t.Fatal(e)
		}
		out = append(out, table)
	}
	return out
}

func TestUpgradeFromDeliveredPrefixPreservesRoleAndData(t *testing.T) {
	f := upgradeFixture(t, 22)
	ctx := t.Context()
	if state, e := InspectUpgrade(ctx, f.service.DB, f.lease, f.input); e != nil || state != UpgradeRequired {
		t.Fatal(state, e)
	}
	before, e := readRuntimePrincipal(ctx, f.lease, f.input)
	if e != nil {
		t.Fatal(e)
	}
	// Migration 32 drops the log table: the telemetry stores own that data (ADR 0012).
	oldTables := slices.DeleteFunc(existingTables(t, f), func(name string) bool { return name == "component_log_snapshots" })
	data := upgradeDataFingerprint(t, f, oldTables)
	if state, e := Upgrade(ctx, f.service.DB, f.lease, f.input); e != nil || state != UpgradeComplete {
		t.Fatal(state, e)
	}
	if after, e := readRuntimePrincipal(ctx, f.lease, f.input); e != nil || after != before {
		t.Fatal("runtime principal changed", e)
	}
	if upgradeDataFingerprint(t, f, oldTables) != data {
		t.Fatal("existing rows changed")
	}
	if e := database.Check(ctx, f.service.DB); e != nil {
		t.Fatal(e)
	}
	if status, e := Check(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	// The principal that serving uses must pass the serving admission itself.
	runtime, e := pgxpool.NewWithConfig(ctx, f.input.connection.Copy())
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.Close()
	store, e := transaction.FromPostgreSQL(runtime)
	if e != nil {
		t.Fatal(e)
	}
	if e = database.CheckRuntimeWithin(ctx, store); e != nil {
		t.Fatal("runtime principal refused by serving admission", e)
	}
	if state, e := Upgrade(ctx, f.service.DB, f.lease, f.input); e != nil || state != UpgradeComplete {
		t.Fatal("repeat", state, e)
	}
}

func TestUpgradeResumesAfterSchemaCommittedAndUnknownGrantCommit(t *testing.T) {
	f := upgradeFixture(t, 22)
	ctx := t.Context()
	lost := errors.New("synthetic commit acknowledgement loss")
	steps := upgradeSteps{database.Migrate, func(ctx context.Context, tx pgx.Tx) error {
		if e := tx.Commit(ctx); e != nil {
			return e
		}
		return lost
	}}
	if state, e := upgrade(ctx, f.service.DB, f.lease, f.input, steps); e == nil || e.Error() != string(transaction.CommitUnknown) || state != "" {
		t.Fatal("unknown grant commit reported success", state, e)
	}
	if state, e := InspectUpgrade(ctx, f.service.DB, f.lease, f.input); e != nil || state != UpgradeComplete {
		t.Fatal("committed grants not observed", state, e)
	}
}

func TestUpgradeResumesGrantsAfterSchemaCommitted(t *testing.T) {
	g := upgradeFixture(t, 22)
	ctx := t.Context()
	if e := database.Migrate(ctx, g.lease); e != nil {
		t.Fatal(e)
	}
	if state, e := InspectUpgrade(ctx, g.service.DB, g.lease, g.input); e != nil || state != UpgradeGrantsPending {
		t.Fatal(state, e)
	}
	if state, e := Upgrade(ctx, g.service.DB, g.lease, g.input); e != nil || state != UpgradeComplete {
		t.Fatal(state, e)
	}
}

func TestUpgradeRejectsForeignLedgerAndMissingRole(t *testing.T) {
	f := upgradeFixture(t, 22)
	ctx := t.Context()
	if _, e := f.lease.Exec(ctx, "UPDATE schema_migrations SET checksum=repeat('0',64) WHERE version=3"); e != nil {
		t.Fatal(e)
	}
	if _, e := InspectUpgrade(ctx, f.service.DB, f.lease, f.input); e == nil || e.Error() != "UPGRADE_ACCEPTED_LINEAGE_REQUIRED" {
		t.Fatal("foreign ledger admitted", e)
	}
}

func TestUpgradeRejectsMissingRole(t *testing.T) {
	g := actualFixtureFor(t, identityfixture.NewAtVersion(t, 22))
	identityfixture.Bootstrap(t, g.service)
	if _, e := InspectUpgrade(t.Context(), g.service.DB, g.lease, g.input); e == nil || e.Error() != "UPGRADE_EXISTING_RUNTIME_ROLE_REQUIRED" {
		t.Fatal("missing runtime role admitted", e)
	}
}

func TestUpgradeRejectsElevatedRoleBeforeDDL(t *testing.T) {
	ctx := t.Context()
	f := upgradeFixture(t, 22)
	if _, e := f.lease.Exec(ctx, "ALTER ROLE "+pgx.Identifier{f.role}.Sanitize()+" CREATEDB"); e != nil {
		t.Fatal(e)
	}
	if _, e := Upgrade(ctx, f.service.DB, f.lease, f.input); e == nil || e.Error() != "RUNTIME_ACCESS_ROLE_OR_CREDENTIAL_CONFLICT" {
		t.Fatal("elevated role reached upgrade", e)
	}
	var applied int
	if e := f.lease.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&applied); e != nil || applied != 22 {
		t.Fatal("DDL ran for elevated role", applied, e)
	}
}

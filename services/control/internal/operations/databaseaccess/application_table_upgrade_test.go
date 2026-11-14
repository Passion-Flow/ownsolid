package databaseaccess

import (
	"errors"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// resumeTableGrantUpgrade retains the exact predecessor principal and ledger
// while exercising a newly introduced application table's actual privileges.
func resumeTableGrantUpgrade(t *testing.T, predecessor int, table string) (actualFixture, *pgxpool.Pool) {
	t.Helper()
	f := upgradeFixture(t, predecessor)
	ctx := t.Context()
	before, err := readRuntimePrincipal(ctx, f.lease, f.input)
	if err != nil {
		t.Fatal(err)
	}
	var oldLedger string
	if err := f.lease.QueryRow(ctx, "SELECT jsonb_agg(to_jsonb(m) ORDER BY version)::text FROM schema_migrations m").Scan(&oldLedger); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, f.lease); err != nil {
		t.Fatal(err)
	}
	if state, err := InspectUpgrade(ctx, f.service.DB, f.lease, f.input); err != nil || state != UpgradeGrantsPending {
		t.Fatal("new table must require grant resume", state, err)
	}
	runtime, err := pgxpool.NewWithConfig(ctx, f.input.connection.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	store, err := transaction.FromPostgreSQL(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CheckRuntimeWithin(ctx, store); err == nil || err.Error() != "DATABASE_RUNTIME_PRIVILEGES_INCOMPATIBLE" {
		t.Fatal("missing runtime grant did not receive the exact serving refusal", err)
	}
	_, missingGrant := runtime.Exec(ctx, "SELECT * FROM "+pgx.Identifier{table}.Sanitize())
	var missingGrantError *pgconn.PgError
	if !errors.As(missingGrant, &missingGrantError) || missingGrantError.Code != "42501" {
		t.Fatal("new table did not refuse the predecessor runtime grant", missingGrant)
	}
	for range 2 {
		if state, err := Upgrade(ctx, f.service.DB, f.lease, f.input); err != nil || state != UpgradeComplete {
			t.Fatal("grant resume", state, err)
		}
		if after, err := readRuntimePrincipal(ctx, f.lease, f.input); err != nil || after != before {
			t.Fatal("grant resume changed runtime identity", err)
		}
		var retainedLedger string
		if err := f.lease.QueryRow(ctx, "SELECT jsonb_agg(to_jsonb(m) ORDER BY version)::text FROM schema_migrations m WHERE version<=$1", predecessor).Scan(&retainedLedger); err != nil || retainedLedger != oldLedger {
			t.Fatal("grant resume changed the predecessor ledger", err)
		}
		if err := database.CheckRuntimeWithin(ctx, store); err != nil {
			t.Fatal("runtime admission after grant resume", err)
		}
	}

	for _, statement := range []string{
		"SELECT * FROM audit_chain_head",
		"UPDATE schema_migrations SET checksum=checksum WHERE version=1",
		"TRUNCATE " + pgx.Identifier{table}.Sanitize(),
		"ALTER TABLE " + pgx.Identifier{table}.Sanitize() + " DISABLE TRIGGER ALL",
	} {
		_, err := runtime.Exec(ctx, statement)
		var denied *pgconn.PgError
		if !errors.As(err, &denied) || denied.Code != "42501" {
			t.Fatal("runtime exceeded its grants", statement, err)
		}
	}
	return f, runtime
}

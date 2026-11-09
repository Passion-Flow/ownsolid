package metering

import (
	_ "embed"
	"errors"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed testdata/ledger.down.sql
var testLedgerDownSQL string

func TestPostgresLedgerCandidateUpgradesFromSchema55(t *testing.T) {
	old := identityfixture.NewAtVersion(t, 55)
	ctx := t.Context()
	scope := Scope{TenantID: uuid.New(), WorkspaceID: uuid.New()}
	if _, err := old.DB.Exec(ctx, "INSERT INTO tenants(id,name,status) VALUES($1,'Metering predecessor tenant','active')", scope.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := old.DB.Exec(ctx, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Metering predecessor workspace','active')",
		scope.WorkspaceID, scope.TenantID); err != nil {
		t.Fatal(err)
	}
	history := func(through int) string {
		t.Helper()
		var encoded string
		if err := old.DB.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(m) ORDER BY version)::text
 FROM schema_migrations m WHERE version <= $1`, through).Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	predecessor := history(55)
	if err := database.Migrate(ctx, old.DB); err != nil {
		t.Fatal("canonical predecessor upgrade:", err)
	}
	if err := database.Check(ctx, old.DB); err != nil {
		t.Fatal("canonical predecessor readiness:", err)
	}
	if got := history(55); got != predecessor {
		t.Fatal("canonical upgrade changed the historical migration ledger")
	}
	lineage := history(database.SchemaVersion)
	if err := pgx.BeginFunc(ctx, old.DB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, testSchemaSQL)
		return err
	}); err != nil {
		t.Fatal("install unnumbered candidate:", err)
	}
	var name string
	if err := old.DB.QueryRow(ctx, "SELECT name FROM workspaces WHERE tenant_id=$1 AND id=$2", scope.TenantID, scope.WorkspaceID).
		Scan(&name); err != nil || name != "Metering predecessor workspace" {
		t.Fatalf("predecessor workspace lost: %q, %v", name, err)
	}
	if got := history(database.SchemaVersion + 1); got != lineage {
		t.Fatal("the unnumbered candidate changed the numbered ledger")
	}

	at := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	event := Event{Producer: "fixture/migration", ID: "first-usage", OccurredAt: at, Usage: Usage{Requests: 1}}
	if inserted, err := writeEvent(ctx, old.DB, scope, event); err != nil || !inserted {
		t.Fatalf("write after predecessor upgrade: %v, %v", inserted, err)
	}
	if inserted, err := writeEvent(ctx, old.DB, scope, event); err != nil || inserted {
		t.Fatalf("replay after predecessor upgrade: %v, %v", inserted, err)
	}
	err := pgx.BeginFunc(ctx, old.DB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, testLedgerDownSQL)
		return err
	})
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "55000" || pgError.Message != "METERING_LEDGER_NOT_EMPTY" {
		t.Fatalf("occupied ledger rollback was not refused: %v", err)
	}
	if got := readTotals(t, old.DB, scope, at, at.Add(time.Hour)); got.Requests != "1" {
		t.Fatalf("failed rollback changed accrued usage: %+v", got)
	}
}

func TestPostgresLedgerCandidateDownRemovesEmptyTable(t *testing.T) {
	pool := newTestLedger(t)
	ctx := t.Context()
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, testLedgerDownSQL)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var gone bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('usage_ledger') IS NULL
  AND to_regprocedure('guard_usage_ledger_write()') IS NULL`).Scan(&gone); err != nil || !gone {
		t.Fatalf("empty candidate objects remain: %v, %v", gone, err)
	}
}

func TestPostgresLedgerCandidateRefusesForeignCellReplayAndFiltersReports(t *testing.T) {
	pool := newTestLedger(t)
	scope := addTestScope(t, pool)
	ctx := t.Context()
	at := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	end := at.Add(time.Hour)
	foreign := Event{Producer: "fixture/foreign-cell", ID: "retained", OccurredAt: at, Usage: Usage{Requests: 900}}
	// Record the retained event while its cell is the fixture's local cell.
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE cells SET is_local=false WHERE is_local;
 UPDATE cells SET is_local=true WHERE id='sibling-cell'`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO usage_ledger
 (producer,event_id,tenant_id,workspace_id,occurred_at,gpu_microseconds,tokens,storage_byte_seconds,requests)
 VALUES($1,$2,$3,$4,$5,0,0,0,$6)`,
			foreign.Producer, foreign.ID, scope.TenantID, scope.WorkspaceID, at, foreign.Usage.Requests); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE cells SET is_local=false WHERE is_local;
 UPDATE cells SET is_local=true WHERE id='fixture-cell'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if inserted, err := writeEvent(ctx, pool, scope, foreign); inserted || !errors.Is(err, ErrEventConflict) {
		t.Fatalf("foreign event reused as local: %v, %v", inserted, err)
	}
	local := Event{Producer: "fixture/local-cell", ID: "current", OccurredAt: at, Usage: Usage{Requests: 3}}
	if inserted, err := writeEvent(ctx, pool, scope, local); err != nil || !inserted {
		t.Fatalf("local event failed: %v, %v", inserted, err)
	}
	if got := readTotals(t, pool, scope, at, end); got.Requests != "3" {
		t.Fatalf("foreign usage affected local totals: %+v", got)
	}
	if got := testShowback(t, pool, scope, at, end, testCard(t, testRateCard)); !got.FullyRated ||
		got.KnownAmountMicros != "2" || len(got.Lines) != 1 || got.Lines[0].Quantity != "3" {
		t.Fatalf("foreign usage affected local showback: %+v", got)
	}
}

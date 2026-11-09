package metering

import (
	"context"
	_ "embed"
	"errors"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed testdata/ledger.sql
var testSchemaSQL string

const testWorkspaceSQL = `CREATE TABLE workspaces (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  PRIMARY KEY (tenant_id, id)
);

`

func newTestLedger(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsnFile := os.Getenv("OWNSOLID_METERING_TEST_DSN_FILE")
	if dsnFile == "" {
		t.Skip("set OWNSOLID_METERING_TEST_DSN_FILE to a disposable local PostgreSQL database")
	}
	raw, err := os.ReadFile(dsnFile)
	if err != nil {
		t.Fatal("read test database configuration:", err)
	}
	cfg, err := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") ||
		(!filepath.IsAbs(cfg.ConnConfig.Host) && !net.ParseIP(cfg.ConnConfig.Host).IsLoopback()) ||
		len(cfg.ConnConfig.Fallbacks) != 0 {
		t.Fatal("test database must use a local socket or loopback with no fallbacks and end in _test")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := "ledger_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE"); err != nil {
			t.Error("drop test ledger schema:", err)
		}
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(t.Context(), testWorkspaceSQL+testSchemaSQL); err != nil {
		t.Fatal(err)
	}
	return pool
}

func addTestScope(t *testing.T, pool *pgxpool.Pool) Scope {
	t.Helper()
	scope := Scope{TenantID: uuid.New(), WorkspaceID: uuid.New()}
	if _, err := pool.Exec(t.Context(), "INSERT INTO workspaces VALUES ($1,$2)", scope.TenantID, scope.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	return scope
}

func writeEvent(ctx context.Context, pool *pgxpool.Pool, scope Scope, event Event) (bool, error) {
	var inserted bool
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var err error
		inserted, err = AppendWithin(ctx, tx, scope, event)
		return err
	})
	return inserted && err == nil, err
}

func readTotals(t *testing.T, pool *pgxpool.Pool, scope Scope, from, to time.Time) Totals {
	t.Helper()
	var total Totals
	err := pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
		var err error
		total, err = TotalsWithin(t.Context(), tx, scope, from, to)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return total
}

func TestPostgresLedgerAtomicConcurrentReplayAndClockRollback(t *testing.T) {
	pool := newTestLedger(t)
	scope := addTestScope(t, pool)
	foreign := addTestScope(t, pool)
	eventTime := time.Date(2026, 11, 9, 2, 0, 0, 123456789, time.UTC)
	event := Event{Producer: "scheduler/cell", ID: "attempt-a/1", OccurredAt: eventTime,
		Usage: Usage{GPUMicroseconds: math.MaxInt64, Tokens: 10, StorageByteSeconds: 50, Requests: 1}}

	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	var inserted atomic.Int32
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			<-start
			ok, err := writeEvent(t.Context(), pool, scope, event)
			if ok {
				inserted.Add(1)
			}
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if inserted.Load() != 1 {
		t.Fatalf("duplicate race inserted %d entries", inserted.Load())
	}

	changed := event
	changed.Usage.Tokens++
	if _, err := writeEvent(t.Context(), pool, scope, changed); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	if _, err := writeEvent(t.Context(), pool, foreign, event); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("cross-workspace replay: %v", err)
	}
	rolledBackTime := event
	rolledBackTime.ID = "attempt-a/2"
	rolledBackTime.OccurredAt = event.OccurredAt.Add(-time.Minute)
	if ok, err := writeEvent(t.Context(), pool, scope, rolledBackTime); !ok || err != nil {
		t.Fatalf("valid event after clock rollback: %v, %v", ok, err)
	}
	if ok, err := writeEvent(t.Context(), pool, scope, event); ok || err != nil {
		t.Fatalf("replay after lost acknowledgement: %v, %v", ok, err)
	}
	if got := readTotals(t, pool, scope, eventTime.Add(-time.Hour), eventTime.Add(time.Hour)); got != (Totals{"18446744073709551614", "20", "100", "2", "0"}) {
		t.Fatalf("incorrect total: %+v", got)
	}
	if got := readTotals(t, pool, scope, eventTime.Add(-time.Hour), eventTime); got.Requests != "1" {
		t.Fatalf("incorrect half-open window: %+v", got)
	}
	if got := readTotals(t, pool, foreign, eventTime.Add(-time.Hour), eventTime.Add(time.Hour)); got != (Totals{"0", "0", "0", "0", "0"}) {
		t.Fatalf("another workspace can read usage: %+v", got)
	}
	for _, stmt := range []string{"UPDATE usage_ledger SET requests = 9", "DELETE FROM usage_ledger", "TRUNCATE usage_ledger"} {
		var pgErr *pgconn.PgError
		if _, err := pool.Exec(t.Context(), stmt); !errors.As(err, &pgErr) || pgErr.Code != "55000" {
			t.Fatalf("ledger mutation was not refused: %v", err)
		}
	}
}

func TestPostgresLedgerConnectionLossBeforeCommit(t *testing.T) {
	pool := newTestLedger(t)
	scope := addTestScope(t, pool)
	now := time.Now()
	event := Event{Producer: "scheduler/cell", ID: "attempt-b/1", OccurredAt: now, Usage: Usage{Requests: 1}}
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), "SET LOCAL synchronous_commit = off"); err != nil {
		t.Fatal(err)
	}
	if inserted, err := AppendWithin(t.Context(), tx, scope, event); !inserted || err != nil {
		t.Fatalf("initial write: %v, %v", inserted, err)
	}
	var syncCommit string
	if err := tx.QueryRow(t.Context(), "SHOW synchronous_commit").Scan(&syncCommit); err != nil || syncCommit != "on" {
		t.Fatalf("ledger write did not require a synchronous commit: %q, %v", syncCommit, err)
	}
	pid := conn.Conn().PgConn().PID()
	if _, err := pool.Exec(t.Context(), "SELECT pg_terminate_backend($1, 5000)", pid); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err == nil {
		t.Fatal("interrupted connection reported a commit")
	}
	if got := readTotals(t, pool, scope, now.Add(-time.Hour), now.Add(time.Hour)); got.Requests != "0" {
		t.Fatalf("uncommitted event visible: %+v", got)
	}
	if ok, err := writeEvent(t.Context(), pool, scope, event); !ok || err != nil {
		t.Fatalf("retry after connection loss: %v, %v", ok, err)
	}
	if got := readTotals(t, pool, scope, now.Add(-time.Hour), now.Add(time.Hour)); got.Requests != "1" {
		t.Fatalf("retried event not recorded exactly once: %+v", got)
	}
}

func TestLedgerValidation(t *testing.T) {
	scope := Scope{TenantID: uuid.New(), WorkspaceID: uuid.New()}
	event := Event{Producer: "scheduler/cell", ID: "attempt/1", OccurredAt: time.Now(), Usage: Usage{Requests: 1}}
	if _, err := event.normalized(scope); err != nil {
		t.Fatal(err)
	}
	negative := event
	negative.Usage.Tokens = -1
	missingTime := event
	missingTime.OccurredAt = time.Time{}
	for _, invalid := range []Event{{}, negative, missingTime} {
		if _, err := invalid.normalized(scope); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("accepted invalid event: %+v", invalid)
		}
	}
	if _, err := event.normalized(Scope{}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("missing attribution: %v", err)
	}
}

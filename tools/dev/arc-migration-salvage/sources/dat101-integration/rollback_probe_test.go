package draftprobe

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fixture struct {
	pool *pgxpool.Pool
	ctx  context.Context
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	raw, err := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_DSN_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("invalid owned fixture connection")
	}
	if config.ConnConfig.Host != "127.0.0.1" || !strings.HasSuffix(config.ConnConfig.Database, "_test") {
		t.Fatal("owned loopback test database required")
	}
	config.MaxConns = 4 // Writer, downgrade, observer and cleanup are independent connections.
	base, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	schema := "arc_draft_" + strings.ToLower(rand.Text())
	if _, err = base.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		defer base.Close()
		if _, err := base.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error("owned schema cleanup", err)
		}
	})
	_, err = pool.Exec(ctx, `CREATE FUNCTION local_site_id() RETURNS varchar LANGUAGE sql IMMUTABLE AS $$SELECT 'default'::varchar$$;
 CREATE FUNCTION local_cell_id() RETURNS varchar LANGUAGE sql IMMUTABLE AS $$SELECT 'default'::varchar$$;
 CREATE TABLE dataset_drafts(tenant_id uuid NOT NULL,workspace_id uuid NOT NULL,dataset_id uuid NOT NULL,id uuid PRIMARY KEY,site_id varchar(32) NOT NULL DEFAULT local_site_id(),cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),legacy jsonb NOT NULL);
 INSERT INTO dataset_drafts(tenant_id,workspace_id,dataset_id,id,legacy) VALUES('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000004','{"source":"retained legacy JSONL"}');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, read(t, "ARC_DRAFT_UP_FILE")); err != nil {
		t.Fatal(err)
	}
	return fixture{pool, ctx}
}
func read(t *testing.T, key string) string {
	t.Helper()
	raw, err := os.ReadFile(os.Getenv(key))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func (f fixture) receipt(t *testing.T, format string) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO dataset_draft_import_candidates(tenant_id,workspace_id,dataset_id,draft_id,site_id,cell_id,source_format,report) SELECT tenant_id,workspace_id,dataset_id,id,site_id,cell_id,$1,'{"source_bytes":42,"output_bytes":31}'::jsonb FROM dataset_drafts`, format); err != nil {
		t.Fatal(err)
	}
}
func (f fixture) retained(t *testing.T) string {
	t.Helper()
	var out string
	if err := f.pool.QueryRow(f.ctx, `SELECT jsonb_build_object('drafts',(SELECT jsonb_agg(to_jsonb(d) ORDER BY id) FROM dataset_drafts d),'receipts',(SELECT jsonb_agg(to_jsonb(c) ORDER BY draft_id) FROM dataset_draft_import_candidates c))::text`).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
func refusal(t *testing.T, err error, code string) {
	t.Helper()
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != code || code == "55000" && pg.Message != "DATASET_DRAFT_IMPORT_DOWNGRADE_REQUIRES_RESTORE" {
		t.Fatalf("expected %s retention refusal, got %v", code, err)
	}
}
func (f fixture) down(t *testing.T, sql string) error {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, sql); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}

func TestOriginalDownRetainsReceipt(t *testing.T) {
	f := newFixture(t)
	f.receipt(t, "parquet")
	before := f.retained(t)
	tx, err := f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	_, err = tx.Exec(f.ctx, read(t, "ARC_DRAFT_ORIGINAL_DOWN_FILE"))
	if err != nil {
		t.Fatal("original reproduction did not reach destructive DDL", err)
	}
	var gone bool
	if err = tx.QueryRow(f.ctx, `SELECT to_regclass('dataset_draft_import_candidates') IS NULL`).Scan(&gone); err != nil || !gone {
		t.Fatal("original destructive result not observed", err)
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if f.retained(t) != before {
		t.Fatal("independent reproduction rollback failed to retain fixture data")
	}
	t.Fatal("original down removed a retained import receipt; outer rollback preserved the reproduction data")
}
func TestGuardedPristineDown(t *testing.T) {
	f := newFixture(t)
	if err := f.down(t, read(t, "ARC_DRAFT_SAFE_DOWN_FILE")); err != nil {
		t.Fatal(err)
	}
	var intact bool
	if err := f.pool.QueryRow(f.ctx, `SELECT to_regclass('dataset_drafts') IS NOT NULL AND to_regclass('dataset_draft_import_candidates') IS NULL AND NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='dataset_drafts'::regclass AND conname='dataset_draft_import_scope_key') AND (SELECT legacy='{"source":"retained legacy JSONL"}'::jsonb FROM dataset_drafts)`).Scan(&intact); err != nil || !intact {
		t.Fatal("pristine rollback changed the legacy draft or retained its new key", err)
	}
}
func TestGuardedRetainedReceipts(t *testing.T) {
	for _, format := range []string{"csv", "parquet"} {
		t.Run(format, func(t *testing.T) {
			f := newFixture(t)
			f.receipt(t, format)
			before := f.retained(t)
			for range 2 {
				refusal(t, f.down(t, read(t, "ARC_DRAFT_SAFE_DOWN_FILE")), "55000")
				if f.retained(t) != before {
					t.Fatal("refused rollback changed retained data")
				}
			}
		})
	}
}
func TestGuardedPriorSnapshot(t *testing.T) {
	for _, isolation := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(isolation), func(t *testing.T) {
			f := newFixture(t)
			tx, err := f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			var initial int
			if err = tx.QueryRow(f.ctx, "SELECT count(*) FROM dataset_draft_import_candidates").Scan(&initial); err != nil || initial != 0 {
				t.Fatal("prior snapshot", err)
			}
			f.receipt(t, "parquet")
			before := f.retained(t)
			_, err = tx.Exec(f.ctx, read(t, "ARC_DRAFT_SAFE_DOWN_FILE"))
			refusal(t, err, "25001")
			if err = tx.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			if f.retained(t) != before {
				t.Fatal("old snapshot guard changed committed data")
			}
		})
	}
}
func TestGuardedFreshIsolation(t *testing.T) {
	for _, isolation := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.ReadUncommitted, pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(isolation), func(t *testing.T) {
			f := newFixture(t)
			tx, err := f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			f.receipt(t, "csv")
			before := f.retained(t)
			_, err = tx.Exec(f.ctx, read(t, "ARC_DRAFT_SAFE_DOWN_FILE"))
			refusal(t, err, "55000")
			if err = tx.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			if f.retained(t) != before {
				t.Fatal("fresh isolation guard changed committed data")
			}
		})
	}
}
func TestGuardedWaitsForCommittedWriter(t *testing.T) {
	f := newFixture(t)
	writer, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(f.ctx)
	if _, err = writer.Exec(f.ctx, `INSERT INTO dataset_draft_import_candidates(tenant_id,workspace_id,dataset_id,draft_id,site_id,cell_id,source_format,report) SELECT tenant_id,workspace_id,dataset_id,id,site_id,cell_id,'parquet','{}'::jsonb FROM dataset_drafts`); err != nil {
		t.Fatal(err)
	}
	down, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer down.Rollback(f.ctx)
	result := make(chan error, 1)
	sql := read(t, "ARC_DRAFT_SAFE_DOWN_FILE")
	go func() { _, err := down.Exec(f.ctx, sql); result <- err }()
	wait, stop := context.WithTimeout(f.ctx, 3*time.Second)
	defer stop()
	for {
		var blocked bool
		if err = f.pool.QueryRow(wait, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='dataset_drafts'::regclass AND mode='AccessExclusiveLock' AND NOT granted)`, down.Conn().PgConn().PID()).Scan(&blocked); err != nil {
			t.Fatal("observe waiting rollback", err)
		}
		if blocked {
			break
		}
		select {
		case err := <-result:
			t.Fatal("rollback did not wait for its writer", err)
		case <-wait.Done():
			t.Fatal("rollback did not reach the table retention fence")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err = writer.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	refusal(t, <-result, "55000")
	if err = down.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	var receipts int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM dataset_draft_import_candidates").Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("waiting rollback discarded the committed receipt", err)
	}
}

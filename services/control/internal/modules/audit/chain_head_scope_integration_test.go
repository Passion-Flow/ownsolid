package audit_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const auditHeadScopeMigration = "postgresql/000058_audit_chain_head_scope"
const auditHeadScopePredecessor = 57

type auditHeadState struct {
	position, scopedFrom int64
	hash                 string
}

func applyAuditHeadScopeMigration(t *testing.T, db *pgxpool.Pool, direction string) {
	t.Helper()
	raw, err := migrations.Files.ReadFile(auditHeadScopeMigration + "." + direction + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err := tx.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func readAuditHeadState(t *testing.T, db *pgxpool.Pool) auditHeadState {
	t.Helper()
	var state auditHeadState
	err := db.QueryRow(t.Context(), `SELECT chain_position, coalesce(scoped_from,0), encode(hash,'hex')
		FROM audit_chain_head WHERE singleton`).Scan(&state.position, &state.scopedFrom, &state.hash)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func requireAuditHeadScope(t *testing.T, db *pgxpool.Pool, site, cell string) {
	t.Helper()
	var actualSite, actualCell string
	err := db.QueryRow(t.Context(), "SELECT site_id,cell_id FROM audit_chain_head WHERE singleton").Scan(&actualSite, &actualCell)
	if err != nil || actualSite != site || actualCell != cell {
		t.Fatalf("head scope %q/%q, want %q/%q: %v", actualSite, actualCell, site, cell, err)
	}
	var defaults int
	err = db.QueryRow(t.Context(), `SELECT count(*) FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='audit_chain_head' AND is_nullable='NO'
		  AND ((column_name='site_id' AND column_default='local_site_id()')
		    OR (column_name='cell_id' AND column_default='local_cell_id()'))`).Scan(&defaults)
	if err != nil || defaults != 2 {
		t.Fatalf("head has %d non-null local defaults: %v", defaults, err)
	}
}

func TestAuditChainHeadScopeFresh(t *testing.T) {
	for _, scope := range []struct{ site, cell string }{{"default", "default"}, {"fixture-site", "fixture-cell"}} {
		t.Run(scope.cell, func(t *testing.T) {
			db := newAuditFixture(t, auditHeadScopePredecessor)
			ctx := t.Context()
			if scope.cell != "default" {
				if _, err := db.Exec(ctx, `INSERT INTO sites(id,display_name) VALUES($1,'Fixture Site')`, scope.site); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(ctx, "UPDATE cells SET is_local=false WHERE is_local"); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(ctx, `INSERT INTO cells(id,site_id,display_name,is_local)
					VALUES($1,$2,'Fixture Cell',true)`, scope.cell, scope.site); err != nil {
					t.Fatal(err)
				}
			}
			before := readAuditHeadState(t, db)
			applyAuditHeadScopeMigration(t, db, "up")
			requireAuditHeadScope(t, db, scope.site, scope.cell)
			if after := readAuditHeadState(t, db); before != after || after.position != 0 {
				t.Fatalf("empty head changed: %+v -> %+v", before, after)
			}
			if _, err := db.Exec(ctx, chainInsert, uuid.NewString(), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			event := auditExport(t, db)
			if len(event) != 1 || field(t, event[0], "site_id") != scope.site ||
				field(t, event[0], "cell_id") != scope.cell || field(t, event[0], "prev_hash") != before.hash {
				t.Fatal("first event did not take the local scope and genesis predecessor")
			}
		})
	}
}

func requireAuditScopeRefusal(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.Message != "AUDIT_CHAIN_CELL_SCOPE_MISMATCH" {
		t.Fatalf("expected scoped append refusal, got %v", err)
	}
}

func rejectAuditForeignScopeAsWriter(t *testing.T, db *pgxpool.Pool, at time.Time) {
	t.Helper()
	ctx := t.Context()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	exec := func(sql string) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	var schema string
	if err := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	role := pgx.Identifier{"audit_scope_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	exec("CREATE ROLE " + role + " NOLOGIN")
	exec("GRANT USAGE ON SCHEMA " + pgx.Identifier{schema}.Sanitize() + " TO " + role)
	exec("GRANT SELECT ON cells TO " + role)
	exec("GRANT INSERT ON audit_events TO " + role)
	exec("SET LOCAL ROLE " + role)
	for _, scope := range [][2]string{{"foreign", "default"}, {"default", "foreign"}, {"foreign", "foreign"}} {
		attempt, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := attempt.Exec(ctx, `INSERT INTO audit_events
			(id,occurred_at,audience,action,outcome,request_id,site_id,cell_id)
			VALUES($1,$2,'system','fixture.foreign','success',$1,$3,$4)`, uuid.NewString(), at, scope[0], scope[1])
		if err := attempt.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		requireAuditScopeRefusal(t, writeErr)
	}
	attempt, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := attempt.Exec(ctx, "SELECT site_id,cell_id FROM audit_chain_head")
	if err := attempt.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var pgErr *pgconn.PgError
	if !errors.As(readErr, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("serving role read the private head: %v", readErr)
	}
	if _, err := tx.Exec(ctx, chainInsert, uuid.NewString(), at); err != nil {
		t.Fatalf("local append failed for serving role: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAuditChainHeadScopeRetainsMixedChain(t *testing.T) {
	db := newAuditFixture(t, auditChainFirstSchema)
	ctx := t.Context()
	at := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Exec(ctx, chainInsert, uuid.NewString(), at); err != nil {
		t.Fatal(err)
	}
	appendAuditFixtureMigrations(t, db, auditChainFirstSchema+1, auditHeadScopePredecessor)
	if _, err := db.Exec(ctx, chainInsert, uuid.NewString(), at); err != nil {
		t.Fatal(err)
	}
	prefix := auditExport(t, db)
	before := readAuditHeadState(t, db)
	if len(prefix) != 2 || before.position != 2 || before.scopedFrom != 2 ||
		field(t, prefix[0], "format") != "ownsolid.audit.v1" || field(t, prefix[1], "format") != "ownsolid.audit.v2" {
		t.Fatal("expected V1 and V2 predecessor evidence")
	}
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	requireAuditHeadScope(t, db, "default", "default")
	if after := readAuditHeadState(t, db); after != before {
		t.Fatalf("migration changed historical head fields: %+v -> %+v", before, after)
	}
	rejectAuditForeignScopeAsWriter(t, db, at)
	drift, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer drift.Rollback(context.WithoutCancel(ctx))
	if _, err := drift.Exec(ctx, `INSERT INTO cells(id,site_id,display_name) VALUES('fixture-other','default','Other');
		UPDATE cells SET is_local=false WHERE is_local;
		UPDATE cells SET is_local=true WHERE id='fixture-other'`); err != nil {
		t.Fatal(err)
	}
	_, writeErr := drift.Exec(ctx, chainInsert, uuid.NewString(), at)
	requireAuditScopeRefusal(t, writeErr)
	if err := drift.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if after := readAuditHeadState(t, db); after != before {
		t.Fatal("scope refusal or rolled-back append advanced the head")
	}

	const count = 32
	workCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range count {
		workers.Go(func() {
			<-start
			if _, err := db.Exec(workCtx, chainInsert, uuid.NewString(), at); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	workers.Wait()
	lines := auditExport(t, db)
	if len(lines) != len(prefix)+count || !bytes.Equal(bytes.Join(lines[:len(prefix)], nil), bytes.Join(prefix, nil)) {
		t.Fatal("concurrent appends rewrote or lost the retained prefix")
	}
	previous := before.hash
	for index, line := range lines[len(prefix):] {
		if field(t, line, "position") != strconv.Itoa(len(prefix)+index+1) ||
			field(t, line, "prev_hash") != previous || field(t, line, "site_id") != "default" ||
			field(t, line, "cell_id") != "default" {
			t.Fatal("concurrent positions, predecessors or cell scope diverged")
		}
		previous = field(t, line, "hash")
	}
	after := readAuditHeadState(t, db)
	if after.position != int64(len(lines)) || after.hash != previous || after.scopedFrom != before.scopedFrom {
		t.Fatalf("head does not match the final retained event: %+v", after)
	}

	t.Run("offline_verifier", func(t *testing.T) {
		binary := os.Getenv("OWNSOLID_TEST_AUDIT_VERIFY")
		if binary == "" {
			t.Skip("set OWNSOLID_TEST_AUDIT_VERIFY to verify the saved checkpoint")
		}
		verify := func(records [][]byte, args ...string) {
			t.Helper()
			cmd := exec.CommandContext(t.Context(), binary, append([]string{"--file", "-"}, args...)...)
			cmd.Stdin = bytes.NewReader(bytes.Join(records, []byte("\n")))
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("offline verification failed: %v: %s", err, output)
			}
		}
		verify(lines[:len(prefix)], "--expected-last-hash", before.hash)
		verify(lines[len(prefix):], "--previous-hash", before.hash, "--expected-last-hash", after.hash)
	})
	applyAuditHeadScopeMigration(t, db, "down")
	if readAuditHeadState(t, db) != after || !bytes.Equal(bytes.Join(auditExport(t, db), nil), bytes.Join(lines, nil)) {
		t.Fatal("reverse migration changed the retained chain")
	}
	if _, err := db.Exec(ctx, chainInsert, uuid.NewString(), at); err != nil {
		t.Fatal(err)
	}
	if next := readAuditHeadState(t, db); next.position != after.position+1 || next.scopedFrom != after.scopedFrom {
		t.Fatal("original appender could not continue after the reverse migration")
	}
}

//go:build repository_integration

package audit_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This fixture is only used by an explicit tagged quality gate,never production.
// Missing,nonlocal or nonisolated inputs fail rather than become skipped passes.
type repositoryFixture struct {
	store                         transaction.Store
	postgres                      *pgxpool.Pool
	tenant, workspace, user, note string
}

func actualRepository(t *testing.T) repositoryFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := repositoryFixture{tenant: uuid.NewString(), workspace: uuid.NewString(), user: uuid.NewString(), note: uuid.NewString()}
	switch os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") {
	case "mysql":
		c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
		if e != nil || c.Host != "127.0.0.1" || !strings.HasPrefix(c.Database, "m20_repository_") || !strings.HasSuffix(c.Database, "_test") {
			t.Fatal("REPOSITORY_ACTUAL_ISOLATED_MYSQL_REQUIRED")
		}
		kernel, _, e := mysqlstore.Open(ctx, c)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if e := kernel.Close(); e != nil {
				t.Error("actual MySQL close")
			}
		})
		catalog, e := mysqlmigrate.DefaultCatalog()
		if e != nil {
			t.Fatal(e)
		}
		opts := mysqlmigrate.Options{LockWaitSeconds: 1, StepSeconds: 5, TotalSeconds: 20, CleanupSeconds: 2, MaximumSteps: 100, MaximumMetadataBytes: 1 << 20}
		if _, e = mysqlmigrate.Run(ctx, kernel, catalog, opts, mysqlmigrate.Apply, nil); e != nil {
			t.Fatal(e)
		}
		f.store, e = transaction.FromMySQL(kernel)
		if e != nil {
			t.Fatal(e)
		}
	case "postgresql":
		raw, e := config.Secret(os.Getenv("PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE"))
		if e != nil {
			t.Fatal("REPOSITORY_PRIVATE_POSTGRES_INPUT_REQUIRED")
		}
		pc, e := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
		if e != nil || pc.ConnConfig.Host != "127.0.0.1" || !strings.HasSuffix(pc.ConnConfig.Database, "_test") {
			t.Fatal("REPOSITORY_ACTUAL_ISOLATED_POSTGRES_REQUIRED")
		}
		pc.MaxConns = 2
		pc.ConnConfig.RuntimeParams["timezone"] = "UTC"
		pc.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
		pc.ConnConfig.RuntimeParams["lock_timeout"] = "3000"
		operator, e := pgxpool.NewWithConfig(ctx, pc)
		if e != nil {
			t.Fatal("actual PostgreSQL operator open")
		}
		schema := os.Getenv("PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA")
		if !regexp.MustCompile(`^m20_repository_[a-f0-9]{32}$`).MatchString(schema) {
			operator.Close()
			t.Fatal("REPOSITORY_EXACT_OWNED_SCHEMA_REQUIRED")
		}
		if _, e = operator.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
			operator.Close()
			t.Fatal("actual PostgreSQL schema create")
		}
		pc.ConnConfig.RuntimeParams["search_path"] = schema
		f.postgres, e = pgxpool.NewWithConfig(ctx, pc)
		if e != nil {
			t.Fatal("actual PostgreSQL fixture pool open")
		}
		t.Cleanup(func() {
			f.postgres.Close()
			cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			if _, e := operator.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"); e != nil {
				t.Error("actual owned schema cleanup failed")
			}
			operator.Close()
		})
		if e = database.Migrate(ctx, f.postgres); e != nil {
			t.Fatal("actual PostgreSQL fixture migration", e)
		}
		f.store, e = transaction.FromPostgreSQL(f.postgres)
		if e != nil {
			t.Fatal(e)
		}
	default:
		t.Fatal("REPOSITORY_EXPLICIT_REAL_ENGINE_REQUIRED")
	}
	if e := f.store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		commands := []transaction.Statement{
			f.query("INSERT INTO tenants(id,name,status) VALUES($1,$2,'active')", "INSERT INTO tenants(id,name,status) VALUES(?,?,'active')", f.tenant, "Repository tenant"),
			f.query("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,$3,'active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,?,'active')", f.workspace, f.tenant, "Repository Workspace"),
			f.query("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,$4,'en','active',true)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,?,'en','active',true)", f.user, "synthetic@example.invalid", "synthetic@example.invalid", "Repository actor"),
			f.query("UPDATE installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1", "UPDATE installation_state SET bootstrap_complete=true,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=UTC_TIMESTAMP(6) WHERE id=1", f.tenant, f.workspace, f.user),
			f.query("INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id) VALUES($1,$2,$3,$4,$5,$6)", "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,`text`,author_user_id) VALUES(?,?,?,?,?,?)", f.note, f.tenant, f.workspace, "Repository note", "original", f.user),
		}
		for _, q := range commands {
			if _, e := tx.Exec(ctx, q); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal("actual fixture seed", e)
	}
	return f
}

func (f repositoryFixture) query(pg, my string, args ...any) transaction.Statement {
	if f.store.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, args...)
	}
	return transaction.SQL(transaction.PostgreSQL, pg, args...)
}
func (f repositoryFixture) noteText(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var text string
	if e := f.store.QueryRow(ctx, f.query("SELECT text FROM workspace_notes WHERE id=$1", "SELECT `text` FROM workspace_notes WHERE id=?", f.note)).Scan(&text); e != nil {
		t.Fatal(e)
	}
	return text
}
func (f repositoryFixture) count(t *testing.T) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var count int
	if e := f.store.QueryRow(ctx, f.query("SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")).Scan(&count); e != nil {
		t.Fatal(e)
	}
	return count
}

func TestActualRepositoryTransactionsAndAudit(t *testing.T) {
	f := actualRepository(t)
	t.Run("commit_exact_unicode_scope_and_nullable_rows", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		request := uuid.NewString()
		beforeTime := time.Now().UTC()
		digest := bytes.Repeat([]byte{0x34}, 32)
		e := f.store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			n, e := tx.Exec(ctx, f.query("UPDATE workspace_notes SET text=$1 WHERE id=$2", "UPDATE workspace_notes SET `text`=? WHERE id=?", "committed \u4e16\u754c", f.note))
			if e != nil {
				return e
			}
			if n != 1 {
				return errors.New("ACTUAL_AFFECTED_ROWS_MISMATCH")
			}
			if e = audit.WriteWithin(ctx, tx, f.user, f.user, "admin", "repository.committed", "success", "workspace_note", f.note, "\u7406\u7531 ' ? $1 \u4e16\u754c", request); e != nil {
				return e
			}
			return audit.WriteAnonymousWithin(ctx, tx, "portal", "repository.anonymous", "failure", "", digest)
		})
		if e != nil || f.noteText(t) != "committed \u4e16\u754c" || f.count(t) != 2 {
			t.Fatal("actual atomic commit", e)
		}
		rows, e := f.store.Query(ctx, f.query("SELECT id::text,actor_user_id::text,subject_user_id::text,tenant_id::text,workspace_id::text,target_id::text,reason,request_id::text,identifier_digest,occurred_at FROM audit_events ORDER BY action", "SELECT id,actor_user_id,subject_user_id,tenant_id,workspace_id,target_id,reason,request_id,identifier_digest,occurred_at FROM audit_events ORDER BY action"))
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var id, scope, workspace, requestID string
			var actor, subject, target, reason *string
			var got []byte
			var occurred time.Time
			if e = rows.Scan(&id, &actor, &subject, &scope, &workspace, &target, &reason, &requestID, &got, &occurred); e != nil {
				t.Fatal(e)
			}
			if _, e = uuid.Parse(id); e != nil {
				t.Fatal("actual event UUID")
			}
			if _, e = uuid.Parse(requestID); e != nil {
				t.Fatal("actual request UUID")
			}
			if scope != f.tenant || workspace != f.workspace || occurred.IsZero() || occurred.Before(beforeTime.Add(-time.Second)) || occurred.After(time.Now().UTC().Add(time.Second)) {
				t.Fatal("actual scope/time")
			}
			if count == 0 {
				if actor != nil || subject != nil || target != nil || reason != nil || !bytes.Equal(got, digest) {
					t.Fatal("actual anonymous nullable values")
				}
			} else if actor == nil || *actor != f.user || subject == nil || *subject != f.user || target == nil || *target != f.note || reason == nil || *reason != "\u7406\u7531 ' ? $1 \u4e16\u754c" || requestID != request || got != nil {
				t.Fatal("actual precise actor event")
			}
			count++
		}
		if e = rows.Err(); e != nil || count != 2 {
			t.Fatal("actual iterator", e, count)
		}
	})
	t.Run("domain_failure_rolls_back_business_and_audit", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sentinel := errors.New("REPOSITORY_TEST_DOMAIN_CONFLICT")
		e := f.store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			if _, e := tx.Exec(ctx, f.query("UPDATE workspace_notes SET text=$1 WHERE id=$2", "UPDATE workspace_notes SET `text`=? WHERE id=?", "must roll back", f.note)); e != nil {
				return e
			}
			if e := audit.WriteWithin(ctx, tx, "", "", "system", "repository.rolled_back", "success", "", "", "", ""); e != nil {
				return e
			}
			return sentinel
		})
		if e != sentinel || f.noteText(t) != "committed \u4e16\u754c" || f.count(t) != 2 {
			t.Fatal("actual domain rollback", e)
		}
	})
	t.Run("failed_audit_prevents_business_commit", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		e := f.store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			if _, e := tx.Exec(ctx, f.query("UPDATE workspace_notes SET text=$1 WHERE id=$2", "UPDATE workspace_notes SET `text`=? WHERE id=?", "must roll back", f.note)); e != nil {
				return e
			}
			return audit.WriteWithin(ctx, tx, "", "", "invalid_audience", "repository.invalid", "success", "", "", "", "")
		})
		var safe *transaction.Error
		if !errors.As(e, &safe) || safe.Kind != transaction.CheckConflict || f.noteText(t) != "committed \u4e16\u754c" || f.count(t) != 2 {
			t.Fatal("actual audit failure atomicity", e)
		}
	})
	t.Run("read_only_rejects_actual_write", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		e := f.store.Within(ctx, true, func(ctx context.Context, tx transaction.Executor) error {
			_, e := tx.Exec(ctx, f.query("UPDATE workspace_notes SET text=$1 WHERE id=$2", "UPDATE workspace_notes SET `text`=? WHERE id=?", "read only must refuse", f.note))
			return e
		})
		if e == nil || f.noteText(t) != "committed \u4e16\u754c" {
			t.Fatal("actual read-only violation", e)
		}
	})
	t.Run("prepared_projection_no_rows_constraint_and_wrong_dialect", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		q := transaction.SQL(transaction.PostgreSQL, "SELECT $1::int+$1", 7)
		if f.store.Dialect() == transaction.MySQL {
			q = transaction.SQL(transaction.MySQL, "SELECT ?+?", 7, 7)
		}
		var sum int
		if e := f.store.QueryRow(ctx, q).Scan(&sum); e != nil || sum != 14 {
			t.Fatal("actual owned parameter projection", e, sum)
		}
		var id string
		e := f.store.QueryRow(ctx, f.query("SELECT id::text FROM users WHERE id=$1", "SELECT id FROM users WHERE id=?", uuid.NewString())).Scan(&id)
		if !errors.Is(e, pgx.ErrNoRows) || !errors.Is(e, sql.ErrNoRows) {
			t.Fatal("actual no-row identity", e)
		}
		e = f.store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			_, e := tx.Exec(ctx, f.query("INSERT INTO tenants(id,name,status) VALUES($1,$2,'active')", "INSERT INTO tenants(id,name,status) VALUES(?,?,'active')", f.tenant, "secret duplicate value"))
			return e
		})
		var safe *transaction.Error
		if !errors.As(e, &safe) || safe.Kind != transaction.UniqueConflict || strings.Contains(e.Error(), "secret") {
			t.Fatal("actual safe duplicate", e)
		}
		wrong := transaction.MySQL
		if f.store.Dialect() == transaction.MySQL {
			wrong = transaction.PostgreSQL
		}
		if _, e := f.store.Exec(ctx, transaction.SQL(wrong, "DELETE FROM audit_events")); e == nil || e.Error() != "REPOSITORY_STATEMENT_DIALECT_MISMATCH" || f.count(t) != 2 {
			t.Fatal("actual wrong dialect refusal", e)
		}
	})
	t.Run("cancelled_callback_has_no_durable_mutation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		e := f.store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			if _, e := tx.Exec(ctx, f.query("UPDATE workspace_notes SET text=$1 WHERE id=$2", "UPDATE workspace_notes SET `text`=? WHERE id=?", "cancelled mutation", f.note)); e != nil {
				return e
			}
			cancel()
			return nil
		})
		if !errors.Is(e, context.Canceled) || f.noteText(t) != "committed \u4e16\u754c" || f.count(t) != 2 {
			t.Fatal("actual cancellation rollback", e)
		}
	})
	t.Run("two_current_row_locks_preserve_audit_and_version", func(t *testing.T) {
		start := make(chan struct{})
		results := make(chan error, 2)
		var wait sync.WaitGroup
		for i := 0; i < 2; i++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				results <- f.store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
					var version int64
					if e := tx.QueryRow(ctx, f.query("SELECT version FROM workspace_notes WHERE id=$1 FOR UPDATE", "SELECT version FROM workspace_notes WHERE id=? FOR UPDATE", f.note)).Scan(&version); e != nil {
						return e
					}
					if _, e := tx.Exec(ctx, f.query("UPDATE workspace_notes SET version=$1 WHERE id=$2", "UPDATE workspace_notes SET version=? WHERE id=?", version+1, f.note)); e != nil {
						return e
					}
					return audit.WriteWithin(ctx, tx, f.user, f.user, "admin", "repository.concurrent", "success", "workspace_note", f.note, "", "")
				})
			}()
		}
		close(start)
		wait.Wait()
		close(results)
		for e := range results {
			if e != nil {
				t.Fatal("actual concurrent transaction", e)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var version int64
		if e := f.store.QueryRow(ctx, f.query("SELECT version FROM workspace_notes WHERE id=$1", "SELECT version FROM workspace_notes WHERE id=?", f.note)).Scan(&version); e != nil || version != 3 || f.count(t) != 4 {
			t.Fatal("actual lost version/audit", e, version)
		}
	})
	if f.postgres != nil {
		t.Run("existing_postgresql_public_audit_entrypoints", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			tx, e := f.postgres.Begin(ctx)
			if e != nil {
				t.Fatal("actual legacy begin")
			}
			defer tx.Rollback(ctx)
			if e = audit.Write(ctx, tx, f.user, f.user, "admin", "repository.legacy", "success", "workspace_note", f.note, "", ""); e != nil {
				t.Fatal(e)
			}
			if e = audit.WriteAnonymous(ctx, tx, "portal", "repository.legacy_anonymous", "failure", "", bytes.Repeat([]byte{0x21}, 32)); e != nil {
				t.Fatal(e)
			}
			if e = tx.Commit(ctx); e != nil || f.count(t) != 6 {
				t.Fatal("actual public entrypoints", e)
			}
		})
	}
}

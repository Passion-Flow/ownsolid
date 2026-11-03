//go:build repository_integration && task_events_schema_integration

package databaseaccess

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualPostgreSQLTaskEventsAccess(t *testing.T) {
	f := repositoryfixture.NewUnbootstrapped(t)
	if f.PostgreSQL == nil {
		t.Fatal("TASK_EVENTS_REAL_POSTGRES_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	role := "m25_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password, e := NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	pc := f.PostgreSQL.Config().Copy()
	pc.ConnConfig.User = role
	pc.ConnConfig.Password = password
	pc.MaxConns = 2
	pc.MinConns = 0
	c := config.Config{}
	c.Database.MaxOpenConnections = 2
	c.Database.StatementTimeoutSeconds = 5
	input := RuntimeInput{configuration: c, connection: pc}
	lease, e := f.PostgreSQL.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var locked bool
	if e = lease.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, database.InstallationLockID).Scan(&locked); e != nil || !locked {
		lease.Release()
		t.Fatal(e)
	}
	var runtime *pgxpool.Pool
	defer func() {
		if runtime != nil {
			runtime.Close()
		}
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if _, e := lease.Exec(cleanup, `SELECT pg_advisory_unlock($1)`, database.InstallationLockID); e != nil {
			t.Error(e)
		}
		lease.Release()
		for _, q := range []string{"DROP OWNED BY " + pgx.Identifier{role}.Sanitize(), "DROP ROLE " + pgx.Identifier{role}.Sanitize()} {
			if _, e := f.PostgreSQL.Exec(cleanup, q); e != nil {
				t.Error("exact role cleanup", e)
			}
		}
		var remains bool
		if e := f.PostgreSQL.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&remains); e != nil || remains {
			t.Error("role remains", e)
		}
	}()
	if !t.Run("actual_role_prepare_repeat_check", func(t *testing.T) {
		for range 2 {
			if status, e := Prepare(ctx, f.PostgreSQL, lease, input); e != nil || status != Verified {
				t.Fatal("actual role preparation", status, e)
			}
		}
		runtime, e = pgxpool.NewWithConfig(ctx, pc)
		if e != nil {
			t.Fatal(e)
		}
		var actor string
		if e = runtime.QueryRow(ctx, `SELECT current_user`).Scan(&actor); e != nil || actor != role {
			t.Fatal("actual separate runtime identity", e)
		}
		for _, name := range []string{"task_event_outbox", "task_event_receipts", "task_event_dead_letters"} {
			var allowed, forbidden bool
			if e = runtime.QueryRow(ctx, `SELECT has_table_privilege(current_user,$1,'SELECT') AND has_table_privilege(current_user,$1,'INSERT') AND has_table_privilege(current_user,$1,'UPDATE') AND has_table_privilege(current_user,$1,'DELETE'),has_table_privilege(current_user,$1,'TRUNCATE,REFERENCES,TRIGGER,SELECT WITH GRANT OPTION,INSERT WITH GRANT OPTION,UPDATE WITH GRANT OPTION,DELETE WITH GRANT OPTION')`, name).Scan(&allowed, &forbidden); e != nil || !allowed || forbidden {
				t.Fatal("actual DML only", name, e)
			}
			if _, e = runtime.Exec(ctx, "UPDATE "+pgx.Identifier{name}.Sanitize()+" SET tenant_id=tenant_id WHERE false"); e != nil {
				t.Fatal("actual permitted empty DML", name, e)
			}
		}
		if status, e := Check(ctx, f.PostgreSQL, lease, input); e != nil || status != Verified {
			t.Fatal("actual role check", status, e)
		}
	}) {
		return
	}
	t.Run("actual_ddl_journal_and_truncate_refused", func(t *testing.T) {
		for _, query := range []string{"CREATE TABLE forbidden_task_event_table(id integer)", "TRUNCATE task_event_outbox", "UPDATE schema_migrations SET checksum=checksum WHERE false"} {
			_, e := runtime.Exec(ctx, query)
			var denied *pgconn.PgError
			if !errors.As(e, &denied) || denied.Code != "42501" {
				t.Fatal("actual runtime privilege refusal required", e)
			}
		}
		if status, e := Check(ctx, f.PostgreSQL, lease, input); e != nil || status != Verified {
			t.Fatal("refusal altered role receipt", status, e)
		}
	})
}

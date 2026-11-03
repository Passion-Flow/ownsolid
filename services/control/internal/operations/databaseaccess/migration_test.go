package databaseaccess

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestMigrationContextRequiresActualPoolLeaseAndProtectedInput(t *testing.T) {
	if e := MigrateWithRuntimeContext(context.Background(), nil, nil, RuntimeInput{}); e == nil || e.Error() != "RUNTIME_ACCESS_CONTEXT_INVALID" {
		t.Fatal("missing context admitted", e)
	}
}

type emptyMigrationFixture struct {
	pool         *pgxpool.Pool
	lease        *pgxpool.Conn
	input        RuntimeInput
	schema, role string
}

// A new generated empty application schema, never an existing application's
// public schema. The real owned loopback PostgreSQL fixture is preflighted first.
func newEmptyMigrationFixture(t *testing.T) emptyMigrationFixture {
	t.Helper()
	dsn := os.Getenv("PROJECT_ONE_TEST_DSN")
	if dsn == "" {
		t.Skip("PROJECT_ONE_TEST_DSN required for actual isolated PostgreSQL")
	}
	identityfixture.PreflightOwnedPostgres(t)
	pc, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal("explicit fixture connection invalid")
	}
	pc.MaxConns, pc.MinConns = 4, 0
	pc.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	pc.ConnConfig.RuntimeParams["lock_timeout"] = "3000"
	pc.ConnConfig.RuntimeParams["timezone"] = "UTC"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, e := pgxpool.NewWithConfig(ctx, pc.Copy())
	if e != nil {
		t.Fatal("owned fixture operator unavailable")
	}
	schema := "m01_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		admin.Close()
		t.Fatal("isolated empty schema creation failed")
	}
	var pool *pgxpool.Pool
	var lease *pgxpool.Conn
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if lease != nil {
			lease.Exec(cleanup, "SELECT pg_advisory_unlock($1)", database.InstallationLockID)
			lease.Release()
		}
		if pool != nil {
			pool.Close()
		}
		if _, e := admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); e != nil {
			t.Error("owned migration schema cleanup failed")
		}
		admin.Close()
	})
	pc.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e = pgxpool.NewWithConfig(ctx, pc.Copy())
	if e != nil {
		t.Fatal("isolated pool unavailable")
	}
	lease, e = pool.Acquire(ctx)
	if e != nil {
		t.Fatal("isolated operator lease unavailable")
	}
	var locked bool
	if e = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || !locked {
		t.Fatal("isolated installation lock unavailable")
	}
	runtime := pc.Copy()
	runtime.ConnConfig.User = "m18_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	runtime.ConnConfig.Password, e = NewCredential()
	if e != nil {
		t.Fatal("synthetic intended credential generation failed")
	}
	c := config.Config{}
	c.Database.MaxOpenConnections, c.Database.MaxIdleConnections, c.Database.StatementTimeoutSeconds = 4, 0, 5
	return emptyMigrationFixture{pool: pool, lease: lease, input: RuntimeInput{configuration: c, connection: runtime}, schema: schema, role: runtime.ConnConfig.User}
}

func (f emptyMigrationFixture) assertNoDDL(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var tables, roles int
	if e := f.lease.QueryRow(ctx, "SELECT (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('r','p','v','m','S','f')),(SELECT count(*) FROM pg_roles WHERE rolname=$2)", f.schema, f.role).Scan(&tables, &roles); e != nil || tables != 0 || roles != 0 {
		t.Fatal("refused migration changed empty schema or intended role")
	}
}

func TestActualMigrationWithRuntimeContextEmptySchemaRepeatAndUncreatedRole(t *testing.T) {
	f := newEmptyMigrationFixture(t)
	f.assertNoDDL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if e := MigrateWithRuntimeContext(ctx, f.pool, f.lease, f.input); e != nil {
		t.Fatal("actual guarded empty-schema migration", e)
	}
	if e := database.Check(ctx, f.pool); e != nil {
		t.Fatal("independent actual current schema check", e)
	}
	var before, after string
	query := "SELECT string_agg(version::text||':'||checksum,',' ORDER BY version) FROM schema_migrations"
	if e := f.lease.QueryRow(ctx, query).Scan(&before); e != nil || before == "" {
		t.Fatal("actual migration inventory missing")
	}
	if e := MigrateWithRuntimeContext(ctx, f.pool, f.lease, f.input); e != nil {
		t.Fatal("explicit current-schema repeat", e)
	}
	if e := f.lease.QueryRow(ctx, query).Scan(&after); e != nil || before != after {
		t.Fatal("repeat changed migration checksums")
	}
	var roles int
	if e := f.lease.QueryRow(ctx, "SELECT count(*) FROM pg_roles WHERE rolname=$1", f.role).Scan(&roles); e != nil || roles != 0 {
		t.Fatal("migration silently created runtime role")
	}
}

func TestActualMigrationWrongContextAndMissingOrContendedLockRefuseBeforeDDL(t *testing.T) {
	f := newEmptyMigrationFixture(t)
	for _, fault := range []string{"user", "host", "port", "database", "search_path"} {
		t.Run(fault, func(t *testing.T) {
			input := f.input
			input.connection = f.input.connection.Copy()
			connection := input.connection.ConnConfig
			switch fault {
			case "user":
				connection.User = f.pool.Config().ConnConfig.User
			case "host":
				connection.Host = "localhost"
			case "port":
				connection.Port++
			case "database":
				connection.Database = "synthetic_other_test"
			case "search_path":
				connection.RuntimeParams["search_path"] = "public"
			}
			if e := MigrateWithRuntimeContext(context.Background(), f.pool, f.lease, input); e == nil || e.Error() != "RUNTIME_ACCESS_CONNECTION_CONTEXT_INVALID" {
				t.Fatal("wrong intended target admitted", e)
			}
			f.assertNoDDL(t)
		})
	}
	if _, e := f.lease.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
		t.Fatal("owned lock release")
	}
	if e := MigrateWithRuntimeContext(context.Background(), f.pool, f.lease, f.input); e == nil || e.Error() != "RUNTIME_ACCESS_INSTALLATION_LOCK_REQUIRED" {
		t.Fatal("missing lock admitted", e)
	}
	f.assertNoDDL(t)
	contender, e := f.pool.Acquire(context.Background())
	if e != nil {
		t.Fatal("explicit contender unavailable")
	}
	defer contender.Release()
	var locked bool
	if e = contender.QueryRow(context.Background(), "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || !locked {
		t.Fatal("contender lock unavailable")
	}
	defer contender.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", database.InstallationLockID)
	if e = f.lease.QueryRow(context.Background(), "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || locked {
		t.Fatal("actual concurrent contender did not exclude original lease")
	}
	if e = MigrateWithRuntimeContext(context.Background(), f.pool, f.lease, f.input); e == nil || e.Error() != "RUNTIME_ACCESS_INSTALLATION_LOCK_REQUIRED" {
		t.Fatal("contended migration admitted", e)
	}
	f.assertNoDDL(t)
}

func TestActualMigrationRejectsMismatchedPoolAndLockedSession(t *testing.T) {
	f := newEmptyMigrationFixture(t)
	pc := f.pool.Config().Copy()
	pc.ConnConfig.RuntimeParams["search_path"] = "public"
	other, e := pgxpool.NewWithConfig(context.Background(), pc)
	if e != nil {
		t.Fatal("explicit other-schema pool unavailable")
	}
	defer other.Close()
	input := f.input
	input.connection = f.input.connection.Copy()
	input.connection.ConnConfig.RuntimeParams["search_path"] = "public"
	if e = MigrateWithRuntimeContext(context.Background(), other, f.lease, input); e == nil || e.Error() != "RUNTIME_ACCESS_TARGET_MISMATCH" {
		t.Fatal("pool/session target mismatch admitted", e)
	}
	f.assertNoDDL(t)
}

func TestActualMigrationRestrictedRuntimeRoleCannotBecomeMigrationOperator(t *testing.T) {
	f := newActualFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if status, e := Prepare(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal("actual restricted runtime setup", e)
	}
	var before, after string
	query := "SELECT string_agg(version::text||':'||checksum,',' ORDER BY version) FROM schema_migrations"
	if e := f.lease.QueryRow(ctx, query).Scan(&before); e != nil {
		t.Fatal("actual migration snapshot")
	}
	if _, e := f.lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); e != nil {
		t.Fatal("owned original operator unlock")
	}
	runtime, e := pgxpool.NewWithConfig(ctx, f.input.connection.Copy())
	if e != nil {
		t.Fatal("actual restricted runtime pool")
	}
	defer runtime.Close()
	lease, e := runtime.Acquire(ctx)
	if e != nil {
		t.Fatal("actual restricted runtime login", e)
	}
	defer lease.Release()
	var locked bool
	if e = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || !locked {
		t.Fatal("runtime fixture advisory lock")
	}
	defer lease.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", database.InstallationLockID)
	input := f.input
	input.connection = f.input.connection.Copy()
	input.connection.ConnConfig.User = "m18_candidate_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if e = MigrateWithRuntimeContext(ctx, runtime, lease, input); e == nil || e.Error() != "RUNTIME_ACCESS_PRIVILEGED_OPERATOR_REQUIRED" {
		t.Fatal("restricted runtime admitted as migration operator", e)
	}
	if e = f.lease.QueryRow(ctx, query).Scan(&after); e != nil || before != after {
		t.Fatal("refused restricted operator changed schema")
	}
}

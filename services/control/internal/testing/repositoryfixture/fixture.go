//go:build repository_integration

// Package repositoryfixture owns real isolated synthetic repository acceptance.
package repositoryfixture

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type Fixture struct {
	Store                   transaction.Store
	PostgreSQL              *pgxpool.Pool
	Tenant, Workspace, User string
}

const (
	repositoryFixtureCleanupSeconds = 25
	maximumRepositoryCleanupTables  = 512
)

var (
	repositoryOwnedSchema = regexp.MustCompile(`^m20_repository_[a-f0-9]{32}$`)
	repositoryOwnedTable  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
)

func New(t *testing.T) Fixture {
	return newFixture(t, true, false)
}

func NewUnbootstrapped(t *testing.T) Fixture {
	return newFixture(t, false, false)
}

// NewRecoverableUnbootstrapped preserves the exact owned PostgreSQL schema when
// its test fails. The caller must join workers before fixture cleanup and retain
// the outer database and protected connection inputs for explicit recovery.
// Successful tests and both ordinary constructors retain their normal cleanup.
func NewRecoverableUnbootstrapped(t *testing.T) Fixture {
	return newFixture(t, false, true)
}

func newFixture(t *testing.T, seed, preserveOnFailure bool) Fixture {
	t.Helper()
	setup, e := loadSetupPolicy()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(setup.SetupSeconds)*time.Second)
	defer cancel()
	f := Fixture{Tenant: uuid.NewString(), Workspace: uuid.NewString(), User: uuid.NewString()}
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
		options := setup.mysqlOptions(catalog.Steps())
		if _, e = mysqlmigrate.Run(ctx, kernel, catalog, options, mysqlmigrate.Apply, nil); e != nil {
			t.Fatal(e)
		}
		f.Store, e = transaction.FromMySQL(kernel)
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
		if !repositoryOwnedSchema.MatchString(schema) {
			operator.Close()
			t.Fatal("REPOSITORY_EXACT_OWNED_SCHEMA_REQUIRED")
		}
		if _, e = operator.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
			operator.Close()
			t.Fatal("actual PostgreSQL schema create")
		}
		pc.ConnConfig.RuntimeParams["search_path"] = schema
		f.PostgreSQL, e = pgxpool.NewWithConfig(ctx, pc)
		if e != nil {
			t.Fatal("actual PostgreSQL fixture open")
		}
		t.Cleanup(func() {
			if preserveOnFailure && t.Failed() {
				t.Log("REPOSITORY_FAILURE_PRESERVED_SCHEMA", schema)
				f.PostgreSQL.Close()
				operator.Close()
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), repositoryFixtureCleanupSeconds*time.Second)
			defer cancel()
			// One autocommit DROP TABLE bounds relation locks. A single
			// DROP SCHEMA exceeds max_locks_per_transaction on this catalog.
			disposeError := disposeOwnedRepositoryTables(ctx, f.PostgreSQL, schema)
			f.PostgreSQL.Close()
			if disposeError != nil {
				t.Error("actual owned schema cleanup failed")
				operator.Close()
				return
			}
			if _, e := operator.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); e != nil {
				t.Error("actual owned schema cleanup failed")
			}
			operator.Close()
		})
		if e = database.Migrate(ctx, f.PostgreSQL); e != nil {
			t.Fatal("actual PostgreSQL fixture migration", e)
		}
		f.Store, e = transaction.FromPostgreSQL(f.PostgreSQL)
		if e != nil {
			t.Fatal(e)
		}
	default:
		t.Fatal("REPOSITORY_EXPLICIT_REAL_ENGINE_REQUIRED")
	}
	if !seed {
		return f
	}
	if e := f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		commands := []transaction.Statement{
			f.Statement("INSERT INTO tenants(id,name,status) VALUES($1,$2,'active')", "INSERT INTO tenants(id,name,status) VALUES(?,?,'active')", f.Tenant, "Repository tenant"),
			f.Statement("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,$3,'active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,?,'active')", f.Workspace, f.Tenant, "Repository Workspace"),
			f.Statement("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,$4,'en','active',true)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,?,'en','active',true)", f.User, "synthetic@example.invalid", "synthetic@example.invalid", "Repository actor"),
			f.Statement("UPDATE installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1", "UPDATE installation_state SET bootstrap_complete=true,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=UTC_TIMESTAMP(6) WHERE id=1", f.Tenant, f.Workspace, f.User),
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
func (f Fixture) Statement(pg, my string, args ...any) transaction.Statement {
	if f.Store.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, args...)
	}
	return transaction.SQL(transaction.PostgreSQL, pg, args...)
}

func disposeOwnedRepositoryTables(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	if ctx == nil || pool == nil || !repositoryOwnedSchema.MatchString(schema) {
		return errors.New("OWNED_REPOSITORY_SCHEMA_CLEANUP_CONTEXT_INVALID")
	}
	c := pool.Config().ConnConfig
	if c.Host != "127.0.0.1" || c.Port < 1024 || !strings.HasSuffix(c.Database, "_test") || !repositoryOwnedTable.MatchString(c.User) || c.RuntimeParams["search_path"] != schema {
		return errors.New("OWNED_REPOSITORY_SCHEMA_CLEANUP_CONTEXT_INVALID")
	}
	rows, e := pool.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname=$1 ORDER BY tablename", schema)
	if e != nil {
		return errors.New("OWNED_REPOSITORY_TABLE_INVENTORY_FAILED")
	}
	var names []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil || !repositoryOwnedTable.MatchString(name) || len(names) >= maximumRepositoryCleanupTables {
			rows.Close()
			return errors.New("OWNED_REPOSITORY_TABLE_INVENTORY_INVALID")
		}
		names = append(names, name)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return errors.New("OWNED_REPOSITORY_TABLE_INVENTORY_FAILED")
	}
	for _, name := range names {
		if _, e = pool.Exec(ctx, "DROP TABLE "+pgx.Identifier{schema, name}.Sanitize()+" CASCADE"); e != nil {
			return errors.New("OWNED_REPOSITORY_TABLE_DISPOSAL_FAILED")
		}
	}
	return nil
}

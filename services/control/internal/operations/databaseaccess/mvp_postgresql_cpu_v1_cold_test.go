package databaseaccess

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestMVPPostgreSQLCPUV1ColdRuntimePrincipalAndRepeat(t *testing.T) {
	f := newEmptyMigrationFixture(t)
	f.assertNoDDL(t)
	ctx := t.Context()
	c, e := config.Load("../../../config/examples/control/mvp-postgresql-cpu-v1.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c.Database.MaxOpenConnections = f.input.configuration.Database.MaxOpenConnections
	c.Database.MaxIdleConnections = f.input.configuration.Database.MaxIdleConnections
	f.input.configuration = c
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, e := f.pool.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{f.role}.Sanitize()); e != nil {
			t.Error("owned cold grants cleanup", e)
		}
		if _, e := f.pool.Exec(cleanup, "DROP ROLE "+pgx.Identifier{f.role}.Sanitize()); e != nil {
			t.Error("owned cold role cleanup", e)
		}
	})
	if e = MigrateWithRuntimeContext(ctx, f.pool, f.lease, f.input); e != nil {
		t.Fatal("actual guarded cold migration", e)
	}
	if status, e := Prepare(ctx, f.pool, f.lease, f.input); e != nil || status != Verified {
		t.Fatal("actual cold role preparation", status, e)
	}
	before, e := readMVPPrincipal(ctx, f.lease, f.input)
	if e != nil {
		t.Fatal(e)
	}
	if e = MigrateWithRuntimeContext(ctx, f.pool, f.lease, f.input); e != nil {
		t.Fatal("cold migration noop", e)
	}
	if status, e := Prepare(ctx, f.pool, f.lease, f.input); e != nil || status != Verified {
		t.Fatal("cold preparation noop", status, e)
	}
	after, e := readMVPPrincipal(ctx, f.lease, f.input)
	if e != nil || after != before {
		t.Fatal("cold repeated principal changed", e)
	}
	runtime, e := pgxpool.NewWithConfig(ctx, f.input.connection.Copy())
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.Close()
	store, e := transaction.FromPostgreSQL(runtime)
	if e != nil {
		t.Fatal(e)
	}
	if e = database.CheckMVPPostgreSQLCPUV1RuntimeWithin(ctx, store); e != nil {
		t.Fatal("actual new cold runtime admission", e)
	}
	if e = VerifyMVPPostgreSQLCPUV1Principal(ctx, f.pool, f.lease, f.input); e != nil {
		t.Fatal("original-session cold principal", e)
	}
	var count, version int
	if e = runtime.QueryRow(ctx, "SELECT (SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()),(SELECT max(version) FROM schema_migrations)").Scan(&count, &version); e != nil || count != 108 || version != 29 {
		t.Fatal(count, version, e)
	}
	for _, sql := range []string{"CREATE TABLE cold_runtime_ddl(id integer)", "DELETE FROM schema_migrations", "TRUNCATE private_cluster_intents"} {
		if _, e = runtime.Exec(ctx, sql); e == nil {
			t.Fatal("cold runtime elevated authority", sql)
		}
	}
}

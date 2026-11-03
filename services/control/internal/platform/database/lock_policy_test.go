package database_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestDatabaseLockPolicyOpenRejectsBeforeSecrets(t *testing.T) {
	var c config.Config
	c.Database.StatementTimeoutSeconds = 5
	c.Database.DSNFile = "/sensitive-unreadable-test-reference"
	for _, value := range []int{0, -1, 5000, 30001} {
		c.Database.LockTimeoutMilliseconds = &value
		pool, e := database.Open(context.Background(), c)
		if pool != nil || e == nil || e.Error() != "CONFIG_DATABASE_LOCK_TIMEOUT_INVALID" {
			t.Fatal("invalid typed timeout must fail before secret reads or network")
		}
	}
}

func TestActualPostgreSQLLockPolicySessionTimeoutRollbackAndRecovery(t *testing.T) {
	s := fixture.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dsn := s.DB.Config().ConnConfig.Copy()
	// ConnString returns the original parsed input, not subsequent configuration
	// mutations. Explicitly encode the exclusive schema and DSN precedence probe.
	input, e := url.Parse(dsn.ConnString())
	if e != nil || (input.Scheme != "postgresql" && input.Scheme != "postgres") {
		t.Fatal("owned URI test connection required")
	}
	parameters := input.Query()
	parameters.Set("search_path", dsn.RuntimeParams["search_path"])
	parameters.Set("lock_timeout", "9876")
	input.RawQuery = parameters.Encode()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(root, 0700) != nil {
		t.Fatal("private test directory")
	}
	path := filepath.Join(root, "dsn")
	if os.WriteFile(path, []byte(input.String()), 0600) != nil {
		t.Fatal("private test DSN")
	}
	var c config.Config
	c.Database.DSNFile = path
	c.Database.MaxOpenConnections = 2
	c.Database.StatementTimeoutSeconds = 5
	for _, milliseconds := range []int{175, 325} {
		c.Database.LockTimeoutMilliseconds = &milliseconds
		pool, e := database.Open(ctx, c)
		if e != nil {
			t.Fatal("actual configured PostgreSQL pool unavailable")
		}
		defer pool.Close()
		first, e := pool.Acquire(ctx)
		if e != nil {
			t.Fatal("first selected session unavailable")
		}
		second, e := pool.Acquire(ctx)
		if e != nil {
			first.Release()
			t.Fatal("second selected session unavailable")
		}
		for _, connection := range []*pgxpool.Conn{first, second} {
			var observed int
			var statement, schema string
			e = connection.QueryRow(ctx, "SELECT (EXTRACT(EPOCH FROM current_setting('lock_timeout')::interval)*1000)::integer,current_setting('statement_timeout'),current_schema()").Scan(&observed, &statement, &schema)
			if e != nil || observed != milliseconds || statement != "5s" || schema != dsn.RuntimeParams["search_path"] {
				first.Release()
				second.Release()
				t.Fatal("typed policy must override DSN on every session")
			}
		}
		first.Release()
		second.Release()
		if milliseconds == 325 {
			pool.Close()
			continue
		}
		if _, e = s.DB.Exec(ctx, "CREATE TABLE lock_policy_probe(id integer PRIMARY KEY,value integer NOT NULL);INSERT INTO lock_policy_probe VALUES(1,0)"); e != nil {
			t.Fatal("owned synthetic row setup")
		}
		holder, e := s.DB.Begin(ctx)
		if e != nil {
			t.Fatal("holder transaction")
		}
		defer holder.Rollback(context.Background())
		if _, e = holder.Exec(ctx, "SELECT id FROM lock_policy_probe WHERE id=1 FOR UPDATE"); e != nil {
			t.Fatal("actual synthetic row lock")
		}
		contender, e := pool.Begin(ctx)
		if e != nil {
			t.Fatal("contender transaction")
		}
		defer contender.Rollback(context.Background())
		_, e = contender.Exec(ctx, "UPDATE lock_policy_probe SET value=1 WHERE id=1")
		var vendor *pgconn.PgError
		if !errors.As(e, &vendor) || vendor.Code != "55P03" {
			if vendor != nil {
				t.Logf("actual vendor_sqlstate=%s", vendor.Code)
			}
			t.Fatal("expected actual lock timeout55P03 without partial write")
		}
		if contender.Rollback(ctx) != nil || holder.Rollback(ctx) != nil {
			t.Fatal("bounded transaction rollback")
		}
		var observed int
		if pool.QueryRow(ctx, "SELECT value FROM lock_policy_probe WHERE id=1").Scan(&observed) != nil || observed != 0 {
			t.Fatal("timed-out mutation changed stored row")
		}
		if _, e = pool.Exec(ctx, "UPDATE lock_policy_probe SET value=1 WHERE id=1"); e != nil || pool.QueryRow(ctx, "SELECT value FROM lock_policy_probe WHERE id=1").Scan(&observed) != nil || observed != 1 || pool.Stat().AcquiredConns() != 0 {
			t.Fatal("released lock and recovered pool must allow a fresh explicit write")
		}
		pool.Close()
	}
}

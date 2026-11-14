//go:build mysql_integration

package mysqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	driver "github.com/go-sql-driver/mysql"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These opt-in tests require the actual owned TLS fixture. Missing configuration
// is a failure, never a skipped acceptance or an in-memory backend replacement.
func actualStore(t *testing.T) (*Store, Configuration) {
	t.Helper()
	c, e := Load(os.Getenv("PROJECT_ONE_MYSQL_TEST_CONFIGURATION"), "")
	if e != nil || !strings.HasSuffix(c.Database, "_test") {
		t.Fatal("MYSQL_ACTUAL_ISOLATED_FIXTURE_REQUIRED")
	}
	s, observed, e := Open(context.Background(), c)
	if e != nil {
		// Read-only diagnostic uses the same captured connector and never logs raw
		// vendor strings,addresses,credentials or certificate contents.
		config, constructorError := connectorConfiguration(c)
		if constructorError == nil {
			connector, connectorError := driver.NewConnector(config)
			if connectorError == nil {
				candidate := sql.OpenDB(connector)
				rawError := candidate.PingContext(context.Background())
				candidate.Close()
				t.Logf("safe connector error type=%T", rawError)
				var network *net.OpError
				if errors.As(rawError, &network) {
					t.Logf("safe network operation=%s cause_type=%T", network.Op, network.Err)
				}
				var errno syscall.Errno
				if errors.As(rawError, &errno) {
					t.Logf("safe network errno=%d", errno)
				}
				for name, known := range map[string]error{"native_password": driver.ErrNativePassword, "no_TLS": driver.ErrNoTLS, "invalid_connection": driver.ErrInvalidConn, "unknown_plugin": driver.ErrUnknownPlugin, "malformed_packet": driver.ErrMalformPkt} {
					if errors.Is(rawError, known) {
						t.Log("safe driver error category=" + name)
					}
				}
			}
		}
		var metadata *Error
		if errors.As(e, &metadata) {
			t.Logf("safe vendor metadata: number=%d SQLSTATE=%x", metadata.Number, metadata.SQLState)
		}
		t.Fatal("actual connector", e)
	}
	t.Cleanup(func() {
		if s.Close() != nil {
			t.Error("pool close")
		}
	})
	if !observed.TLSAuthenticated || observed.Version != c.ExpectedVersion || observed.Collation != ConnectionCollation || s.Stats().MaxOpenConnections != c.Policy.MaximumOpenConnections {
		t.Fatal("actual session/pool invariants")
	}
	return s, c
}

func actualExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, e := s.Pool().ExecContext(context.Background(), query, args...); e != nil {
		t.Fatal("fixture SQL", SafeError(e))
	}
}
func actualCount(t *testing.T, s *Store, query string, expected int) {
	t.Helper()
	var count int
	if e := s.Pool().QueryRowContext(context.Background(), query).Scan(&count); e != nil || count != expected {
		t.Fatal("durable count", count, expected, SafeError(e))
	}
}

func TestActualMySQLKernel(t *testing.T) {
	s, c := actualStore(t)
	ctx := context.Background()
	for _, q := range []string{
		"CREATE TABLE kernel_parent (id BIGINT PRIMARY KEY,name VARCHAR(64) COLLATE utf8mb4_0900_bin NOT NULL UNIQUE,created_at DATETIME(6) NOT NULL,payload JSON NOT NULL,amount DECIMAL(18,6) NOT NULL,enabled BOOLEAN NOT NULL CHECK(enabled IN(0,1)),note VARCHAR(32) NULL) ENGINE=InnoDB",
		"CREATE TABLE kernel_child (id BIGINT PRIMARY KEY,parent_id BIGINT NOT NULL,CONSTRAINT kernel_parent_fk FOREIGN KEY(parent_id) REFERENCES kernel_parent(id)) ENGINE=InnoDB",
		"CREATE TABLE kernel_locks (id INT PRIMARY KEY,value INT NOT NULL CHECK(value>=0)) ENGINE=InnoDB",
		"INSERT INTO kernel_locks VALUES(1,0),(2,0)",
	} {
		actualExec(t, s, q)
	}
	stamp := time.Date(2026, 10, 19, 1, 2, 3, 123456000, time.UTC)
	insert := "INSERT INTO kernel_parent(id,name,created_at,payload,amount,enabled,note) VALUES(?,?,?,?,?,?,?)"
	for i, name := range []string{"Unicode \u4e16\u754c🙂", "Case", "case", "Case ", "quote'; UPDATE kernel_locks SET value=99; --"} {
		actualExec(t, s, insert, i+1, name, stamp, "{\"kind\":\"synthetic\",\"unicode\":\"\u4e16\u754c🙂\"}", "123.456789", true, nil)
	}
	actualCount(t, s, "SELECT COUNT(*) FROM kernel_locks WHERE value=99", 0)
	actualCount(t, s, "SELECT COUNT(*) FROM kernel_parent", 5)
	t.Run("typed-values", func(t *testing.T) {
		var name, amount string
		var at time.Time
		var raw []byte
		var enabled bool
		var note sql.NullString
		e := s.Pool().QueryRowContext(ctx, "SELECT name,created_at,payload,amount,enabled,note FROM kernel_parent WHERE id=?", 1).Scan(&name, &at, &raw, &amount, &enabled, &note)
		var object map[string]string
		if e != nil || json.Unmarshal(raw, &object) != nil || object["unicode"] != "\u4e16\u754c🙂" || name != "Unicode \u4e16\u754c🙂" || !at.Equal(stamp) || at.Location() != time.UTC || amount != "123.456789" || !enabled || note.Valid {
			t.Fatal("typed roundtrip", SafeError(e))
		}
	})
	t.Run("unsafe-and-invalid-writes", func(t *testing.T) {
		for _, q := range []string{"SELECT 1; UPDATE kernel_locks SET value=99", "LOAD DATA LOCAL INFILE '/etc/passwd' INTO TABLE kernel_locks", "INSERT INTO kernel_parent VALUES(20,REPEAT('x',65),NOW(),JSON_OBJECT(),0,1,NULL)", "INSERT INTO kernel_parent VALUES(20,'invalid-time','0000-00-00',JSON_OBJECT(),0,1,NULL)"} {
			if _, e := s.Pool().ExecContext(ctx, q); e == nil {
				t.Fatal("unsafe/invalid SQL admitted")
			}
		}
		for _, item := range []struct{ q, code string }{{"INSERT INTO kernel_parent SELECT * FROM kernel_parent WHERE id=1", "MYSQL_UNIQUE_CONFLICT"}, {"INSERT INTO kernel_child VALUES(1,999)", "MYSQL_FOREIGN_KEY_CONFLICT"}, {"UPDATE kernel_locks SET value=-1 WHERE id=1", "MYSQL_CHECK_CONFLICT"}} {
			_, e := s.Pool().ExecContext(ctx, item.q)
			if e == nil || SafeError(e).Error() != item.code {
				t.Fatal("typed actual vendor error", SafeError(e))
			}
		}
		actualCount(t, s, "SELECT COUNT(*) FROM kernel_locks WHERE value<>0", 0)
	})
	t.Run("commit-and-callback-rollback", func(t *testing.T) {
		if e := s.Within(ctx, false, func(ctx context.Context, tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, "INSERT INTO kernel_child VALUES(1,1)")
			return e
		}); e != nil {
			t.Fatal(e)
		}
		if e := s.Within(ctx, false, func(ctx context.Context, tx *sql.Tx) error {
			if _, e := tx.ExecContext(ctx, "INSERT INTO kernel_child VALUES(2,1)"); e != nil {
				return e
			}
			return errors.New("synthetic callback failure")
		}); e == nil {
			t.Fatal("callback failure ignored")
		}
		actualCount(t, s, "SELECT COUNT(*) FROM kernel_child", 1)
		if e := s.Within(ctx, true, func(ctx context.Context, tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, "INSERT INTO kernel_child VALUES(3,1)")
			return e
		}); e == nil {
			t.Fatal("read-only write admitted")
		}
		actualCount(t, s, "SELECT COUNT(*) FROM kernel_child", 1)
	})
	t.Run("read-committed-and-lock-timeout", func(t *testing.T) {
		tx, e := s.Pool().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if e != nil {
			t.Fatal(SafeError(e))
		}
		defer tx.Rollback()
		var value int
		if e = tx.QueryRowContext(ctx, "SELECT value FROM kernel_locks WHERE id=1").Scan(&value); e != nil || value != 0 {
			t.Fatal("initial read")
		}
		actualExec(t, s, "UPDATE kernel_locks SET value=1 WHERE id=1")
		if e = tx.QueryRowContext(ctx, "SELECT value FROM kernel_locks WHERE id=1 FOR UPDATE").Scan(&value); e != nil || value != 1 {
			t.Fatal("current committed read")
		}
		_, e = s.Pool().ExecContext(ctx, "UPDATE kernel_locks SET value=2 WHERE id=1")
		if e == nil || SafeError(e).Error() != "MYSQL_LOCK_TIMEOUT" {
			t.Fatal("actual held row lock", SafeError(e))
		}
		if tx.Rollback() != nil {
			t.Fatal("lock release")
		}
		actualExec(t, s, "UPDATE kernel_locks SET value=0 WHERE id=1")
	})
	t.Run("actual-deadlock-no-retry", func(t *testing.T) {
		left, e := s.Pool().BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(SafeError(e))
		}
		defer left.Rollback()
		right, e := s.Pool().BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(SafeError(e))
		}
		defer right.Rollback()
		if _, e = left.ExecContext(ctx, "UPDATE kernel_locks SET value=1 WHERE id=1"); e != nil {
			t.Fatal(SafeError(e))
		}
		if _, e = right.ExecContext(ctx, "UPDATE kernel_locks SET value=1 WHERE id=2"); e != nil {
			t.Fatal(SafeError(e))
		}
		results := make(chan error, 2)
		go func() {
			_, e := left.ExecContext(ctx, "UPDATE kernel_locks SET value=2 WHERE id=2")
			left.Rollback()
			results <- SafeError(e)
		}()
		go func() {
			_, e := right.ExecContext(ctx, "UPDATE kernel_locks SET value=2 WHERE id=1")
			right.Rollback()
			results <- SafeError(e)
		}()
		a, b := <-results, <-results
		if !((a == nil && b != nil && b.Error() == "MYSQL_DEADLOCK") || (b == nil && a != nil && a.Error() == "MYSQL_DEADLOCK")) {
			t.Fatal("actual deadlock victim", a, b)
		}
		actualCount(t, s, "SELECT COUNT(*) FROM kernel_locks WHERE value<>0", 0)
	})
	t.Run("cancellation-rollback-and-reconnect", func(t *testing.T) {
		limited, cancel := context.WithCancel(ctx)
		e := s.Within(limited, false, func(ctx context.Context, tx *sql.Tx) error {
			if _, e := tx.ExecContext(ctx, "INSERT INTO kernel_child VALUES(4,1)"); e != nil {
				return e
			}
			cancel()
			return ctx.Err()
		})
		cancel()
		if !errors.Is(e, context.Canceled) {
			t.Fatal("cancel classification", e)
		}
		actualCount(t, s, "SELECT COUNT(*) FROM kernel_child", 1)
		deadline, done := context.WithTimeout(ctx, 100*time.Millisecond)
		defer done()
		var n int
		e = s.Pool().QueryRowContext(deadline, "SELECT SLEEP(5)").Scan(&n)
		if !errors.Is(SafeError(e), context.DeadlineExceeded) {
			t.Fatal("actual query deadline", SafeError(e))
		}
		if _, e = s.Check(ctx); e != nil {
			t.Fatal("actual connection recovery", e)
		}
	})
	t.Run("captured-TLS-and-password-refusals", func(t *testing.T) {
		wrong := syntheticConfiguration(t)
		for name, changed := range map[string]Configuration{"CA": func() Configuration { v := c; v.CAFile = wrong.CAFile; return v }(), "password": func() Configuration { v := c; v.PasswordFile = wrong.PasswordFile; return v }(), "SAN": func() Configuration { v := c; v.ServerName = "wrong.example.invalid"; return v }(), "version": func() Configuration { v := c; v.ExpectedVersion = "8.4.0"; return v }()} {
			t.Run(name, func(t *testing.T) {
				candidate, _, e := Open(ctx, changed)
				if candidate != nil {
					candidate.Close()
				}
				if e == nil {
					t.Fatal("invalid actual connection admitted")
				}
				if strings.Contains(e.Error(), "synthetic private password") {
					t.Fatal("secret error disclosure")
				}
			})
		}
	})
	t.Run("pool-bound-and-zero-idle-session-recreation", func(t *testing.T) {
		bounded := c
		bounded.Policy.MaximumOpenConnections = 1
		bounded.Policy.MaximumIdleConnections = 0
		pool, _, e := Open(ctx, bounded)
		if e != nil {
			t.Fatal(e)
		}
		defer pool.Close()
		var first, second uint64
		if e = pool.Pool().QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&first); e != nil {
			t.Fatal(SafeError(e))
		}
		if e = pool.Pool().QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&second); e != nil || first == second {
			t.Fatal("connection recycling")
		}
		held, e := pool.Pool().Conn(ctx)
		if e != nil {
			t.Fatal(SafeError(e))
		}
		limited, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		_, e = pool.Pool().Conn(limited)
		cancel()
		held.Close()
		if !errors.Is(e, context.DeadlineExceeded) || pool.Stats().MaxOpenConnections != 1 {
			t.Fatal("pool bound")
		}
		if _, e = pool.Check(ctx); e != nil {
			t.Fatal("recycled session invariants", e)
		}
	})
	actualDurableReadback(t, s)
}

func actualDurableReadback(t *testing.T, s *Store) {
	t.Helper()
	actualCount(t, s, "SELECT COUNT(*) FROM kernel_parent", 5)
	actualCount(t, s, "SELECT COUNT(*) FROM kernel_child", 1)
	actualCount(t, s, "SELECT COUNT(*) FROM kernel_locks WHERE value=0", 2)
	rows, e := s.Pool().QueryContext(context.Background(), "SELECT id,name,DATE_FORMAT(created_at,'%Y-%m-%dT%H:%i:%s.%fZ'),CAST(payload AS CHAR),CAST(amount AS CHAR),enabled,note FROM kernel_parent ORDER BY id")
	if e != nil {
		t.Fatal(SafeError(e))
	}
	defer rows.Close()
	var values []any
	for rows.Next() {
		var id int
		var name, at, payload, amount string
		var enabled bool
		var note sql.NullString
		if e = rows.Scan(&id, &name, &at, &payload, &amount, &enabled, &note); e != nil {
			t.Fatal(SafeError(e))
		}
		var object any
		if json.Unmarshal([]byte(payload), &object) != nil {
			t.Fatal("JSON")
		}
		values = append(values, []any{id, name, at, object, amount, enabled, note})
	}
	if rows.Err() != nil {
		t.Fatal(SafeError(rows.Err()))
	}
	raw, e := json.Marshal(values)
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(raw)
	t.Log("MYSQL_CANONICAL_SYNTHETIC_ROWS_SHA256=" + hex.EncodeToString(hash[:]))
}

func TestActualMySQLRestartOrFreshRestore(t *testing.T) {
	s, _ := actualStore(t)
	actualDurableReadback(t, s)
	if _, e := s.Pool().ExecContext(context.Background(), "INSERT INTO kernel_child VALUES(99,999)"); e == nil || SafeError(e).Error() != "MYSQL_FOREIGN_KEY_CONFLICT" {
		t.Fatal("restored constraint", SafeError(e))
	}
}

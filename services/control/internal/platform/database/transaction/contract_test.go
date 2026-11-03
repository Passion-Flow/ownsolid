package transaction

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func TestStatementRedactionAndDialect(t *testing.T) {
	q := SQL(MySQL, "SELECT secret FROM private WHERE password=?", "sensitive-data")
	for _, text := range []string{fmt.Sprint(q), fmt.Sprintf("%+v", q), fmt.Sprintf("%#v", q)} {
		if text != "REPOSITORY_STATEMENT_REDACTED" {
			t.Fatal("statement leaked", text)
		}
	}
	for name, executor := range map[string]Executor{"postgres": postgresExecutor{}, "mysql": mysqlExecutor{}} {
		t.Run(name, func(t *testing.T) {
			wrong := PostgreSQL
			if executor.Dialect() == PostgreSQL {
				wrong = MySQL
			}
			q := SQL(wrong, "SELECT secret", "sensitive-data")
			if _, e := executor.Exec(context.Background(), q); e == nil || e.Error() != "REPOSITORY_STATEMENT_DIALECT_MISMATCH" {
				t.Fatal(e)
			}
			if _, e := executor.Query(context.Background(), q); e == nil || e.Error() != "REPOSITORY_STATEMENT_DIALECT_MISMATCH" {
				t.Fatal(e)
			}
			if e := executor.QueryRow(context.Background(), q).Scan(new(string)); e == nil || e.Error() != "REPOSITORY_STATEMENT_DIALECT_MISMATCH" {
				t.Fatal(e)
			}
		})
	}
	for _, q := range []Statement{SQL(MySQL, ""), SQL(MySQL, " \t"), SQL(MySQL, "SELECT\x00data"), SQL("unknown", "SELECT 1")} {
		if q.validate(MySQL) == nil {
			t.Fatal("invalid statement accepted")
		}
	}
}

func TestSafeVendorErrors(t *testing.T) {
	for _, item := range []struct {
		source error
		kind   ErrorKind
		state  string
		number uint16
	}{
		{&pgconn.PgError{Code: "23505", Message: "secret duplicate", Detail: "secret"}, UniqueConflict, "23505", 0},
		{&pgconn.PgError{Code: "23503", Message: "secret FK"}, ForeignKeyConflict, "23503", 0},
		{&pgconn.PgError{Code: "23514"}, CheckConflict, "23514", 0},
		{&pgconn.PgError{Code: "40P01"}, Deadlock, "40P01", 0},
		{&pgconn.PgError{Code: "55P03"}, LockTimeout, "55P03", 0},
		{&mysql.MySQLError{Number: 1062, SQLState: [5]byte{'2', '3', '0', '0', '0'}, Message: "secret duplicate"}, UniqueConflict, "23000", 1062},
		{&mysql.MySQLError{Number: 1452, Message: "secret FK"}, ForeignKeyConflict, "", 1452},
		{&mysql.MySQLError{Number: 3819, Message: "secret check"}, CheckConflict, "", 3819},
		{&mysql.MySQLError{Number: 1213}, Deadlock, "", 1213},
		{&mysql.MySQLError{Number: 1205}, LockTimeout, "", 1205},
		{context.Canceled, Cancelled, "", 0}, {context.DeadlineExceeded, Timeout, "", 0},
		{sql.ErrNoRows, NoRows, "", 0}, {pgx.ErrNoRows, NoRows, "", 0},
		{mysqlstore.ErrCommitOutcomeUnknown, CommitUnknown, "", 0},
		{errors.New("secret DSN and parameters"), Unavailable, "", 0},
	} {
		e := normalize(item.source)
		var safe *Error
		if !errors.As(e, &safe) || safe.Kind != item.kind || safe.SQLState != item.state || safe.Number != item.number || strings.Contains(fmt.Sprintf("%+v", e), "secret") || errors.Unwrap(e) != nil {
			t.Fatalf("unsafe or incorrect error category: %v", e)
		}
		if normalize(e) != e {
			t.Fatal("normalized error identity lost")
		}
	}
	for _, vendor := range []error{sql.ErrNoRows, pgx.ErrNoRows} {
		for _, sentinel := range []error{sql.ErrNoRows, pgx.ErrNoRows} {
			if !errors.Is(normalize(vendor), sentinel) {
				t.Fatal("no-row identity lost")
			}
		}
	}
	if !errors.Is(normalize(context.Canceled), context.Canceled) || !errors.Is(normalize(context.DeadlineExceeded), context.DeadlineExceeded) || normalize(nil) != nil {
		t.Fatal("context identity")
	}
}

func TestMySQLCancelledSQLPreservesCauseWithoutChangingKnownOutcomes(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	for _, item := range []struct {
		ctx  context.Context
		err  error
		kind ErrorKind
	}{
		{context.Background(), sql.ErrTxDone, Unavailable},
		{cancelled, sql.ErrTxDone, Cancelled},
		{cancelled, driver.ErrBadConn, Cancelled},
		{cancelled, mysql.ErrInvalidConn, Cancelled},
		{expired, sql.ErrTxDone, Timeout},
		{cancelled, &mysql.MySQLError{Number: 1213}, Deadlock},
		{cancelled, &mysql.MySQLError{Number: 1452}, ForeignKeyConflict},
		{cancelled, mysqlstore.ErrCommitOutcomeUnknown, CommitUnknown},
	} {
		if actual := mysqlOperationError(item.ctx, item.err); actual == nil || actual.Error() != string(item.kind) {
			t.Fatal("cancelled SQL lost its cause or replaced a known outcome", actual)
		}
	}
	if mysqlOperationError(cancelled, nil) != nil {
		t.Fatal("a completed SQL result became a cancellation")
	}
	row := mysqlRow{errorRow{sql.ErrTxDone}, cancelled}
	if !errors.Is(row.Scan(new(string)), context.Canceled) {
		t.Fatal("deferred SQL row lost transaction cancellation")
	}
}

func TestTransactionAdmissionBeforePoolUse(t *testing.T) {
	callback := func(context.Context, Executor) error { return nil }
	for _, store := range []Store{&postgresStore{}, &mysqlStore{}} {
		if e := store.Within(context.Background(), false, callback); e == nil || e.Error() != "REPOSITORY_TRANSACTION_DEADLINE_REQUIRED" {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if e := store.Within(ctx, false, nil); e == nil || e.Error() != "REPOSITORY_TRANSACTION_OPERATION_REQUIRED" {
			t.Fatal(e)
		}
		cancel()
		if e := store.Within(ctx, false, callback); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	}
	if _, e := FromPostgreSQL(nil); e == nil {
		t.Fatal("nil PostgreSQL pool accepted")
	}
	if _, e := FromMySQL(nil); e == nil {
		t.Fatal("nil MySQL kernel accepted")
	}
}

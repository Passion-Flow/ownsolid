// Package transaction executes repository-owned SQL on real vendor transactions.
// It never rewrites SQL or owns connection configuration, migration or pool closure.
package transaction

import (
	"context"
	"errors"
	"strings"
)

type Dialect string

const (
	PostgreSQL Dialect = "postgresql"
	MySQL      Dialect = "mysql"
	Dameng     Dialect = "dameng"
)

// Statement binds one explicit vendor query to its repository-owned arguments.
// Its text is private so formatting a statement cannot accidentally log SQL/data.
type Statement struct {
	dialect Dialect
	text    string
	args    []any
}

func SQL(dialect Dialect, text string, args ...any) Statement {
	return Statement{dialect: dialect, text: text, args: args}
}
func (Statement) String() string   { return "REPOSITORY_STATEMENT_REDACTED" }
func (Statement) GoString() string { return "REPOSITORY_STATEMENT_REDACTED" }
func (q Statement) validate(dialect Dialect) error {
	if q.dialect != dialect || dialect != PostgreSQL && dialect != MySQL && dialect != Dameng {
		return errors.New("REPOSITORY_STATEMENT_DIALECT_MISMATCH")
	}
	if strings.TrimSpace(q.text) == "" || strings.ContainsRune(q.text, '\x00') {
		return errors.New("REPOSITORY_STATEMENT_INVALID")
	}
	return nil
}

type Row interface{ Scan(...any) error }
type Rows interface {
	Row
	Next() bool
	Err() error
	Close()
}

type Executor interface {
	Dialect() Dialect
	Exec(context.Context, Statement) (int64, error)
	Query(context.Context, Statement) (Rows, error)
	QueryRow(context.Context, Statement) Row
}

// Store borrows a configured pool. Within grants no independent commit authority
// to repository callbacks, so authorization, outbox and audit share one outcome.
type Store interface {
	Executor
	Within(context.Context, bool, func(context.Context, Executor) error) error
}

func operationContext(ctx context.Context, operation func(context.Context, Executor) error) error {
	if operation == nil {
		return errors.New("REPOSITORY_TRANSACTION_OPERATION_REQUIRED")
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("REPOSITORY_TRANSACTION_DEADLINE_REQUIRED")
	}
	return normalize(ctx.Err())
}

type errorRow struct{ err error }

func (r errorRow) Scan(...any) error { return r.err }

type safeRow struct{ row Row }

func (r safeRow) Scan(dest ...any) error { return normalize(r.row.Scan(dest...)) }

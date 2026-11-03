package transaction

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

type mysqlQueries interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type mysqlExecutor struct{ queries mysqlQueries }

func (mysqlExecutor) Dialect() Dialect { return MySQL }
func (m mysqlExecutor) Exec(ctx context.Context, q Statement) (int64, error) {
	if e := q.validate(MySQL); e != nil {
		return 0, e
	}
	r, e := m.queries.ExecContext(ctx, q.text, q.args...)
	if e != nil {
		return 0, mysqlOperationError(ctx, e)
	}
	n, e := r.RowsAffected()
	return n, normalize(e)
}
func (m mysqlExecutor) Query(ctx context.Context, q Statement) (Rows, error) {
	if e := q.validate(MySQL); e != nil {
		return nil, e
	}
	r, e := m.queries.QueryContext(ctx, q.text, q.args...)
	if e != nil {
		return nil, mysqlOperationError(ctx, e)
	}
	return mysqlRows{r, ctx}, nil
}
func (m mysqlExecutor) QueryRow(ctx context.Context, q Statement) Row {
	if e := q.validate(MySQL); e != nil {
		return errorRow{e}
	}
	return mysqlRow{m.queries.QueryRowContext(ctx, q.text, q.args...), ctx}
}

// A cancelled database/sql transaction can race its automatic rollback and
// return ErrTxDone or an invalid connection instead of the context error.
// This boundary handles SQL operations only, never COMMIT acknowledgement.
func mysqlOperationError(ctx context.Context, err error) error {
	safe := normalize(err)
	var outcome *Error
	if ctx.Err() != nil && errors.As(safe, &outcome) && outcome.Kind == Unavailable {
		return normalize(ctx.Err())
	}
	return safe
}

type mysqlRow struct {
	row Row
	ctx context.Context
}

func (r mysqlRow) Scan(dest ...any) error { return mysqlOperationError(r.ctx, r.row.Scan(dest...)) }

type mysqlRows struct {
	rows *sql.Rows
	ctx  context.Context
}

func (r mysqlRows) Scan(dest ...any) error { return mysqlOperationError(r.ctx, r.rows.Scan(dest...)) }
func (r mysqlRows) Next() bool             { return r.rows.Next() }
func (r mysqlRows) Err() error             { return mysqlOperationError(r.ctx, r.rows.Err()) }
func (r mysqlRows) Close()                 { r.rows.Close() }

type mysqlStore struct {
	mysqlExecutor
	kernel *mysqlstore.Store
}

func FromMySQL(kernel *mysqlstore.Store) (Store, error) {
	if kernel == nil {
		return nil, errors.New("REPOSITORY_MYSQL_POOL_REQUIRED")
	}
	return &mysqlStore{mysqlExecutor{kernel.Pool()}, kernel}, nil
}
func (m *mysqlStore) Within(ctx context.Context, readOnly bool, operation func(context.Context, Executor) error) error {
	if e := operationContext(ctx, operation); e != nil {
		return e
	}
	var callbackError error
	e := m.kernel.Within(ctx, readOnly, func(ctx context.Context, tx *sql.Tx) error {
		callbackError = operation(ctx, mysqlExecutor{tx})
		return callbackError
	})
	// Preserve the domain error across the kernel's driver-error redaction.
	if callbackError != nil {
		return callbackError
	}
	return normalize(e)
}

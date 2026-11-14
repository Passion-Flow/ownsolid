package transaction

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

type mysqlSession struct {
	mysqlExecutor
	kernel     *mysqlstore.Store
	connection *sql.Conn
}

func (m *mysqlStore) WithSession(ctx context.Context, operation func(context.Context, Store) error) (result error) {
	if e := sessionContext(ctx, operation); e != nil {
		return e
	}
	connection, e := m.kernel.Pool().Conn(ctx)
	if e != nil {
		return mysqlOperationError(ctx, e)
	}
	defer func() {
		// ErrBadConn tells database/sql to discard the actual driver connection.
		// Conn.Close alone could return a named lock to the reusable pool.
		e := connection.Raw(func(any) error { return driver.ErrBadConn })
		closeError := connection.Close()
		if result == nil {
			if e != nil && !errors.Is(e, driver.ErrBadConn) && !errors.Is(e, sql.ErrConnDone) {
				result = normalize(e)
			} else if closeError != nil && !errors.Is(closeError, sql.ErrConnDone) {
				result = normalize(closeError)
			}
		}
	}()
	result = operation(ctx, &mysqlSession{mysqlExecutor{connection}, m.kernel, connection})
	if result == nil {
		result = normalize(ctx.Err())
	}
	return result
}

func (m *mysqlSession) Within(ctx context.Context, readOnly bool, operation func(context.Context, Executor) error) error {
	if e := operationContext(ctx, operation); e != nil {
		return e
	}
	var callbackError error
	e := m.kernel.WithinConnection(ctx, m.connection, readOnly, func(ctx context.Context, tx *sql.Tx) error {
		callbackError = operation(ctx, mysqlExecutor{tx})
		return callbackError
	})
	if callbackError != nil {
		return callbackError
	}
	return normalize(e)
}

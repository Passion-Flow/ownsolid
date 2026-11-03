//go:build cgo && odbc_manager

package transaction

import (
	"context"
	"database/sql"
)

var _ SessionStore = (*damengStore)(nil)

type damengSession struct {
	damengExecutor
	connection *sql.Conn
}

func (d *damengStore) WithSession(ctx context.Context, operation func(context.Context, Store) error) error {
	if e := sessionContext(ctx, operation); e != nil {
		return e
	}
	var callbackError error
	e := d.kernel.WithSession(ctx, func(ctx context.Context, c *sql.Conn) error {
		callbackError = operation(ctx, &damengSession{damengExecutor{c, d.kernel}, c})
		return callbackError
	})
	return damengSessionOutcome(e, callbackError)
}
func (d *damengSession) Within(ctx context.Context, readOnly bool, operation func(context.Context, Executor) error) error {
	if e := operationContext(ctx, operation); e != nil {
		return e
	}
	var callbackError error
	e := d.kernel.WithinConnection(ctx, d.connection, readOnly, func(ctx context.Context, tx *sql.Tx) error {
		callbackError = operation(ctx, damengExecutor{tx, d.kernel})
		return callbackError
	})
	return damengSessionOutcome(e, callbackError)
}

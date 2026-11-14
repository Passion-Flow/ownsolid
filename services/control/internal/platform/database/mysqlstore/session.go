package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// WithinConnection is for the trusted transaction adapter's exclusively borrowed
// connection from this kernel. The configured kernel still owns transaction
// deadlines and vendor policy; business callbacks never receive the raw handle.
func (s *Store) WithinConnection(ctx context.Context, connection *sql.Conn, readOnly bool, operation func(context.Context, *sql.Tx) error) error {
	if connection == nil || operation == nil {
		return errors.New("MYSQL_SESSION_TRANSACTION_OPERATION_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.configuration.Policy.TransactionSeconds)*time.Second)
	defer cancel()
	tx, e := connection.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: readOnly})
	if e != nil {
		return SafeError(e)
	}
	defer tx.Rollback()
	if e = operation(ctx, tx); e != nil {
		return SafeError(e)
	}
	if e = ctx.Err(); e != nil {
		return SafeError(e)
	}
	if e = tx.Commit(); e != nil {
		return ErrCommitOutcomeUnknown
	}
	return nil
}

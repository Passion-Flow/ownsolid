package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Within never retries. A failed commit is not evidence of rollback: the owning
// repository must reconcile durable operation identity before any explicit retry.
func (s *Store) Within(ctx context.Context, readOnly bool, operation func(context.Context, *sql.Tx) error) error {
	if operation == nil {
		return errors.New("MYSQL_TRANSACTION_OPERATION_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.configuration.Policy.TransactionSeconds)*time.Second)
	defer cancel()
	tx, e := s.pool.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: readOnly})
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

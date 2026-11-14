package transaction

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type postgresSession struct {
	postgresExecutor
	connection *pgx.Conn
}

func (p *postgresStore) WithSession(ctx context.Context, operation func(context.Context, Store) error) (result error) {
	if e := sessionContext(ctx, operation); e != nil {
		return e
	}
	connection, e := p.pool.Acquire(ctx)
	if e != nil {
		return normalize(e)
	}
	physical := connection.Conn()
	defer func() {
		// Keep the pool slot while the physical session exists. Release removes
		// the closed connection; no session-level lock is returned for reuse.
		e := physical.Close(ctx)
		connection.Release()
		if result == nil && e != nil {
			result = normalize(e)
		}
	}()
	result = operation(ctx, &postgresSession{postgresExecutor{physical}, physical})
	if result == nil {
		result = normalize(ctx.Err())
	}
	return result
}

func (p *postgresSession) Within(ctx context.Context, readOnly bool, operation func(context.Context, Executor) error) error {
	if e := operationContext(ctx, operation); e != nil {
		return e
	}
	mode := pgx.ReadWrite
	if readOnly {
		mode = pgx.ReadOnly
	}
	tx, e := p.connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: mode})
	if e != nil {
		return normalize(e)
	}
	defer tx.Rollback(ctx)
	if e = operation(ctx, PostgreSQLTransaction(tx)); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return normalize(e)
	}
	if e = tx.Commit(ctx); errors.Is(e, pgx.ErrTxCommitRollback) {
		return &Error{Kind: CommitRolledBack}
	} else if e != nil {
		return &Error{Kind: CommitUnknown}
	}
	return nil
}

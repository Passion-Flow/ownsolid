package transaction

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type postgresQueries interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}
type postgresExecutor struct{ queries postgresQueries }

func (postgresExecutor) Dialect() Dialect { return PostgreSQL }

// PostgreSQLTransaction borrows an already open transaction. Only its caller
// retains commit/rollback authority; no vendor transaction method is emulated.
func PostgreSQLTransaction(tx pgx.Tx) Executor {
	return postgresExecutor{queries: tx}
}
func (p postgresExecutor) Exec(ctx context.Context, q Statement) (int64, error) {
	if e := q.validate(PostgreSQL); e != nil {
		return 0, e
	}
	if p.queries == nil {
		return 0, &Error{Kind: Unavailable}
	}
	r, e := p.queries.Exec(ctx, q.text, q.args...)
	return r.RowsAffected(), normalize(e)
}
func (p postgresExecutor) Query(ctx context.Context, q Statement) (Rows, error) {
	if e := q.validate(PostgreSQL); e != nil {
		return nil, e
	}
	if p.queries == nil {
		return nil, &Error{Kind: Unavailable}
	}
	r, e := p.queries.Query(ctx, q.text, q.args...)
	if e != nil {
		return nil, normalize(e)
	}
	return postgresRows{rows: r}, nil
}
func (p postgresExecutor) QueryRow(ctx context.Context, q Statement) Row {
	if e := q.validate(PostgreSQL); e != nil {
		return errorRow{e}
	}
	if p.queries == nil {
		return errorRow{&Error{Kind: Unavailable}}
	}
	return safeRow{p.queries.QueryRow(ctx, q.text, q.args...)}
}

type postgresRows struct{ rows pgx.Rows }

func (r postgresRows) Scan(dest ...any) error { return normalize(r.rows.Scan(dest...)) }
func (r postgresRows) Next() bool             { return r.rows.Next() }
func (r postgresRows) Err() error             { return normalize(r.rows.Err()) }
func (r postgresRows) Close()                 { r.rows.Close() }

type postgresStore struct {
	postgresExecutor
	pool *pgxpool.Pool
}

func FromPostgreSQL(pool *pgxpool.Pool) (Store, error) {
	if pool == nil {
		return nil, errors.New("REPOSITORY_POSTGRESQL_POOL_REQUIRED")
	}
	return &postgresStore{postgresExecutor{pool}, pool}, nil
}
func (p *postgresStore) Within(ctx context.Context, readOnly bool, operation func(context.Context, Executor) error) error {
	if e := operationContext(ctx, operation); e != nil {
		return e
	}
	mode := pgx.ReadWrite
	if readOnly {
		mode = pgx.ReadOnly
	}
	tx, e := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: mode})
	if e != nil {
		return normalize(e)
	}
	// pgx closes the connection if a cancelled/failed rollback cannot finish.
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

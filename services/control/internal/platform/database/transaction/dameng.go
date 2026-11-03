//go:build cgo && odbc_manager

package transaction

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
)

// The DM adapter borrows only an admitted native Store. It neither migrates a
// schema nor adds Dameng to the application's PostgreSQL/MySQL engine selector.
type damengQueries interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type damengExecutor struct {
	queries damengQueries
	kernel  *dameng.Store
}

func (damengExecutor) Dialect() Dialect { return Dameng }
func (d damengExecutor) Exec(ctx context.Context, q Statement) (int64, error) {
	if e := q.validate(Dameng); e != nil {
		return 0, e
	}
	bounded, cancel, e := d.kernel.OperationContext(ctx)
	if e != nil {
		return 0, normalizeDameng(e)
	}
	defer cancel()
	r, e := d.queries.ExecContext(bounded, q.text, q.args...)
	if e != nil {
		return 0, normalizeDameng(e)
	}
	n, e := r.RowsAffected()
	return n, normalizeDameng(e)
}
func (d damengExecutor) Query(ctx context.Context, q Statement) (Rows, error) {
	if e := q.validate(Dameng); e != nil {
		return nil, e
	}
	bounded, cancel, e := d.kernel.OperationContext(ctx)
	if e != nil {
		return nil, normalizeDameng(e)
	}
	r, e := d.queries.QueryContext(bounded, q.text, q.args...)
	if e != nil {
		cancel()
		return nil, normalizeDameng(e)
	}
	// database/sql owns the context until the eager native rows are consumed.
	return &damengRows{rows: r, cancel: cancel}, nil
}
func (d damengExecutor) QueryRow(ctx context.Context, q Statement) Row {
	return &damengRow{executor: d, ctx: ctx, statement: q}
}

type damengRow struct {
	executor  damengExecutor
	ctx       context.Context
	statement Statement
	scanned   bool
}

func (r *damengRow) Scan(dest ...any) error {
	if r.scanned {
		return &Error{Kind: Unavailable}
	}
	r.scanned = true
	rows, e := r.executor.Query(r.ctx, r.statement)
	if e != nil {
		return e
	}
	defer rows.Close()
	if !rows.Next() {
		if e = rows.Err(); e != nil {
			return e
		}
		return &Error{Kind: NoRows}
	}
	return rows.Scan(dest...)
}

type damengRows struct {
	rows   *sql.Rows
	cancel context.CancelFunc
}

func (r *damengRows) Scan(dest ...any) error { return normalizeDameng(r.rows.Scan(dest...)) }
func (r *damengRows) Next() bool             { return r.rows.Next() }
func (r *damengRows) Err() error             { return normalizeDameng(r.rows.Err()) }
func (r *damengRows) Close()                 { r.rows.Close(); r.cancel() }

type damengStore struct{ damengExecutor }

// BoundToDameng proves provenance, not just a dialect string. Operator paths
// must not accept an arbitrary Store claiming to be the selected native pool.
func BoundToDameng(store Store, kernel *dameng.Store) bool {
	d, ok := store.(*damengStore)
	return ok && kernel != nil && d.kernel == kernel
}

// OwnsDamengExecutor identifies an exact transaction loan carrying this Store's
// native kernel. It grants no liveness, pool, lock or completion authority.
// Trusted constructors must retain the original transaction-to-kernel binding.
func (d *damengStore) OwnsDamengExecutor(q Executor) bool {
	x, ok := q.(damengExecutor)
	if !ok || d == nil || d.kernel == nil || x.kernel != d.kernel {
		return false
	}
	tx, ok := x.queries.(*sql.Tx)
	return ok && tx != nil
}

func FromDameng(kernel *dameng.Store) (Store, error) {
	if kernel == nil {
		return nil, errors.New("REPOSITORY_DAMENG_POOL_REQUIRED")
	}
	p, e := kernel.BorrowPool()
	if e != nil {
		return nil, normalizeDameng(e)
	}
	return &damengStore{damengExecutor{p, kernel}}, nil
}
func (d *damengStore) Within(ctx context.Context, readOnly bool, operation func(context.Context, Executor) error) error {
	if ctx == nil {
		return &Error{Kind: Unavailable}
	}
	if e := operationContext(ctx, operation); e != nil {
		return e
	}
	var callbackError error
	e := d.kernel.Within(ctx, readOnly, func(ctx context.Context, tx *sql.Tx) error {
		callbackError = operation(ctx, damengExecutor{tx, d.kernel})
		return callbackError
	})
	return damengSessionOutcome(e, callbackError)
}

// DamengCapacity is an explicit optional native capability. Repositories should
// use the returned byte limits with their actual projection, not a guessed UI
// page size. The projection contains no configuration paths or credentials.
func (d damengExecutor) DamengCapacity() (dameng.ODBCCapacity, error) { return d.kernel.Capacity() }

//go:build cgo && odbc_manager

package dameng

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"
)

// WithSession lends exactly one physical connection. Successful transactions
// restore autocommit for this loan only. Native errors permanently poison the
// session; there is no replacement, reconnect, automatic replay or idle reuse.
// The callback must join its consumers and close rows before returning. Manager
// retains failed native cleanup after this function, including during panic.
func (s *Store) WithSession(ctx context.Context, operation func(context.Context, *sql.Conn) error) (result error) {
	if operation == nil {
		return fail("DAMENG_SESSION_OPERATION_REQUIRED")
	}
	if e := odbcContext(ctx); e != nil {
		return SafeError(e)
	}
	p, e := s.BorrowPool()
	if e != nil {
		return SafeError(e)
	}
	c, e := p.Conn(ctx)
	if e != nil {
		return SafeError(e)
	}
	var ownerID uint64
	defer func() {
		// Raw ErrBadConn is disposal, never a mutation result. It cannot replay
		// business SQL. Explicit Close follows even if disposal reports failure.
		discard := c.Raw(func(any) error { return driver.ErrBadConn })
		closed := c.Close()
		if discard != nil && !errors.Is(discard, driver.ErrBadConn) && !errors.Is(discard, sql.ErrConnDone) {
			result = errors.Join(result, SafeError(discard))
		}
		if closed != nil && !errors.Is(closed, sql.ErrConnDone) {
			result = errors.Join(result, SafeError(closed))
		}
		// database/sql may suppress driver.Close errors after ErrBadConn. The
		// physical owner ledger, not logical Conn.Close, proves final disposal.
		for _, retained := range s.Status().Connections {
			if retained.OwnerID == ownerID {
				result = errors.Join(result, fail("DAMENG_SESSION_CLEANUP_PENDING"))
				break
			}
		}
	}()
	e = c.Raw(func(raw any) error {
		n, ok := raw.(*odbcConnection)
		if !ok || n.owner != s.manager {
			return fail("DAMENG_SESSION_OWNER_MISMATCH")
		}
		n.mu.Lock()
		defer n.mu.Unlock()
		ownerID = n.ownerID
		if e := n.ready(); e != nil {
			return e
		}
		if n.tx != nil || n.pendingTransaction || n.pinnedSession {
			return fail("DAMENG_SESSION_STATE_INVALID")
		}
		n.pinnedSession = true
		return nil
	})
	if e != nil {
		return SafeError(e)
	}
	if e = operation(ctx, c); e != nil {
		return e
	}
	return SafeError(ctx.Err())
}

// WithinConnection accepts only this Store's active pinned session. The kernel
// owns deadlines and completion; repository callbacks never receive this handle.
func (s *Store) WithinConnection(ctx context.Context, c *sql.Conn, readOnly bool, operation func(context.Context, *sql.Tx) error) error {
	if c == nil || operation == nil {
		return fail("DAMENG_SESSION_OPERATION_REQUIRED")
	}
	if e := odbcContext(ctx); e != nil {
		return SafeError(e)
	}
	if _, e := s.BorrowPool(); e != nil {
		return SafeError(e)
	}
	if e := c.Raw(func(raw any) error {
		n, ok := raw.(*odbcConnection)
		if !ok || n.owner != s.manager {
			return fail("DAMENG_SESSION_OWNER_MISMATCH")
		}
		n.mu.Lock()
		defer n.mu.Unlock()
		if !n.pinnedSession || n.tx != nil || n.pendingTransaction {
			return fail("DAMENG_SESSION_STATE_INVALID")
		}
		return n.ready()
	}); e != nil {
		return SafeError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.configuration.TransactionSeconds)*time.Second)
	defer cancel()
	tx, e := c.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: readOnly})
	if e != nil {
		return SafeError(e)
	}
	defer tx.Rollback()
	if e = operation(ctx, tx); e != nil {
		rollback := tx.Rollback()
		if rollback != nil && rollback != sql.ErrTxDone {
			return errors.Join(e, SafeError(rollback))
		}
		return e
	}
	return SafeError(tx.Commit())
}

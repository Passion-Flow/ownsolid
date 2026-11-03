package transaction

import (
	"context"
	"errors"
)

// SessionStore is an optional worker capability on an already configured Store.
// WithSession holds one pool slot throughout the callback and physically closes
// its connection afterward, even on panic. Session locks cannot leak into a pool.
// Callers must not retain the borrowed Store after the callback returns.
type SessionStore interface {
	Store
	WithSession(context.Context, func(context.Context, Store) error) error
}

func sessionContext(ctx context.Context, operation func(context.Context, Store) error) error {
	if operation == nil {
		return errors.New("REPOSITORY_SESSION_OPERATION_REQUIRED")
	}
	if ctx == nil {
		return errors.New("REPOSITORY_SESSION_CONTEXT_REQUIRED")
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("REPOSITORY_TRANSACTION_DEADLINE_REQUIRED")
	}
	return normalize(ctx.Err())
}

// Package damengserving composes the existing native connector and schema owner
// into one restricted serving owner. It never performs schema/grant mutation.
package damengserving

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Runtime remains owned even when OpenProtected returns an error. Callers must
// join all business workers before Close, retain failed-close ownership and retry
// only cleanup, never an uncertain mutation or automatic runtime reopen.
type Runtime interface {
	Repository() transaction.Store
	Verify(context.Context) error
	Close(context.Context) error
	CleanupSeconds() int
}

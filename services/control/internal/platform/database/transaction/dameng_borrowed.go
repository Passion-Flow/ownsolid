//go:build cgo && odbc_manager

package transaction

import (
	"database/sql"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
)

// DamengTransaction borrows the transaction supplied by kernel.Within or
// kernel.WithinConnection. The trusted caller must supply that same owner;
// database/sql exposes no independent transaction-to-pool provenance probe.
// Only the caller retains commit/rollback authority. Each operation keeps the
// existing native owner/context/capacity/error boundary and never opens a pool
// connection, retries a statement, or closes the transaction.
func DamengTransaction(tx *sql.Tx, kernel *dameng.Store) Executor {
	if tx == nil || kernel == nil {
		return damengExecutor{}
	}
	return damengExecutor{queries: tx, kernel: kernel}
}

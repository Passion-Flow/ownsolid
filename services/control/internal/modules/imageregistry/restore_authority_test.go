package imageregistry

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type restoreNoQueryExecutor struct {
	transaction.Executor
	dialect transaction.Dialect
}

func (q restoreNoQueryExecutor) Dialect() transaction.Dialect { return q.dialect }

type restoreNoQueryStore struct{ restoreNoQueryExecutor }

func (restoreNoQueryStore) Within(context.Context, bool, func(context.Context, transaction.Executor) error) error {
	panic("restore may not create transaction")
}

func TestRegistryRestoreRefusesPoolOrForeignTransactionWithoutSQL(t *testing.T) {
	for _, q := range []transaction.Executor{nil, restoreNoQueryExecutor{dialect: "unknown"}, restoreNoQueryStore{restoreNoQueryExecutor{dialect: transaction.PostgreSQL}}, restoreNoQueryStore{restoreNoQueryExecutor{dialect: transaction.MySQL}}} {
		if e := InvalidateAuthoritiesWithin(t.Context(), q); e == nil || e.Error() != "REGISTRY_TRANSACTION_REQUIRED" {
			t.Fatal("foreign independent owner admitted", e)
		}
	}
	if e := InvalidateAuthoritiesWithin(nil, restoreNoQueryExecutor{dialect: transaction.PostgreSQL}); e == nil {
		t.Fatal("missing context")
	}
}

type restoreBorrowedExecutor struct {
	restoreNoQueryExecutor
	calls int
}

func (q *restoreBorrowedExecutor) Exec(context.Context, transaction.Statement) (int64, error) {
	q.calls++
	return 1, nil
}
func TestRegistryRestoreUsesBorrowedNativeVendorTransaction(t *testing.T) {
	for _, dialect := range []transaction.Dialect{transaction.PostgreSQL, transaction.MySQL, transaction.Dameng} {
		q := &restoreBorrowedExecutor{restoreNoQueryExecutor: restoreNoQueryExecutor{dialect: dialect}}
		if e := InvalidateAuthoritiesWithin(t.Context(), q); e != nil || q.calls != 1 {
			t.Fatal("borrowed native owner refused or repeated", dialect, e)
		}
	}
}

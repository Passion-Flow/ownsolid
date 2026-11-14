//go:build cgo && odbc_manager

package transaction

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
)

func TestDamengBorrowedTransactionRequiresLiveNativeOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, input := range []struct {
		tx    *sql.Tx
		owner *dameng.Store
	}{
		{}, {tx: new(sql.Tx)}, {owner: new(dameng.Store)}, {tx: new(sql.Tx), owner: new(dameng.Store)},
	} {
		executor := DamengTransaction(input.tx, input.owner)
		if executor.Dialect() != Dameng {
			t.Fatal("borrowed transaction changed native dialect")
		}
		if _, err := executor.Exec(ctx, SQL(Dameng, "SELECT 1 FROM DUAL")); err == nil {
			t.Fatal("unadmitted native owner reached SQL")
		}
		if _, err := executor.Query(ctx, SQL(Dameng, "SELECT 1 FROM DUAL")); err == nil {
			t.Fatal("unadmitted native owner exposed rows")
		}
		var value int
		if err := executor.QueryRow(ctx, SQL(Dameng, "SELECT 1 FROM DUAL")).Scan(&value); err == nil {
			t.Fatal("unadmitted native owner exposed a row")
		}
		if _, ok := executor.(interface{ Commit() error }); ok {
			t.Fatal("borrower gained transaction completion authority")
		}
		if _, ok := executor.(interface{ Rollback() error }); ok {
			t.Fatal("borrower gained rollback authority")
		}
		capacity, ok := executor.(interface {
			DamengCapacity() (dameng.ODBCCapacity, error)
		})
		if !ok {
			t.Fatal("native capacity boundary missing")
		}
		if _, err := capacity.DamengCapacity(); err == nil {
			t.Fatal("unadmitted owner exposed capacity admission")
		}
	}
}

// Wrappers must explicitly preserve their trusted provenance boundary; a
// matching dialect or an embedded Executor alone cannot manufacture this proof.
type borrowedDamengWrapper struct{ Executor }

func TestDamengBorrowedTransactionProvenanceRequiresExactOwnerAndTransaction(t *testing.T) {
	owner := new(dameng.Store)
	other := new(dameng.Store)
	store := &damengStore{damengExecutor: damengExecutor{kernel: owner}}
	var nilTx *sql.Tx
	for _, tc := range []struct {
		name     string
		store    *damengStore
		executor Executor
		want     bool
	}{
		{"same_kernel_transaction", store, DamengTransaction(new(sql.Tx), owner), true},
		{"other_kernel", store, DamengTransaction(new(sql.Tx), other), false},
		{"nil_executor", store, nil, false},
		{"nil_store", nil, DamengTransaction(new(sql.Tx), owner), false},
		{"nil_store_kernel", new(damengStore), DamengTransaction(new(sql.Tx), owner), false},
		{"nil_transaction", store, DamengTransaction(nil, owner), false},
		{"typed_nil_transaction", store, damengExecutor{queries: nilTx, kernel: owner}, false},
		{"pool_executor", store, damengExecutor{queries: new(sql.DB), kernel: owner}, false},
		{"connection_executor", store, damengExecutor{queries: new(sql.Conn), kernel: owner}, false},
		{"wrapped_executor", store, borrowedDamengWrapper{DamengTransaction(new(sql.Tx), owner)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.store.OwnsDamengExecutor(tc.executor); got != tc.want {
				t.Fatal("transaction provenance result", got)
			}
		})
	}
}

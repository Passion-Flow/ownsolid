package artifactstores

import (
	"context"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"testing"
)

func TestObjectShutdownPreservesCancellationAndUncertainCommit(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, &transaction.Error{Kind: transaction.Cancelled}} {
		actual := safe(cause)
		if !transaction.IsCancellationOnly(actual) {
			t.Fatal("normal cancellation lost", actual)
		}
	}
	for _, kind := range []transaction.ErrorKind{transaction.CommitUnknown, transaction.CommitRolledBack} {
		for _, cause := range []error{errors.Join(context.Canceled, &transaction.Error{Kind: kind}), errors.Join(&transaction.Error{Kind: kind}, context.DeadlineExceeded)} {
			actual := safe(cause)
			if actual.Error() != string(kind) || transaction.IsCancellationOnly(actual) {
				t.Fatal("commit outcome suppressed", actual)
			}
		}
	}
	if e := safe(errors.New("sensitive SQL: credential")); e.Error() != "STORAGE_UNAVAILABLE" {
		t.Fatal("vendor error leaked", e)
	}
}

func TestObjectRestoreFenceRequiresActualBorrowedTransaction(t *testing.T) {
	if e := FenceAfterRestoreWithin(context.Background(), nil, "11111111-1111-4111-8111-111111111111"); e == nil {
		t.Fatal("missing borrowed transaction accepted")
	}
}

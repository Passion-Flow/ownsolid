package recovery

import (
	"context"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"testing"
)

func TestM16RestoreCommitOutcomePrecedesCancellation(t *testing.T) {
	for _, kind := range []transaction.ErrorKind{transaction.CommitUnknown, transaction.CommitRolledBack} {
		e := recoveryError(errors.Join(context.Canceled, &transaction.Error{Kind: kind}))
		if e.Error() != string(kind) {
			t.Fatal("commit outcome hidden by cancellation", e)
		}
	}
}

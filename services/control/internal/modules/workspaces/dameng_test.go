package workspaces

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

// Embedded methods panic if the validation boundary unexpectedly touches storage.
type damengCursorBoundary struct{ transaction.Executor }

func (damengCursorBoundary) Dialect() transaction.Dialect { return transaction.Dameng }

func TestDamengWorkspaceCursorRefusesPrecisionLoss(t *testing.T) {
	stamp := time.Date(2026, 10, 24, 0, 0, 0, 123456000, time.UTC)
	for _, invalid := range []time.Time{stamp.Add(time.Nanosecond), stamp.AddDate(-100, 0, 0)} {
		_, e := listSeek(damengCursorBoundary{}, "", "", "", nil, nil, "created_at", "id", pagination.Cursor{Time: invalid, ID: uuid.NewString()}, 10)
		if e == nil || e.Error() != "INVALID_CURSOR" {
			t.Fatal("cursor rounded or accepted outside native timestamp range")
		}
	}
	for _, invalid := range []int{0, pagination.MaximumLimit + 1} {
		if _, e := listSeek(damengCursorBoundary{}, "", "", "", nil, nil, "created_at", "id", pagination.Cursor{}, invalid); e == nil {
			t.Fatal("unbounded native LIMIT accepted")
		}
	}
}
func TestDamengWorkspaceUnknownOutcomePrecedesTimeout(t *testing.T) {
	for _, e := range []error{errors.Join(context.DeadlineExceeded, &transaction.Error{Kind: transaction.CommitUnknown}), errors.Join(&transaction.Error{Kind: transaction.Unavailable}, &transaction.Error{Kind: transaction.CommitUnknown})} {
		if mapped := workspaceError(e); mapped == nil || mapped.Error() != string(transaction.CommitUnknown) {
			t.Fatal("commit uncertainty lost")
		}
	}
}

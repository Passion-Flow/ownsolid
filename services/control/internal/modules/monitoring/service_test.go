package monitoring

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type cyclicSnapshotError struct{}

func (*cyclicSnapshotError) Error() string   { return "private malformed cause" }
func (e *cyclicSnapshotError) Unwrap() error { return e }

func TestSnapshotErrorClassificationPreservesCommitOutcome(t *testing.T) {
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	rollback := &transaction.Error{Kind: transaction.CommitRolledBack}
	cancelled := &transaction.Error{Kind: transaction.Cancelled}
	cases := []struct {
		name string
		err  error
		want transaction.ErrorKind
	}{
		{"cancelled", context.Canceled, transaction.Cancelled},
		{"wrapped_deadline", fmt.Errorf("private query: %w", context.DeadlineExceeded), transaction.Timeout},
		{"all_cancelled", errors.Join(context.Canceled, cancelled), transaction.Cancelled},
		{"mixed_deadline", errors.Join(context.Canceled, context.DeadlineExceeded), transaction.Timeout},
		{"unknown_after_cancel", errors.Join(context.Canceled, unknown), transaction.CommitUnknown},
		{"unknown_before_cancel", errors.Join(unknown, context.DeadlineExceeded), transaction.CommitUnknown},
		{"unknown_after_rollback", errors.Join(cancelled, rollback, fmt.Errorf("private ACK: %w", unknown)), transaction.CommitUnknown},
		{"unknown_before_rollback", errors.Join(unknown, rollback), transaction.CommitUnknown},
		{"rollback_after_cancel", errors.Join(context.Canceled, rollback), transaction.CommitRolledBack},
		{"mixed_failure", errors.Join(cancelled, errors.New("private driver credential")), transaction.Unavailable},
		{"cycle", &cyclicSnapshotError{}, transaction.Unavailable},
		{"secret_cause", errors.New("private driver credential"), transaction.Unavailable},
	}
	oversized := make([]error, 65)
	for i := range oversized {
		oversized[i] = context.Canceled
	}
	cases = append(cases, struct {
		name string
		err  error
		want transaction.ErrorKind
	}{"oversized", errors.Join(oversized...), transaction.Unavailable})
	if safeError(nil) != nil {
		t.Fatal("nil error changed")
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			actual := safeError(item.err)
			if actual == nil || actual.Error() != string(item.want) {
				t.Fatalf("public classification = %v; want %s", actual, item.want)
			}
		})
	}
}

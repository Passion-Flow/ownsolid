package training

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type parallelTrainingCyclicError struct{}

func (parallelTrainingCyclicError) Error() string { return "private driver diagnostic" }
func (parallelTrainingCyclicError) Unwrap() error { return parallelTrainingCyclicError{} }

func TestParallelMySQLTrainingShutdownOutcome(t *testing.T) {
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	rolledBack := &transaction.Error{Kind: transaction.CommitRolledBack}
	for _, test := range []struct {
		name string
		err  error
		want transaction.ErrorKind
	}{
		{"cancel", context.Canceled, transaction.Cancelled},
		{"deadline", fmt.Errorf("private context: %w", context.DeadlineExceeded), transaction.Timeout},
		{"cancel_then_unknown", errors.Join(context.Canceled, unknown), transaction.CommitUnknown},
		{"unknown_then_deadline", errors.Join(unknown, context.DeadlineExceeded), transaction.CommitUnknown},
		{"nested_outcomes", errors.Join(rolledBack, fmt.Errorf("private commit: %w", errors.Join(context.Canceled, unknown))), transaction.CommitUnknown},
		{"cancel_then_rollback", errors.Join(context.Canceled, rolledBack), transaction.CommitRolledBack},
		{"failure_with_cancel", errors.Join(context.Canceled, errors.New("private driver diagnostic")), transaction.Unavailable},
		{"malformed_unwrap", parallelTrainingCyclicError{}, transaction.Unavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := trainingRepositoryError(test.err); got == nil || got.Error() != string(test.want) {
				t.Fatalf("safe outcome=%v expected=%s", got, test.want)
			}
		})
	}
}

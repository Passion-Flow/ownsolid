package imageregistry

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestRegistrySafeErrorPreservesUncertainOutcomes(t *testing.T) {
	if safeError(nil) != nil {
		t.Fatal("nil error changed")
	}
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	rolledBack := &transaction.Error{Kind: transaction.CommitRolledBack}
	for _, test := range []struct {
		name  string
		input error
		want  transaction.ErrorKind
	}{
		{"unknown_before_cancel", errors.Join(unknown, context.Canceled), transaction.CommitUnknown},
		{"cancel_before_unknown", errors.Join(context.Canceled, unknown), transaction.CommitUnknown},
		{"rollback_before_cancel", errors.Join(rolledBack, context.Canceled), transaction.CommitRolledBack},
		{"wrapped_unknown_before_timeout", fmt.Errorf("private vendor detail: %w", errors.Join(context.DeadlineExceeded, unknown)), transaction.CommitUnknown},
		{"unknown_after_rollback_and_cancel", errors.Join(rolledBack, context.Canceled, unknown), transaction.CommitUnknown},
		{"mixed_cancel_critical_redacted", errors.Join(context.Canceled, errors.New("private critical vendor detail")), transaction.Unavailable},
		{"mixed_typed_cancel_critical_redacted", errors.Join(&transaction.Error{Kind: transaction.Cancelled}, errors.New("private critical vendor detail")), transaction.Unavailable},
		{"pure_cancel_join", errors.Join(context.Canceled, context.DeadlineExceeded), transaction.Cancelled},
		{"wrapped_constraint_redacted", fmt.Errorf("private vendor detail: %w", &transaction.Error{Kind: transaction.CheckConflict}), transaction.CheckConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual := safeError(test.input)
			if actual == nil || actual.Error() != string(test.want) || errors.Unwrap(actual) != nil {
				t.Fatalf("safe registry outcome = %v, want %s", actual, test.want)
			}
		})
	}
}

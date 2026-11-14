package nodes

import (
	"context"
	"errors"
	"fmt"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"testing"
)

func TestRegistryRuntimeErrorPreservesCommitOutcomes(t *testing.T) {
	if taskRuntimeError(nil) != nil {
		t.Fatal("nil error changed")
	}
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	rollback := &transaction.Error{Kind: transaction.CommitRolledBack}
	for _, sample := range []struct {
		name  string
		input error
		want  transaction.ErrorKind
	}{
		{"unknown_before_cancel", errors.Join(unknown, context.Canceled), transaction.CommitUnknown},
		{"cancel_before_unknown", errors.Join(context.Canceled, unknown), transaction.CommitUnknown},
		{"rollback_before_cancel", errors.Join(rollback, context.Canceled), transaction.CommitRolledBack},
		{"wrapped_unknown_deadline", fmt.Errorf("private vendor detail: %w", errors.Join(context.DeadlineExceeded, unknown)), transaction.CommitUnknown},
		{"unknown_after_rollback_cancel", errors.Join(rollback, context.Canceled, unknown), transaction.CommitUnknown},
		{"mixed_cancel_critical", errors.Join(context.Canceled, errors.New("private critical vendor detail")), transaction.Unavailable},
		{"mixed_typed_cancel_critical", errors.Join(&transaction.Error{Kind: transaction.Cancelled}, errors.New("private critical vendor detail")), transaction.Unavailable},
		{"pure_cancel_join", errors.Join(context.Canceled, context.DeadlineExceeded), transaction.Cancelled},
		{"pure_timeout", context.DeadlineExceeded, transaction.Timeout},
		{"direct_unknown", unknown, transaction.CommitUnknown},
	} {
		t.Run(sample.name, func(t *testing.T) {
			actual := taskRuntimeError(sample.input)
			if actual == nil || actual.Error() != string(sample.want) || errors.Unwrap(actual) != nil {
				t.Fatalf("safe outcome = %v, want %s", actual, sample.want)
			}
		})
	}
	if e := taskRuntimeError(errors.New("ACCESS_DENIED")); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal("domain refusal changed", e)
	}
}

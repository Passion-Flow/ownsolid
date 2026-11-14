package preferences

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type portalOutcomeCycle struct{}

func (portalOutcomeCycle) Error() string { return "PRIVATE_CYCLE" }
func (portalOutcomeCycle) Unwrap() error { return portalOutcomeCycle{} }

func TestPortalCommitOutcomePrecedesCancellation(t *testing.T) {
	oversized := make([]error, 128)
	for i := range oversized {
		oversized[i] = context.Canceled
	}
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	rollback := &transaction.Error{Kind: transaction.CommitRolledBack}
	for _, item := range []struct {
		name  string
		input error
		want  transaction.ErrorKind
	}{
		{"cancel_then_unknown", errors.Join(context.Canceled, unknown), transaction.CommitUnknown},
		{"unknown_then_deadline", errors.Join(unknown, context.DeadlineExceeded), transaction.CommitUnknown},
		{"rollback_then_unknown", errors.Join(rollback, unknown), transaction.CommitUnknown},
		{"wrapped_plain_unknown", fmt.Errorf("private DSN/password: %w", errors.Join(context.Canceled, errors.New(string(transaction.CommitUnknown)))), transaction.CommitUnknown},
		{"deadline_then_rollback", errors.Join(context.DeadlineExceeded, rollback), transaction.CommitRolledBack},
		{"cancel_with_driver_failure", errors.Join(context.Canceled, errors.New("private database failure")), transaction.Unavailable},
		{"typed_cancel_with_failure", errors.Join(&transaction.Error{Kind: transaction.Cancelled}, errors.New("private database failure")), transaction.Unavailable},
		{"cycle", portalOutcomeCycle{}, transaction.Unavailable},
		{"oversized", errors.Join(oversized...), transaction.Unavailable},
		{"mixed_private", errors.Join(context.Canceled, fmt.Errorf("PRIVATE_PROVIDER_BODY: %w", errors.New("PRIVATE_CAUSE"))), transaction.Unavailable},
		{"cancel_only", context.Canceled, transaction.Cancelled},
		{"deadline_only", context.DeadlineExceeded, transaction.Timeout},
	} {
		t.Run(item.name, func(t *testing.T) {
			got := preferenceError(item.input)
			if got == nil || got.Error() != string(item.want) {
				t.Fatal("public outcome classification changed", got)
			}
			var safe *transaction.Error
			if !errors.As(got, &safe) && item.want != transaction.Unavailable {
				t.Fatal("typed outcome lost")
			}
			if item.want == transaction.CommitUnknown && (errors.Is(got, context.Canceled) || errors.Is(got, context.DeadlineExceeded)) {
				t.Fatal("uncertain commit appears retryable as cancellation")
			}
		})
	}
}

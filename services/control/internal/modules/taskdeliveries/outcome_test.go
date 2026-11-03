package taskdeliveries

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type cyclicEventError struct{}

func (cyclicEventError) Error() string   { return "private cyclic vendor marker" }
func (e cyclicEventError) Unwrap() error { return e }

func TestTaskDeliveryFailurePreservesCommitOutcomeBeforeCancellation(t *testing.T) {
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	rolled := &transaction.Error{Kind: transaction.CommitRolledBack}
	for _, c := range []struct {
		name     string
		input    error
		expected string
	}{
		{"unknown_cancel", errors.Join(context.Canceled, unknown), string(transaction.CommitUnknown)},
		{"unknown_deadline", fmt.Errorf("private SQL path: %w", errors.Join(context.DeadlineExceeded, unknown)), string(transaction.CommitUnknown)},
		{"rollback_cancel", errors.Join(context.Canceled, rolled), string(transaction.CommitRolledBack)},
		{"unknown_before_rollback", errors.Join(rolled, context.Canceled, unknown), string(transaction.CommitUnknown)},
		{"plain_cancel", context.Canceled, string(transaction.Cancelled)},
		{"plain_deadline", context.DeadlineExceeded, string(transaction.Timeout)},
		{"mixed_vendor_cancel", errors.Join(context.Canceled, errors.New("private SQL path")), string(transaction.Unavailable)},
		{"cyclic", cyclicEventError{}, string(transaction.Unavailable)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if e := safe(c.input); e == nil || e.Error() != c.expected {
				t.Fatal("outcome lost or private cause escaped", e, c.expected)
			}
		})
	}
}

func TestTaskDeliveryCancelledWorkerUnknownDominatesAcrossOperations(t *testing.T) {
	u := &transaction.Error{Kind: transaction.CommitUnknown}
	r := &transaction.Error{Kind: transaction.CommitRolledBack}
	for _, c := range []struct {
		name   string
		causes []error
	}{
		{"rollback_then_unknown", []error{r, context.Canceled, u}},
		{"unknown_then_rollback", []error{u, context.DeadlineExceeded, r}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if e := cancelledCommitOutcome(c.causes...); e == nil || e.Error() != string(transaction.CommitUnknown) {
				t.Fatal("operation ordering downgraded unknown outcome", e)
			}
		})
	}
	if cancelledCommitOutcome(nil, context.Canceled) != nil {
		t.Fatal("plain cancellation became an unknown commit")
	}
}

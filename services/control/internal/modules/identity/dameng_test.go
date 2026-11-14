package identity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestDamengAccountCommitOutcomePrecedesCancellation(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, &transaction.Error{Kind: transaction.Unavailable}} {
		for _, combined := range []error{
			errors.Join(cause, &transaction.Error{Kind: transaction.CommitUnknown}),
			errors.Join(&transaction.Error{Kind: transaction.CommitUnknown}, cause),
		} {
			if e := sessionError(combined); e == nil || e.Error() != string(transaction.CommitUnknown) {
				t.Fatal("uncertain commit must not become a retryable authentication failure")
			}
		}
	}
}

func TestDamengAccountRejectsLossyListTime(t *testing.T) {
	exact := time.Date(2026, 10, 24, 0, 0, 0, 123456000, time.UTC)
	for _, stamp := range []time.Time{exact.Add(time.Nanosecond), exact.AddDate(-100, 0, 0), exact.In(time.FixedZone("offset", 3600))} {
		for _, cursor := range []bool{false, true} {
			p := listPlan{dmArgs: []any{"bound-value", stamp}, hasCursor: cursor}
			expected := "VALIDATION_FAILED"
			if cursor {
				expected = "INVALID_CURSOR"
			}
			if e := p.validateDamengTimes(); e == nil || e.Error() != expected {
				t.Fatal("unrepresentable time was accepted or reclassified")
			}
		}
	}
	if e := (listPlan{dmArgs: []any{exact, exact.Add(time.Microsecond)}}).validateDamengTimes(); e != nil {
		t.Fatal(e)
	}
}

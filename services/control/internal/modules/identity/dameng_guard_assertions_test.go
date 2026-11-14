package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// A guard pass requires the sole classified native statement refusal. A joined
// cleanup error, ambiguous commit, permission failure or generic outage does not
// establish that the expected trigger rejected the mutation and rollback ended.
// This is test-only; no production error taxonomy or API is changed.
func damengAccountGuardRefusal(err error, expectedCode int32) bool {
	if _, ok := transaction.CommitOutcome(err); ok {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	native, ok := err.(*transaction.Error)
	return ok && native != nil && native.Kind == transaction.CheckConflict && native.SQLState == "23000" && native.NativeCode == expectedCode
}

func TestDamengAccountGuardRequiresExactNativeRefusal(t *testing.T) {
	for _, expected := range []int32{-20011, -20012, -20013, -20014} {
		exact := &transaction.Error{Kind: transaction.CheckConflict, SQLState: "23000", NativeCode: expected}
		if !damengAccountGuardRefusal(exact, expected) {
			t.Fatal("exact native guard refusal rejected")
		}
		for _, unexpected := range []error{
			nil,
			context.Canceled,
			context.DeadlineExceeded,
			&transaction.Error{Kind: transaction.CheckConflict, SQLState: "23000", NativeCode: expected - 100},
			&transaction.Error{Kind: transaction.CheckConflict, SQLState: "42501", NativeCode: expected},
			&transaction.Error{Kind: transaction.Unavailable, SQLState: "23000", NativeCode: expected},
			&transaction.Error{Kind: transaction.Unavailable, SQLState: "42501", NativeCode: -551},
			&transaction.Error{Kind: transaction.UniqueConflict, SQLState: "23000", NativeCode: expected},
			&transaction.Error{Kind: transaction.CommitUnknown, NativeCode: expected},
			&transaction.Error{Kind: transaction.CommitRolledBack, NativeCode: expected},
			errors.Join(exact, context.DeadlineExceeded),
			errors.Join(exact, &transaction.Error{Kind: transaction.CommitUnknown}),
			errors.Join(&transaction.Error{Kind: transaction.CommitUnknown}, exact),
			errors.Join(exact, &transaction.Error{Kind: transaction.Unavailable}),
			errors.Join(&transaction.Error{Kind: transaction.Unavailable}, exact),
		} {
			if damengAccountGuardRefusal(unexpected, expected) {
				t.Fatal("unrelated or uncertain error falsely proved native guard refusal")
			}
		}
	}
}

package transaction

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
)

func TestDamengSessionOutcomePreservesCleanupWithNilCallback(t *testing.T) {
	cleanup := errors.New("DAMENG_SESSION_CLEANUP_PENDING")
	for _, cancelled := range []error{context.Canceled, context.DeadlineExceeded, &dameng.Error{Kind: dameng.Cancelled}} {
		for _, mixed := range []error{errors.Join(cancelled, cleanup), errors.Join(cleanup, cancelled), fmt.Errorf("session: %w", errors.Join(cancelled, cleanup))} {
			got := damengSessionOutcome(mixed, nil)
			if !errors.Is(got, cleanup) || IsCancellationOnly(got) {
				t.Fatal("cleanup retention disappeared behind cancellation")
			}
		}
	}
}

func TestDamengSessionOutcomePureCancellation(t *testing.T) {
	for _, cancelled := range []error{context.Canceled, context.DeadlineExceeded, errors.Join(context.Canceled, context.DeadlineExceeded), &dameng.Error{Kind: dameng.Cancelled}, &dameng.Error{Kind: dameng.Timeout}} {
		got := damengSessionOutcome(cancelled, nil)
		if !IsCancellationOnly(got) {
			t.Fatal("pure cancellation became an unrelated failure")
		}
	}
	if got := damengSessionOutcome(nil, nil); got != nil {
		t.Fatal("successful session became a failure")
	}
}

func TestDamengSessionOutcomeCommitUnknownPrecedesCleanup(t *testing.T) {
	cleanup := errors.New("DAMENG_SESSION_CLEANUP_PENDING")
	for _, unknown := range []error{dameng.ErrCommitOutcomeUnknown, &dameng.Error{Kind: dameng.CommitUnknown}, &Error{Kind: CommitUnknown}} {
		for _, mixed := range []error{errors.Join(context.Canceled, cleanup, unknown), errors.Join(unknown, cleanup, context.DeadlineExceeded)} {
			got := damengSessionOutcome(mixed, nil)
			if kind, ok := CommitOutcome(got); !ok || kind != CommitUnknown || IsCancellationOnly(got) {
				t.Fatal("commit uncertainty lost to cancellation or cleanup")
			}
		}
	}
	got := damengSessionOutcome(errors.Join(context.Canceled, cleanup), &Error{Kind: CommitUnknown})
	if kind, ok := CommitOutcome(got); !ok || kind != CommitUnknown {
		t.Fatal("callback commit uncertainty lost")
	}
}

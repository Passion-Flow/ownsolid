package workflows

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWorkflowFailureMapsProcessCancellationAndPreservesExplicitCause(t *testing.T) {
	parent, stop := context.WithCancel(context.Background())
	job, cancel := context.WithCancelCause(parent)
	stop()
	// A later explicit shutdown cause cannot replace the inherited cancellation.
	cancel(failure("WORKFLOW_RUN_INTERRUPTED"))
	deadline, done := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer done()
	explicit, end := context.WithCancelCause(context.Background())
	end(failure("WORKFLOW_RUN_CANCELLED"))
	for _, c := range []struct {
		name, state, code string
		ctx               context.Context
		err               error
	}{
		{"signal_before_stop", InterruptedState, "WORKFLOW_RUN_INTERRUPTED", job, failure("STORAGE_UNAVAILABLE")},
		{"ordinary_cancel", InterruptedState, "WORKFLOW_RUN_INTERRUPTED", context.Background(), context.Canceled},
		{"deadline", FailedState, "WORKFLOW_RUN_TIMEOUT", deadline, failure("STORAGE_UNAVAILABLE")},
		{"explicit_owner_cancel", CancelledState, "WORKFLOW_RUN_CANCELLED", explicit, context.Canceled},
		{"authority_loss", FailedState, "WORKFLOW_AUTHORITY_CHANGED", context.Background(), failure("WORKFLOW_AUTHORITY_CHANGED")},
		{"unknown_diagnostic", FailedState, "STORAGE_UNAVAILABLE", context.Background(), errors.New("private database diagnostic")},
	} {
		t.Run(c.name, func(t *testing.T) {
			state, code := runFailure(c.ctx, c.err)
			if state != c.state || code != c.code {
				t.Fatalf("expected %s/%s observed %s/%s", c.state, c.code, state, code)
			}
		})
	}
}

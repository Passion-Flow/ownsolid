//go:build repository_integration && task_events_integration

package taskdeliveries

import (
	"context"
	"errors"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type eventCancelledOutcomeStore struct {
	transaction.Store
	cancel   context.CancelFunc
	rollback bool
	observed int
}

func (s *eventCancelledOutcomeStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	rollback := errors.New("EXPLICIT_CANCELLED_EVENT_FIXTURE_ROLLBACK")
	e := s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		if e := fn(ctx, q); e != nil {
			return e
		}
		if !ro && s.rollback {
			return rollback
		}
		return nil
	})
	if !ro && (e == nil || errors.Is(e, rollback)) {
		s.observed++
		s.cancel()
		kind := transaction.CommitUnknown
		if s.rollback {
			kind = transaction.CommitRolledBack
		}
		return errors.Join(context.Canceled, &transaction.Error{Kind: kind})
	}
	return e
}

func TestTaskEventsRunCancelledOutcome(t *testing.T) {
	for _, leaf := range []string{"committed_claim_unknown", "rolled_back_claim"} {
		t.Run(leaf, func(t *testing.T) {
			g := eventSetup(t)
			_, value := g.task(t)
			ctx, cancel := context.WithCancel(g.ctx)
			defer cancel()
			store := &eventCancelledOutcomeStore{Store: g.f.Store, cancel: cancel, rollback: leaf == "rolled_back_claim"}
			g.owner.Storage = store
			e := g.s.Run(ctx)
			g.owner.Storage = g.f.Store
			expected, state := string(transaction.CommitUnknown), "sent"
			if store.rollback {
				expected, state = string(transaction.CommitRolledBack), "prepared"
			}
			if e == nil || e.Error() != expected || store.observed != 1 || ctx.Err() != context.Canceled {
				t.Fatal("cancellation erased actual transaction outcome", e, store.observed)
			}
			var actual string
			var unknown bool
			if e = g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT state,outcome_unknown FROM task_event_outbox WHERE id=$1", "SELECT state,outcome_unknown FROM task_event_outbox WHERE id=?", value.ID)).Scan(&actual, &unknown); e != nil || actual != state || unknown == store.rollback {
				t.Fatal("actual transaction persistence mismatches outcome", e, actual, unknown)
			}
			stats, e := g.s.Transport.Inspect(g.ctx)
			if e != nil || stats.Messages != 0 || stats.AckPending != 0 {
				t.Fatal("unconfirmed claim sent a broker mutation", e, stats)
			}
		})
	}
}

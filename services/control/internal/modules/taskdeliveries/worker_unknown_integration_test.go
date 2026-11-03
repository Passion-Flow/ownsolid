//go:build repository_integration && task_events_integration

package taskdeliveries

import (
	"context"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// The first writable transaction commits in the real selected database. Only
// its reported outcome is replaced; context remains live and later calls still
// reach real SQL so continued worker activity cannot be hidden by a success stub.
type eventUnknownCommitStore struct {
	transaction.Store
	injected bool
	after    int
}

func (s *eventUnknownCommitStore) observe() {
	if s.injected {
		s.after++
	}
}

func (s *eventUnknownCommitStore) QueryRow(ctx context.Context, q transaction.Statement) transaction.Row {
	s.observe()
	return s.Store.QueryRow(ctx, q)
}

func (s *eventUnknownCommitStore) Query(ctx context.Context, q transaction.Statement) (transaction.Rows, error) {
	s.observe()
	return s.Store.Query(ctx, q)
}

func (s *eventUnknownCommitStore) Exec(ctx context.Context, q transaction.Statement) (int64, error) {
	s.observe()
	return s.Store.Exec(ctx, q)
}

func (s *eventUnknownCommitStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	s.observe()
	err := s.Store.Within(ctx, ro, fn)
	if err == nil && !ro && !s.injected {
		s.injected = true
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return err
}

func TestTaskEventsRunUnknownCommitFence(t *testing.T) {
	t.Run("committed_claim_unknown_without_cancellation", func(t *testing.T) {
		g := eventSetup(t)
		task, value := g.task(t)
		store := &eventUnknownCommitStore{Store: g.f.Store}
		g.owner.Storage = store
		defer func() { g.owner.Storage = g.f.Store }()
		ctx, cancel := context.WithTimeout(g.ctx, 5*time.Second)
		defer cancel()
		err := g.s.Run(ctx)
		if err == nil || err.Error() != string(transaction.CommitUnknown) || !store.injected || store.after != 0 || ctx.Err() != nil {
			t.Fatal("unknown commit admitted a later stage or waited for cancellation", err, store.injected, store.after, ctx.Err())
		}
		if err = g.s.Run(ctx); err == nil || err.Error() != string(transaction.CommitUnknown) || store.after != 0 {
			t.Fatal("unknown worker outcome resumed through stale owner", err, store.after)
		}
		g.owner.Storage = g.f.Store
		var state string
		var unknown bool
		if err = g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT state,outcome_unknown FROM task_event_outbox WHERE id=$1", "SELECT state,outcome_unknown FROM task_event_outbox WHERE id=?", value.ID)).Scan(&state, &unknown); err != nil || state != "sent" || !unknown {
			t.Fatal("unknown claim lost confirmed retained SQL intent", err, state, unknown)
		}
		status, err := g.s.Transport.Inspect(g.ctx)
		if err != nil || status.Messages != 0 || status.Pending != 0 || status.AckPending != 0 ||
			g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 0 ||
			g.count(t, "SELECT count(*) FROM task_event_dead_letters", "SELECT count(*) FROM task_event_dead_letters") != 0 ||
			g.count(t, "SELECT count(*) FROM node_tasks WHERE id=$1 AND state='pending' AND attempt=0", "SELECT count(*) FROM node_tasks WHERE id=? AND state='pending' AND attempt=0", task.ID) != 1 {
			t.Fatal("unknown claim sent, acknowledged or changed physical task authority", err, status)
		}
	})
}

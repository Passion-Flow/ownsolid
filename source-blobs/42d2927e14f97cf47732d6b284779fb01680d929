//go:build repository_integration && task_events_integration

package taskdeliveries

import (
	"context"
	"reflect"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

// The fixture keeps a real committed publication pending in the real broker.
// Only one SQL read is faulted; all subsequent calls still reach the actual
// database so the regression cannot pass by hiding later worker side effects.
type eventWorkerFenceStore struct {
	transaction.Store
	failRead, reads, afterFailure int
	code                          string
}

type eventWorkerFenceRow struct{ code string }

func (r eventWorkerFenceRow) Scan(...any) error { return fail(r.code) }

func (s *eventWorkerFenceStore) observe() {
	if s.reads >= s.failRead {
		s.afterFailure++
	}
}

func (s *eventWorkerFenceStore) QueryRow(ctx context.Context, q transaction.Statement) transaction.Row {
	return s.queryRow(ctx, s.Store, q)
}

func (s *eventWorkerFenceStore) queryRow(ctx context.Context, executor transaction.Executor, q transaction.Statement) transaction.Row {
	s.observe()
	s.reads++
	if s.reads == s.failRead {
		return eventWorkerFenceRow{s.code}
	}
	return executor.QueryRow(ctx, q)
}

func (s *eventWorkerFenceStore) Query(ctx context.Context, q transaction.Statement) (transaction.Rows, error) {
	s.observe()
	return s.Store.Query(ctx, q)
}

func (s *eventWorkerFenceStore) Exec(ctx context.Context, q transaction.Statement) (int64, error) {
	s.observe()
	return s.Store.Exec(ctx, q)
}

func (s *eventWorkerFenceStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	s.observe()
	return s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, eventWorkerFenceExecutor{Executor: q, owner: s})
	})
}

type eventWorkerFenceExecutor struct {
	transaction.Executor
	owner *eventWorkerFenceStore
}

func (q eventWorkerFenceExecutor) QueryRow(ctx context.Context, statement transaction.Statement) transaction.Row {
	return q.owner.queryRow(ctx, q.Executor, statement)
}

func (q eventWorkerFenceExecutor) Query(ctx context.Context, statement transaction.Statement) (transaction.Rows, error) {
	q.owner.observe()
	return q.Executor.Query(ctx, statement)
}

func (q eventWorkerFenceExecutor) Exec(ctx context.Context, statement transaction.Statement) (int64, error) {
	q.owner.observe()
	return q.Executor.Exec(ctx, statement)
}

func TestTaskEventsRunFatalStageFence(t *testing.T) {
	for _, c := range []struct {
		leaf, code string
		read       int
	}{
		{"publisher_read", "TASK_EVENT_INVALID", 1},
		{"expiry_read", "TASK_BUS_IDENTITY_CHANGED", 2},
		{"history_cleanup_read", "TASK_EVENT_RECEIPT_CONFLICT", 3},
		{"quarantine_cleanup_read", "TASK_BUS_READBACK_CONFLICT", 5},
	} {
		t.Run(c.leaf, func(t *testing.T) {
			g := eventSetup(t)
			task, value := g.task(t)
			g.publish(t)
			before, e := g.s.Transport.Inspect(g.ctx)
			if e != nil || before.Messages != 1 || before.Pending != 1 || before.AckPending != 0 {
				t.Fatal("actual untouched broker delivery required", e, before)
			}
			original, e := deliveryWithin(g.ctx, g.f.Store, value.Tenant, value.WorkspaceID, value.NodeID, value.ID, false)
			if e != nil || original.State != "published" {
				t.Fatal("actual committed publication required", e)
			}
			audits := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")
			store := &eventWorkerFenceStore{Store: g.f.Store, failRead: c.read, code: c.code}
			g.owner.Storage = store
			defer func() { g.owner.Storage = g.f.Store }()
			e = g.s.Run(g.ctx)
			g.owner.Storage = g.f.Store
			if e == nil || e.Error() != c.code || store.reads != c.read || store.afterFailure != 0 || g.ctx.Err() != nil {
				t.Fatal("terminal stage did not fence later work", e, store.reads, store.afterFailure)
			}
			// A stale supervisor must not restart the same failed owner even
			// after the explicit read fault has been removed. No SQL or broker
			// work may follow the original terminal failure through another Run.
			g.owner.Storage = store
			if e = g.s.Run(g.ctx); e == nil || e.Error() != c.code || store.reads != c.read || store.afterFailure != 0 {
				t.Fatal("failed owner resumed without fresh admission", e, store.reads, store.afterFailure)
			}
			g.owner.Storage = g.f.Store
			after, e := g.s.Transport.Inspect(g.ctx)
			if e != nil || after != before {
				t.Fatal("terminal stage pulled or acknowledged a pending delivery", e, before, after)
			}
			retained, e := deliveryWithin(g.ctx, g.f.Store, value.Tenant, value.WorkspaceID, value.NodeID, value.ID, false)
			if e != nil || !reflect.DeepEqual(retained, original) ||
				g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events") != audits ||
				g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 0 ||
				g.count(t, "SELECT count(*) FROM task_event_dead_letters", "SELECT count(*) FROM task_event_dead_letters") != 0 {
				t.Fatal("terminal stage changed retained SQL history", e)
			}
			// Removing the explicit read fault leaves the original delivery
			// readable. Resume it only through a newly admitted lifecycle owner.
			if e = g.s.VerifyStored(g.ctx); e != nil {
				t.Fatal("original retained state failed readmission", e)
			}
			configuration := g.s.Configuration
			if e = g.s.Close(g.ctx); e != nil {
				t.Fatal("failed owner did not join", e)
			}
			bus, e := taskbus.NewJetStream(g.ctx, configuration, value.Tenant)
			if e != nil {
				t.Fatal("fresh transport admission", e)
			}
			fresh, e := NewService(g.ctx, g.owner, g.n.Workspaces, g.n, configuration, bus)
			if e != nil {
				bus.Close()
				t.Fatal("fresh owner admission", e)
			}
			g.s, g.n.TaskEvents = fresh, fresh
			g.consume(t)
			if g.count(t, "SELECT count(*) FROM task_event_receipts WHERE event_id=$1", "SELECT count(*) FROM task_event_receipts WHERE event_id=?", value.ID) != 1 ||
				g.count(t, "SELECT count(*) FROM node_tasks WHERE id=$1 AND state='pending' AND attempt=0", "SELECT count(*) FROM node_tasks WHERE id=? AND state='pending' AND attempt=0", task.ID) != 1 {
				t.Fatal("explicit recovery lost receipt or changed physical task authority")
			}
		})
	}
}

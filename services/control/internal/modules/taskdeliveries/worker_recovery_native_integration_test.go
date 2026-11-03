//go:build repository_integration && task_events_integration && task_events_native_integration

package taskdeliveries

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

func TestTaskEventsWorkerRecoveryLifetime(t *testing.T) {
	t.Run("exhausted_owner_requires_fresh_admission", func(t *testing.T) {
		g := eventSetup(t)
		task, original := g.task(t)
		g.publish(t)
		original, err := deliveryWithin(g.ctx, g.f.Store, original.Tenant, original.WorkspaceID, original.NodeID, original.ID, false)
		if err != nil || original.State != "published" || g.nativeInterrupt == nil {
			t.Fatal("real published SQL/native interruption fixture required", err)
		}
		before, err := g.s.Transport.Inspect(g.ctx)
		if err != nil || before.Pending != 1 || before.AckPending != 0 {
			t.Fatal("actual untouched broker delivery required", err, before)
		}
		audits := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")
		g.nativeInterrupt(func() {
			ctx, cancel := context.WithTimeout(g.ctx, 25*time.Second)
			defer cancel()
			if err := g.s.Run(ctx); err == nil || err.Error() != "TASK_BUS_RECOVERY_EXHAUSTED" || ctx.Err() != nil {
				t.Fatal("real offline broker did not exhaust bounded recovery", err, ctx.Err())
			}
			// This deadline is shorter than a single configured reconnect pause.
			// A stale caller must receive the retained code without another try.
			stale, stop := context.WithTimeout(g.ctx, 100*time.Millisecond)
			defer stop()
			if err := g.s.Run(stale); err == nil || err.Error() != "TASK_BUS_RECOVERY_EXHAUSTED" || stale.Err() != nil {
				t.Fatal("stale caller replenished exhausted recovery budget", err, stale.Err())
			}
		})
		configuration := g.s.Configuration
		if err = g.s.Close(g.ctx); err != nil {
			t.Fatal("failed worker did not join", err)
		}
		bus, err := taskbus.NewJetStream(g.ctx, configuration, original.Tenant)
		if err != nil {
			t.Fatal("same-store fresh transport admission", err)
		}
		fresh, err := NewService(g.ctx, g.owner, g.n.Workspaces, g.n, configuration, bus)
		if err != nil {
			bus.Close()
			t.Fatal("fresh owner admission", err)
		}
		g.s, g.n.TaskEvents = fresh, fresh
		after, err := bus.Inspect(g.ctx)
		if err != nil || after != before {
			t.Fatal("failed recovery pulled or ACKed original event", err, before, after)
		}
		retained, err := deliveryWithin(g.ctx, g.f.Store, original.Tenant, original.WorkspaceID, original.NodeID, original.ID, false)
		if err != nil || !reflect.DeepEqual(retained, original) || g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events") != audits {
			t.Fatal("failed recovery changed retained SQL intent/audit", err)
		}
		g.consume(t)
		if g.count(t, "SELECT count(*) FROM task_event_receipts WHERE event_id=$1", "SELECT count(*) FROM task_event_receipts WHERE event_id=?", original.ID) != 1 ||
			g.count(t, "SELECT count(*) FROM node_tasks WHERE id=$1 AND state='pending' AND attempt=0", "SELECT count(*) FROM node_tasks WHERE id=? AND state='pending' AND attempt=0", task.ID) != 1 {
			t.Fatal("fresh recovery duplicated receipt or changed physical task")
		}
	})
}

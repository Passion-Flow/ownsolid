//go:build repository_integration && task_events_integration

package taskdeliveries

import (
	"context"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

func (g *eventStory) reopenDeliveryOwner(t *testing.T) {
	t.Helper()
	c := g.s.Configuration
	if err := g.s.Close(g.ctx); err != nil {
		t.Fatal("join previous event owner", err)
	}
	bus, err := taskbus.NewJetStream(g.ctx, c, g.f.Tenant)
	if err != nil {
		t.Fatal("real fresh transport admission", err)
	}
	fresh, err := NewService(g.ctx, g.owner, g.n.Workspaces, g.n, c, bus)
	if err != nil {
		bus.Close()
		t.Fatal("real fresh event owner admission", err)
	}
	g.s, g.n.TaskEvents = fresh, fresh
}

func TestTaskEventsAuthorityAfterUnknownCommit(t *testing.T) {
	t.Run("retry_unknown_replay_rechecks_role_and_session", func(t *testing.T) {
		g := eventSetup(t)
		task, event := g.task(t)
		g.fence(t)
		input := RetryRequest{Generation: 1, Reason: "Review committed synthetic transport retry"}
		key := uuid.NewString()
		g.owner.Storage = &eventOutcomeStore{Store: g.f.Store, unknown: true}
		defer func() { g.owner.Storage = g.f.Store }()
		out, err := g.s.Retry(g.ctx, g.admin, g.f.Workspace, g.enrolled.NodeID, event.ID, input, key, uuid.NewString())
		g.owner.Storage = g.f.Store
		if err == nil || err.Error() != string(transaction.CommitUnknown) || out != (RetryReceipt{}) {
			t.Fatal("unknown retry exposed a success DTO", out, err)
		}
		var original RetryReceipt
		err = g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT event_id::text,retry_delivery_id::text,retry_generation,retried_at FROM task_event_dead_letters WHERE retry_actor=$1 AND retry_key=$2", "SELECT event_id,retry_delivery_id,retry_generation,retried_at FROM task_event_dead_letters WHERE retry_actor=? AND retry_key=?", g.f.User, key)).Scan(&original.EventID, &original.DeliveryID, &original.Generation, &original.RequestedAt)
		original.RequestedAt = original.RequestedAt.UTC()
		if err != nil || original.Generation != 2 || original.EventID != event.ID {
			t.Fatal("actual committed retry proof missing", err)
		}
		retained, err := deliveryWithin(g.ctx, g.f.Store, event.Tenant, event.WorkspaceID, event.NodeID, event.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		g.exec(t, "UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=0 WHERE id=?", g.f.User)
		if value, err := g.s.Retry(g.ctx, g.admin, g.f.Workspace, g.enrolled.NodeID, event.ID, input, key, uuid.NewString()); err == nil || err.Error() != "AUTH_SESSION_EXPIRED" || value != (RetryReceipt{}) {
			t.Fatal("stale Admin read back an unknown retry result", value, err)
		}
		if page, err := g.s.Quarantines(g.ctx, g.admin, url.Values{}); err == nil || err.Error() != "AUTH_SESSION_EXPIRED" || !reflect.DeepEqual(page, Page[Quarantine]{}) {
			t.Fatal("stale Admin retained installation quarantine authority", err)
		}
		if value, err := g.s.Get(g.ctx, g.portal, g.f.Workspace, g.enrolled.NodeID, event.ID); err != nil || value.Generation != 2 || value.CanRetry {
			t.Fatal("current Portal read scope changed with Admin role", err)
		}
		if value, err := g.s.Retry(g.ctx, g.portal, g.f.Workspace, g.enrolled.NodeID, event.ID, input, key, uuid.NewString()); err == nil || err.Error() != "ACCESS_DENIED" || value != (RetryReceipt{}) {
			t.Fatal("Portal replayed a retained Admin retry", err)
		}
		g.exec(t, "UPDATE users SET is_platform_admin=true WHERE id=$1", "UPDATE users SET is_platform_admin=1 WHERE id=?", g.f.User)
		g.exec(t, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1", "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='logout' WHERE id=?", g.admin.SessionID)
		if value, err := g.s.Retry(g.ctx, g.admin, g.f.Workspace, g.enrolled.NodeID, event.ID, input, key, uuid.NewString()); err == nil || err.Error() != "AUTH_SESSION_EXPIRED" || value != (RetryReceipt{}) {
			t.Fatal("revoked session replayed a committed retry", err)
		}
		current := g.session(t, "admin")
		if value, err := g.s.Retry(g.ctx, current, g.f.Workspace, g.enrolled.NodeID, event.ID, input, key, uuid.NewString()); err != nil || value != original {
			t.Fatal("fresh authorized session lost exact idempotent receipt", value, err)
		}
		after, err := deliveryWithin(g.ctx, g.f.Store, event.Tenant, event.WorkspaceID, event.NodeID, event.ID, false)
		if err != nil || !reflect.DeepEqual(after, retained) ||
			g.count(t, "SELECT count(*) FROM audit_events WHERE action='task_event.retry_requested'", "SELECT count(*) FROM audit_events WHERE action='task_event.retry_requested'") != 1 ||
			g.count(t, "SELECT count(*) FROM node_tasks WHERE id=$1 AND state='pending' AND attempt=0", "SELECT count(*) FROM node_tasks WHERE id=? AND state='pending' AND attempt=0", task.ID) != 1 {
			t.Fatal("retry readback changed event/audit or physical task", err)
		}
	})
	t.Run("terminal_duplicate_after_unknown_receipt_reopen", func(t *testing.T) {
		g := eventSetup(t)
		task, event := g.task(t)
		g.publish(t)
		g.owner.Storage = &eventOutcomeStore{Store: g.f.Store, unknown: true}
		defer func() { g.owner.Storage = g.f.Store }()
		worked, err := g.s.ConsumeNext(g.ctx)
		g.owner.Storage = g.f.Store
		if err == nil || err.Error() != string(transaction.CommitUnknown) || worked {
			t.Fatal("unknown receipt projected success", worked, err)
		}
		status, err := g.s.Transport.Inspect(g.ctx)
		if err != nil || status.AckPending != 1 {
			t.Fatal("unknown receipt sent positive ACK", status, err)
		}
		audits := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")
		g.reopenDeliveryOwner(t)
		waitEventTime(t, g.ctx, time.Now().Add(time.Duration(g.s.Configuration.RetrySeconds)*time.Second+time.Millisecond))
		g.consume(t)
		if g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events") != audits || g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 1 {
			t.Fatal("fresh owner repeated an unknown committed receipt effect")
		}
		g.completeFacts(t, task)
		physical := func() nodes.Task {
			var out nodes.Task
			err := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
				_, value, err := g.n.TaskEventHistorySourceWithin(ctx, q, event.Tenant, event.WorkspaceID, event.NodeID, event.TaskID)
				out = value
				return err
			})
			if err != nil {
				t.Fatal("actual retained physical task read", err)
			}
			return out
		}
		before := physical()
		if before.State != "succeeded" || before.Attempt != 1 {
			t.Fatal("real terminal physical task required", before.State, before.Attempt)
		}
		audits = g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")
		waitEventTime(t, g.ctx, time.Now().Add(time.Duration(g.s.Configuration.DuplicateWindowSeconds)*time.Second+time.Millisecond))
		publication, err := g.s.Transport.Publish(g.ctx, envelope(event))
		if err != nil || publication.Sequence <= 1 {
			t.Fatal("real duplicate beyond broker duplicate window required", publication, err)
		}
		g.consume(t)
		if !reflect.DeepEqual(physical(), before) ||
			g.count(t, "SELECT count(*) FROM node_tasks", "SELECT count(*) FROM node_tasks") != 1 ||
			g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 1 ||
			g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events") != audits {
			t.Fatal("terminal duplicate reopened task or repeated logical effect")
		}
		if err := g.s.VerifyStored(g.ctx); err != nil {
			t.Fatal("terminal duplicate retained integrity", err)
		}
	})
}

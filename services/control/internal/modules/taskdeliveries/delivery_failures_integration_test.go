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
)

func (g *eventStory) refuseDeliveryAudit(t *testing.T) {
	t.Helper()
	if g.f.Store.Dialect() == transaction.PostgreSQL {
		g.exec(t, `CREATE FUNCTION m25_fixture_delivery_audit_refuse() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action LIKE 'task_event.%' THEN RAISE EXCEPTION 'EXPLICIT_DELIVERY_AUDIT_FAILURE'; END IF; RETURN NEW; END $$`, "")
		g.exec(t, `CREATE TRIGGER m25_fixture_delivery_audit_refuse BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION m25_fixture_delivery_audit_refuse()`, "")
	} else {
		g.exec(t, "", `CREATE TRIGGER m25_fixture_delivery_audit_refuse BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action LIKE 'task_event.%' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='EXPLICIT_DELIVERY_AUDIT_FAILURE'; END IF; END`)
	}
}
func (g *eventStory) allowDeliveryAudit(t *testing.T) {
	t.Helper()
	g.exec(t, "DROP TRIGGER m25_fixture_delivery_audit_refuse ON audit_events", "DROP TRIGGER m25_fixture_delivery_audit_refuse")
}
func waitEventTime(t *testing.T, ctx context.Context, when time.Time) {
	t.Helper()
	remaining := time.Until(when)
	if remaining <= 0 {
		return
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-timer.C:
	}
}
func (g *eventStory) deliveryFailures(t *testing.T, leaf string, task nodes.Task, value storedDelivery) {
	t.Helper()
	switch leaf {
	case "consume_unknown_no_ack":
		g.publish(t)
		g.owner.Storage = &eventOutcomeStore{Store: g.f.Store, unknown: true}
		worked, e := g.s.ConsumeNext(g.ctx)
		g.owner.Storage = g.f.Store
		if e == nil || worked || g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 1 {
			t.Fatal("unknown SQL receipt projected success or lost confirmed proof", worked, e)
		}
		stats, e := g.s.Transport.Inspect(g.ctx)
		if e != nil || stats.AckPending != 1 {
			t.Fatal("unknown SQL outcome sent positive broker ACK", e)
		}
		before := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")
		waitEventTime(t, g.ctx, time.Now().Add(time.Duration(g.s.Configuration.RetrySeconds)*time.Second+time.Millisecond))
		g.consume(t)
		if g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events") != before || g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 1 {
			t.Fatal("redelivery repeated durable logical receipt effect")
		}
	case "consume_audit_rollback_no_ack", "application_delivery_dlq":
		g.publish(t)
		g.refuseDeliveryAudit(t)
		attempts := 1
		if leaf == "application_delivery_dlq" {
			attempts = g.s.Configuration.MaximumDeliveries
		}
		for attempt := 0; attempt < attempts; attempt++ {
			if worked, e := g.s.ConsumeNext(g.ctx); e == nil || worked {
				t.Fatal("actual audit failure ACKed or projected progress", e)
			}
			if g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 0 {
				t.Fatal("audit rollback retained partial receipt")
			}
			stats, e := g.s.Transport.Inspect(g.ctx)
			if e != nil || stats.AckPending != 1 {
				t.Fatal("audit rollback positively ACKed broker", e)
			}
			waitEventTime(t, g.ctx, time.Now().Add(time.Duration(g.s.Configuration.RetrySeconds)*time.Second+time.Millisecond))
		}
		g.allowDeliveryAudit(t)
		g.consume(t)
		if leaf == "application_delivery_dlq" {
			out, e := g.s.Get(g.ctx, g.admin, g.f.Workspace, g.enrolled.NodeID, value.ID)
			if e != nil || out.State != "dead" || out.ErrorCode == nil || *out.ErrorCode != "TASK_BUS_CONSUME_EXHAUSTED" || g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 0 || g.count(t, "SELECT count(*) FROM task_event_dead_letters", "SELECT count(*) FROM task_event_dead_letters") != 1 {
				t.Fatal("application delivery budget did not persist DLQ", e)
			}
		} else if g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 1 {
			t.Fatal("successful redelivery did not commit one receipt")
		}
		stats, e := g.s.Transport.Inspect(g.ctx)
		if e != nil || stats.AckPending != 0 {
			t.Fatal("confirmed receipt/DLQ did not receive broker ACK", e)
		}
	case "poison_quarantine_current_admin":
		unknown := envelope(value)
		unknown.Event.ID = uuid.NewString()
		if _, e := g.s.Transport.Publish(g.ctx, unknown); e != nil {
			t.Fatal(e)
		}
		g.consume(t)
		out, e := g.s.Quarantines(g.ctx, g.admin, url.Values{})
		if e != nil || len(out.Items) != 1 || out.Items[0].ErrorCode != "TASK_EVENT_UNCOMMITTED_SOURCE" {
			t.Fatal("uncommitted source was not quarantined", e)
		}
		if p, e := g.s.Quarantines(g.ctx, g.portal, url.Values{}); e == nil || !reflect.DeepEqual(p, Page[Quarantine]{}) {
			t.Fatal("Portal accessed installation quarantine", e)
		}
		if g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 0 || g.count(t, "SELECT count(*) FROM task_event_outbox", "SELECT count(*) FROM task_event_outbox") != 1 {
			t.Fatal("untrusted message created authoritative task/event proof")
		}
	case "stale_retry_does_not_block_current":
		g.fence(t)
		if _, e := g.s.Retry(g.ctx, g.admin, g.f.Workspace, g.enrolled.NodeID, value.ID, RetryRequest{Generation: 1, Reason: "Review synthetic stale-generation recovery"}, uuid.NewString(), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if _, e := g.s.Transport.Publish(g.ctx, envelope(value)); e != nil {
			t.Fatal(e)
		}
		g.consume(t)
		if g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 0 {
			t.Fatal("stale generation blocked valid logical receipt")
		}
		g.publish(t)
		g.consume(t)
		if e := g.s.VerifyStored(g.ctx); e != nil {
			t.Fatal("stale/current delivery integrity", e)
		}
		var generation int
		if e := g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT generation FROM task_event_receipts WHERE event_id=$1", "SELECT generation FROM task_event_receipts WHERE event_id=?", value.ID)).Scan(&generation); e != nil || generation != 2 {
			t.Fatal("current retry generation did not receive logical receipt", e)
		}
	case "published_expiry_retained_dlq", "inactive_workspace_expiry":
		g.publish(t)
		if leaf == "inactive_workspace_expiry" {
			g.exec(t, "UPDATE workspaces SET status='disabled' WHERE tenant_id=$1 AND id=$2", "UPDATE workspaces SET status='disabled' WHERE tenant_id=? AND id=?", g.f.Tenant, g.f.Workspace)
		}
		waitEventTime(t, g.ctx, value.ExpiresAt.Add(time.Millisecond))
		if worked, e := g.s.ExpireNext(g.ctx); e != nil || !worked {
			t.Fatal("actual expiry/readback failed", e)
		}
		if g.count(t, "SELECT count(*) FROM task_event_dead_letters WHERE event_id=$1 AND kind='expiry'", "SELECT count(*) FROM task_event_dead_letters WHERE event_id=? AND kind='expiry'", value.ID) != 1 {
			t.Fatal("expired publication lacks retained DLQ")
		}
		if leaf == "inactive_workspace_expiry" {
			if out, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, g.enrolled.NodeID, value.ID); e == nil || !reflect.DeepEqual(out, Delivery{}) {
				t.Fatal("retention facts became inactive human read authority", e)
			}
		}
	case "resolved_terminal_history_retention":
		g.publish(t)
		g.consume(t)
		g.completeFacts(t, task)
		out, e := g.s.Get(g.ctx, g.admin, g.f.Workspace, g.enrolled.NodeID, value.ID)
		if e != nil {
			t.Fatal(e)
		}
		until := out.UpdatedAt.Add(time.Duration(g.s.Configuration.HistoryRetentionSeconds) * time.Second)
		brokerUntil := out.ExpiresAt.Add(time.Duration(g.s.Configuration.StreamRetentionSeconds+g.s.Configuration.AckWaitSeconds) * time.Second)
		if brokerUntil.After(until) {
			until = brokerUntil
		}
		waitEventTime(t, g.ctx, until.Add(time.Millisecond))
		if removed, e := g.s.Cleanup(g.ctx); e != nil || removed != 1 {
			t.Fatal("actual configured proof retention failed", removed, e)
		}
		if g.count(t, "SELECT count(*) FROM task_event_outbox", "SELECT count(*) FROM task_event_outbox") != 0 || g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 0 || g.count(t, "SELECT count(*) FROM node_tasks WHERE id=$1 AND state='succeeded'", "SELECT count(*) FROM node_tasks WHERE id=? AND state='succeeded'", task.ID) != 1 {
			t.Fatal("cleanup removed physical task or retained partial transport proof")
		}
	}
}

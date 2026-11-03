//go:build cgo && odbc_manager && repository_integration && dameng_repository_integration && task_events_native_integration

package taskdeliveries

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/taskbusfixture"
)

func TestActualDamengTaskDeliveryRecovery(t *testing.T) {
	g := newDamengStory(t)
	policy := taskbus.Configuration{ConnectSeconds: 1, RequestSeconds: 2, PullSeconds: 1, AckWaitSeconds: 8, WorkerPollSeconds: 1, RetrySeconds: 1, ReconnectSeconds: 1, MaximumReconnectAttempts: 3, MaximumPublishAttempts: 3, MaximumDeliveries: 3, MaximumAckPending: 2, StreamMaximumMessages: 100, StreamMaximumBytes: 1 << 20, StreamRetentionSeconds: 60, DuplicateWindowSeconds: 2, RetryLifetimeSeconds: 30, HistoryRetentionSeconds: 90, CleanupBatch: 8, ShutdownSeconds: 4}
	broker := taskbusfixture.NewForTenant(t, policy, g.tenant)
	c := broker.Profiles[0].Configuration
	bus, e := taskbus.NewJetStream(g.ctx, c, g.tenant)
	if e != nil { t.Fatal("actual native NATS required", e) }
	w := &workspaces.Service{Identity: g.owner}
	n := &nodes.Service{Identity: g.owner, Workspaces: w}
	s, e := NewService(g.ctx, g.owner, w, n, c, bus)
	if e != nil { bus.Close(); t.Fatal("actual native full schema required", e) }
	n.TaskEvents = s
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.ShutdownSeconds)*time.Second); defer cancel()
		if e := s.Close(ctx); e != nil { t.Error(e) }
	})
	join, e := n.MintForProfile(g.ctx, g.admin, g.workspace, "Synthetic native event node", wire.ReadOnlyProfile, uuid.NewString())
	if e != nil { t.Fatal(e) }
	facts := wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-native-event-node", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
	enrolled, e := n.Enroll(g.ctx, wire.Enrollment{JoinToken: join.Token, Handshake: wire.Handshake{ProtocolVersion: wire.Version, ExecutionProfile: wire.ReadOnlyProfile, Capabilities: wire.Capabilities(wire.ReadOnlyProfile), Facts: facts}}, uuid.NewString())
	if e != nil { t.Fatal(e) }
	task, e := n.CreateTask(g.ctx, g.admin, enrolled.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil { t.Fatal(e) }
	value, e := scanDelivery(g.store.QueryRow(g.ctx, transaction.SQL(transaction.Dameng, "SELECT "+dmFields+" FROM task_event_outbox WHERE task_id=?", task.ID)))
	if e != nil || value.State != "prepared" { t.Fatal("same-transaction native intent", e) }
	// Real commit, scripted lost acknowledgement plus concurrent cancellation.
	interrupted, cancel := context.WithCancel(g.ctx)
	drop := &damengDropCommit{Store: g.store, cancel: cancel}
	g.owner.Storage = drop
	worked, e := s.ProcessNext(interrupted)
	g.owner.Storage = g.store
	cancel()
	if worked || e == nil || e.Error() != string(transaction.CommitUnknown) || !drop.dropped || drop.after != 0 { t.Fatal("unknown claim replayed or reported success", e) }
	if g.count(t, "SELECT count(*) FROM task_event_outbox WHERE id=? AND state='sent' AND outcome_unknown=1", value.ID) != 1 { t.Fatal("original native claim was not retained") }
	stats, e := bus.Inspect(g.ctx)
	if e != nil || stats.Messages != 0 { t.Fatal("uncertain claim published before readback", e) }
	// Explicit operator/test readback above precedes retry of the same immutable
	// transport identity. There is no physical node-task replay.
	g.exec(t, "UPDATE task_event_outbox SET claim_expires_at=DATEADD(SECOND,-1,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) WHERE id=? AND state='sent'", value.ID)
	if ok, e := s.ProcessNext(g.ctx); e != nil || !ok { t.Fatal("native publish recovery", e) }
	broker.Restart()
	if e := bus.Reconnect(g.ctx); e != nil { t.Fatal("actual broker restart", e) }
	if ok, e := s.ConsumeNext(g.ctx); e != nil || !ok { t.Fatal("native receipt and ACK", e) }
	if g.count(t, "SELECT count(*) FROM task_event_receipts WHERE event_id=?", value.ID) != 1 || g.count(t, "SELECT count(*) FROM node_tasks WHERE id=? AND state='pending' AND attempt=0", task.ID) != 1 { t.Fatal("transport duplicated receipt or executed physical task") }
	if _, e := s.Get(g.ctx, g.admin, uuid.NewString(), enrolled.NodeID, value.ID); e == nil { t.Fatal("foreign workspace accepted") }
	claim, e := n.Claim(g.ctx, enrolled.NodeID, enrolled.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil { t.Fatal(e) }
	if state, e := n.Complete(g.ctx, enrolled.NodeID, enrolled.Credential, task.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Facts: &facts}, uuid.NewString()); e != nil || state != "succeeded" { t.Fatal(e) }
	next, e := n.CreateTask(g.ctx, g.admin, enrolled.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil { t.Fatal(e) }
	nextValue, e := scanDelivery(g.store.QueryRow(g.ctx, transaction.SQL(transaction.Dameng, "SELECT "+dmFields+" FROM task_event_outbox WHERE task_id=?", next.ID)))
	if e != nil { t.Fatal(e) }
	if e = g.store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error { return FenceAfterRestoreWithin(ctx, q, n, g.tenant) }); e != nil { t.Fatal(e) }
	if g.count(t, "SELECT count(*) FROM task_event_outbox WHERE id=? AND state='dead' AND outcome_unknown=0", nextValue.ID) != 1 { t.Fatal("restore fence failed") }
	key := uuid.NewString()
	retry, e := s.Retry(g.ctx, g.admin, g.workspace, enrolled.NodeID, nextValue.ID, RetryRequest{Generation: 1, Reason: "Synthetic native recovery"}, key, uuid.NewString())
	if e != nil || retry.Generation != 2 { t.Fatal(e) }
	again, e := s.Retry(g.ctx, g.admin, g.workspace, enrolled.NodeID, nextValue.ID, RetryRequest{Generation: 1, Reason: "Synthetic native recovery"}, key, uuid.NewString())
	if e != nil || again.DeliveryID != retry.DeliveryID { t.Fatal("retry intent duplicated", e) }
	page, e := s.List(g.ctx, g.admin, g.workspace, enrolled.NodeID, url.Values{"limit": {"1"}})
	if e != nil || len(page.Items) != 1 || page.NextCursor == nil { t.Fatal("native bounded history", e) }
	second, e := s.List(g.ctx, g.admin, g.workspace, enrolled.NodeID, url.Values{"limit": {"1"}, "cursor": {*page.NextCursor}})
	if e != nil || len(second.Items) != 1 || second.Items[0].ID == page.Items[0].ID { t.Fatal("native cursor identity", e) }
	if e := s.VerifyStored(g.ctx); e != nil { t.Fatal("native retained state integrity", e) }
	// A rejected immutable mutation aborts its transaction. Readback uses a fresh
	// native connection; no SQL follows the failed statement in that callback.
	e = g.store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error { _, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, "UPDATE task_event_receipts SET outcome='obsolete' WHERE event_id=?", value.ID)); return e })
	if e == nil || g.count(t, "SELECT count(*) FROM task_event_receipts WHERE event_id=? AND outcome='received'", value.ID) != 1 { t.Fatal("immutable native receipt guard failed") }
	g.exec(t, "UPDATE users SET status='disabled' WHERE id=?", g.user)
	if out, e := s.Get(g.ctx, g.admin, g.workspace, enrolled.NodeID, value.ID); e == nil || out.ID != "" { t.Fatal("revoked current actor retained history access") }
}

//go:build repository_integration

package components_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestActualPairedComponentDisabledRecovery(t *testing.T) {
	g := pairedComponentSetup(t)
	g.installed(t)
	task := g.request(t, "restart")
	claim := g.claim(t, task)
	current, e := g.s.Nodes.Get(g.ctx, g.admin, "", g.node.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	disabled, e := g.s.Nodes.Update(g.ctx, g.admin, current.ID, current.Name, "disabled", current.Version, "Explicit interrupted maintenance fence", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}

	// This is a synthetic physical rollback observation, delivered through the
	// actual selected heartbeat and component owner, with no direct SQL repair.
	restored := wire.DefaultComponentConfig()
	desired := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	o := maintenanceObservation("1.12.0", &restored)
	o.MaintenanceError, o.MaintenanceCause = "COMPONENT_UPDATE_ROLLED_BACK", "COMPONENT_INTERRUPTED"
	h := g.handshake
	h.Component = &o
	disposition, e := g.s.Nodes.Heartbeat(g.ctx, g.node.NodeID, g.node.Credential, h)
	if e != nil || disposition.Status != "disabled" {
		t.Fatal("disabled node could not report its physical recovery", e)
	}
	view, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, g.node.NodeID)
	if e != nil || !view.Stale || view.ManagementAvailable || view.MaintenanceAvailable ||
		view.AppliedConfig == nil || *view.AppliedConfig != restored || view.Observation == nil ||
		view.DesiredConfig == nil || *view.DesiredConfig != desired ||
		view.Observation.MaintenanceCause != "COMPONENT_INTERRUPTED" || view.LastTaskID == nil || *view.LastTaskID != task.ID {
		t.Fatal("disabled recovery lost actual observation, desired intent, task history or stale marker", e)
	}
	if out, e := g.s.Nodes.Claim(g.ctx, g.node.NodeID, g.node.Credential, uuid.NewString()); e == nil || !reflect.DeepEqual(out, wire.Claim{}) {
		t.Fatal("passive recovery granted a new task", e)
	}
	if out, e := g.s.Nodes.Renew(g.ctx, g.node.NodeID, g.node.Credential, task.ID, claim.Lease.LeaseToken); e == nil || !reflect.DeepEqual(out, wire.RenewedLease{}) {
		t.Fatal("disabled node renewed its cancelled lease", e)
	}
	result := wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o}
	if out, e := g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, task.ID, result, uuid.NewString()); e == nil || out != "" {
		t.Fatal("passive recovery settled cancelled maintenance", e)
	}
	e = g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		return g.s.ObserveWithin(ctx, q, current, o, task.ID)
	})
	pairedComponentCode(t, e, "AGENT_PROFILE_REJECTED")
	if out, e := g.s.Request(g.ctx, g.admin, g.node.NodeID, "restart", uuid.NewString(), "Disabled task refused", uuid.NewString()); e == nil || !reflect.DeepEqual(out, nodes.Task{}) {
		t.Fatal("disabled observation enabled a new maintenance request", e)
	}

	active, e := g.s.Nodes.Update(g.ctx, g.admin, disabled.ID, disabled.Name, "active", disabled.Version, "Explicit enable after recovery", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if disposition, e = g.s.Nodes.Heartbeat(g.ctx, g.node.NodeID, g.node.Credential, h); e != nil || disposition.Status != "active" {
		t.Fatal("reenabled node did not retain its original identity", e)
	}
	view, e = g.s.Get(g.ctx, g.portal, g.f.Workspace, g.node.NodeID)
	if e != nil || view.Stale || view.AppliedConfig == nil || *view.AppliedConfig != restored {
		t.Fatal("reenabled observation did not refresh recovered state", e)
	}
	fresh := g.request(t, "observe")
	freshClaim := g.claim(t, fresh)
	result.LeaseToken = freshClaim.Lease.LeaseToken
	if out, e := g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, fresh.ID, result, uuid.NewString()); e != nil || out != "succeeded" {
		t.Fatal("fresh fenced observation did not settle", e)
	}
	before := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")
	if out, e := g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, fresh.ID, result, uuid.NewString()); e != nil || out != "succeeded" {
		t.Fatal("exact terminal result did not reconcile idempotently", e)
	}
	if g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events") != before {
		t.Fatal("result replay duplicated its audit")
	}
	if _, e = g.s.Nodes.Update(g.ctx, g.admin, active.ID, active.Name, "removed", active.Version, "Explicit retired identity", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if out, e := g.s.Nodes.Heartbeat(g.ctx, g.node.NodeID, g.node.Credential, h); e == nil || !reflect.DeepEqual(out, wire.Disposition{}) {
		t.Fatal("removed credential published a recovery observation", e)
	}
}

func TestActualPairedDisabledComponentHeartbeatUnknownCommit(t *testing.T) {
	g := pairedComponentSetup(t)
	g.installed(t)
	n, e := g.s.Nodes.Get(g.ctx, g.admin, "", g.node.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = g.s.Nodes.Update(g.ctx, g.admin, n.ID, n.Name, "disabled", n.Version, "Unknown heartbeat fixture fence", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	restored := wire.DefaultComponentConfig()
	o := maintenanceObservation("1.12.0", &restored)
	h := g.handshake
	h.Component = &o
	// Explicit post-commit outcome double; actual wire-loss coverage belongs to
	// the node removal tests. The underlying SQL/observer transaction is real.
	hook := &pairedComponentOutcomeStore{Store: g.f.Store, at: 1}
	s := g.wrapped(hook)
	out, e := s.Nodes.Heartbeat(g.ctx, g.node.NodeID, g.node.Credential, h)
	pairedComponentCode(t, e, string(transaction.CommitUnknown))
	if !reflect.DeepEqual(out, wire.Disposition{}) || hook.calls != 1 {
		t.Fatal("unknown heartbeat exposed a disposition or retried")
	}
	view, e := g.s.Get(g.ctx, g.admin, "", g.node.NodeID)
	if e != nil || !view.Stale || view.AppliedConfig == nil || *view.AppliedConfig != restored {
		t.Fatal("independent observation readback failed", e)
	}
}

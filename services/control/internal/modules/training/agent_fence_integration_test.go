package training_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/google/uuid"
)

// fencedNode enrolls a node in a Workspace of its own, which can be disabled, unlike the
// default Workspace of the scenario.
func fencedNode(t *testing.T, f scenario) (workspaces.Workspace, string, string, agentwire.Handshake) {
	t.Helper()
	ctx := context.Background()
	w, e := f.s.Workspaces.Create(ctx, f.admin, "Fenced workspace", f.admin.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	join, e := f.s.Nodes.MintForProfile(ctx, f.admin, w.ID, "Fenced node", agentwire.DockerAIProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var facts agentwire.Facts
	var stored []byte
	if e = f.s.DB.QueryRow(ctx, "SELECT facts FROM managed_nodes WHERE id=$1", f.input.NodeID).Scan(&stored); e != nil || json.Unmarshal(stored, &facts) != nil {
		t.Fatal("stored facts", e)
	}
	handshake := agentwire.Handshake{ProtocolVersion: agentwire.AIVersion, ExecutionProfile: agentwire.DockerAIProfile, Capabilities: agentwire.Capabilities(agentwire.DockerAIProfile), Facts: facts}
	enrolled, e := f.s.Nodes.Enroll(ctx, agentwire.Enrollment{JoinToken: join.Token, Handshake: handshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return w, enrolled.NodeID, enrolled.Credential, handshake
}

// An agent call reads the Workspace status under its node lock, so a status change must
// wait for a call in flight, and the next call must see the change (ADR 0020).
func TestWorkspaceStatusChangeWaitsForAnAgentCallInFlight(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	w, node, credential, handshake := fencedNode(t, f)
	if d, e := f.s.Nodes.Heartbeat(ctx, node, credential, handshake); e != nil || d.Status != "active" {
		t.Fatal(d.Status, e)
	}
	inFlight, e := f.s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer inFlight.Rollback(ctx)
	if _, e = inFlight.Exec(ctx, "SELECT 1 FROM managed_nodes WHERE id=$1 FOR UPDATE", node); e != nil {
		t.Fatal(e)
	}
	changed := make(chan error, 1)
	go func() {
		_, e := f.s.Workspaces.Update(ctx, f.admin, w.ID, w.Name, "disabled", w.Version, "Fence the node", uuid.NewString())
		changed <- e
	}()
	select {
	case e = <-changed:
		t.Fatal("the status change did not wait for the agent call", e)
	case <-time.After(300 * time.Millisecond):
	}
	if e = inFlight.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	if e = <-changed; e != nil {
		t.Fatal(e)
	}
	if d, e := f.s.Nodes.Heartbeat(ctx, node, credential, handshake); e != nil || d.Status != "disabled" {
		t.Fatal(d.Status, e)
	}
}

// Foreign key checks take a key share on the Workspace row with every audit event, so a
// Workspace writer must not conflict with them: a steady stream of key shares would starve
// it, and it would deadlock with a call that already holds its node.
func TestWorkspaceWritersDoNotWaitForForeignKeyChecks(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	w, _, _, _ := fencedNode(t, f)
	check, e := f.s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer check.Rollback(ctx)
	if _, e = check.Exec(ctx, "SELECT 1 FROM workspaces WHERE id=$1 FOR KEY SHARE", w.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Workspaces.Update(ctx, f.admin, w.ID, w.Name, "disabled", w.Version, "Writers pass key shares", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
}

func TestLiveNodeCountFollowsNodeWrites(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	tenant := f.admin.Me.Workspace.TenantID
	expect := func(want int) {
		t.Helper()
		var counted, stored int
		if e := f.s.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM managed_nodes WHERE tenant_id=$1 AND status<>'removed'),(SELECT live_nodes FROM tenant_node_counts WHERE tenant_id=$1)", tenant).Scan(&counted, &stored); e != nil {
			t.Fatal(e)
		}
		if counted != want || stored != want {
			t.Fatalf("counted %d stored %d want %d", counted, stored, want)
		}
	}
	expect(1)
	_, node, _, _ := fencedNode(t, f)
	expect(2)
	for _, step := range []struct {
		sql  string
		live int
	}{
		{"UPDATE managed_nodes SET status='disabled' WHERE id=$1", 2},
		{"UPDATE managed_nodes SET status='removed',credential_hash=NULL WHERE id=$1", 1},
	} {
		if _, e := f.s.DB.Exec(ctx, step.sql, node); e != nil {
			t.Fatal(e)
		}
		expect(step.live)
	}
	if _, e := f.s.DB.Exec(ctx, "UPDATE managed_nodes SET name='Renamed' WHERE id=$1", f.input.NodeID); e != nil {
		t.Fatal(e)
	}
	expect(1)
}

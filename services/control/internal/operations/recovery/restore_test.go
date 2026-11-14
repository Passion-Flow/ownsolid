package recovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM03RestoreInvalidatesAllAuthorityAtomically(t *testing.T) {
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	ctx := context.Background()
	admin := fixture.Login(t, accounts, "admin@example.invalid", password, "admin")
	s := &nodes.Service{DB: accounts.DB, Identity: accounts, Workspaces: &workspaces.Service{DB: accounts.DB, Identity: accounts}}
	h := wire.Handshake{ProtocolVersion: wire.Version, Capabilities: []string{wire.CollectFacts}, Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-restore-node", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 1024, DiskAvailableBytes: 512, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	token, e := s.Mint(ctx, admin, admin.Me.Workspace.ID, "Restore node", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	node, e := s.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	pending, e := s.Mint(ctx, admin, admin.Me.Workspace.ID, "Pending node", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	task, e := s.CreateTask(ctx, admin, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = accounts.DB.Exec(ctx, `CREATE FUNCTION reject_restore_node() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic node restore failure'; END$$;CREATE TRIGGER reject_restore_node BEFORE UPDATE ON managed_nodes FOR EACH ROW EXECUTE FUNCTION reject_restore_node()`); e != nil {
		t.Fatal(e)
	}
	if e = recovery.InvalidateRestored(ctx, accounts, "Synthetic restore failure"); e == nil {
		t.Fatal("partial restore invalidation reported success")
	}
	if _, e = accounts.UpdateMe(ctx, admin, admin.Me.User.DisplayName, "en", admin.Me.User.Version, uuid.NewString()); e != nil {
		t.Fatal("account authority changed before node rollback", e)
	}
	if _, e = accounts.DB.Exec(ctx, "DROP TRIGGER reject_restore_node ON managed_nodes"); e != nil {
		t.Fatal(e)
	}
	if e = recovery.InvalidateRestored(ctx, accounts, "Synthetic isolated restore"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Heartbeat(ctx, node.NodeID, node.Credential, h); e == nil || e.Error() != "AGENT_UNAUTHORIZED" {
		t.Fatal("restored node authority survived", e)
	}
	if _, e = s.Enroll(ctx, wire.Enrollment{JoinToken: pending.Token, Handshake: h}, uuid.NewString()); e == nil || e.Error() != "TOKEN_UNUSABLE" {
		t.Fatal("restored join token survived", e)
	}
	var state string
	if e = accounts.DB.QueryRow(ctx, "SELECT state FROM node_tasks WHERE id=$1", task.ID).Scan(&state); e != nil || state != "cancelled" {
		t.Fatal("restored job stayed executable", e)
	}
	fixture.Login(t, accounts, "admin@example.invalid", password, "admin")
}

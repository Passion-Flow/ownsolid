package databaseaccess

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
	"time"
)

func TestM04RuntimeUpgradePreservesRoleAndResumesGrants(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "resume-after-schema"}[interrupted], func(t *testing.T) {
			f := newActualFixture(t, "nodes")
			ctx := t.Context()
			if status, e := Prepare(ctx, f.service.DB, f.lease, f.input); e != nil || status != Verified {
				t.Fatal(status, e)
			}
			password := identityfixture.Bootstrap(t, f.service)
			var oid uint32
			var verifier string
			if e := f.lease.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", f.role).Scan(&oid, &verifier); e != nil {
				t.Fatal(e)
			}
			profile := "components"
			f.input.configuration.Server.ApplicationProfile = &profile
			if interrupted {
				if e := database.Migrate(ctx, f.lease, "components"); e != nil {
					t.Fatal(e)
				}
				if _, e := Check(ctx, f.service.DB, f.lease, f.input); e == nil {
					t.Fatal("missing grants accepted")
				}
			}
			for i := 0; i < 2; i++ {
				if status, e := UpgradeComponents(ctx, f.service.DB, f.lease, f.input); e != nil || status != ComponentsUpgradeComplete {
					t.Fatal(status, e)
				}
			}
			var after uint32
			var hash string
			if e := f.lease.QueryRow(ctx, "SELECT oid,rolpassword FROM pg_authid WHERE rolname=$1", f.role).Scan(&after, &hash); e != nil || after != oid || hash != verifier {
				t.Fatal("runtime principal changed", e)
			}
			runtime, e := pgxpool.NewWithConfig(ctx, f.input.connection.Copy())
			if e != nil {
				t.Fatal(e)
			}
			defer runtime.Close()
			clone := *f.service
			clone.DB = runtime
			clone.Config.Server.ApplicationProfile = &profile
			a := identityfixture.Login(t, &clone, "admin@example.invalid", password, "portal")
			w := workspaces.Service{DB: runtime, Identity: &clone}
			if _, e = w.CreateNote(ctx, a, a.Me.Workspace.ID, "Upgraded runtime note", "synthetic", uuid.NewString()); e != nil {
				t.Fatal(e)
			}
			admin := identityfixture.Login(t, &clone, "admin@example.invalid", password, "admin")
			n := nodes.Service{DB: runtime, Identity: &clone, Workspaces: &w}
			if _, e = n.Mint(ctx, admin, a.Me.Workspace.ID, "Restricted node", uuid.NewString()); e != nil {
				t.Fatal(e)
			}

			component := &components.Service{Nodes: &n}
			n.Components = component
			token, e := n.MintForProfile(ctx, admin, a.Me.Workspace.ID, "Restricted management", wire.SystemdComponentsProfile, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			facts := wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-runtime", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 2048, DiskAvailableBytes: 1024, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
			node, e := n.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: wire.Handshake{ProtocolVersion: wire.ManagementVersion, ExecutionProfile: wire.SystemdComponentsProfile, Capabilities: wire.Capabilities(wire.SystemdComponentsProfile), Facts: facts}}, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = component.Request(ctx, admin, node.NodeID, "start", uuid.NewString(), "Synthetic restricted DML", uuid.NewString()); e != nil {
				t.Fatal("runtime component write", e)
			}
			if _, e = component.Get(ctx, a, a.Me.Workspace.ID, node.NodeID); e != nil {
				t.Fatal("runtime component read", e)
			}

			for _, sql := range []string{"CREATE TABLE forbidden_m02(id integer)", "DELETE FROM schema_migrations"} {
				if _, e = runtime.Exec(ctx, sql); e == nil {
					t.Fatal("runtime DDL/ledger mutation allowed")
				}
			}
		})
	}
}
func TestM04FreshRuntimeGrants(t *testing.T) {
	f := newActualFixture(t, "components")
	if status, e := Prepare(t.Context(), f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	if status, e := Check(t.Context(), f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
}

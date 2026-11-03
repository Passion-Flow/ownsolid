package databaseaccess

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

func TestM03RuntimeUpgradePreservesRoleAndResumesGrants(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "resume-after-schema"}[interrupted], func(t *testing.T) {
			f := newActualFixture(t, "workspaces")
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
			profile := "nodes"
			f.input.configuration.Server.ApplicationProfile = &profile
			if interrupted {
				if e := database.Migrate(ctx, f.lease, "nodes"); e != nil {
					t.Fatal(e)
				}
				if _, e := Check(ctx, f.service.DB, f.lease, f.input); e == nil {
					t.Fatal("missing grants accepted")
				}
			}
			for i := 0; i < 2; i++ {
				if status, e := UpgradeNodes(ctx, f.service.DB, f.lease, f.input); e != nil || status != NodesUpgradeComplete {
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
			for _, sql := range []string{"CREATE TABLE forbidden_m02(id integer)", "DELETE FROM schema_migrations"} {
				if _, e = runtime.Exec(ctx, sql); e == nil {
					t.Fatal("runtime DDL/ledger mutation allowed")
				}
			}
		})
	}
}
func TestM03FreshRuntimeGrants(t *testing.T) {
	f := newActualFixture(t, "nodes")
	if status, e := Prepare(t.Context(), f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	if status, e := Check(t.Context(), f.service.DB, f.lease, f.input); e != nil || status != Verified {
		t.Fatal(status, e)
	}
}

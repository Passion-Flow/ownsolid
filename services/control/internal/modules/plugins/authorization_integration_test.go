//go:build plugin_database_integration

package plugins

import (
	"context"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM14ActualCurrentViewerForeignWorkspaceMembershipAndDisabledGuards(t *testing.T) {
	s, admin, _, private := pluginFixture(t)
	ctx := context.Background()
	account := s.Identity
	input := pluginInput(t, private, "1.0.0")
	version, e := s.Import(ctx, admin, input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	version, e = s.Install(ctx, admin, version.ID, RevisionReason{ExpectedRevision: version.Revision, Reason: "Actual scope fixture install"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	email := "plugin-scope@example.invalid"
	password := fixture.Password()
	invitation, e := account.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = account.Register(ctx, fixture.InvitationToken(t, account, invitation.ID), "Synthetic plugin scope user", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	actor := fixture.Login(t, account, email, password, "portal")
	owned, e := s.Workspaces.Create(ctx, admin, "Plugin caller-owned scope", actor.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	foreign, e := s.Workspaces.Create(ctx, admin, "Plugin foreign scope", admin.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	grant, e := s.Grant(ctx, admin, owned.ID, GrantRequest{PluginVersionID: version.ID, Reason: "Exact caller scope grant"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	refs, e := s.References(ctx, admin, version.ID)
	if e != nil {
		t.Fatal(e)
	}
	checks := map[string]func() error{
		"configuration": func() error { _, e := s.Configuration(ctx, actor); return e },
		"list":          func() error { _, e := s.List(ctx, actor, url.Values{}); return e },
		"get":           func() error { _, e := s.Get(ctx, actor, version.ID); return e },
		"import":        func() error { _, e := s.Import(ctx, actor, input, uuid.NewString(), uuid.NewString()); return e },
		"install": func() error {
			_, e := s.Install(ctx, actor, version.ID, RevisionReason{ExpectedRevision: version.Revision, Reason: "Denied management"}, uuid.NewString(), uuid.NewString())
			return e
		},
		"references": func() error { _, e := s.References(ctx, actor, version.ID); return e },
		"uninstall": func() error {
			_, e := s.Uninstall(ctx, actor, version.ID, UninstallRequest{ExpectedRevision: version.Revision, ReferencesSHA256: refs.SHA256, Reason: "Denied management"}, uuid.NewString(), uuid.NewString())
			return e
		},
		"grant": func() error {
			_, e := s.Grant(ctx, actor, owned.ID, GrantRequest{PluginVersionID: version.ID, Reason: "Denied management"}, uuid.NewString(), uuid.NewString())
			return e
		},
		"update_grant": func() error {
			_, e := s.UpdateGrant(ctx, actor, owned.ID, grant.ID, GrantUpdate{Enabled: false, ExpectedRevision: grant.Revision, Reason: "Denied management"}, uuid.NewString(), uuid.NewString())
			return e
		},
	}
	for name, check := range checks {
		t.Run("portal_cannot_manage/"+name, func(t *testing.T) { expectCode(t, check(), "ACCESS_DENIED") })
	}
	workspace := owned.ID
	invoke := func(a identity.Auth) error {
		return modelTransaction(t, s, a, func(tx pgx.Tx) error {
			_, e := s.ExecuteModelListInTransaction(ctx, tx, a, workspace, version.ID, uuid.NewString(), "Actual authorized model directory")
			return e
		})
	}
	if e = invoke(actor); e != nil {
		t.Fatal("current ordinary caller", e)
	}
	if e = invoke(admin); e == nil {
		t.Fatal("platform metadata admin delegated member authority")
	}
	if _, e = s.Workspaces.AddMember(ctx, admin, owned.ID, "admin@example.invalid", "owner", uuid.NewString()); e != nil {
		t.Fatal("second owner required before viewer transition", e)
	}
	var memberRevision int64
	if e = account.DB.QueryRow(ctx, "SELECT version FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", workspace, actor.Me.User.ID).Scan(&memberRevision); e != nil {
		t.Fatal(e)
	}
	viewer, e := s.Workspaces.ChangeMember(ctx, admin, workspace, actor.Me.User.ID, "viewer", memberRevision, "Actual current viewer", uuid.NewString(), false)
	if e != nil {
		t.Fatal(e)
	}
	if e = invoke(actor); e != nil {
		t.Fatal("current viewer ordinary read-only capability", e)
	}
	if _, e = s.Grants(ctx, actor, workspace, url.Values{}); e != nil {
		t.Fatal("viewer exact available grants", e)
	}
	deny := func(phase string) {
		t.Helper()
		var before, after int
		count := `SELECT (SELECT count(*) FROM audit_events)+(SELECT count(*) FROM plugin_versions)+(SELECT count(*) FROM plugin_grants)+(SELECT count(*) FROM plugin_mutation_intents)`
		if e = account.DB.QueryRow(ctx, count).Scan(&before); e != nil {
			t.Fatal(e)
		}
		t.Run(phase+"/grants", func(t *testing.T) {
			if _, e := s.Grants(ctx, actor, workspace, url.Values{}); e == nil {
				t.Fatal("unauthorized grant discovery")
			}
		})
		t.Run(phase+"/invocation", func(t *testing.T) {
			if e := invoke(actor); e == nil {
				t.Fatal("unauthorized invocation")
			}
		})
		if e = account.DB.QueryRow(ctx, count).Scan(&after); e != nil || before != after {
			t.Fatal("denied scope changed provenance or audit", e, before, after)
		}
	}
	workspace = foreign.ID
	deny("foreign_workspace")
	workspace = owned.ID
	if _, e = s.Workspaces.ChangeMember(ctx, admin, workspace, actor.Me.User.ID, "", viewer.Version, "Actual member removal", uuid.NewString(), true); e != nil {
		t.Fatal(e)
	}
	deny("removed_member")
	if _, e = s.Workspaces.AddMember(ctx, admin, workspace, email, "viewer", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	current, e := s.Workspaces.Get(ctx, admin, workspace, true)
	if e != nil {
		t.Fatal(e)
	}
	current, e = s.Workspaces.Update(ctx, admin, workspace, current.Name, "disabled", current.Version, "Actual disabled scope", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	deny("disabled_workspace")
	if _, e = s.Workspaces.Update(ctx, admin, workspace, current.Name, "active", current.Version, "Restore exact fixture scope", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	// This operator-only fixture exercises corruption/revocation rechecks. Normal
	// account administration remains covered by the actual M01 account journey.
	if _, e = account.DB.Exec(ctx, "UPDATE users SET status='disabled' WHERE id=$1", actor.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	deny("disabled_identity")
}

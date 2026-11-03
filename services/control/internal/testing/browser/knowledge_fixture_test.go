package browser_test

import (
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

// All actors are ordinary synthetic users admitted by the real identity/Workspace
// service. No browser response or authentication is replaced by a frontend mock.
func configureKnowledgeBrowserActors(t *testing.T, s *identity.Service, password, memberPassword string, manifest map[string]any) {
	t.Helper()
	ctx := t.Context()
	admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	viewer := fixture.Login(t, s, "alice@example.invalid", memberPassword, "portal")
	member := fixture.Login(t, s, "bob@example.invalid", memberPassword, "portal")
	w := &workspaces.Service{DB: s.DB, Identity: s}
	if _, e := w.ChangeMember(ctx, admin, admin.Me.Workspace.ID, viewer.Me.User.ID, "viewer", 1, "Synthetic current knowledge viewer", uuid.NewString(), false); e != nil {
		t.Fatal(e)
	}
	foreign, e := w.Create(ctx, admin, "Private foreign knowledge scope", admin.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	manifest["workspace_id"], manifest["foreign_workspace"] = admin.Me.Workspace.ID, foreign.ID
	manifest["viewer_email"], manifest["viewer_password"] = viewer.Me.User.Email, memberPassword
	manifest["member_email"], manifest["member_password"], manifest["member_id"] = member.Me.User.Email, memberPassword, member.Me.User.ID
}

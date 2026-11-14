package browser_test

import (
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

func configureDatasetBrowserActors(t *testing.T, s *identity.Service, password, viewerPassword string, manifest map[string]any) {
	t.Helper()
	ctx := t.Context()
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	email := "dataset-viewer@example.invalid"
	invite, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Register(ctx, fixture.InvitationToken(t, s, invite.ID), "Synthetic dataset viewer", viewerPassword, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	viewer := fixture.Login(t, s, email, viewerPassword, "portal")
	w := &workspaces.Service{DB: s.DB, Identity: s}
	if _, e = w.ChangeMember(ctx, a, a.Me.Workspace.ID, viewer.Me.User.ID, "viewer", 1, "Synthetic read-only dataset browser", uuid.NewString(), false); e != nil {
		t.Fatal(e)
	}
	other, e := w.Create(ctx, a, "Private foreign dataset scope", a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	manifest["dataset_workspace"] = a.Me.Workspace.ID
	manifest["viewer_email"], manifest["viewer_password"], manifest["foreign_workspace"] = email, viewerPassword, other.ID
}

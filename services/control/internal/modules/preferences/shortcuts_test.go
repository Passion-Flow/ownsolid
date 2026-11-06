package preferences

import (
	"context"
	"testing"

	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

func TestSingleKeyShortcutsFollowTheAccountAndLeaveOtherChoicesAlone(t *testing.T) {
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	s := &Service{DB: account.DB, Identity: account}
	ctx := context.Background()
	start, e := s.Get(ctx, admin)
	if e != nil || !start.SingleKeyShortcuts {
		t.Fatal("shortcuts start on", e, start)
	}
	dark, e := s.Update(ctx, admin, UpdateRequest{Theme: new(DarkTheme), ExpectedRevision: start.Revision}, uuid.NewString())
	if e != nil || !dark.SingleKeyShortcuts {
		t.Fatal("another field keeps shortcuts", e, dark)
	}
	off, e := s.Update(ctx, admin, UpdateRequest{SingleKeyShortcuts: new(false), ExpectedRevision: dark.Revision}, uuid.NewString())
	if e != nil || off.SingleKeyShortcuts || off.Theme != DarkTheme || off.Revision != dark.Revision+1 {
		t.Fatal("turned off alone", e, off)
	}
	read, e := s.Get(ctx, admin)
	if e != nil || read.SingleKeyShortcuts {
		t.Fatal("stored", e, read)
	}
	if _, e = s.Update(ctx, admin, UpdateRequest{SingleKeyShortcuts: new(true), ExpectedRevision: dark.Revision}, uuid.NewString()); e == nil || e.Error() != "PREFERENCE_REVISION_CONFLICT" {
		t.Fatal("stale revision", e)
	}
	if _, e = s.Update(ctx, admin, UpdateRequest{ExpectedRevision: off.Revision}, uuid.NewString()); e == nil || e.Error() != "VALIDATION_FAILED" {
		t.Fatal("no change named", e)
	}
}

func TestCollapsedNavigationFollowsTheAccountAndLeavesOtherChoicesAlone(t *testing.T) {
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	s := &Service{DB: account.DB, Identity: account}
	ctx := context.Background()
	start, e := s.Get(ctx, admin)
	if e != nil || start.NavigationCollapsed {
		t.Fatal("the sidebar starts open", e, start)
	}
	icons, e := s.Update(ctx, admin, UpdateRequest{NavigationCollapsed: new(true), ExpectedRevision: start.Revision}, uuid.NewString())
	if e != nil || !icons.NavigationCollapsed || !icons.SingleKeyShortcuts || icons.Theme != start.Theme || icons.Revision != start.Revision+1 {
		t.Fatal("collapsed alone", e, icons)
	}
	read, e := s.Get(ctx, admin)
	if e != nil || !read.NavigationCollapsed {
		t.Fatal("stored", e, read)
	}
	dark, e := s.Update(ctx, admin, UpdateRequest{Theme: new(DarkTheme), ExpectedRevision: icons.Revision}, uuid.NewString())
	if e != nil || !dark.NavigationCollapsed {
		t.Fatal("another field keeps it", e, dark)
	}
}

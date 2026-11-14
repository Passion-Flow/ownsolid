package preferences

import (
	"context"
	"slices"
	"strconv"
	"strings"
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

func TestPinsBelongToAnEntranceAndLeaveOtherChoicesAlone(t *testing.T) {
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	portal := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	s := &Service{DB: account.DB, Identity: account}
	ctx := context.Background()
	start, e := s.Get(ctx, admin)
	if e != nil || start.PinnedDestinations == nil || len(start.PinnedDestinations) != 0 {
		t.Fatal("no pins, and an empty list rather than none", e, start)
	}
	pins := []string{"/tasks", "/users/workspaces", "/"}
	pinned, e := s.Update(ctx, admin, UpdateRequest{PinnedDestinations: &pins, ExpectedRevision: start.Revision}, uuid.NewString())
	if e != nil || !slices.Equal(pinned.PinnedDestinations, pins) || pinned.Revision != start.Revision+1 || pinned.Theme != start.Theme {
		t.Fatal("kept in order", e, pinned)
	}
	other, e := s.Get(ctx, portal)
	if e != nil || len(other.PinnedDestinations) != 0 {
		t.Fatal("the other entrance has its own list", e, other)
	}
	dark, e := s.Update(ctx, admin, UpdateRequest{Theme: new(DarkTheme), ExpectedRevision: pinned.Revision}, uuid.NewString())
	if e != nil || !slices.Equal(dark.PinnedDestinations, pins) {
		t.Fatal("another field keeps them", e, dark)
	}
	portalPins := []string{"/"}
	mine, e := s.Update(ctx, portal, UpdateRequest{PinnedDestinations: &portalPins, ExpectedRevision: dark.Revision}, uuid.NewString())
	if e != nil || !slices.Equal(mine.PinnedDestinations, portalPins) {
		t.Fatal("the portal's list", e, mine)
	}
	read, e := s.Get(ctx, admin)
	if e != nil || !slices.Equal(read.PinnedDestinations, pins) {
		t.Fatal("the console's list stays", e, read)
	}
	none := []string{}
	cleared, e := s.Update(ctx, admin, UpdateRequest{PinnedDestinations: &none, ExpectedRevision: read.Revision}, uuid.NewString())
	if e != nil || cleared.PinnedDestinations == nil || len(cleared.PinnedDestinations) != 0 {
		t.Fatal("cleared", e, cleared)
	}
	tooMany := make([]string, MaximumPinned+1)
	for index := range tooMany {
		tooMany[index] = "/p" + strconv.Itoa(index)
	}
	for _, bad := range [][]string{tooMany, {"/a", "/a"}, {"nodes"}, {"https://example.invalid/"}, {"//example.invalid"}, {"/A"}, {"/a b"}, {""}, {"/" + strings.Repeat("a", 100)}} {
		if _, e = s.Update(ctx, admin, UpdateRequest{PinnedDestinations: &bad, ExpectedRevision: cleared.Revision}, uuid.NewString()); e == nil || e.Error() != "VALIDATION_FAILED" {
			t.Fatal(bad, e)
		}
	}
}

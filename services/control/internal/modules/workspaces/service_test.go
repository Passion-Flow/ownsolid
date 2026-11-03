package workspaces_test

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func expected(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s, observed %v", code, e)
	}
}

func TestM02WaitingWritesRecheckMembership(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "downgrade", true: "remove"}[revoke], func(t *testing.T) {
			s, a, alice, bob, _ := setup(t)
			ctx := context.Background()
			w, e := s.Create(ctx, a, "Fenced workspace", alice.Me.User.ID, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.AddMember(ctx, a, w.ID, "bob@example.invalid", "member", uuid.NewString()); e != nil {
				t.Fatal(e)
			}
			tx, e := s.DB.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback(ctx)
			if _, e = tx.Exec(ctx, "SELECT id FROM workspaces WHERE id=$1 FOR UPDATE", w.ID); e != nil {
				t.Fatal(e)
			}
			result := make(chan error, 1)
			go func() {
				_, e := s.CreateNote(ctx, bob, w.ID, "Must not persist", "sentinel", uuid.NewString())
				result <- e
			}()
			deadline := time.Now().Add(2 * time.Second)
			blocked := false
			for time.Now().Before(deadline) {
				var n int
				if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%FOR UPDATE OF w%'").Scan(&n); e != nil {
					t.Fatal(e)
				}
				if n > 0 {
					blocked = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !blocked {
				t.Fatal("workspace lock barrier not reached")
			}
			query := "UPDATE workspace_memberships SET role='viewer',version=version+1 WHERE workspace_id=$1 AND user_id=$2"
			code := "ACCESS_DENIED"
			if revoke {
				query = "DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2"
				code = "RESOURCE_NOT_FOUND"
			}
			// Synthetic privileged mutation commits while the real request waits on its Workspace lock.
			if _, e = tx.Exec(ctx, query, w.ID, bob.Me.User.ID); e != nil {
				t.Fatal(e)
			}
			if e = tx.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			select {
			case e = <-result:
				expected(t, e, code)
			case <-time.After(4 * time.Second):
				t.Fatal("waiting request did not terminate")
			}
			var count int
			if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM workspace_notes WHERE workspace_id=$1", w.ID).Scan(&count); e != nil || count != 0 {
				t.Fatal("revoked write persisted", e)
			}
		})
	}
}

func TestM02AuditFailureRollsBackAllMutations(t *testing.T) {
	s, a, alice, bob, _ := setup(t)
	ctx := context.Background()
	w, e := s.Create(ctx, a, "Atomic workspace", alice.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.AddMember(ctx, a, w.ID, "bob@example.invalid", "member", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	n, e := s.CreateNote(ctx, alice, w.ID, "Atomic note", "PRIVATE_NOTE_SENTINEL", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	w, e = s.Get(ctx, a, w.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	var originalAudits int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events").Scan(&originalAudits); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION reject_workspace_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic audit failure'; END$$; CREATE TRIGGER reject_workspace_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_workspace_audit()`); e != nil {
		t.Fatal(e)
	}
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"create_workspace", func() error { _, e := s.Create(ctx, a, "Must roll back", alice.Me.User.ID, uuid.NewString()); return e }},
		{"update_workspace", func() error {
			_, e := s.Update(ctx, a, w.ID, "Must roll back", "disabled", w.Version, "Synthetic", uuid.NewString())
			return e
		}},
		{"add_member", func() error {
			_, e := s.AddMember(ctx, a, w.ID, "admin@example.invalid", "owner", uuid.NewString())
			return e
		}},
		{"change_member", func() error {
			_, e := s.ChangeMember(ctx, a, w.ID, bob.Me.User.ID, "viewer", m.Version, "Synthetic", uuid.NewString(), false)
			return e
		}},
		{"remove_member", func() error {
			_, e := s.ChangeMember(ctx, a, w.ID, bob.Me.User.ID, "", m.Version, "Synthetic", uuid.NewString(), true)
			return e
		}},
		{"create_note", func() error {
			_, e := s.CreateNote(ctx, alice, w.ID, "Must roll back", "sentinel", uuid.NewString())
			return e
		}},
		{"update_note", func() error {
			_, e := s.ChangeNote(ctx, alice, w.ID, n.ID, "Must roll back", "sentinel", n.Version, uuid.NewString(), false)
			return e
		}},
		{"delete_note", func() error {
			_, e := s.ChangeNote(ctx, alice, w.ID, n.ID, "", "", n.Version, uuid.NewString(), true)
			return e
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if operation.run() == nil {
				t.Fatal("audit failure ignored")
			}
		})
	}
	fresh, e := s.Get(ctx, a, w.ID, false)
	if e != nil || fresh.Name != w.Name || fresh.Status != w.Status || fresh.Version != w.Version {
		t.Fatal("workspace partially mutated", e)
	}
	list, e := s.Members(ctx, a, w.ID, url.Values{})
	if e != nil || len(list.Items) != 2 {
		t.Fatal("membership partially mutated", e)
	}
	for _, member := range list.Items {
		if member.UserID == bob.Me.User.ID && (member.Role != "member" || member.Version != m.Version) {
			t.Fatal("membership version/role partially mutated")
		}
	}
	notes, e := s.Notes(ctx, alice, w.ID, url.Values{})
	if e != nil || len(notes.Items) != 1 || notes.Items[0].Text != n.Text || notes.Items[0].Version != n.Version {
		t.Fatal("note partially mutated", e)
	}
	var count, leaks int
	if e = s.DB.QueryRow(ctx, "SELECT count(*),count(*) FILTER (WHERE row_to_json(a)::text LIKE '%PRIVATE_NOTE_SENTINEL%') FROM audit_events a").Scan(&count, &leaks); e != nil || count != originalAudits || leaks != 0 {
		t.Fatal("audit count or content leak", e)
	}
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER reject_workspace_audit ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeNote(ctx, alice, w.ID, n.ID, "Recovered", n.Text, n.Version, uuid.NewString(), false); e != nil {
		t.Fatal("audit recovery failed", e)
	}
}

func TestM02PaginationScopeAndResourceBounds(t *testing.T) {
	s, a, alice, bob, _ := setup(t)
	ctx := context.Background()
	w, e := s.Create(ctx, a, "Paged", alice.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	other, e := s.Create(ctx, a, "Other", bob.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 41; i++ {
		if _, e = s.CreateNote(ctx, alice, w.ID, "Note", "text", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	seen := map[string]bool{}
	values := url.Values{"limit": {"20"}}
	for {
		page, e := s.Notes(ctx, alice, w.ID, values)
		if e != nil {
			t.Fatal(e)
		}
		for _, n := range page.Items {
			if seen[n.ID] {
				t.Fatal("duplicate pagination row")
			}
			seen[n.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		_, e = s.Notes(ctx, bob, other.ID, url.Values{"cursor": {*page.NextCursor}})
		expected(t, e, "INVALID_CURSOR")
		values.Set("cursor", *page.NextCursor)
	}
	if len(seen) != 41 {
		t.Fatal("pagination lost notes")
	}
	_, e = s.CreateNote(ctx, alice, w.ID, "Oversize", strings.Repeat("x", 8193), uuid.NewString())
	expected(t, e, "VALIDATION_FAILED")
	if _, e = s.DB.Exec(ctx, "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id) SELECT gen_random_uuid(),$1,$2,'Capacity fixture','',$3 FROM generate_series(1,9959)", w.TenantID, w.ID, alice.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.CreateNote(ctx, alice, w.ID, "Over capacity", "", uuid.NewString())
	expected(t, e, "RESOURCE_LIMIT_REACHED")
	if _, e = s.DB.Exec(ctx, "INSERT INTO workspaces(id,tenant_id,name,status) SELECT gen_random_uuid(),$1,'Capacity workspace '||n,'active' FROM generate_series(1,97) n", w.TenantID); e != nil {
		t.Fatal(e)
	}
	_, e = s.Create(ctx, a, "Over capacity", alice.Me.User.ID, uuid.NewString())
	expected(t, e, "RESOURCE_LIMIT_REACHED")
}

func TestM02InactiveAndUnknownMembershipAssignment(t *testing.T) {
	s, a, alice, bob, _ := setup(t)
	ctx := context.Background()
	w, e := s.Create(ctx, a, "Assignment checks", alice.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.AddMember(ctx, alice, w.ID, "unknown@example.invalid", "member", uuid.NewString())
	expected(t, e, "RESOURCE_NOT_FOUND")
	if _, e = s.Identity.SetStatus(ctx, a, bob.Me.User.ID, "disabled", bob.Me.User.Version, "Synthetic disabled fixture", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e = s.AddMember(ctx, alice, w.ID, "bob@example.invalid", "member", uuid.NewString())
	expected(t, e, "RESOURCE_NOT_FOUND")
	_, e = s.Create(ctx, a, "Inactive owner", bob.Me.User.ID, uuid.NewString())
	expected(t, e, "RESOURCE_NOT_FOUND")
	_, e = s.AddMember(ctx, alice, w.ID, "admin@example.invalid", "custom-role", uuid.NewString())
	expected(t, e, "VALIDATION_FAILED")
}
func setup(t *testing.T) (*workspaces.Service, identity.Auth, identity.Auth, identity.Auth, string) {
	t.Helper()
	s := fixture.NewM02(t)
	p := fixture.Bootstrap(t, s)
	admin := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	password := fixture.Password()
	actors := []identity.Auth{}
	for _, email := range []string{"alice@example.invalid", "bob@example.invalid"} {
		i, e := s.Invite(context.Background(), admin, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		if e = s.Register(context.Background(), fixture.InvitationToken(t, s, i.ID), "Workspace fixture", password, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		actors = append(actors, fixture.Login(t, s, email, password, "portal"))
	}
	return &workspaces.Service{DB: s.DB, Identity: s}, admin, actors[0], actors[1], password
}
func TestM02LifecycleRolesAndRealNotes(t *testing.T) {
	s, a, alice, bob, _ := setup(t)
	ctx := context.Background()
	w, e := s.Create(ctx, a, "Alpha", alice.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	other, e := s.Create(ctx, a, "Beta", bob.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Create(ctx, a, "Alpha", alice.Me.User.ID, uuid.NewString())
	expected(t, e, "WORKSPACE_EXISTS")
	if _, e = s.AddMember(ctx, alice, w.ID, "bob@example.invalid", "viewer", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	note, e := s.CreateNote(ctx, alice, w.ID, "Visible note", "Synthetic private content", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if read, e := s.GetNote(ctx, bob, w.ID, note.ID); e != nil || read.Text != note.Text {
		t.Fatal("viewer read failed", e)
	}
	_, e = s.CreateNote(ctx, bob, w.ID, "Forbidden", "text", uuid.NewString())
	expected(t, e, "ACCESS_DENIED")
	_, e = s.ChangeNote(ctx, bob, w.ID, note.ID, "Override", "text", note.Version, uuid.NewString(), false)
	expected(t, e, "ACCESS_DENIED")
	_, e = s.ChangeNote(ctx, bob, w.ID, note.ID, "", "", note.Version, uuid.NewString(), true)
	expected(t, e, "ACCESS_DENIED")
	_, e = s.Members(ctx, bob, w.ID, url.Values{})
	expected(t, e, "ACCESS_DENIED")
	_, e = s.GetNote(ctx, alice, other.ID, note.ID)
	expected(t, e, "RESOURCE_NOT_FOUND")
	members, e := s.Members(ctx, alice, w.ID, url.Values{})
	if e != nil {
		t.Fatal(e)
	}
	var bm workspaces.Membership
	for _, m := range members.Items {
		if m.UserID == bob.Me.User.ID {
			bm = m
		}
	}
	bm, e = s.ChangeMember(ctx, alice, w.ID, bob.Me.User.ID, "member", bm.Version, "Synthetic promotion", uuid.NewString(), false)
	if e != nil {
		t.Fatal(e)
	}
	edited, e := s.ChangeNote(ctx, bob, w.ID, note.ID, "Edited note", "Updated content", note.Version, uuid.NewString(), false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.ChangeNote(ctx, bob, w.ID, note.ID, "stale", "text", note.Version, uuid.NewString(), false)
	expected(t, e, "VERSION_CONFLICT")
	if _, e = s.ChangeMember(ctx, alice, w.ID, bob.Me.User.ID, "", bm.Version, "Synthetic revoke", uuid.NewString(), true); e != nil {
		t.Fatal(e)
	}
	_, e = s.GetNote(ctx, bob, w.ID, note.ID)
	expected(t, e, "RESOURCE_NOT_FOUND")
	if _, e = s.ChangeNote(ctx, alice, w.ID, note.ID, "", "", edited.Version, uuid.NewString(), true); e != nil {
		t.Fatal(e)
	}
	_, e = s.GetNote(ctx, alice, w.ID, note.ID)
	expected(t, e, "RESOURCE_NOT_FOUND")
	fresh, e := s.Get(ctx, a, w.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	archived, e := s.Update(ctx, a, w.ID, "Renamed", "disabled", fresh.Version, "Synthetic archive", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Get(ctx, alice, w.ID, false)
	expected(t, e, "RESOURCE_NOT_FOUND")
	if _, e = s.Update(ctx, a, w.ID, "Renamed", "active", archived.Version, "Synthetic restore", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	defaultW, e := s.Get(ctx, a, a.Me.Workspace.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Update(ctx, a, defaultW.ID, defaultW.Name, "disabled", defaultW.Version, "Forbidden archive", uuid.NewString())
	expected(t, e, "DEFAULT_WORKSPACE_PROTECTED")
}
func TestM02LastOwnerCannotRaceToZero(t *testing.T) {
	s, a, alice, bob, _ := setup(t)
	ctx := context.Background()
	w, e := s.Create(ctx, a, "Owners", alice.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	bm, e := s.AddMember(ctx, a, w.ID, "bob@example.invalid", "owner", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, m := range []struct {
		id      string
		version int64
	}{{alice.Me.User.ID, 1}, {bob.Me.User.ID, bm.Version}} {
		wg.Add(1)
		go func(id string, v int64) {
			defer wg.Done()
			<-start
			_, e := s.ChangeMember(ctx, a, w.ID, id, "member", v, "Concurrent demotion", uuid.NewString(), false)
			results <- e
		}(m.id, m.version)
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for e := range results {
		if e == nil {
			wins++
		} else {
			expected(t, e, "LAST_WORKSPACE_OWNER")
		}
	}
	if wins != 1 {
		t.Fatal("owner race had wrong winner count")
	}
	var owners int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM workspace_memberships WHERE workspace_id=$1 AND role='owner'", w.ID).Scan(&owners); e != nil || owners != 1 {
		t.Fatal("Workspace orphaned", e)
	}
}
func TestM02TenantIDsAndZeroMembershipDoNotGrantAccess(t *testing.T) {
	s, a, alice, bob, password := setup(t)
	ctx := context.Background()
	tenant, workspace, note := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO tenants(id,name,status) VALUES($1,'Foreign tenant','active')", []any{tenant}},
		{"INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign workspace','active')", []any{workspace, tenant}},
		{"INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')", []any{bob.Me.User.ID, tenant, workspace}},
		{"INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id) VALUES($1,$2,$3,'Foreign','sentinel',$4)", []any{note, tenant, workspace, bob.Me.User.ID}},
	} {
		if _, e := s.DB.Exec(ctx, q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	for _, actor := range []identity.Auth{a, alice, bob} {
		_, e := s.Get(ctx, actor, workspace, false)
		expected(t, e, "RESOURCE_NOT_FOUND")
		_, e = s.GetNote(ctx, actor, workspace, note)
		expected(t, e, "RESOURCE_NOT_FOUND")
	}
	members, e := s.Members(ctx, a, a.Me.Workspace.ID, url.Values{})
	if e != nil {
		t.Fatal(e)
	}
	var version int64
	for _, m := range members.Items {
		if m.UserID == alice.Me.User.ID {
			version = m.Version
		}
	}
	if _, e = s.ChangeMember(ctx, a, a.Me.Workspace.ID, alice.Me.User.ID, "", version, "Remove only synthetic membership", uuid.NewString(), true); e != nil {
		t.Fatal(e)
	}
	_, token, _, e := s.Identity.Login(ctx, "alice@example.invalid", password, "portal", "", uuid.NewString())
	if e != nil {
		t.Fatal("zero-membership login rejected", e)
	}
	who, e := s.Identity.Authenticate(ctx, token, "portal")
	if e != nil || who.Me.Workspace != nil {
		t.Fatal("zero-membership context not empty", e)
	}
	list, e := s.List(ctx, who, url.Values{})
	if e != nil || len(list.Items) != 0 {
		t.Fatal("zero-membership scope leak", e)
	}
	if _, e = s.Identity.UpdateMe(ctx, who, "Still my account", "en", who.Me.User.Version, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
}

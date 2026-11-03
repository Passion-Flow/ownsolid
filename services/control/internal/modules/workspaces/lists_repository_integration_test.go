//go:build repository_integration

package workspaces_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualWorkspaceListRepository(t *testing.T) {
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	accounts := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		accounts.Storage, accounts.DB = nil, f.PostgreSQL
	}
	s := &workspaces.Service{Identity: accounts, DB: f.PostgreSQL}
	exec := func(t *testing.T, pg, my string, args ...any) {
		t.Helper()
		if _, e := f.Store.Exec(ctx, f.Statement(pg, my, args...)); e != nil {
			t.Fatal(e)
		}
	}
	want := func(t *testing.T, e error, code string) {
		t.Helper()
		if e == nil || e.Error() != code {
			t.Fatalf("expected %s,got %v", code, e)
		}
	}
	issue := func(t *testing.T, user, audience string) identity.Auth {
		t.Helper()
		var issued identity.IssuedSession
		if e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			issued, e = accounts.IssueSessionWithin(ctx, q, user, 1, audience, "", uuid.NewString(), nil)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		a, e := accounts.Authenticate(ctx, issued.Token, audience)
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	stamp := time.Date(2026, 10, 19, 1, 2, 3, 123456000, time.UTC)
	exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')", f.User, f.Tenant, f.Workspace)
	scopes, users := []string{f.Workspace}, []string{f.User}
	for n := 0; n < 100; n++ {
		id, user, address := uuid.NewString(), uuid.NewString(), "synthetic+"+uuid.NewString()+"@example.invalid"
		scopes = append(scopes, id)
		users = append(users, user)
		exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,$3,'active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,?,'active')", id, f.Tenant, "Synthetic scope \u4e16\u754c "+strconv.Itoa(n))
		exec(t, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,$2,$3,'Synthetic list actor \u4e16\u754c','en','active')", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES(?,?,?,'Synthetic list actor \u4e16\u754c','en','active')", user, address, address)
		role := "member"
		if n == 0 {
			role = "owner"
		}
		if n == 1 {
			role = "viewer"
		}
		exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,$4)", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,?)", user, f.Tenant, f.Workspace, role)
	}
	for n := 0; n < 101; n++ {
		exec(t, "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id,created_at) VALUES($1,$2,$3,'Synthetic note','Private list \u4e16\u754c🙂 sentinel',$4,$5)", "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id,created_at) VALUES(?,?,?,'Synthetic note','Private list \u4e16\u754c🙂 sentinel',?,?)", uuid.NewString(), f.Tenant, f.Workspace, f.User, stamp)
	}
	exec(t, "UPDATE workspaces SET created_at=$1,version=$2 WHERE tenant_id=$3", "UPDATE workspaces SET created_at=?,version=? WHERE tenant_id=?", stamp, identity.MaximumAuthVersion, f.Tenant)
	exec(t, "UPDATE workspace_memberships SET created_at=$1,version=$2 WHERE workspace_id=$3", "UPDATE workspace_memberships SET created_at=?,version=? WHERE workspace_id=?", stamp, identity.MaximumAuthVersion, f.Workspace)
	exec(t, "UPDATE workspace_notes SET version=$1 WHERE workspace_id=$2", "UPDATE workspace_notes SET version=? WHERE workspace_id=?", identity.MaximumAuthVersion, f.Workspace)
	exec(t, "UPDATE users SET status='disabled' WHERE id=$1", "UPDATE users SET status='disabled' WHERE id=?", users[len(users)-1])
	admin, portal, owner, viewer := issue(t, f.User, "admin"), issue(t, f.User, "portal"), issue(t, users[1], "portal"), issue(t, users[2], "portal")
	events := func(t *testing.T) int {
		t.Helper()
		var n int
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	t.Run("three_tied_lists_have_bounded_stable_seeks", func(t *testing.T) {
		before := events(t)
		for _, kind := range []string{"workspaces", "members", "notes"} {
			values := url.Values{"limit": {"17"}}
			seen := map[string]bool{}
			pages := 0
			for {
				var ids []string
				var cursor *string
				switch kind {
				case "workspaces":
					page, e := s.List(ctx, admin, values)
					if e != nil {
						t.Fatal(e)
					}
					cursor = page.NextCursor
					for _, v := range page.Items {
						ids = append(ids, v.ID)
						if v.TenantID != f.Tenant || v.Version != identity.MaximumAuthVersion {
							t.Fatal("typed Workspace projection")
						}
					}
				case "members":
					page, e := s.Members(ctx, admin, f.Workspace, values)
					if e != nil {
						t.Fatal(e)
					}
					cursor = page.NextCursor
					for _, v := range page.Items {
						ids = append(ids, v.UserID)
						if v.Version != identity.MaximumAuthVersion || v.Email == "" {
							t.Fatal("typed member projection")
						}
					}
				case "notes":
					page, e := s.Notes(ctx, admin, f.Workspace, values)
					if e != nil {
						t.Fatal(e)
					}
					cursor = page.NextCursor
					for _, v := range page.Items {
						ids = append(ids, v.ID)
						if v.Version != identity.MaximumAuthVersion || v.Text != "Private list \u4e16\u754c🙂 sentinel" || v.WorkspaceID != f.Workspace {
							t.Fatal("typed note projection")
						}
					}
				}
				if len(ids) < 1 || len(ids) > 17 {
					t.Fatal("unbounded/empty intermediate page")
				}
				for _, id := range ids {
					if seen[id] {
						t.Fatal("repeated tied row")
					}
					seen[id] = true
				}
				pages++
				if pages == 1 {
					id := uuid.NewString()
					switch kind {
					case "workspaces":
						exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic newer head','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic newer head','active')", id, f.Tenant)
					case "members":
						email := "synthetic+" + uuid.NewString() + "@example.invalid"
						exec(t, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,$2,$3,'Synthetic newer member','en','active')", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES(?,?,?,'Synthetic newer member','en','active')", id, email, email)
						exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'viewer')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'viewer')", id, f.Tenant, f.Workspace)
					case "notes":
						exec(t, "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id) VALUES($1,$2,$3,'Synthetic newer note','New head',$4)", "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id) VALUES(?,?,?,'Synthetic newer note','New head',?)", id, f.Tenant, f.Workspace, f.User)
					}
				}
				if cursor == nil {
					break
				}
				if pages > 10 {
					t.Fatal("cursor did not terminate")
				}
				values.Set("cursor", *cursor)
			}
			if len(seen) != 101 || pages != 6 {
				t.Fatal("seek continuity changed under new head", kind, len(seen), pages)
			}
		}
		if events(t) != before {
			t.Fatal("list read created audit")
		}
	})
	t.Run("configured_size_strict_inputs_and_context_bound_cursors", func(t *testing.T) {
		policy := config.DefaultRuntimePolicy()
		policy.UI.PageSize = 7
		accounts.Config.Runtime = &policy
		page, e := s.List(ctx, admin, nil)
		if e != nil || len(page.Items) != 7 || page.NextCursor == nil {
			t.Fatal("configured omitted limit", e)
		}
		members, e := s.Members(ctx, admin, f.Workspace, nil)
		if e != nil || len(members.Items) != 7 || members.NextCursor == nil {
			t.Fatal("member default policy", e)
		}
		notes, e := s.Notes(ctx, admin, f.Workspace, nil)
		if e != nil || len(notes.Items) != 7 || notes.NextCursor == nil {
			t.Fatal("note default policy", e)
		}
		for _, q := range []url.Values{{"limit": {""}}, {"limit": {"101"}}, {"limit": {"1", "2"}}, {"tenant_id": {f.Tenant}}} {
			p, e := s.List(ctx, admin, q)
			want(t, e, "VALIDATION_FAILED")
			if len(p.Items) != 0 || p.NextCursor != nil {
				t.Fatal("invalid list returned partial page")
			}
		}
		_, e = s.Members(ctx, admin, f.Workspace, url.Values{"cursor": {*page.NextCursor}})
		want(t, e, "INVALID_CURSOR")
		_, e = s.Notes(ctx, admin, f.Workspace, url.Values{"cursor": {*members.NextCursor}})
		want(t, e, "INVALID_CURSOR")
		_, e = s.List(ctx, portal, url.Values{"cursor": {*page.NextCursor}})
		want(t, e, "INVALID_CURSOR")
		_, e = s.Members(ctx, owner, f.Workspace, url.Values{"cursor": {*members.NextCursor}})
		want(t, e, "INVALID_CURSOR")
		_, e = s.Notes(ctx, admin, scopes[1], url.Values{"cursor": {*notes.NextCursor}})
		want(t, e, "INVALID_CURSOR")
		if p, e := s.List(ctx, admin, url.Values{"limit": {"3"}, "cursor": {*page.NextCursor}}); e != nil || len(p.Items) != 3 {
			t.Fatal("limit change invalidated seek", e)
		}
		body, _ := json.Marshal(members)
		if strings.Contains(string(body), "authority_id") || strings.Contains(string(body), "password_phc") || strings.Contains(string(body), "token_hash") {
			t.Fatal("sensitive member projection")
		}
	})
	t.Run("portal_roles_foreign_archived_tombstones_and_current_actor", func(t *testing.T) {
		p, e := s.List(ctx, portal, url.Values{"limit": {"100"}})
		if e != nil || len(p.Items) != 1 || p.Items[0].ID != f.Workspace {
			t.Fatal("Portal admin bypassed membership", e)
		}
		_, e = s.Members(ctx, viewer, f.Workspace, nil)
		want(t, e, "ACCESS_DENIED")
		if p, e := s.Notes(ctx, viewer, f.Workspace, nil); e != nil || len(p.Items) != 7 {
			t.Fatal("viewer note list", e)
		}
		foreign, scope := uuid.NewString(), uuid.NewString()
		exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign list tenant','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic foreign list tenant','active')", foreign)
		exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign list scope','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic foreign list scope','active')", scope, foreign)
		_, e = s.Notes(ctx, admin, scope, nil)
		want(t, e, "RESOURCE_NOT_FOUND")
		exec(t, "UPDATE workspaces SET status='disabled' WHERE id=$1", "UPDATE workspaces SET status='disabled' WHERE id=?", scopes[1])
		if p, e := s.List(ctx, admin, url.Values{"limit": {"100"}}); e != nil {
			t.Fatal(e)
		} else {
			for _, v := range p.Items {
				if v.ID == scope {
					t.Fatal("foreign scope in discovery")
				}
			}
		}
		deleted := uuid.NewString()
		exec(t, "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id,deleted_at) VALUES($1,$2,$3,'Deleted','Deleted sentinel',$4,clock_timestamp())", "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id,deleted_at) VALUES(?,?,?,'Deleted','Deleted sentinel',?,UTC_TIMESTAMP(6))", deleted, f.Tenant, f.Workspace, f.User)
		notes, e := s.Notes(ctx, admin, f.Workspace, url.Values{"limit": {"100"}})
		if e != nil {
			t.Fatal(e)
		}
		for _, n := range notes.Items {
			if n.ID == deleted {
				t.Fatal("tombstone in note list")
			}
		}
		before := events(t)
		exec(t, "UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", f.User)
		page, e := s.List(ctx, admin, nil)
		want(t, e, "AUTH_SESSION_EXPIRED")
		if len(page.Items) != 0 {
			t.Fatal("stale role discovery")
		}
		exec(t, "UPDATE users SET is_platform_admin=true WHERE id=$1", "UPDATE users SET is_platform_admin=true WHERE id=?", f.User)
		if events(t) != before {
			t.Fatal("denied discovery wrote audit")
		}
	})
	t.Run("waiting_member_and_note_lists_recheck_scope", func(t *testing.T) {
		for _, kind := range []string{"members", "notes"} {
			result := make(chan error, 1)
			e := f.Store.Within(ctx, false, func(txctx context.Context, q transaction.Executor) error {
				var id string
				if e := q.QueryRow(txctx, f.Statement("SELECT id::text FROM workspaces WHERE id=$1 FOR UPDATE", "SELECT id FROM workspaces WHERE id=? FOR UPDATE", f.Workspace)).Scan(&id); e != nil {
					return e
				}
				go func() {
					if kind == "members" {
						p, e := s.Members(ctx, owner, f.Workspace, nil)
						if len(p.Items) > 0 {
							result <- errorsListPartial()
							return
						}
						result <- e
					} else {
						p, e := s.Notes(ctx, viewer, f.Workspace, nil)
						if len(p.Items) > 0 {
							result <- errorsListPartial()
							return
						}
						result <- e
					}
				}()
				select {
				case e := <-result:
					t.Fatalf("list escaped Workspace fence: %v", e)
				case <-time.After(120 * time.Millisecond):
				}
				if kind == "members" {
					_, e := q.Exec(txctx, f.Statement("UPDATE workspace_memberships SET role='viewer' WHERE user_id=$1 AND workspace_id=$2", "UPDATE workspace_memberships SET role='viewer' WHERE user_id=? AND workspace_id=?", owner.Me.User.ID, f.Workspace))
					return e
				}
				_, e := q.Exec(txctx, f.Statement("UPDATE workspaces SET status='disabled' WHERE id=$1", "UPDATE workspaces SET status='disabled' WHERE id=?", f.Workspace))
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			code := "ACCESS_DENIED"
			if kind == "notes" {
				code = "RESOURCE_NOT_FOUND"
			}
			want(t, <-result, code)
			if kind == "notes" {
				exec(t, "UPDATE workspaces SET status='active' WHERE id=$1", "UPDATE workspaces SET status='active' WHERE id=?", f.Workspace)
			}
		}
	})
	t.Run("cancelled_and_failed_transactions_return_no_page", func(t *testing.T) {
		waiting, stop := context.WithCancel(ctx)
		defer stop()
		result := make(chan error, 1)
		if e := f.Store.Within(ctx, false, func(txctx context.Context, q transaction.Executor) error {
			var id string
			if e := q.QueryRow(txctx, f.Statement("SELECT id::text FROM workspaces WHERE id=$1 FOR UPDATE", "SELECT id FROM workspaces WHERE id=? FOR UPDATE", f.Workspace)).Scan(&id); e != nil {
				return e
			}
			go func() {
				p, e := s.Notes(waiting, viewer, f.Workspace, nil)
				if len(p.Items) > 0 || p.NextCursor != nil {
					result <- errorsListPartial()
					return
				}
				result <- e
			}()
			select {
			case e := <-result:
				t.Fatalf("cancel waiter escaped fence: %v", e)
			case <-time.After(120 * time.Millisecond):
			}
			stop()
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		if e := <-result; e == nil || e.Error() == "SYNTHETIC_PARTIAL_LIST" {
			t.Fatal("cancelled list", e)
		}
		copy := *accounts
		copy.DB = nil
		probe := &scopeReadFaultStore{Store: f.Store}
		copy.Storage = probe
		scoped := &workspaces.Service{Identity: &copy}
		page, e := scoped.List(ctx, admin, nil)
		if e == nil || len(page.Items) > 0 || page.NextCursor != nil || !probe.reached {
			t.Fatal("failed discovery transaction returned page", e)
		}
		probe.reached = false
		notes, e := scoped.Notes(ctx, viewer, f.Workspace, nil)
		if e == nil || len(notes.Items) > 0 || notes.NextCursor != nil || !probe.reached {
			t.Fatal("failed note transaction returned page", e)
		}
	})
}
func errorsListPartial() error { return errors.New("SYNTHETIC_PARTIAL_LIST") }

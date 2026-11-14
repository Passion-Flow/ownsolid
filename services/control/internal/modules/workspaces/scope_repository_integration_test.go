//go:build repository_integration

package workspaces_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualWorkspaceScopeRepository(t *testing.T) {
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
		if e := f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			var e error
			issued, e = accounts.IssueSessionWithin(ctx, tx, user, 1, audience, "", uuid.NewString(), nil)
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
	account := func(t *testing.T, role string, admin bool) identity.Auth {
		t.Helper()
		id, address := uuid.NewString(), "synthetic+"+uuid.NewString()+"@example.invalid"
		exec(t, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Synthetic scope actor','en','active',$4)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Synthetic scope actor','en','active',?)", id, address, address, admin)
		if role != "" {
			exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,$4)", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,?)", id, f.Tenant, f.Workspace, role)
		}
		audience := "portal"
		if admin {
			audience = "admin"
		}
		return issue(t, id, audience)
	}
	exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')", f.User, f.Tenant, f.Workspace)
	admin := issue(t, f.User, "admin")
	portalAdmin := issue(t, f.User, "portal")
	owner := account(t, "owner", false)
	member := account(t, "member", false)
	viewer := account(t, "viewer", false)
	empty := account(t, "", false)
	other, foreignTenant, foreignScope := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic second scope','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic second scope','active')", other, f.Tenant)
	exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign tenant','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic foreign tenant','active')", foreignTenant)
	exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign scope','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic foreign scope','active')", foreignScope, foreignTenant)
	exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')", f.User, foreignTenant, foreignScope)
	note, otherNote, deleted := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, item := range []struct{ id, scope string }{{note, f.Workspace}, {otherNote, other}, {deleted, f.Workspace}} {
		exec(t, "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id) VALUES($1,$2,$3,'Synthetic note','Private \u4e16\u754c🙂 sentinel',$4)", "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id) VALUES(?,?,?,'Synthetic note','Private \u4e16\u754c🙂 sentinel',?)", item.id, f.Tenant, item.scope, f.User)
	}
	exec(t, "UPDATE workspace_notes SET deleted_at=clock_timestamp() WHERE id=$1", "UPDATE workspace_notes SET deleted_at=UTC_TIMESTAMP(6) WHERE id=?", deleted)
	events := func(t *testing.T) int {
		t.Helper()
		var n int
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	t.Run("real_roles_admin_portal_owner_and_projection", func(t *testing.T) {
		before := events(t)
		for _, a := range []identity.Auth{admin, portalAdmin, owner, member, viewer} {
			w, e := s.Get(ctx, a, f.Workspace, false)
			if e != nil || w.ID != f.Workspace || w.TenantID != f.Tenant || w.Role == nil || w.Version != 1 || !w.IsDefault || w.CreatedAt.IsZero() || w.UpdatedAt.IsZero() {
				t.Fatal("real scope projection", e)
			}
		}
		if _, e := s.Get(ctx, owner, f.Workspace, true); e != nil {
			t.Fatal(e)
		}
		for _, a := range []identity.Auth{member, viewer} {
			_, e := s.Get(ctx, a, f.Workspace, true)
			want(t, e, "ACCESS_DENIED")
		}
		if w, e := s.Get(ctx, admin, other, true); e != nil || w.Role != nil || w.IsDefault {
			t.Fatal("installed admin no-membership scope", e)
		}
		for _, a := range []identity.Auth{portalAdmin, owner, member, viewer, empty} {
			_, e := s.Get(ctx, a, other, false)
			want(t, e, "RESOURCE_NOT_FOUND")
		}
		if events(t) != before {
			t.Fatal("reads wrote audit")
		}
	})
	t.Run("live_tombstone_and_cross_scope_note_reads", func(t *testing.T) {
		for _, a := range []identity.Auth{admin, owner, member, viewer} {
			n, e := s.GetNote(ctx, a, f.Workspace, note)
			if e != nil || n.ID != note || n.WorkspaceID != f.Workspace || n.TenantID != f.Tenant || n.Text != "Private \u4e16\u754c🙂 sentinel" || n.AuthorUserID != f.User || n.Version != 1 {
				t.Fatal("real note projection", e)
			}
		}
		for _, id := range []string{deleted, otherNote, uuid.NewString()} {
			n, e := s.GetNote(ctx, member, f.Workspace, id)
			want(t, e, "RESOURCE_NOT_FOUND")
			if n.ID != "" || n.Text != "" {
				t.Fatal("failed note exposed partial content")
			}
		}
		if _, e := s.GetNote(ctx, empty, f.Workspace, note); e == nil {
			t.Fatal("zero membership read note")
		}
	})
	t.Run("foreign_tenant_archived_and_zero_membership", func(t *testing.T) {
		for _, a := range []identity.Auth{admin, portalAdmin, owner, empty} {
			_, e := s.Get(ctx, a, foreignScope, false)
			want(t, e, "RESOURCE_NOT_FOUND")
		}
		exec(t, "UPDATE workspaces SET status='disabled' WHERE id=$1", "UPDATE workspaces SET status='disabled' WHERE id=?", other)
		if w, e := s.Get(ctx, admin, other, false); e != nil || w.Status != "disabled" {
			t.Fatal("admin archive inspection", e)
		}
		_, e := s.Get(ctx, portalAdmin, other, false)
		want(t, e, "RESOURCE_NOT_FOUND")
		_, e = s.Get(ctx, empty, f.Workspace, false)
		want(t, e, "RESOURCE_NOT_FOUND")
	})
	t.Run("current_role_status_session_and_no_read_audit", func(t *testing.T) {
		a := account(t, "", true)
		before := events(t)
		exec(t, "UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", a.Me.User.ID)
		w, e := s.Get(ctx, a, f.Workspace, false)
		want(t, e, "AUTH_SESSION_EXPIRED")
		if w.ID != "" {
			t.Fatal("stale administrator exposed resource")
		}
		exec(t, "UPDATE users SET is_platform_admin=true WHERE id=$1", "UPDATE users SET is_platform_admin=true WHERE id=?", a.Me.User.ID)
		exec(t, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1", "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='logout' WHERE id=?", a.SessionID)
		_, e = s.GetNote(ctx, a, f.Workspace, note)
		want(t, e, "AUTH_SESSION_EXPIRED")
		if events(t) != before {
			t.Fatal("denied reads wrote audit")
		}
	})
	t.Run("real_caller_transaction_lifetime_and_foreign_lookup", func(t *testing.T) {
		e := f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			w, e := s.RecheckScopeWithin(ctx, tx, admin, f.Workspace)
			if e != nil {
				return e
			}
			before, e := s.MembershipAuthorityWithin(ctx, tx, w.TenantID, w.ID, member.Me.User.ID)
			if e != nil {
				return e
			}
			if id, e := uuid.Parse(before); e != nil || id == uuid.Nil {
				t.Fatal("real stored authority missing")
			}
			if _, e = s.MembershipAuthorityWithin(ctx, tx, foreignTenant, foreignScope, f.User); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
				t.Fatal("foreign authority lookup", e)
			}
			if _, e = tx.Exec(ctx, f.Statement("DELETE FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", "DELETE FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", w.TenantID, w.ID, member.Me.User.ID)); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, f.Statement("INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'member')", member.Me.User.ID, w.TenantID, w.ID)); e != nil {
				return e
			}
			after, e := s.MembershipAuthorityWithin(ctx, tx, w.TenantID, w.ID, member.Me.User.ID)
			if e != nil {
				return e
			}
			if before == after {
				t.Fatal("membership lifetime resurrected")
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
	})
	t.Run("waiting_reads_recheck_downgrade_remove_and_archive", func(t *testing.T) {
		for _, mode := range []string{"downgrade", "remove", "archive"} {
			a := account(t, "owner", false)
			waiting := make(chan error, 1)
			e := f.Store.Within(ctx, false, func(txctx context.Context, tx transaction.Executor) error {
				var id string
				if e := tx.QueryRow(txctx, f.Statement("SELECT id::text FROM workspaces WHERE id=$1 FOR UPDATE", "SELECT id FROM workspaces WHERE id=? FOR UPDATE", f.Workspace)).Scan(&id); e != nil {
					return e
				}
				go func() { _, e := s.Get(ctx, a, f.Workspace, mode == "downgrade"); waiting <- e }()
				select {
				case e := <-waiting:
					t.Fatalf("scope read escaped exclusive fence: %v", e)
				case <-time.After(120 * time.Millisecond):
				}
				var q transaction.Statement
				switch mode {
				case "downgrade":
					q = f.Statement("UPDATE workspace_memberships SET role='viewer' WHERE user_id=$1 AND workspace_id=$2", "UPDATE workspace_memberships SET role='viewer' WHERE user_id=? AND workspace_id=?", a.Me.User.ID, f.Workspace)
				case "remove":
					q = f.Statement("DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2", "DELETE FROM workspace_memberships WHERE user_id=? AND workspace_id=?", a.Me.User.ID, f.Workspace)
				case "archive":
					q = f.Statement("UPDATE workspaces SET status='disabled' WHERE id=$1", "UPDATE workspaces SET status='disabled' WHERE id=?", f.Workspace)
				}
				_, e := tx.Exec(txctx, q)
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			code := "RESOURCE_NOT_FOUND"
			if mode == "downgrade" {
				code = "ACCESS_DENIED"
			}
			want(t, <-waiting, code)
			if mode == "archive" {
				exec(t, "UPDATE workspaces SET status='active' WHERE id=$1", "UPDATE workspaces SET status='active' WHERE id=?", f.Workspace)
			}
		}
	})
	t.Run("cancelled_waiting_note_returns_no_content", func(t *testing.T) {
		waitingCtx, stop := context.WithCancel(ctx)
		defer stop()
		result := make(chan error, 1)
		if e := f.Store.Within(ctx, false, func(txctx context.Context, tx transaction.Executor) error {
			var id string
			if e := tx.QueryRow(txctx, f.Statement("SELECT id::text FROM workspaces WHERE id=$1 FOR UPDATE", "SELECT id FROM workspaces WHERE id=? FOR UPDATE", f.Workspace)).Scan(&id); e != nil {
				return e
			}
			go func() {
				n, e := s.GetNote(waitingCtx, viewer, f.Workspace, note)
				if n.ID != "" || n.Text != "" {
					result <- errors.New("SYNTHETIC_PARTIAL_NOTE_EXPOSED")
					return
				}
				result <- e
			}()
			select {
			case e := <-result:
				t.Fatalf("note read escaped scope fence: %v", e)
			case <-time.After(120 * time.Millisecond):
			}
			stop()
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		if e := <-result; e == nil || e.Error() == "SYNTHETIC_PARTIAL_NOTE_EXPOSED" {
			t.Fatal("cancelled note read", e)
		}
	})
	t.Run("failed_owned_read_transaction_returns_no_success_data", func(t *testing.T) {
		copy := *accounts
		copy.DB = nil
		probe := &scopeReadFaultStore{Store: f.Store}
		copy.Storage = probe
		scoped := &workspaces.Service{Identity: &copy}
		w, e := scoped.Get(ctx, admin, f.Workspace, false)
		if e == nil || w.ID != "" || !probe.reached {
			t.Fatal("failed read returned Workspace", e)
		}
		probe.reached = false
		n, e := scoped.GetNote(ctx, member, f.Workspace, note)
		if e == nil || n.ID != "" || n.Text != "" || !probe.reached {
			t.Fatal("failed read returned note", e)
		}
	})
}

// Test-only pre-commit fault wraps real SQL;no simulated resource success exists.
type scopeReadFaultStore struct {
	transaction.Store
	reached bool
}

func (s *scopeReadFaultStore) Within(ctx context.Context, read bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, read, func(ctx context.Context, q transaction.Executor) error {
		if e := fn(ctx, q); e != nil {
			return e
		}
		s.reached = true
		return errors.New("SYNTHETIC_READ_PRE_COMMIT_FAILURE")
	})
}

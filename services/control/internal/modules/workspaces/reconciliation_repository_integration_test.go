//go:build repository_integration

package workspaces_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualWorkspaceOperationReceipts(t *testing.T) {
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	accounts := &identity.Service{Storage: f.Store}
	s := &workspaces.Service{Identity: accounts}
	exec := func(t *testing.T, pg, my string, args ...any) {
		t.Helper()
		if _, e := f.Store.Exec(ctx, f.Statement(pg, my, args...)); e != nil {
			t.Fatal(e)
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
	admin := issue(t, f.User, "admin")
	portal := issue(t, f.User, "portal")
	other := uuid.NewString()
	exec(t, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,$2,$3,'Receipt actor','en','active')", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES(?,?,?,'Receipt actor','en','active')", other, other+"@example.invalid", other+"@example.invalid")
	// Each vendor owns its explicit statement and bound parameters.
	owner := issue(t, other, "portal")
	assert := func(t *testing.T, a identity.Auth, request, action, target string) workspaces.OperationReceipt {
		t.Helper()
		out, e := s.Operation(ctx, a, request)
		if e != nil || out.RequestID != request || out.State != workspaces.OperationCommitted || out.Event == nil || out.Event.Action != action || out.Event.TargetID != target {
			t.Fatalf("receipt %s: %+v %v", action, out, e)
		}
		return out
	}
	missing := func(t *testing.T, a identity.Auth, request string) {
		t.Helper()
		out, e := s.Operation(ctx, a, request)
		if e != nil || out.RequestID != request || out.State != workspaces.OperationUnconfirmed || out.Event != nil {
			t.Fatal("receipt isolation or absence", out, e)
		}
	}
	request := uuid.NewString()
	w, e := s.Create(ctx, admin, "Receipt synthetic workspace", other, request)
	if e != nil {
		t.Fatal(e)
	}
	assert(t, admin, request, "workspace.created", w.ID)
	missing(t, portal, request)
	missing(t, owner, request)
	missing(t, admin, uuid.NewString())

	t.Run("all_actual_mutations_and_self_removal", func(t *testing.T) {
		for _, step := range []struct{ name, status, action string }{{"Receipt renamed", "active", "workspace.updated"}, {"Receipt renamed", "disabled", "workspace.archived"}, {"Receipt renamed", "active", "workspace.restored"}} {
			request = uuid.NewString()
			w, e = s.Update(ctx, admin, w.ID, step.name, step.status, w.Version, "Synthetic reconciliation", request)
			if e != nil {
				t.Fatal(e)
			}
			assert(t, admin, request, step.action, w.ID)
		}
		request = uuid.NewString()
		m, e := s.AddMember(ctx, owner, w.ID, admin.Me.User.Email, "owner", request)
		if e != nil {
			t.Fatal(e)
		}
		assert(t, owner, request, "workspace.member_added", f.User)
		request = uuid.NewString()
		m, e = s.ChangeMember(ctx, owner, w.ID, f.User, "viewer", m.Version, "Synthetic role", request, false)
		if e != nil {
			t.Fatal(e)
		}
		assert(t, owner, request, "workspace.member_updated", f.User)
		request = uuid.NewString()
		n, e := s.CreateNote(ctx, owner, w.ID, "Receipt note", "Synthetic text", request)
		if e != nil {
			t.Fatal(e)
		}
		assert(t, owner, request, "workspace.note_created", n.ID)
		request = uuid.NewString()
		n, e = s.ChangeNote(ctx, owner, w.ID, n.ID, "Receipt revised", "Synthetic changed", n.Version, request, false)
		if e != nil {
			t.Fatal(e)
		}
		assert(t, owner, request, "workspace.note_updated", n.ID)
		request = uuid.NewString()
		if _, e = s.ChangeNote(ctx, owner, w.ID, n.ID, "", "", n.Version, request, true); e != nil {
			t.Fatal(e)
		}
		assert(t, owner, request, "workspace.note_deleted", n.ID)
		// Promote a second Owner, then let the original Owner remove itself.
		if _, e = s.ChangeMember(ctx, owner, w.ID, f.User, "owner", m.Version, "Synthetic second Owner", uuid.NewString(), false); e != nil {
			t.Fatal(e)
		}
		var ownVersion int64
		if e = f.Store.QueryRow(ctx, f.Statement("SELECT version FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", "SELECT version FROM workspace_memberships WHERE workspace_id=? AND user_id=?", w.ID, other)).Scan(&ownVersion); e != nil {
			t.Fatal(e)
		}
		request = uuid.NewString()
		if _, e = s.ChangeMember(ctx, owner, w.ID, other, "", ownVersion, "Synthetic self removal", request, true); e != nil {
			t.Fatal(e)
		}
		assert(t, owner, request, "workspace.member_removed", other)
		if _, e = s.Get(ctx, owner, w.ID, false); e == nil {
			t.Fatal("receipt granted removed membership")
		}
		missing(t, admin, request)
	})

	t.Run("current_authority_and_duplicate_corruption_fail_closed", func(t *testing.T) {
		exec(t, "UPDATE tenants SET status='disabled' WHERE id=$1", "UPDATE tenants SET status='disabled' WHERE id=?", f.Tenant)
		out, e := s.Operation(ctx, admin, request)
		if e == nil || e.Error() != "SERVICE_NOT_READY" || out != (workspaces.OperationReceipt{}) {
			t.Fatal("inactive tenant", out, e)
		}
		exec(t, "UPDATE tenants SET status='active' WHERE id=$1", "UPDATE tenants SET status='active' WHERE id=?", f.Tenant)
		exec(t, "UPDATE users SET status='disabled' WHERE id=$1", "UPDATE users SET status='disabled' WHERE id=?", f.User)
		out, e = s.Operation(ctx, admin, request)
		if e == nil || out != (workspaces.OperationReceipt{}) {
			t.Fatal("inactive actor", out, e)
		}
		exec(t, "UPDATE users SET status='active' WHERE id=$1", "UPDATE users SET status='active' WHERE id=?", f.User)
		// A privileged synthetic insert creates two receipts, never a fake success.
		request = uuid.NewString()
		for range 2 {
			exec(t, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,'admin','workspace.updated','success','workspace',$5,$6)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'admin','workspace.updated','success','workspace',?,?)`, uuid.NewString(), f.User, f.Tenant, w.ID, w.ID, request)
		}
		out, e = s.Operation(ctx, admin, request)
		if e == nil || e.Error() != "STORAGE_UNAVAILABLE" || out != (workspaces.OperationReceipt{}) {
			t.Fatal("partial corrupt receipt", out, e)
		}
	})
}

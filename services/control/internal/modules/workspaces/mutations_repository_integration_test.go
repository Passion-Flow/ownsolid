//go:build repository_integration

package workspaces_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualWorkspaceMutationRepository(t *testing.T) {
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	accounts := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		accounts.Storage = nil
		accounts.DB = f.PostgreSQL
	}
	s := &workspaces.Service{Identity: accounts, DB: f.PostgreSQL}
	exec := func(t *testing.T, pg, my string, args ...any) {
		t.Helper()
		if _, e := f.Store.Exec(ctx, f.Statement(pg, my, args...)); e != nil {
			t.Fatal(e)
		}
	}
	issue := func(t *testing.T, id, audience string) identity.Auth {
		t.Helper()
		var issued identity.IssuedSession
		if e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			issued, e = accounts.IssueSessionWithin(ctx, q, id, 1, audience, "", uuid.NewString(), nil)
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
	// Keep argument lists identical across explicit vendor statements.
	newActor := func(t *testing.T, platformAdmin bool) identity.Auth {
		t.Helper()
		id, address := uuid.NewString(), "synthetic+"+uuid.NewString()+"@example.invalid"
		exec(t, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Mutation actor','en','active',$4)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Mutation actor','en','active',?)", id, address, address, platformAdmin)
		audience := "portal"
		if platformAdmin {
			audience = "admin"
		}
		return issue(t, id, audience)
	}
	want := func(t *testing.T, e error, code string) {
		t.Helper()
		if e == nil || e.Error() != code {
			t.Fatalf("wanted %s,got %v", code, e)
		}
	}
	admin, otherAdmin := issue(t, f.User, "admin"), newActor(t, true)
	alice, bob, viewer := newActor(t, false), newActor(t, false), newActor(t, false)
	create := func(t *testing.T, name string) workspaces.Workspace {
		t.Helper()
		w, e := s.Create(ctx, admin, name, alice.Me.User.ID, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		return w
	}
	digest := func(t *testing.T) string {
		t.Helper()
		parts := []string{}
		for _, pair := range [][2]string{
			{"SELECT id::text||':'||tenant_id::text||':'||name||':'||status||':'||version::text||':'||updated_at::text FROM workspaces ORDER BY id", "SELECT CONCAT(id,':',tenant_id,':',name,':',status,':',version,':',updated_at) FROM workspaces ORDER BY id"},
			{"SELECT user_id::text||':'||tenant_id::text||':'||workspace_id::text||':'||role||':'||version::text||':'||authority_id::text FROM workspace_memberships ORDER BY workspace_id,user_id", "SELECT CONCAT(user_id,':',tenant_id,':',workspace_id,':',role,':',version,':',authority_id) FROM workspace_memberships ORDER BY workspace_id,user_id"},
			{"SELECT id::text||':'||workspace_id::text||':'||title||':'||text||':'||version::text||':'||COALESCE(deleted_at::text,'')||':'||updated_at::text FROM workspace_notes ORDER BY id", "SELECT CONCAT(id,':',workspace_id,':',title,':',text,':',version,':',COALESCE(CAST(deleted_at AS CHAR),''),':',updated_at) FROM workspace_notes ORDER BY id"},
			{"SELECT id::text||':'||action||':'||request_id::text FROM audit_events ORDER BY id", "SELECT CONCAT(id,':',action,':',request_id) FROM audit_events ORDER BY id"},
		} {
			rows, e := f.Store.Query(ctx, f.Statement(pair[0], pair[1]))
			if e != nil {
				t.Fatal(e)
			}
			for rows.Next() {
				var value string
				if e = rows.Scan(&value); e != nil {
					rows.Close()
					t.Fatal(e)
				}
				parts = append(parts, value)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				t.Fatal(e)
			}
		}
		raw, e := json.Marshal(parts)
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	t.Run("lifecycle_normalization_exact_uniqueness_default_and_archive_preservation", func(t *testing.T) {
		w := create(t, "  Cafe\u0301 synthetic  ")
		if w.Name != "Caf\u00e9 synthetic" || w.TenantID != f.Tenant || w.Version != 1 {
			t.Fatal("created projection", w)
		}
		_, e := s.Create(ctx, otherAdmin, "Caf\u00e9 synthetic", bob.Me.User.ID, uuid.NewString())
		want(t, e, "WORKSPACE_EXISTS")
		if _, e = s.Create(ctx, otherAdmin, "CAF\u00c9 synthetic", bob.Me.User.ID, uuid.NewString()); e != nil {
			t.Fatal("binary exact name semantics", e)
		}
		if _, e = s.Get(ctx, alice, w.ID, true); e != nil {
			t.Fatal("initial owner", e)
		}
		if _, e = s.Create(ctx, alice, "Portal must not create", alice.Me.User.ID, uuid.NewString()); e == nil {
			t.Fatal("portal create escaped")
		}
		w, e = s.Update(ctx, alice, w.ID, "Owner renamed", "active", w.Version, "Synthetic owner rename", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.Update(ctx, admin, w.ID, w.Name, "active", 1, "Stale version", uuid.NewString())
		want(t, e, "VERSION_CONFLICT")
		note, e := s.CreateNote(ctx, alice, w.ID, "Preserved", "Synthetic durable text", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		w, e = s.Update(ctx, admin, w.ID, w.Name, "disabled", w.Version, "Synthetic archive", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.GetNote(ctx, alice, w.ID, note.ID)
		want(t, e, "RESOURCE_NOT_FOUND")
		w, e = s.Update(ctx, admin, w.ID, w.Name, "active", w.Version, "Synthetic restore", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		restored, e := s.GetNote(ctx, alice, w.ID, note.ID)
		if e != nil || restored.Text != note.Text {
			t.Fatal("archive lost data", e)
		}
		original, e := s.Get(ctx, admin, f.Workspace, false)
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.Update(ctx, admin, f.Workspace, original.Name, "disabled", original.Version, "Protected default", uuid.NewString())
		want(t, e, "DEFAULT_WORKSPACE_PROTECTED")
	})
	t.Run("members_lifetime_CAS_disabled_target_and_two_admin_last_owner_race", func(t *testing.T) {
		w := create(t, "Membership mutation")
		initial := ""
		if e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			initial, e = s.MembershipAuthorityWithin(ctx, q, w.TenantID, w.ID, alice.Me.User.ID)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		m, e := s.AddMember(ctx, admin, w.ID, bob.Me.User.Email, "owner", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.AddMember(ctx, admin, w.ID, bob.Me.User.Email, "member", uuid.NewString())
		want(t, e, "MEMBERSHIP_EXISTS")
		outcome := make(chan error, 2)
		var wait sync.WaitGroup
		for i, id := range []string{alice.Me.User.ID, bob.Me.User.ID} {
			wait.Add(1)
			go func(index int, user string) {
				defer wait.Done()
				a := admin
				if index == 1 {
					a = otherAdmin
				}
				_, e := s.ChangeMember(ctx, a, w.ID, user, "member", 1, "Concurrent distinct-admin demotion", uuid.NewString(), false)
				outcome <- e
			}(i, id)
		}
		wait.Wait()
		close(outcome)
		success, protected := 0, 0
		for e := range outcome {
			if e == nil {
				success++
			} else if e.Error() == "LAST_WORKSPACE_OWNER" {
				protected++
			} else {
				t.Fatal(e)
			}
		}
		if success != 1 || protected != 1 {
			t.Fatal("final Owner escaped", success, protected)
		}
		var owners int
		if e = f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM workspace_memberships WHERE workspace_id=$1 AND role='owner'", "SELECT count(*) FROM workspace_memberships WHERE workspace_id=? AND role='owner'", w.ID)).Scan(&owners); e != nil || owners != 1 {
			t.Fatal(owners, e)
		}
		current, e := s.Members(ctx, admin, w.ID, nil)
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range current.Items {
			if entry.Role == "member" {
				_, e = s.ChangeMember(ctx, admin, w.ID, entry.UserID, "viewer", entry.Version-1, "Stale membership", uuid.NewString(), false)
				want(t, e, "VERSION_CONFLICT")
				before := ""
				if e = f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
					var e error
					before, e = s.MembershipAuthorityWithin(ctx, q, w.TenantID, w.ID, entry.UserID)
					return e
				}); e != nil {
					t.Fatal(e)
				}
				entry, e = s.ChangeMember(ctx, admin, w.ID, entry.UserID, "viewer", entry.Version, "Preserve lifetime", uuid.NewString(), false)
				if e != nil {
					t.Fatal(e)
				}
				same := ""
				if e = f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
					var e error
					same, e = s.MembershipAuthorityWithin(ctx, q, w.TenantID, w.ID, entry.UserID)
					return e
				}); e != nil || same != before {
					t.Fatal("role changed lifetime", e)
				}
				if _, e = s.ChangeMember(ctx, admin, w.ID, entry.UserID, "", entry.Version, "Remove membership", uuid.NewString(), true); e != nil {
					t.Fatal(e)
				}
				email := alice.Me.User.Email
				if entry.UserID == bob.Me.User.ID {
					email = bob.Me.User.Email
				}
				if _, e = s.AddMember(ctx, admin, w.ID, email, "member", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
				after := ""
				if e = f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
					var e error
					after, e = s.MembershipAuthorityWithin(ctx, q, w.TenantID, w.ID, entry.UserID)
					return e
				}); e != nil || after == before {
					t.Fatal("readded lifetime reused", e)
				}
			}
		}
		_ = initial
		_ = m
		disabled := newActor(t, false)
		if _, e = accounts.SetStatus(ctx, admin, disabled.Me.User.ID, "disabled", disabled.Me.User.Version, "Synthetic disabled target", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		_, e = s.AddMember(ctx, admin, w.ID, disabled.Me.User.Email, "member", uuid.NewString())
		want(t, e, "RESOURCE_NOT_FOUND")
	})
	t.Run("notes_roles_exact_scope_Unicode_byte_bound_tombstone_and_version_exhaustion", func(t *testing.T) {
		w := create(t, "Notes mutation")
		other := create(t, "Other notes mutation")
		for _, entry := range []struct {
			actor identity.Auth
			role  string
		}{{bob, "member"}, {viewer, "viewer"}} {
			if _, e := s.AddMember(ctx, admin, w.ID, entry.actor.Me.User.Email, entry.role, uuid.NewString()); e != nil {
				t.Fatal(e)
			}
		}
		text := strings.Repeat("\u6d4b", 2730) + "xx"
		n, e := s.CreateNote(ctx, bob, w.ID, "Unicode note", text, uuid.NewString())
		if e != nil || n.Text != text || n.AuthorUserID != bob.Me.User.ID {
			t.Fatal("exact byte boundary", e)
		}
		if _, e = s.CreateNote(ctx, viewer, w.ID, "Forbidden", "text", uuid.NewString()); e == nil {
			t.Fatal("viewer mutated")
		}
		_, e = s.ChangeNote(ctx, alice, other.ID, n.ID, "Wrong scope", "text", n.Version, uuid.NewString(), false)
		want(t, e, "RESOURCE_NOT_FOUND")
		_, e = s.ChangeNote(ctx, bob, w.ID, n.ID, "Stale", "text", n.Version+1, uuid.NewString(), false)
		want(t, e, "VERSION_CONFLICT")
		n, e = s.ChangeNote(ctx, bob, w.ID, n.ID, "Edited", "SYNTHETIC_NOTE_CONTENT_NOT_AUDITED", n.Version, uuid.NewString(), false)
		if e != nil {
			t.Fatal(e)
		}
		exec(t, "UPDATE workspace_notes SET version=$1 WHERE id=$2", "UPDATE workspace_notes SET version=? WHERE id=?", workspaces.MaximumVersion, n.ID)
		_, e = s.ChangeNote(ctx, bob, w.ID, n.ID, "Exhausted", "text", workspaces.MaximumVersion, uuid.NewString(), false)
		want(t, e, "RESOURCE_LIMIT_REACHED")
		fresh, e := s.CreateNote(ctx, bob, w.ID, "Delete note", "tombstone text", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.ChangeNote(ctx, bob, w.ID, fresh.ID, "", "", fresh.Version, uuid.NewString(), true); e != nil {
			t.Fatal(e)
		}
		_, e = s.GetNote(ctx, bob, w.ID, fresh.ID)
		want(t, e, "RESOURCE_NOT_FOUND")
		exec(t, "UPDATE workspaces SET version=$1 WHERE id=$2", "UPDATE workspaces SET version=? WHERE id=?", workspaces.MaximumVersion, other.ID)
		_, e = s.Update(ctx, admin, other.ID, other.Name, "active", workspaces.MaximumVersion, "Exhausted counter", uuid.NewString())
		want(t, e, "RESOURCE_LIMIT_REACHED")
		_, e = s.AddMember(ctx, admin, other.ID, bob.Me.User.Email, "viewer", uuid.NewString())
		want(t, e, "RESOURCE_LIMIT_REACHED")
	})
	t.Run("foreign_tenant_and_current_stale_admin_authority", func(t *testing.T) {
		tenant, foreign := uuid.NewString(), uuid.NewString()
		exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Foreign synthetic tenant','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Foreign synthetic tenant','active')", tenant)
		exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign synthetic Workspace','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Foreign synthetic Workspace','active')", foreign, tenant)
		before := digest(t)
		checks := []func() error{
			func() error {
				_, e := s.Update(ctx, admin, foreign, "Foreign", "active", 1, "Foreign deny", uuid.NewString())
				return e
			},
			func() error {
				_, e := s.AddMember(ctx, admin, foreign, alice.Me.User.Email, "owner", uuid.NewString())
				return e
			},
			func() error {
				_, e := s.ChangeMember(ctx, admin, foreign, alice.Me.User.ID, "owner", 1, "Foreign deny", uuid.NewString(), false)
				return e
			},
			func() error {
				_, e := s.CreateNote(ctx, admin, foreign, "Foreign", "Synthetic", uuid.NewString())
				return e
			},
			func() error {
				_, e := s.ChangeNote(ctx, admin, foreign, uuid.NewString(), "Foreign", "Synthetic", 1, uuid.NewString(), false)
				return e
			},
		}
		for _, run := range checks {
			want(t, run(), "RESOURCE_NOT_FOUND")
		}
		if digest(t) != before {
			t.Fatal("foreign mutation left state")
		}
		for _, disabled := range []bool{false, true} {
			stale := newActor(t, true)
			if disabled {
				if _, e := accounts.SetStatus(ctx, admin, stale.Me.User.ID, "disabled", stale.Me.User.Version, "Disable synthetic actor", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
			} else {
				exec(t, "UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", stale.Me.User.ID)
			}
			before = digest(t)
			out, e := s.Create(ctx, stale, "Stale Admin must fail", alice.Me.User.ID, uuid.NewString())
			want(t, e, "AUTH_SESSION_EXPIRED")
			if out.ID != "" || digest(t) != before {
				t.Fatal("stale Admin mutated state")
			}
		}
	})
	t.Run("distinct_actor_quota_races_and_recovery", func(t *testing.T) {
		var count int
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM workspaces WHERE tenant_id=$1", "SELECT count(*) FROM workspaces WHERE tenant_id=?", f.Tenant)).Scan(&count); e != nil {
			t.Fatal(e)
		}
		policy := config.DefaultRuntimePolicy()
		policy.Workspaces.MaximumCount = count + 1
		policy.Workspaces.MaximumLiveNotes = 1
		accounts.Config.Runtime = &policy
		defer func() { accounts.Config.Runtime = nil }()
		type made struct {
			w workspaces.Workspace
			e error
		}
		results := make(chan made, 2)
		for i, a := range []identity.Auth{admin, otherAdmin} {
			go func(index int, a identity.Auth) {
				owner := alice.Me.User.ID
				if index == 1 {
					owner = bob.Me.User.ID
				}
				w, e := s.Create(ctx, a, fmt.Sprintf("Quota candidate %d", index), owner, uuid.NewString())
				results <- made{w, e}
			}(i, a)
		}
		var w workspaces.Workspace
		success := 0
		for i := 0; i < 2; i++ {
			r := <-results
			if r.e == nil {
				w = r.w
				success++
			} else {
				want(t, r.e, "RESOURCE_LIMIT_REACHED")
				if r.w.ID != "" {
					t.Fatal("partial quota result")
				}
			}
		}
		if success != 1 {
			t.Fatal("installation count raced")
		}
		policy.Workspaces.MaximumCount = count + 10
		if _, e := s.AddMember(ctx, admin, w.ID, viewer.Me.User.Email, "member", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		owner := alice
		if w.Role == nil { // Resolve actual initial owner without trusting the Admin's optional role.
			var id string
			if e := f.Store.QueryRow(ctx, f.Statement("SELECT user_id::text FROM workspace_memberships WHERE workspace_id=$1 AND role='owner'", "SELECT user_id FROM workspace_memberships WHERE workspace_id=? AND role='owner'", w.ID)).Scan(&id); e != nil {
				t.Fatal(e)
			}
			if id == bob.Me.User.ID {
				owner = bob
			}
		}
		type noted struct {
			n workspaces.Note
			e error
		}
		notes := make(chan noted, 2)
		for i, a := range []identity.Auth{owner, viewer} {
			go func(index int, a identity.Auth) {
				n, e := s.CreateNote(ctx, a, w.ID, fmt.Sprintf("Quota note %d", index), "Synthetic", uuid.NewString())
				notes <- noted{n, e}
			}(i, a)
		}
		var n workspaces.Note
		success = 0
		for i := 0; i < 2; i++ {
			r := <-notes
			if r.e == nil {
				n = r.n
				success++
			} else {
				want(t, r.e, "RESOURCE_LIMIT_REACHED")
			}
		}
		if success != 1 {
			t.Fatal("live note count raced")
		}
		if _, e := s.ChangeNote(ctx, owner, w.ID, n.ID, "", "", n.Version, uuid.NewString(), true); e != nil {
			t.Fatal(e)
		}
		if _, e := s.CreateNote(ctx, owner, w.ID, "Recovered note", "Synthetic", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		accounts.Config.Runtime = nil
	})
	t.Run("real_waiting_membership_archive_default_and_tenant_fences", func(t *testing.T) {
		for _, mode := range []string{"downgrade", "remove", "archive"} {
			w := create(t, "Waiting "+mode)
			progress := create(t, "Parallel "+mode)
			if _, e := s.AddMember(ctx, admin, w.ID, bob.Me.User.Email, "member", uuid.NewString()); e != nil {
				t.Fatal(e)
			}
			result := make(chan error, 1)
			probe := &mutationLockProbe{Store: f.Store}
			copy := *accounts
			copy.DB = nil
			copy.Storage = probe
			waitingService := &workspaces.Service{Identity: &copy}
			if e := f.Store.Within(ctx, false, func(txctx context.Context, q transaction.Executor) error {
				var locked string
				if e := q.QueryRow(txctx, f.Statement("SELECT id::text FROM workspaces WHERE id=$1 FOR UPDATE", "SELECT id FROM workspaces WHERE id=? FOR UPDATE", w.ID)).Scan(&locked); e != nil {
					return e
				}
				// Another Workspace can progress while this resource's fence is held.
				if note, e := s.CreateNote(ctx, alice, progress.ID, "Other progress", "Synthetic", uuid.NewString()); e != nil || note.ID == "" {
					return fmt.Errorf("unrelated Workspace did not progress: %v", e)
				}
				go func() {
					n, e := waitingService.CreateNote(ctx, bob, w.ID, "Waiting write", "Must not persist", uuid.NewString())
					if n.ID != "" {
						result <- errors.New("PARTIAL_WAITING_RESULT")
						return
					}
					result <- e
				}()
				select {
				case e := <-result:
					return fmt.Errorf("write escaped held Workspace: %v", e)
				case <-time.After(120 * time.Millisecond):
				}
				switch mode {
				case "downgrade":
					_, e := q.Exec(txctx, f.Statement("UPDATE workspace_memberships SET role='viewer',version=version+1 WHERE workspace_id=$1 AND user_id=$2", "UPDATE workspace_memberships SET role='viewer',version=version+1 WHERE workspace_id=? AND user_id=?", w.ID, bob.Me.User.ID))
					return e
				case "remove":
					_, e := q.Exec(txctx, f.Statement("DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", "DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?", w.ID, bob.Me.User.ID))
					return e
				default:
					_, e := q.Exec(txctx, f.Statement("UPDATE workspaces SET status='disabled',version=version+1 WHERE id=$1", "UPDATE workspaces SET status='disabled',version=version+1 WHERE id=?", w.ID))
					return e
				}
			}); e != nil {
				t.Fatal(e)
			}
			code := "RESOURCE_NOT_FOUND"
			if mode == "downgrade" {
				code = "ACCESS_DENIED"
			}
			observed := <-result
			if observed == nil || observed.Error() != code {
				t.Logf("SAFE_WAITING_STORAGE_DIAGNOSIS %s", probe.failure)
			}
			want(t, observed, code)
		}
		replacement := create(t, "Waiting default")
		defaultResult := make(chan error, 1)
		if e := f.Store.Within(ctx, false, func(txctx context.Context, q transaction.Executor) error {
			if _, e := q.Exec(txctx, f.Statement("UPDATE installation_state SET default_workspace_id=$1 WHERE id=1", "UPDATE installation_state SET default_workspace_id=? WHERE id=1", replacement.ID)); e != nil {
				return e
			}
			go func() {
				out, e := s.Update(ctx, admin, replacement.ID, replacement.Name, "disabled", replacement.Version, "Wait for current default", uuid.NewString())
				if out.ID != "" {
					defaultResult <- errors.New("PARTIAL_DEFAULT_RESULT")
					return
				}
				defaultResult <- e
			}()
			select {
			case e := <-defaultResult:
				return fmt.Errorf("archive escaped default fence: %v", e)
			case <-time.After(120 * time.Millisecond):
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		want(t, <-defaultResult, "DEFAULT_WORKSPACE_PROTECTED")
		exec(t, "UPDATE installation_state SET default_workspace_id=$1 WHERE id=1", "UPDATE installation_state SET default_workspace_id=? WHERE id=1", f.Workspace)
		w := create(t, "Tenant fenced")
		result := make(chan error, 1)
		if e := f.Store.Within(ctx, false, func(txctx context.Context, q transaction.Executor) error {
			if _, e := q.Exec(txctx, f.Statement("UPDATE tenants SET status='disabled' WHERE id=$1", "UPDATE tenants SET status='disabled' WHERE id=?", f.Tenant)); e != nil {
				return e
			}
			go func() {
				_, e := s.CreateNote(ctx, alice, w.ID, "Tenant disabled", "Synthetic", uuid.NewString())
				result <- e
			}()
			select {
			case e := <-result:
				return fmt.Errorf("tenant write escaped disable fence: %v", e)
			case <-time.After(120 * time.Millisecond):
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		want(t, <-result, "SERVICE_NOT_READY")
		exec(t, "UPDATE tenants SET status='active' WHERE id=$1", "UPDATE tenants SET status='active' WHERE id=?", f.Tenant)
	})
	t.Run("each_durable_step_precommit_cancel_and_commit_unknown_returns_no_object", func(t *testing.T) {
		w := create(t, "Atomic mutations")
		m, e := s.AddMember(ctx, admin, w.ID, bob.Me.User.Email, "member", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		n, e := s.CreateNote(ctx, alice, w.ID, "Atomic note", "Synthetic", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		operations := []struct {
			name   string
			writes int
			run    func(*workspaces.Service) error
		}{
			{"create", 3, func(s *workspaces.Service) error {
				v, e := s.Create(ctx, admin, "Must roll back", alice.Me.User.ID, uuid.NewString())
				if e != nil && v.ID != "" {
					return errors.New("PARTIAL_RESULT")
				}
				return e
			}},
			{"update", 2, func(s *workspaces.Service) error {
				v, e := s.Update(ctx, admin, w.ID, w.Name, "active", w.Version, "Atomic update", uuid.NewString())
				if e != nil && v.ID != "" {
					return errors.New("PARTIAL_RESULT")
				}
				return e
			}},
			{"add", 3, func(s *workspaces.Service) error {
				v, e := s.AddMember(ctx, admin, w.ID, viewer.Me.User.Email, "viewer", uuid.NewString())
				if e != nil && v.UserID != "" {
					return errors.New("PARTIAL_RESULT")
				}
				return e
			}},
			{"member", 3, func(s *workspaces.Service) error {
				v, e := s.ChangeMember(ctx, admin, w.ID, bob.Me.User.ID, "viewer", m.Version, "Atomic member", uuid.NewString(), false)
				if e != nil && v.UserID != "" {
					return errors.New("PARTIAL_RESULT")
				}
				return e
			}},
			{"remove", 3, func(s *workspaces.Service) error {
				_, e := s.ChangeMember(ctx, admin, w.ID, bob.Me.User.ID, "", m.Version, "Atomic removal", uuid.NewString(), true)
				return e
			}},
			{"note", 2, func(s *workspaces.Service) error {
				v, e := s.CreateNote(ctx, alice, w.ID, "Must roll back", "Synthetic", uuid.NewString())
				if e != nil && v.ID != "" {
					return errors.New("PARTIAL_RESULT")
				}
				return e
			}},
			{"edit", 2, func(s *workspaces.Service) error {
				v, e := s.ChangeNote(ctx, alice, w.ID, n.ID, "Must roll back", "Synthetic", n.Version, uuid.NewString(), false)
				if e != nil && v.ID != "" {
					return errors.New("PARTIAL_RESULT")
				}
				return e
			}},
			{"delete", 2, func(s *workspaces.Service) error {
				_, e := s.ChangeNote(ctx, alice, w.ID, n.ID, "", "", n.Version, uuid.NewString(), true)
				return e
			}},
		}
		// Membership changes above increment the Workspace version before this snapshot.
		w, e = s.Get(ctx, admin, w.ID, false)
		if e != nil {
			t.Fatal(e)
		}
		before := digest(t)
		for _, op := range operations {
			for point := 0; point <= op.writes; point++ {
				fault := &mutationFaultStore{Store: f.Store, failAt: point}
				copy := *accounts
				copy.DB = nil
				copy.Storage = fault
				wanted := point
				code := "SYNTHETIC_DURABLE_STEP_FAILURE"
				if point == 0 {
					wanted = op.writes
					code = "SYNTHETIC_PRE_COMMIT_FAILURE"
				}
				if e := op.run(&workspaces.Service{Identity: &copy}); e == nil || e.Error() != code || fault.observed != wanted {
					t.Fatalf("%s point %d: observed %d, %v", op.name, point, fault.observed, e)
				}
				if digest(t) != before {
					t.Fatalf("%s point %d left durable state", op.name, point)
				}
			}
		}
		fault := &mutationFaultStore{Store: f.Store, commitUnknown: true}
		copy := *accounts
		copy.DB = nil
		copy.Storage = fault
		_, e = (&workspaces.Service{Identity: &copy}).CreateNote(ctx, alice, w.ID, "Unknown outcome", "Synthetic", uuid.NewString())
		want(t, e, "STORAGE_COMMIT_OUTCOME_UNKNOWN")
		if digest(t) != before {
			t.Fatal("precommit contract fixture changed state")
		}
		cancelled, stop := context.WithCancel(ctx)
		stop()
		v, e := s.CreateNote(cancelled, alice, w.ID, "Cancelled", "Synthetic", uuid.NewString())
		if e == nil || v.ID != "" || digest(t) != before {
			t.Fatal("cancelled result", e)
		}
	})
	t.Run("real_audit_failure_and_scoped_content_free_events", func(t *testing.T) {
		w := create(t, "Actual audit failure")
		before := digest(t)
		if f.Store.Dialect() == transaction.PostgreSQL {
			exec(t, `CREATE FUNCTION reject_mutation_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic audit rejection'; END$$`, "unused")
			exec(t, `CREATE TRIGGER reject_mutation_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_mutation_audit()`, "unused")
		} else {
			exec(t, "unused", `CREATE TRIGGER reject_mutation_audit BEFORE INSERT ON audit_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic audit rejection'`)
		}
		v, e := s.CreateNote(ctx, alice, w.ID, "Must not persist", "SYNTHETIC_NOTE_CONTENT_NOT_AUDITED", uuid.NewString())
		if e == nil || v.ID != "" || digest(t) != before {
			t.Fatal("actual audit failure escaped", e)
		}
		exec(t, "DROP TRIGGER reject_mutation_audit ON audit_events", "DROP TRIGGER reject_mutation_audit")
		if _, e = s.CreateNote(ctx, alice, w.ID, "Recovered", "SYNTHETIC_NOTE_CONTENT_NOT_AUDITED", uuid.NewString()); e != nil {
			t.Fatal("audit repair", e)
		}
		rows, e := f.Store.Query(ctx, f.Statement("SELECT tenant_id::text,workspace_id::text,actor_user_id::text,target_id::text,reason FROM audit_events WHERE action='workspace.note_created'", "SELECT tenant_id,workspace_id,actor_user_id,target_id,reason FROM audit_events WHERE action='workspace.note_created'"))
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		for rows.Next() {
			var tenant, workspace, actor, target string
			var reason *string
			if e = rows.Scan(&tenant, &workspace, &actor, &target, &reason); e != nil {
				t.Fatal(e)
			}
			if tenant != f.Tenant || workspace == "" || actor == "" || target == "" || reason != nil && strings.Contains(*reason, "SYNTHETIC_NOTE_CONTENT_NOT_AUDITED") {
				t.Fatal("invalid scoped audit")
			}
		}
		if rows.Err() != nil {
			t.Fatal(rows.Err())
		}
	})
}

// Test-only faults surround real vendor SQL and rollback; commitUnknown checks
// conservative error propagation, not real network commit-ACK-loss qualification.
type mutationFaultStore struct {
	transaction.Store
	failAt, observed int
	commitUnknown    bool
}
type mutationFaultExecutor struct {
	transaction.Executor
	writes int
	failAt int
}

func (q *mutationFaultExecutor) Exec(ctx context.Context, s transaction.Statement) (int64, error) {
	q.writes++
	if q.failAt > 0 && q.writes == q.failAt {
		return 0, errors.New("SYNTHETIC_DURABLE_STEP_FAILURE")
	}
	return q.Executor.Exec(ctx, s)
}
func (s *mutationFaultStore) Within(ctx context.Context, read bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, read, func(ctx context.Context, q transaction.Executor) error {
		wrapped := &mutationFaultExecutor{Executor: q, failAt: s.failAt}
		defer func() {
			if wrapped.writes > 0 {
				s.observed = wrapped.writes
			}
		}()
		if e := fn(ctx, wrapped); e != nil {
			return e
		}
		if wrapped.writes > 0 {
			if s.commitUnknown {
				return &transaction.Error{Kind: transaction.CommitUnknown}
			}
			return errors.New("SYNTHETIC_PRE_COMMIT_FAILURE")
		}
		return nil
	})
}

// Test-only observer records sanitized storage classification and query position;
// it forwards actual SQL unchanged and fabricates no result or success.
type mutationLockProbe struct {
	transaction.Store
	failure string
}
type mutationLockExecutor struct {
	transaction.Executor
	probe    *mutationLockProbe
	position int
}
type mutationLockRow struct {
	transaction.Row
	probe    *mutationLockProbe
	position int
}

func (q *mutationLockExecutor) QueryRow(ctx context.Context, s transaction.Statement) transaction.Row {
	q.position++
	return mutationLockRow{Row: q.Executor.QueryRow(ctx, s), probe: q.probe, position: q.position}
}
func (r mutationLockRow) Scan(dest ...any) error {
	e := r.Row.Scan(dest...)
	var safe *transaction.Error
	if errors.As(e, &safe) {
		r.probe.failure = fmt.Sprintf("row=%d kind=%s SQLState=%s number=%d", r.position, safe.Kind, safe.SQLState, safe.Number)
	}
	return e
}
func (s *mutationLockProbe) Within(ctx context.Context, read bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, read, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, &mutationLockExecutor{Executor: q, probe: s})
	})
}

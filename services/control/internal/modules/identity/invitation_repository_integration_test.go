//go:build repository_integration

package identity_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualIdentityInvitationRepository(t *testing.T) {
	f := repositoryfixture.NewUnbootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := t.TempDir()
	path := filepath.Join(dir, "synthetic-blocklist")
	if e := os.WriteFile(path, []byte("passwordpasswordpassword\n"), 0600); e != nil {
		t.Fatal(e)
	}
	passwords, e := security.NewPasswords(path)
	if e != nil {
		t.Fatal(e)
	}
	key := make([]byte, security.MinimumHMACKeyBytes)
	if _, e = rand.Read(key); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(mail.Keyring{Active: "synthetic", Keys: map[string]string{"synthetic": base64.StdEncoding.EncodeToString(key)}})
	path = filepath.Join(dir, "synthetic-mail-keyring.json")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	keys, e := mail.LoadKeyring(path)
	if e != nil {
		t.Fatal(e)
	}
	s := &identity.Service{Storage: f.Store, Passwords: passwords, HMACKey: key}
	if f.PostgreSQL != nil {
		s.Storage, s.DB = nil, f.PostgreSQL
	}
	s.Config.Server.PortalOrigin = "https://portal.example.invalid"
	s.Config.Server.AdminOrigin = "https://admin.example.invalid"
	s.Config.SMTP.FromAddress = "platform@example.invalid"
	s.Mail = &mail.Outbox{Storage: f.Store, Keys: keys, Config: s.Config}
	password := "Synthetic invitation phrase " + uuid.NewString()
	email := "synthetic+" + uuid.NewString() + "@example.invalid"
	if e = s.Bootstrap(ctx, email, "Synthetic primary", password, "Synthetic installation", "Synthetic Workspace"); e != nil {
		t.Fatal(e)
	}
	var primary, workspace, phc string
	if e = f.Store.QueryRow(ctx, f.Statement("SELECT u.id::text,i.default_workspace_id::text,c.password_phc FROM users u JOIN password_credentials c ON c.user_id=u.id CROSS JOIN installation_state i WHERE u.email_canonical=$1", "SELECT u.id,i.default_workspace_id,c.password_phc FROM users u JOIN password_credentials c ON c.user_id=u.id CROSS JOIN installation_state i WHERE u.email_canonical=?", email)).Scan(&primary, &workspace, &phc); e != nil {
		t.Fatal(e)
	}
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
	count := func(t *testing.T, query string) int {
		t.Helper()
		var n int
		if e := f.Store.QueryRow(ctx, f.Statement(query, query)).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	_, sessionToken, _, e := s.Login(ctx, email, password, "admin", "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	admin, e := s.Authenticate(ctx, sessionToken, "admin")
	if e != nil {
		t.Fatal(e)
	}
	create := func(t *testing.T) (identity.Invitation, string) {
		t.Helper()
		address := "synthetic+" + uuid.NewString() + "@example.invalid"
		i, e := s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), address, "en", 0, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		return i, address
	}
	token := func(t *testing.T, id string) string {
		t.Helper()
		var delivery, key string
		var nonce, cipher []byte
		e := f.Store.QueryRow(ctx, f.Statement("SELECT id::text,payload_key_id,payload_nonce,payload_ciphertext FROM mail_deliveries WHERE invitation_id=$1 AND state='queued' ORDER BY created_at DESC,id DESC LIMIT 1", "SELECT id,payload_key_id,payload_nonce,payload_ciphertext FROM mail_deliveries WHERE invitation_id=? AND state='queued' ORDER BY created_at DESC,id DESC LIMIT 1", id)).Scan(&delivery, &key, &nonce, &cipher)
		if e != nil {
			t.Fatal(e)
		}
		plain, e := keys.Decrypt(key, delivery, "invitation", nonce, cipher)
		if e != nil {
			t.Fatal(e)
		}
		var p mail.Payload
		if e = json.Unmarshal(plain, &p); e != nil {
			t.Fatal(e)
		}
		_, value, ok := strings.Cut(p.Link, "#token=")
		if !ok || !security.ValidToken(value) || strings.Contains(string(cipher), value) {
			t.Fatal("real encrypted invitation payload")
		}
		return value
	}
	expiry := func(t *testing.T, id string) {
		exec(t, "UPDATE invitations SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", "UPDATE invitations SET created_at=TIMESTAMPADD(HOUR,-2,UTC_TIMESTAMP(6)),expires_at=TIMESTAMPADD(HOUR,-1,UTC_TIMESTAMP(6)) WHERE id=?", id)
	}
	t.Run("real_create_idempotency_original_response_and_expired_key", func(t *testing.T) {
		key, address := uuid.NewString(), "synthetic+"+uuid.NewString()+"@example.invalid"
		first, e := s.Invite(ctx, admin, "", "createInvitation", key, " "+strings.ToUpper(address)+" ", "en", 0, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		before := count(t, "SELECT count(*) FROM mail_deliveries")
		old := token(t, first.ID)
		saved, e := s.RevokeInvitation(ctx, admin, first.ID, first.Version, "Synthetic obsolete", uuid.NewString())
		if e != nil || saved.Status != "revoked" {
			t.Fatal(e)
		}
		replay, e := s.Invite(ctx, admin, "", "createInvitation", key, address, "en", 0, uuid.NewString())
		if e != nil || replay.ID != first.ID || replay.Version != first.Version || replay.Status != "pending" || count(t, "SELECT count(*) FROM mail_deliveries") != before {
			t.Fatal("original idempotent response", e)
		}
		body, _ := json.Marshal(replay)
		if strings.Contains(string(body), old) || strings.Contains(string(body), "ciphertext") || strings.Contains(string(body), "token_hash") {
			t.Fatal("secret in safe response")
		}
		_, e = s.Invite(ctx, admin, "", "createInvitation", key, "other@example.invalid", "en", 0, uuid.NewString())
		want(t, e, "IDEMPOTENCY_CONFLICT")
		exec(t, "UPDATE idempotency_records SET expires_at=clock_timestamp()-interval '1 second',created_at=clock_timestamp()-interval '2 seconds' WHERE idempotency_key=$1", "UPDATE idempotency_records SET expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)),created_at=TIMESTAMPADD(SECOND,-2,UTC_TIMESTAMP(6)) WHERE idempotency_key=?", key)
		fresh, e := s.Invite(ctx, admin, "", "createInvitation", key, address, "en", 0, uuid.NewString())
		if e != nil || fresh.ID == first.ID {
			t.Fatal("expired key renewal", e)
		}
		if _, e = s.ValidateInvitation(ctx, old); e == nil {
			t.Fatal("revoked token revived")
		}
	})
	t.Run("resend_generation_expiry_cancellation_and_state_versions", func(t *testing.T) {
		first, address := create(t)
		old := token(t, first.ID)
		expiry(t, first.ID)
		_, e := s.ValidateInvitation(ctx, old)
		want(t, e, "TOKEN_UNUSABLE")
		replacement, e := s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), address, "en", 0, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		var version int64
		if e = f.Store.QueryRow(ctx, f.Statement("SELECT version FROM invitations WHERE id=$1", "SELECT version FROM invitations WHERE id=?", first.ID)).Scan(&version); e != nil {
			t.Fatal(e)
		}
		_, e = s.Invite(ctx, admin, first.ID, "resendInvitation", uuid.NewString(), "", "", version, uuid.NewString())
		want(t, e, "INVITATION_EXISTS")
		if _, e = s.RevokeInvitation(ctx, admin, replacement.ID, replacement.Version, "Synthetic replacement", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		renewed, e := s.Invite(ctx, admin, first.ID, "resendInvitation", uuid.NewString(), "", "", version, uuid.NewString())
		if e != nil || renewed.ID != first.ID || renewed.Version != version+1 {
			t.Fatal(e)
		}
		fresh := token(t, first.ID)
		if fresh == old {
			t.Fatal("token not rotated")
		}
		_, e = s.ValidateInvitation(ctx, old)
		want(t, e, "TOKEN_UNUSABLE")
		_, e = s.RevokeInvitation(ctx, admin, first.ID, version, "Synthetic stale", uuid.NewString())
		want(t, e, "VERSION_CONFLICT")
		revoked, e := s.RevokeInvitation(ctx, admin, first.ID, renewed.Version, "Synthetic revoke", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.Invite(ctx, admin, first.ID, "resendInvitation", uuid.NewString(), "", "", revoked.Version, uuid.NewString())
		want(t, e, "INVITATION_STATE_CONFLICT")
		var generation, live int
		if e = f.Store.QueryRow(ctx, f.Statement("SELECT token_generation FROM invitations WHERE id=$1", "SELECT token_generation FROM invitations WHERE id=?", first.ID)).Scan(&generation); e != nil || generation != 2 {
			t.Fatal("actual generation", e)
		}
		if e = f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM mail_deliveries WHERE invitation_id=$1 AND (payload_ciphertext IS NOT NULL OR state IN('queued','sending'))", "SELECT count(*) FROM mail_deliveries WHERE invitation_id=? AND (payload_ciphertext IS NOT NULL OR state IN('queued','sending'))", first.ID)).Scan(&live); e != nil || live != 0 {
			t.Fatal("obsolete delivery authority retained", e)
		}
	})
	t.Run("real_registration_policy_normalization_member_and_one_consumption", func(t *testing.T) {
		i, address := create(t)
		value := token(t, i.ID)
		want(t, s.Register(ctx, value, "Member", "short", uuid.NewString()), "PASSWORD_POLICY_VIOLATION")
		want(t, s.Register(ctx, value, "Member", "passwordpasswordpassword", uuid.NewString()), "PASSWORD_BLOCKED")
		exec(t, "UPDATE invitations SET locale='fr' WHERE id=$1", "UPDATE invitations SET locale='fr' WHERE id=?", i.ID)
		before := count(t, "SELECT count(*) FROM sessions")
		start := make(chan struct{})
		result := make(chan error, 2)
		for range 2 {
			go func() { <-start; result <- s.Register(ctx, value, "  Cafe\u0301 \u4e16\u754c  ", password, uuid.NewString()) }()
		}
		close(start)
		wins := 0
		for range 2 {
			if e := <-result; e == nil {
				wins++
			} else {
				want(t, e, "TOKEN_UNUSABLE")
			}
		}
		if wins != 1 || count(t, "SELECT count(*) FROM sessions") != before {
			t.Fatal("registration created duplicate/session")
		}
		var id, name, locale, role, tenant, scope string
		var verified, isAdmin bool
		if e = f.Store.QueryRow(ctx, f.Statement("SELECT u.id::text,u.display_name,u.locale,u.email_verified_at IS NOT NULL,u.is_platform_admin,m.role,m.tenant_id::text,m.workspace_id::text FROM users u JOIN workspace_memberships m ON m.user_id=u.id WHERE u.email_canonical=$1", "SELECT u.id,u.display_name,u.locale,u.email_verified_at IS NOT NULL,u.is_platform_admin,m.role,m.tenant_id,m.workspace_id FROM users u JOIN workspace_memberships m ON m.user_id=u.id WHERE u.email_canonical=?", address)).Scan(&id, &name, &locale, &verified, &isAdmin, &role, &tenant, &scope); e != nil || name != "Caf\u00e9 \u4e16\u754c" || locale != "fr" || !verified || isAdmin || role != "member" || scope != workspace {
			t.Fatal("real persisted verified member", e)
		}
		if _, _, _, e = s.Login(ctx, address, password, "portal", "", uuid.NewString()); e != nil {
			t.Fatal("real registered credential", e)
		}
		_, _, _, e = s.Login(ctx, address, password, "admin", "", uuid.NewString())
		want(t, e, "AUTH_INVALID_CREDENTIALS")
		_, e = s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), address, "en", 0, uuid.NewString())
		want(t, e, "ACCOUNT_EXISTS")
	})
	t.Run("installed_scope_default_change_and_archival_wait", func(t *testing.T) {
		i, _ := create(t)
		value := token(t, i.ID)
		other := uuid.NewString()
		var tenant string
		if e = f.Store.QueryRow(ctx, f.Statement("SELECT tenant_id::text FROM installation_state", "SELECT tenant_id FROM installation_state")).Scan(&tenant); e != nil {
			t.Fatal(e)
		}
		exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic next default','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic next default','active')", other, tenant)
		exec(t, "UPDATE installation_state SET default_workspace_id=$1 WHERE id=1", "UPDATE installation_state SET default_workspace_id=? WHERE id=1", other)
		if _, e = s.ValidateInvitation(ctx, value); e != nil {
			t.Fatal("stored Workspace no longer default", e)
		}
		waiting := make(chan error, 1)
		if e = f.Store.Within(ctx, false, func(txctx context.Context, tx transaction.Executor) error {
			var id string
			if e := tx.QueryRow(txctx, f.Statement("SELECT id::text FROM workspaces WHERE id=$1 FOR UPDATE", "SELECT id FROM workspaces WHERE id=? FOR UPDATE", workspace)).Scan(&id); e != nil {
				return e
			}
			go func() { waiting <- s.Register(ctx, value, "Archived race", password, uuid.NewString()) }()
			select {
			case e := <-waiting:
				t.Fatalf("registration escaped Workspace fence: %v", e)
			case <-time.After(250 * time.Millisecond):
			}
			_, e := tx.Exec(txctx, f.Statement("UPDATE workspaces SET status='disabled' WHERE id=$1", "UPDATE workspaces SET status='disabled' WHERE id=?", workspace))
			return e
		}); e != nil {
			t.Fatal(e)
		}
		want(t, <-waiting, "TOKEN_UNUSABLE")
		if _, e = s.RevokeInvitation(ctx, admin, i.ID, i.Version, "Synthetic archived cleanup", uuid.NewString()); e != nil {
			t.Fatal("archive blocked safe revocation", e)
		}
		exec(t, "UPDATE workspaces SET status='active' WHERE id=$1", "UPDATE workspaces SET status='active' WHERE id=?", workspace)
		exec(t, "UPDATE installation_state SET default_workspace_id=$1 WHERE id=1", "UPDATE installation_state SET default_workspace_id=? WHERE id=1", workspace)
		foreignTenant, foreignScope, foreignID, foreignToken := uuid.NewString(), uuid.NewString(), uuid.NewString(), security.Token()
		exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic foreign','active')", foreignTenant)
		exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic foreign','active')", foreignScope, foreignTenant)
		exec(t, "INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES($1,$2,$3,'foreign@example.invalid','en','pending',$4,$5,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '1 hour')", "INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES(?,?,?,'foreign@example.invalid','en','pending',?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)))", foreignID, foreignTenant, foreignScope, security.Digest(foreignToken), primary)
		_, e = s.ValidateInvitation(ctx, foreignToken)
		want(t, e, "TOKEN_UNUSABLE")
		_, e = s.RevokeInvitation(ctx, admin, foreignID, 1, "Synthetic foreign denial", uuid.NewString())
		want(t, e, "RESOURCE_NOT_FOUND")

		// The installed administrator must not expire another tenant's invitation.
		expiry(t, foreignID)
		before := count(t, "SELECT count(*) FROM audit_events")
		_, e = s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), "foreign@example.invalid", "en", 0, uuid.NewString())
		want(t, e, "INVITATION_EXISTS")
		var status string
		var live bool
		if e = f.Store.QueryRow(ctx, f.Statement("SELECT status,token_hash IS NOT NULL FROM invitations WHERE id=$1", "SELECT status,token_hash IS NOT NULL FROM invitations WHERE id=?", foreignID)).Scan(&status, &live); e != nil || status != "pending" || !live || count(t, "SELECT count(*) FROM audit_events") != before {
			t.Fatal("installed administrator changed foreign expired authority", e)
		}
	})
	t.Run("post_lock_expiry_and_cancelled_waiter_have_no_partial_user", func(t *testing.T) {
		i, address := create(t)
		value := token(t, i.ID)
		waiting := make(chan error, 1)
		if e = f.Store.Within(ctx, false, func(txctx context.Context, tx transaction.Executor) error {
			if _, e := tx.Exec(txctx, f.Statement("UPDATE invitations SET expires_at=clock_timestamp()+interval '800 milliseconds' WHERE id=$1", "UPDATE invitations SET expires_at=TIMESTAMPADD(MICROSECOND,800000,UTC_TIMESTAMP(6)) WHERE id=?", i.ID)); e != nil {
				return e
			}
			go func() { waiting <- s.Register(ctx, value, "Expiry race", password, uuid.NewString()) }()
			select {
			case e := <-waiting:
				t.Fatalf("registration escaped invitation lock: %v", e)
			case <-time.After(1100 * time.Millisecond):
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		want(t, <-waiting, "TOKEN_UNUSABLE")
		var n int
		if e = f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM users WHERE email_canonical=$1", "SELECT count(*) FROM users WHERE email_canonical=?", address)).Scan(&n); e != nil || n != 0 {
			t.Fatal("expired wait created user", e)
		}
		i, _ = create(t)
		value = token(t, i.ID)
		waitingCtx, stop := context.WithCancel(ctx)
		defer stop()
		if e = f.Store.Within(ctx, false, func(txctx context.Context, tx transaction.Executor) error {
			var locked string
			if e := tx.QueryRow(txctx, f.Statement("SELECT id::text FROM workspaces WHERE id=$1 FOR UPDATE", "SELECT id FROM workspaces WHERE id=? FOR UPDATE", workspace)).Scan(&locked); e != nil {
				return e
			}
			go func() { waiting <- s.Register(waitingCtx, value, "Cancelled", password, uuid.NewString()) }()
			select {
			case e := <-waiting:
				t.Fatalf("cancelled waiter did not block: %v", e)
			case <-time.After(250 * time.Millisecond):
			}
			stop()
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		if e = <-waiting; e == nil {
			t.Fatal("cancelled registration succeeded")
		}
		if _, e = s.ValidateInvitation(ctx, value); e != nil {
			t.Fatal("cancelled registration consumed token", e)
		}
	})
	t.Run("same_email_concurrency_and_current_admin_authority", func(t *testing.T) {
		address := "synthetic+" + uuid.NewString() + "@example.invalid"
		start := make(chan struct{})
		result := make(chan error, 2)
		for range 2 {
			go func() {
				<-start
				_, e := s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), address, "en", 0, uuid.NewString())
				result <- e
			}()
		}
		close(start)
		wins := 0
		for range 2 {
			if e := <-result; e == nil {
				wins++
			} else {
				want(t, e, "INVITATION_EXISTS")
			}
		}
		if wins != 1 {
			t.Fatal("pending email uniqueness")
		}
		portal := admin
		portal.Me.Audience = "portal"
		_, e := s.Invite(ctx, portal, "", "createInvitation", uuid.NewString(), "denied@example.invalid", "en", 0, uuid.NewString())
		want(t, e, "ACCESS_DENIED")
		exec(t, "UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", primary)
		_, e = s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), "denied@example.invalid", "en", 0, uuid.NewString())
		want(t, e, "AUTH_SESSION_EXPIRED")
		exec(t, "UPDATE users SET is_platform_admin=true WHERE id=$1", "UPDATE users SET is_platform_admin=true WHERE id=?", primary)
	})
	t.Run("actual_durable_step_faults_rollback_all_business_state", func(t *testing.T) {
		snapshot := func() []int {
			t.Helper()
			out := []int{}
			for _, table := range []string{"users", "password_credentials", "workspace_memberships", "invitations", "mail_deliveries", "audit_events", "idempotency_records"} {
				out = append(out, count(t, "SELECT count(*) FROM "+table))
			}
			for _, q := range []string{"SELECT COALESCE(SUM(attempt_count),0) FROM rate_limit_buckets", "SELECT COALESCE(SUM(version),0) FROM invitations", "SELECT count(*) FROM mail_deliveries WHERE payload_ciphertext IS NOT NULL"} {
				out = append(out, count(t, q))
			}
			return out
		}
		for _, operation := range []string{"create", "register"} {
			var i identity.Invitation
			var value string
			if operation == "register" {
				i, _ = create(t)
				value = token(t, i.ID)
			}
			before := snapshot()
			maximum := 0
			for failAt := 0; failAt <= maximum; failAt++ {
				probe := &invitationFaultStore{Store: f.Store, failAt: failAt}
				copy := *s
				copy.DB = nil
				copy.Storage = probe
				if operation == "create" {
					out, e := copy.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), "fault+"+uuid.NewString()+"@example.invalid", "en", 0, uuid.NewString())
					if e == nil || out.ID != "" {
						t.Fatal("failed commit returned invitation", e)
					}
				} else {
					if e := copy.Register(ctx, value, "Fault member", password, uuid.NewString()); e == nil {
						t.Fatal("failed registration succeeded")
					}
				}
				if !probe.reached {
					t.Fatalf("fault %s/%d was not exercised", operation, failAt)
				}
				if failAt == 0 {
					maximum = probe.calls
					if maximum < 6 {
						t.Fatal("actual durable workflow did not execute")
					}
				}
				if actual := snapshot(); !equalInvitationSnapshot(actual, before) {
					t.Fatalf("durable fault %s/%d escaped rollback", operation, failAt)
				}
				if operation == "register" {
					if _, e = s.ValidateInvitation(ctx, value); e != nil {
						t.Fatal("rollback consumed real token", e)
					}
				}
			}
		}
	})
	_ = phc
}

// Test-only fault injection wraps a real transaction and fails after a successful
// durable statement,or immediately before its commit. It never simulates SQL.
type invitationFaultStore struct {
	transaction.Store
	failAt, calls int
	reached       bool
}
type invitationFaultExecutor struct {
	transaction.Executor
	owner *invitationFaultStore
}

func (f *invitationFaultStore) Within(ctx context.Context, read bool, fn func(context.Context, transaction.Executor) error) error {
	return f.Store.Within(ctx, read, func(ctx context.Context, tx transaction.Executor) error {
		e := fn(ctx, &invitationFaultExecutor{tx, f})
		if e != nil {
			return e
		}
		f.reached = true
		return errors.New("SYNTHETIC_INVITATION_PRE_COMMIT_FAILURE")
	})
}
func (f *invitationFaultExecutor) Exec(ctx context.Context, q transaction.Statement) (int64, error) {
	n, e := f.Executor.Exec(ctx, q)
	if e != nil {
		return n, e
	}
	f.owner.calls++
	if f.owner.failAt > 0 && f.owner.calls == f.owner.failAt {
		f.owner.reached = true
		return n, errors.New("SYNTHETIC_INVITATION_DURABLE_STEP_FAILURE")
	}
	return n, nil
}
func equalInvitationSnapshot(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

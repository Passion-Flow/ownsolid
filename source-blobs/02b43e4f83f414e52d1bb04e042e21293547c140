//go:build repository_integration

package identity_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualIdentitySessionRepository(t *testing.T) {
	f := repositoryfixture.NewUnbootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "synthetic-blocklist")
	if e := os.WriteFile(path, []byte("passwordpasswordpassword\n"), 0600); e != nil {
		t.Fatal(e)
	}
	passwords, e := security.NewPasswords(path)
	if e != nil {
		t.Fatal(e)
	}
	s := &identity.Service{Passwords: passwords, HMACKey: make([]byte, security.MinimumHMACKeyBytes), Storage: f.Store}
	if _, e = rand.Read(s.HMACKey); e != nil {
		t.Fatal(e)
	}
	if f.PostgreSQL != nil {
		s.Storage, s.DB = nil, f.PostgreSQL
	}
	policy := config.DefaultRuntimePolicy()
	policy.Identity.PortalIdleSeconds = 600
	policy.Identity.PortalAbsoluteSeconds = 1800
	policy.Identity.AdminIdleSeconds = 300
	policy.Identity.AdminAbsoluteSeconds = 1200
	policy.Identity.SessionTouchSeconds = 30
	if e = policy.Validate(); e != nil {
		t.Fatal(e)
	}
	s.Config.Runtime = &policy
	password := "Synthetic account phrase " + uuid.NewString()
	email := "synthetic+" + uuid.NewString() + "@example.invalid"
	if e = s.Bootstrap(ctx, email, "Synthetic \u4e16\u754c", password, "Synthetic installation", "Synthetic Workspace"); e != nil {
		t.Fatal(e)
	}
	var user, tenant, workspace, phc string
	if e = f.Store.QueryRow(ctx, f.Statement("SELECT u.id::text,i.tenant_id::text,i.default_workspace_id::text,c.password_phc FROM users u JOIN password_credentials c ON c.user_id=u.id CROSS JOIN installation_state i WHERE u.email_canonical=$1", "SELECT u.id,i.tenant_id,i.default_workspace_id,c.password_phc FROM users u JOIN password_credentials c ON c.user_id=u.id CROSS JOIN installation_state i WHERE u.email_canonical=?", email)).Scan(&user, &tenant, &workspace, &phc); e != nil {
		t.Fatal(e)
	}
	exec := func(pg, my string, args ...any) {
		t.Helper()
		if _, e := f.Store.Exec(ctx, f.Statement(pg, my, args...)); e != nil {
			t.Fatal(e)
		}
	}
	count := func(table string) int {
		t.Helper()
		var n int
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT COUNT(*) FROM "+table, "SELECT COUNT(*) FROM "+table)).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	want := func(e error, c string) {
		t.Helper()
		if e == nil || e.Error() != c {
			t.Fatalf("expected %s,got %v", c, e)
		}
	}
	login := func(address, audience, previous string) (identity.Auth, string) {
		t.Helper()
		me, token, expiry, e := s.Login(ctx, address, password, audience, previous, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		a, e := s.Authenticate(ctx, token, audience)
		if e != nil || a.Me.User.ID != me.User.ID || !security.ValidToken(token) || !expiry.After(time.Now()) {
			t.Fatal("real login/authentication", e)
		}
		return a, token
	}
	second, secondEmail := uuid.NewString(), "synthetic+"+uuid.NewString()+"@example.invalid"
	exec("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Second actor','en','active',false)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Second actor','en','active',false)", second, secondEmail, secondEmail)
	exec("INSERT INTO password_credentials(user_id,password_phc) VALUES($1,$2)", "INSERT INTO password_credentials(user_id,password_phc) VALUES(?,?)", second, phc)
	t.Run("real_login_private_digest_scope_and_denials", func(t *testing.T) {
		a, token := login(" "+email+" ", "portal", "")
		if a.Me.Workspace == nil || a.Me.Workspace.ID != workspace || a.Me.Workspace.TenantID != tenant || a.Me.Workspace.Role != "owner" || a.Epoch != 1 {
			t.Fatal("server Workspace context")
		}
		var hash []byte
		var created, expiry time.Time
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT token_hash,created_at,absolute_expires_at FROM sessions WHERE id=$1", "SELECT token_hash,created_at,absolute_expires_at FROM sessions WHERE id=?", a.SessionID)).Scan(&hash, &created, &expiry); e != nil || !bytes.Equal(hash, security.Digest(token)) || bytes.Equal(hash, []byte(token)) || expiry.Sub(created) < s.Absolute("portal")-time.Second || expiry.Sub(created) > s.Absolute("portal")+time.Second {
			t.Fatal("real token/expiry persistence", e)
		}
		before := count("sessions")
		for _, v := range []struct{ email, password, audience string }{{email, "wrong-password", "portal"}, {"unknown@example.invalid", password, "portal"}, {secondEmail, password, "admin"}} {
			_, token, expiry, e := s.Login(ctx, v.email, v.password, v.audience, "", uuid.NewString())
			want(e, "AUTH_INVALID_CREDENTIALS")
			if token != "" || !expiry.IsZero() {
				t.Fatal("failed login returned credential")
			}
		}
		if count("sessions") != before {
			t.Fatal("denial issued a session")
		}
		var digest []byte
		var actor *string
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT identifier_digest,actor_user_id::text FROM audit_events WHERE action='auth.login_failed' ORDER BY occurred_at DESC,id DESC LIMIT 1", "SELECT identifier_digest,actor_user_id FROM audit_events WHERE action='auth.login_failed' ORDER BY occurred_at DESC,id DESC LIMIT 1")).Scan(&digest, &actor); e != nil || len(digest) != 32 || actor != nil {
			t.Fatal("anonymous private failure audit", e)
		}
		_, e := s.Authenticate(ctx, token, "admin")
		want(e, "AUTH_SESSION_EXPIRED")
	})
	t.Run("cross_user_rotation_audience_and_idempotent_logout", func(t *testing.T) {
		_, portal := login(email, "portal", "")
		_, admin := login(email, "admin", "")
		other, fresh := login(secondEmail, "portal", portal)
		if other.Me.User.ID != second || other.Me.Workspace != nil {
			t.Fatal("other account inherited scope")
		}
		_, e := s.Authenticate(ctx, portal, "portal")
		want(e, "AUTH_SESSION_EXPIRED")
		if _, e = s.Authenticate(ctx, admin, "admin"); e != nil {
			t.Fatal("rotation crossed audience", e)
		}
		before := count("audit_events")
		if e = s.Logout(ctx, fresh, "admin", uuid.NewString()); e != nil || count("audit_events") != before {
			t.Fatal("wrong-audience logout changed state", e)
		}
		for range 2 {
			if e = s.Logout(ctx, fresh, "portal", uuid.NewString()); e != nil {
				t.Fatal(e)
			}
		}
		if count("audit_events") != before+1 {
			t.Fatal("logout audit was not exactly once")
		}
		_, e = s.Authenticate(ctx, fresh, "portal")
		want(e, "AUTH_SESSION_EXPIRED")
	})
	t.Run("database_expiry_touch_epoch_status_and_admin_role", func(t *testing.T) {
		a, token := login(email, "portal", "")
		exec("UPDATE sessions SET created_at=clock_timestamp()-interval '10 minutes',last_seen_at=clock_timestamp()-interval '1 minute' WHERE id=$1", "UPDATE sessions SET created_at=TIMESTAMPADD(MINUTE,-10,UTC_TIMESTAMP(6)),last_seen_at=TIMESTAMPADD(MINUTE,-1,UTC_TIMESTAMP(6)) WHERE id=?", a.SessionID)
		if _, e := s.Authenticate(ctx, token, "portal"); e != nil {
			t.Fatal(e)
		}
		var age float64
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT EXTRACT(EPOCH FROM(clock_timestamp()-last_seen_at))::double precision FROM sessions WHERE id=$1", "SELECT TIMESTAMPDIFF(MICROSECOND,last_seen_at,UTC_TIMESTAMP(6))/1000000.0 FROM sessions WHERE id=?", a.SessionID)).Scan(&age); e != nil || age > 2 {
			t.Fatal("nondefault conditional touch", e)
		}
		exec("UPDATE sessions SET created_at=clock_timestamp()-interval '2 hours',last_seen_at=clock_timestamp()-interval '11 minutes' WHERE id=$1", "UPDATE sessions SET created_at=TIMESTAMPADD(HOUR,-2,UTC_TIMESTAMP(6)),last_seen_at=TIMESTAMPADD(MINUTE,-11,UTC_TIMESTAMP(6)) WHERE id=?", a.SessionID)
		_, e := s.Authenticate(ctx, token, "portal")
		want(e, "AUTH_SESSION_EXPIRED")
		a, token = login(email, "admin", "")
		exec("UPDATE sessions SET created_at=clock_timestamp()-interval '1 hour',last_seen_at=clock_timestamp()-interval '30 minutes',absolute_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE sessions SET created_at=TIMESTAMPADD(HOUR,-1,UTC_TIMESTAMP(6)),last_seen_at=TIMESTAMPADD(MINUTE,-30,UTC_TIMESTAMP(6)),absolute_expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", a.SessionID)
		_, e = s.Authenticate(ctx, token, "admin")
		want(e, "AUTH_SESSION_EXPIRED")
		a, token = login(secondEmail, "portal", "")
		exec("UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", second)
		_, e = s.Authenticate(ctx, token, "portal")
		want(e, "AUTH_SESSION_EXPIRED")
		want(f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error { return s.RecheckWithin(ctx, tx, a) }), "AUTH_SESSION_EXPIRED")
		_, token = login(secondEmail, "portal", "")
		exec("UPDATE users SET status='disabled' WHERE id=$1", "UPDATE users SET status='disabled' WHERE id=?", second)
		_, e = s.Authenticate(ctx, token, "portal")
		want(e, "AUTH_SESSION_EXPIRED")
		exec("UPDATE users SET status='active',is_platform_admin=true WHERE id=$1", "UPDATE users SET status='active',is_platform_admin=true WHERE id=?", second)
		_, token = login(secondEmail, "admin", "")
		exec("UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", second)
		_, e = s.Authenticate(ctx, token, "admin")
		want(e, "AUTH_SESSION_EXPIRED")
	})
	t.Run("inactive_and_cross_tenant_membership_never_grants_scope", func(t *testing.T) {
		foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
		exec("INSERT INTO tenants(id,name,status) VALUES($1,'Foreign tenant','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Foreign tenant','active')", foreignTenant)
		exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign Workspace','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Foreign Workspace','active')", foreignWorkspace, foreignTenant)
		exec("INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')", user, foreignTenant, foreignWorkspace)
		exec("UPDATE workspaces SET status='disabled' WHERE id=$1", "UPDATE workspaces SET status='disabled' WHERE id=?", workspace)
		a, _ := login(email, "portal", "")
		if a.Me.Workspace != nil {
			t.Fatal("foreign/inactive Workspace granted")
		}
		exec("UPDATE workspaces SET status='active' WHERE id=$1", "UPDATE workspaces SET status='active' WHERE id=?", workspace)
	})
	t.Run("opaque_confirmation_credential_browser_and_revocation", func(t *testing.T) {
		a, token := login(email, "portal", "")
		b, _ := login(email, "portal", "")
		before, events := count("sessions"), count("audit_events")
		_, e := s.ConfirmPassword(ctx, a, "wrong")
		want(e, "SSO_PASSWORD_REQUIRED")
		proof, e := s.ConfirmPassword(ctx, a, password)
		if e != nil || proof.Version() != 1 {
			t.Fatal(e)
		}
		check := func(actor identity.Auth) error {
			return f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
				return s.RecheckConfirmationWithin(ctx, tx, actor, proof)
			})
		}
		if e = check(a); e != nil {
			t.Fatal(e)
		}
		want(check(b), "SSO_PASSWORD_REQUIRED")
		if count("sessions") != before || count("audit_events") != events {
			t.Fatal("confirmation changed login/session state")
		}
		exec("UPDATE password_credentials SET version=version+1 WHERE user_id=$1", "UPDATE password_credentials SET version=version+1 WHERE user_id=?", user)
		want(check(a), "SSO_PASSWORD_CHANGED")
		if e = s.Logout(ctx, token, "portal", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		want(check(a), "AUTH_SESSION_EXPIRED")
	})
	t.Run("actual_audit_failure_and_cancel_rollback_rotation_and_logout", func(t *testing.T) {
		a, token := login(email, "portal", "")
		before, events := count("sessions"), count("audit_events")
		_, fresh, expiry, e := s.Login(ctx, email, password, "portal", token, "invalid-request-id")
		want(e, "STORAGE_UNAVAILABLE")
		if fresh != "" || !expiry.IsZero() || count("sessions") != before || count("audit_events") != events {
			t.Fatal("failed audit left issued session/token")
		}
		if _, e = s.Authenticate(ctx, token, "portal"); e != nil {
			t.Fatal("failed rotation revoked old browser", e)
		}
		want(s.Logout(ctx, token, "portal", "invalid-request-id"), "STORAGE_UNAVAILABLE")
		if _, e = s.Authenticate(ctx, token, "portal"); e != nil || count("audit_events") != events {
			t.Fatal("logout audit failure escaped rollback", e)
		}
		cancelled, stop := context.WithCancel(ctx)
		e = f.Store.Within(cancelled, false, func(ctx context.Context, tx transaction.Executor) error {
			if _, e := s.IssueSessionWithin(ctx, tx, user, a.Epoch, "portal", token, uuid.NewString(), nil); e != nil {
				return e
			}
			stop()
			return nil
		})
		stop()
		if !errors.Is(e, context.Canceled) {
			t.Fatal("cancelled actual issuance committed", e)
		}
		if count("sessions") != before || count("audit_events") != events {
			t.Fatal("cancel escaped actual rollback")
		}
		if _, e = s.Authenticate(ctx, token, "portal"); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("actor_write_fence_blocks_logout_through_real_commit", func(t *testing.T) {
		a, token := login(email, "portal", "")
		operationContext := ctx
		result := make(chan error, 1)
		e := f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			if e := s.RecheckWithin(ctx, tx, a); e != nil {
				return e
			}
			go func() { result <- s.Logout(operationContext, token, "portal", uuid.NewString()) }()
			select {
			case e := <-result:
				t.Fatal("logout escaped user/session fence", e)
			case <-time.After(150 * time.Millisecond):
			}
			return audit.WriteWithin(ctx, tx, user, user, "portal", "auth.synthetic_fenced_write", "success", "user", user, "", uuid.NewString())
		})
		if e != nil {
			t.Fatal(e)
		}
		if e = <-result; e != nil {
			t.Fatal(e)
		}
		want(f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error { return s.RecheckWithin(ctx, tx, a) }), "AUTH_SESSION_EXPIRED")
	})
	t.Run("opposite_browser_rotations_use_sorted_user_locks", func(t *testing.T) {
		a, first := login(email, "portal", "")
		b, other := login(secondEmail, "portal", "")
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, v := range []struct {
			user, old string
			epoch     int64
		}{{user, other, a.Epoch}, {second, first, b.Epoch}} {
			go func() {
				<-start
				results <- f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
					_, e := s.IssueSessionWithin(ctx, tx, v.user, v.epoch, "portal", v.old, uuid.NewString(), nil)
					return e
				})
			}()
		}
		close(start)
		for range 2 {
			if e := <-results; e != nil {
				t.Fatal("opposite rotation lock order", e)
			}
		}
		for _, old := range []string{first, other} {
			_, e := s.Authenticate(ctx, old, "portal")
			want(e, "AUTH_SESSION_EXPIRED")
		}
	})
}

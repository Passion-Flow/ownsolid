package identity_test

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	recovery "github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/retention"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func expect(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s, got %v", code, e)
	}
}
func count(t *testing.T, s *identity.Service, sql string, args ...any) int {
	t.Helper()
	var n int
	if e := s.DB.QueryRow(context.Background(), sql, args...).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func member(t *testing.T, s *identity.Service, a identity.Auth, email string) (string, string) {
	t.Helper()
	i, e := s.Invite(context.Background(), a, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	token := fixture.InvitationToken(t, s, i.ID)
	p := fixture.Password()
	if e = s.Register(context.Background(), token, "Member <text>", p, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	var userID string
	if e = s.DB.QueryRow(context.Background(), "SELECT id::text FROM users WHERE email_canonical=$1", email).Scan(&userID); e != nil {
		t.Fatal(e)
	}
	return userID, p
}
func TestM01BootstrapAndOperatorRecovery(t *testing.T) {
	s := fixture.New(t)
	ctx := context.Background()
	p := fixture.Password()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.Bootstrap(ctx, "admin@example.invalid", "Admin", p, "Installation", "Default")
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for e := range results {
		if e == nil {
			wins++
		} else {
			expect(t, e, "BOOTSTRAP_ALREADY_COMPLETE")
		}
	}
	if wins != 1 || count(t, s, "SELECT count(*) FROM users") != 1 || count(t, s, "SELECT count(*) FROM tenants") != 1 {
		t.Fatal("bootstrap atomicity")
	}
	expect(t, s.Bootstrap(ctx, "another@example.invalid", "Other", p, "Other", "Other"), "BOOTSTRAP_ALREADY_COMPLETE")
	if e := database.Check(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	_, token, _, e := s.Login(ctx, "admin@example.invalid", p, "admin", "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	newer := fixture.Password()
	if e = s.RecoverAdmin(ctx, newer, "Synthetic recovery"); e != nil {
		t.Fatal(e)
	}
	_, e = s.Authenticate(ctx, token, "admin")
	expect(t, e, "AUTH_SESSION_EXPIRED")
	fixture.Login(t, s, "admin@example.invalid", newer, "admin")
	if count(t, s, "SELECT count(*) FROM audit_events WHERE action='operator.admin_recovered'") != 1 {
		t.Fatal("missing operator audit")
	}
	if e = recovery.InvalidateRestored(ctx, s, "Synthetic restore"); e != nil {
		t.Fatal(e)
	}
	if count(t, s, "SELECT count(*) FROM sessions WHERE revoked_at IS NULL") != 0 {
		t.Fatal("restored authority survived")
	}
}
func TestM01InvitationRegistrationLifecycle(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	ctx := context.Background()
	key := uuid.NewString()
	i, e := s.Invite(ctx, a, "", "createInvitation", key, " New@EXAMPLE.invalid ", "zh-CN", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	token := fixture.InvitationToken(t, s, i.ID)
	same, e := s.Invite(ctx, a, "", "createInvitation", key, "new@example.invalid", "zh-CN", 0, uuid.NewString())
	if e != nil || same.ID != i.ID {
		t.Fatal("idempotent create failed", e)
	}
	if count(t, s, "SELECT count(*) FROM mail_deliveries") != 1 {
		t.Fatal("duplicate mail")
	}
	_, e = s.Invite(ctx, a, "", "createInvitation", key, "other@example.invalid", "zh-CN", 0, uuid.NewString())
	expect(t, e, "IDEMPOTENCY_CONFLICT")
	_, e = s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "new@example.invalid", "en", 0, uuid.NewString())
	expect(t, e, "INVITATION_EXISTS")
	if _, e = s.ValidateInvitation(ctx, token); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ValidateInvitation(ctx, token); e != nil {
		t.Fatal(e)
	}
	expect(t, s.Register(ctx, token, "Member", "short", uuid.NewString()), "PASSWORD_POLICY_VIOLATION")
	expect(t, s.Register(ctx, token, "Member", "passwordpassword", uuid.NewString()), "PASSWORD_BLOCKED")
	key = uuid.NewString()
	i, e = s.Invite(ctx, a, i.ID, "resendInvitation", key, "", "", i.Version, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.ValidateInvitation(ctx, token)
	expect(t, e, "TOKEN_UNUSABLE")
	if _, e = s.Invite(ctx, a, i.ID, "resendInvitation", key, "", "", i.Version-1, uuid.NewString()); e != nil {
		t.Fatal("resend replay failed", e)
	}
	token = fixture.InvitationToken(t, s, i.ID)
	password := fixture.Password()
	barrier := make(chan struct{})
	results := make(chan error, 2)
	for n := 0; n < 2; n++ {
		go func() { <-barrier; results <- s.Register(ctx, token, "\u6d4b\u8bd5 Member", password, uuid.NewString()) }()
	}
	close(barrier)
	wins := 0
	for n := 0; n < 2; n++ {
		e := <-results
		if e == nil {
			wins++
		} else {
			expect(t, e, "TOKEN_UNUSABLE")
		}
	}
	if wins != 1 || count(t, s, "SELECT count(*) FROM users WHERE email_canonical='new@example.invalid'") != 1 || count(t, s, "SELECT count(*) FROM workspace_memberships") != 2 {
		t.Fatal("registration race violated atomicity")
	}
	if count(t, s, "SELECT count(*) FROM sessions") != 1 {
		t.Fatal("registration auto-created session")
	}
	fixture.Login(t, s, "new@example.invalid", password, "portal")
	_, e = s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "new@example.invalid", "en", 0, uuid.NewString())
	expect(t, e, "ACCOUNT_EXISTS")
}
func TestM01ResetDisableProfileBoundaries(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	id, password := member(t, s, a, "alice@example.invalid")
	ctx := context.Background()
	_, token, _, e := s.Login(ctx, "alice@example.invalid", password, "portal", "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	u, e := s.Authenticate(ctx, token, "portal")
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Authenticate(ctx, token, "admin")
	expect(t, e, "AUTH_SESSION_EXPIRED")
	me, e := s.UpdateMe(ctx, u, " Alice \u4e2d\u6587 ", "zh-CN", u.Me.User.Version, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if me.User.DisplayName != "Alice \u4e2d\u6587" {
		t.Fatal("profile normalization")
	}
	_, e = s.UpdateMe(ctx, u, "stale", "en", u.Me.User.Version, uuid.NewString())
	expect(t, e, "VERSION_CONFLICT")
	for _, email := range []string{"unknown@example.invalid", "alice@example.invalid"} {
		start := time.Now()
		if e = s.RequestReset(ctx, email, "portal", email, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if time.Since(start) < 290*time.Millisecond {
			t.Fatal("recovery timing mitigation absent")
		}
	}
	reset := fixture.ResetToken(t, s, "alice@example.invalid")
	expect(t, s.Reset(ctx, reset, "short", "portal", uuid.NewString()), "PASSWORD_POLICY_VIOLATION")
	newer := fixture.Password()
	barrier := make(chan struct{})
	results := make(chan error, 2)
	for n := 0; n < 2; n++ {
		go func() { <-barrier; results <- s.Reset(ctx, reset, newer, "portal", uuid.NewString()) }()
	}
	close(barrier)
	wins := 0
	for n := 0; n < 2; n++ {
		e := <-results
		if e == nil {
			wins++
		} else {
			expect(t, e, "TOKEN_UNUSABLE")
		}
	}
	if wins != 1 {
		t.Fatal("reset race")
	}
	_, e = s.Authenticate(ctx, token, "portal")
	expect(t, e, "AUTH_SESSION_EXPIRED")
	_, _, _, e = s.Login(ctx, "alice@example.invalid", password, "portal", "", uuid.NewString())
	expect(t, e, "AUTH_INVALID_CREDENTIALS")
	alice := fixture.Login(t, s, "alice@example.invalid", newer, "portal")
	disabled, e := s.SetStatus(ctx, a, id, "disabled", alice.Me.User.Version, "Synthetic disable", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, _, _, e = s.Login(ctx, "alice@example.invalid", newer, "portal", "", uuid.NewString())
	expect(t, e, "AUTH_INVALID_CREDENTIALS")
	if _, e = s.SetStatus(ctx, a, id, "active", disabled.Version, "Synthetic enable", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e = s.UpdateMe(ctx, alice, "Stale actor", "en", alice.Me.User.Version, uuid.NewString())
	expect(t, e, "AUTH_SESSION_EXPIRED")
	fixture.Login(t, s, "alice@example.invalid", newer, "portal")
	_, e = s.SetStatus(ctx, a, a.Me.User.ID, "disabled", a.Me.User.Version, "Synthetic protected attempt", uuid.NewString())
	expect(t, e, "PROTECTED_ACCOUNT")
	if count(t, s, "SELECT count(*) FROM mail_deliveries WHERE purpose='password_changed'") != 1 {
		t.Fatal("reset notification missing")
	}
}
func TestM01PersistentLimitsAndRetention(t *testing.T) {
	s := fixture.New(t)
	fixture.Bootstrap(t, s)
	ctx := context.Background()
	l := identity.Limit{"synthetic", "subject", 2, time.Hour, 0, false}
	for n := 0; n < 2; n++ {
		if _, e := s.Admit(ctx, l); e != nil {
			t.Fatal(e)
		}
	}
	_, e := s.Admit(ctx, l)
	expect(t, e, "RATE_LIMITED")
	if count(t, s, "SELECT attempt_count FROM rate_limit_buckets") != 2 {
		t.Fatal("rejected admission changed counters")
	}
	if _, e = s.DB.Exec(ctx, "UPDATE rate_limit_buckets SET window_started_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour'"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Admit(ctx, l); e != nil {
		t.Fatal(e)
	}
	if e = retention.Pass(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
}
func TestM01VersionedListsAndConstraints(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	ctx := context.Background()
	for n := 0; n < 3; n++ {
		_, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), uuid.NewString()+"@example.invalid", "en", 0, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
	}
	first, e := s.List(ctx, "invitations", url.Values{"limit": {"2"}, "status": {"pending"}})
	if e != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatal("list pagination", e)
	}
	second, e := s.List(ctx, "invitations", url.Values{"limit": {"2"}, "status": {"pending"}, "cursor": {*first.NextCursor}})
	if e != nil || len(second.Items) != 1 || second.NextCursor != nil {
		t.Fatal("list page two", e)
	}
	_, e = s.List(ctx, "invitations", url.Values{"status": {"expired"}, "cursor": {*first.NextCursor}})
	expect(t, e, "INVALID_CURSOR")
	for _, kind := range []string{"users", "mail-deliveries", "audit-events"} {
		if _, e = s.List(ctx, kind, url.Values{}); e != nil {
			t.Fatal(kind, e)
		}
	}
	if _, e = s.DB.Exec(ctx, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) SELECT $1,email_canonical,email_display,display_name,locale,status FROM users LIMIT 1", uuid.NewString()); e == nil {
		t.Fatal("duplicate email constraint absent")
	}
	if _, e = s.DB.Exec(ctx, "UPDATE users SET auth_epoch=0"); e == nil {
		t.Fatal("epoch check absent")
	}
	if _, e = s.DB.Exec(ctx, "UPDATE mail_deliveries SET state='sent'"); e == nil {
		t.Fatal("mail terminal constraint absent")
	}
}
func TestM01AuditFailureRollsBack(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	ctx := context.Background()
	_, e := s.DB.Exec(ctx, `CREATE FUNCTION reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic failure'; END$$; CREATE TRIGGER reject_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_audit()`)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "new@example.invalid", "en", 0, uuid.NewString())
	if e == nil {
		t.Fatal("audit failure ignored")
	}
	if count(t, s, "SELECT count(*) FROM invitations") != 0 || count(t, s, "SELECT count(*) FROM mail_deliveries") != 0 {
		t.Fatal("partial invitation persisted")
	}
	_, e = s.UpdateMe(ctx, a, "Changed", "en", a.Me.User.Version, uuid.NewString())
	if e == nil {
		t.Fatal("audit failure ignored")
	}
	u, e := s.GetUser(ctx, a.Me.User.ID)
	if e != nil || u.DisplayName != "Test Admin" {
		t.Fatal("profile partial write")
	}
}

func waitForLock(t *testing.T, s *identity.Service, fragment string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		e := s.DB.QueryRow(context.Background(), "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1", "%"+fragment+"%").Scan(&n)
		if e != nil {
			t.Fatal(e)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("deterministic database barrier was not reached")
}
func TestM01RevocationRacesRecheckFinalState(t *testing.T) {
	t.Run("login_after_password_change", func(t *testing.T) {
		s := fixture.New(t)
		p := fixture.Bootstrap(t, s)
		ctx := context.Background()
		a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
		tx, e := s.DB.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		if _, e = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", a.Me.User.ID); e != nil {
			t.Fatal(e)
		}
		result := make(chan error, 1)
		go func() {
			_, _, _, e := s.Login(ctx, "admin@example.invalid", p, "admin", "", uuid.NewString())
			result <- e
		}()
		waitForLock(t, s, "FROM users WHERE id=")
		newer := fixture.Password()
		phc, e := s.Passwords.Hash(ctx, newer)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, "UPDATE password_credentials SET password_phc=$2,version=version+1 WHERE user_id=$1", a.Me.User.ID, phc); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1,version=version+1 WHERE id=$1", a.Me.User.ID); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
		expect(t, <-result, "AUTH_INVALID_CREDENTIALS")
		fixture.Login(t, s, "admin@example.invalid", newer, "admin")
	})
	t.Run("registration_after_revoke", func(t *testing.T) {
		s := fixture.New(t)
		p := fixture.Bootstrap(t, s)
		ctx := context.Background()
		a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
		i, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "race@example.invalid", "en", 0, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		token := fixture.InvitationToken(t, s, i.ID)
		tx, e := s.DB.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		if _, e = tx.Exec(ctx, "SELECT id FROM invitations WHERE id=$1 FOR UPDATE", i.ID); e != nil {
			t.Fatal(e)
		}
		result := make(chan error, 1)
		go func() { result <- s.Register(ctx, token, "Race", fixture.Password(), uuid.NewString()) }()
		waitForLock(t, s, "SELECT id::text FROM invitations WHERE id=$1 FOR UPDATE")
		if _, e = tx.Exec(ctx, "UPDATE invitations SET status='revoked',token_hash=NULL,revoked_at=clock_timestamp(),version=version+1 WHERE id=$1", i.ID); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
		expect(t, <-result, "TOKEN_UNUSABLE")
		if count(t, s, "SELECT count(*) FROM users") != 1 {
			t.Fatal("registration survived revoke")
		}
	})
	t.Run("reset_after_disable", func(t *testing.T) {
		s := fixture.New(t)
		p := fixture.Bootstrap(t, s)
		ctx := context.Background()
		a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
		id, _ := member(t, s, a, "race@example.invalid")
		if e := s.RequestReset(ctx, "race@example.invalid", "portal", "synthetic", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		token := fixture.ResetToken(t, s, "race@example.invalid")
		tx, e := s.DB.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		if _, e = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", id); e != nil {
			t.Fatal(e)
		}
		result := make(chan error, 1)
		go func() { result <- s.Reset(ctx, token, fixture.Password(), "portal", uuid.NewString()) }()
		waitForLock(t, s, "FROM users WHERE id=")
		if _, e = tx.Exec(ctx, "UPDATE users SET status='disabled',auth_epoch=auth_epoch+1,version=version+1 WHERE id=$1", id); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
		expect(t, <-result, "TOKEN_UNUSABLE")
	})
}
func TestM01SessionExpiryAndRotation(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	ctx := context.Background()
	_, token, _, e := s.Login(ctx, "admin@example.invalid", p, "portal", "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, fresh, _, e := s.Login(ctx, "admin@example.invalid", p, "portal", token, uuid.NewString())
	if e != nil || fresh == token {
		t.Fatal("session token fixation")
	}
	_, e = s.Authenticate(ctx, token, "portal")
	expect(t, e, "AUTH_SESSION_EXPIRED")
	a, e := s.Authenticate(ctx, fresh, "portal")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE sessions SET created_at=clock_timestamp()-interval '2 hours',last_seen_at=clock_timestamp()-interval '31 minutes' WHERE id=$1", a.SessionID); e != nil {
		t.Fatal(e)
	}
	_, e = s.Authenticate(ctx, fresh, "portal")
	expect(t, e, "AUTH_SESSION_EXPIRED")
	_, token, _, e = s.Login(ctx, "admin@example.invalid", p, "admin", "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	a, e = s.Authenticate(ctx, token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE sessions SET created_at=clock_timestamp()-interval '5 hours',absolute_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", a.SessionID); e != nil {
		t.Fatal(e)
	}
	_, e = s.Authenticate(ctx, token, "admin")
	expect(t, e, "AUTH_SESSION_EXPIRED")
	if e = s.Logout(ctx, token, "admin", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if e = s.Logout(ctx, token, "admin", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if count(t, s, "SELECT count(*) FROM audit_events WHERE action='auth.logout'") != 1 {
		t.Fatal("logout duplicated audit")
	}
}
func TestM01RecoveryCapsAndUniformTokenErrors(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	ctx := context.Background()
	_, portalToken, _, e := s.Login(ctx, "admin@example.invalid", p, "portal", "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, adminToken, _, e := s.Login(ctx, "admin@example.invalid", p, "admin", "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var first string
	for n := 0; n < 5; n++ {
		if n > 0 {
			if _, e = s.DB.Exec(ctx, "UPDATE rate_limit_buckets SET last_admitted_at=clock_timestamp()-interval '2 minutes',attempt_count=0"); e != nil {
				t.Fatal(e)
			}
		}
		if e = s.RequestReset(ctx, "admin@example.invalid", "portal", "synthetic", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if n == 0 {
			first = fixture.ResetToken(t, s, "admin@example.invalid")
		}
	}
	if count(t, s, "SELECT count(*) FROM password_reset_tokens WHERE token_hash IS NOT NULL") != 3 {
		t.Fatal("live reset cap")
	}
	expect(t, s.Reset(ctx, first, fixture.Password(), "admin", uuid.NewString()), "TOKEN_UNUSABLE")
	expect(t, s.Reset(ctx, first, "passwordpassword", "portal", uuid.NewString()), "PASSWORD_BLOCKED")
	newer := fixture.Password()
	if e = s.Reset(ctx, first, newer, "portal", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	for _, a := range []struct{ token, audience string }{{portalToken, "portal"}, {adminToken, "admin"}} {
		_, e = s.Authenticate(ctx, a.token, a.audience)
		expect(t, e, "AUTH_SESSION_EXPIRED")
	}
	expect(t, s.Reset(ctx, first, fixture.Password(), "portal", uuid.NewString()), "TOKEN_UNUSABLE")
	expect(t, s.Reset(ctx, security.Token(), fixture.Password(), "portal", uuid.NewString()), "TOKEN_UNUSABLE")
	if count(t, s, "SELECT count(*) FROM password_reset_tokens WHERE token_hash IS NOT NULL") != 0 {
		t.Fatal("other reset tokens survived")
	}
}
func TestM01ExpiredInvitationResendAndTokenStates(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	ctx := context.Background()
	a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	i, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "expired@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	old := fixture.InvitationToken(t, s, i.ID)
	if _, e = s.DB.Exec(ctx, "UPDATE invitations SET created_at=clock_timestamp()-interval '2 days',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", i.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.ValidateInvitation(ctx, old)
	expect(t, e, "TOKEN_UNUSABLE")
	newer, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "expired@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow(ctx, "SELECT version FROM invitations WHERE id=$1", i.ID).Scan(&i.Version); e != nil {
		t.Fatal(e)
	}
	_, e = s.Invite(ctx, a, i.ID, "resendInvitation", uuid.NewString(), "", "", i.Version, uuid.NewString())
	expect(t, e, "INVITATION_EXISTS")
	if _, e = s.RevokeInvitation(ctx, a, newer.ID, newer.Version, "Remove only newer synthetic invite", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	i, e = s.Invite(ctx, a, i.ID, "resendInvitation", uuid.NewString(), "", "", i.Version, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	fresh := fixture.InvitationToken(t, s, i.ID)
	_, e = s.ValidateInvitation(ctx, old)
	expect(t, e, "TOKEN_UNUSABLE")
	if _, e = s.RevokeInvitation(ctx, a, i.ID, i.Version, "Synthetic revoke", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	for _, token := range []string{old, fresh, security.Token()} {
		_, e = s.ValidateInvitation(ctx, token)
		expect(t, e, "TOKEN_UNUSABLE")
		expect(t, s.Register(ctx, token, "Name", fixture.Password(), uuid.NewString()), "TOKEN_UNUSABLE")
	}
}
func TestM01PasswordVerificationTimingPaths(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	ctx := context.Background()
	measure := func(email string) []float64 {
		samples := []float64{}
		for n := 0; n < 5; n++ {
			start := time.Now()
			_, _, _, e := s.Login(ctx, email, p+"wrong", "portal", "", uuid.NewString())
			expect(t, e, "AUTH_INVALID_CREDENTIALS")
			samples = append(samples, float64(time.Since(start).Microseconds())/1000)
		}
		return samples
	}
	t.Logf("known_wrong_password_ms=%v unknown_user_ms=%v; both use real bounded Argon2id; no universal timing claim", measure("admin@example.invalid"), measure("unknown@example.invalid"))
}

func TestM01ResetRollsBackAtEveryDurableStep(t *testing.T) {
	for _, step := range []struct{ name, table, event, condition string }{
		{"credential", "password_credentials", "UPDATE", "true"}, {"epoch", "users", "UPDATE", "true"}, {"token", "password_reset_tokens", "UPDATE", "true"}, {"session", "sessions", "UPDATE", "true"}, {"mail_cancellation", "mail_deliveries", "UPDATE", "true"}, {"notification", "mail_deliveries", "INSERT", "NEW.purpose='password_changed'"}, {"audit", "audit_events", "INSERT", "true"},
	} {
		t.Run(step.name, func(t *testing.T) {
			s := fixture.New(t)
			p := fixture.Bootstrap(t, s)
			ctx := context.Background()
			a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
			if e := s.RequestReset(ctx, "admin@example.invalid", "portal", "synthetic", uuid.NewString()); e != nil {
				t.Fatal(e)
			}
			token := fixture.ResetToken(t, s, "admin@example.invalid")
			sql := `CREATE FUNCTION fail_reset_step() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF ` + step.condition + ` THEN RAISE EXCEPTION 'synthetic step failure'; END IF; RETURN NEW; END$$; CREATE TRIGGER fail_reset_step BEFORE ` + step.event + ` ON ` + step.table + ` FOR EACH ROW EXECUTE FUNCTION fail_reset_step()`
			if _, e := s.DB.Exec(ctx, sql); e != nil {
				t.Fatal(e)
			}
			if e := s.Reset(ctx, token, fixture.Password(), "portal", uuid.NewString()); e == nil {
				t.Fatal("injected failure did not abort")
			}
			if count(t, s, "SELECT version FROM password_credentials") != 1 || count(t, s, "SELECT auth_epoch FROM users") != 1 || count(t, s, "SELECT count(*) FROM password_reset_tokens WHERE token_hash IS NOT NULL") != 1 || count(t, s, "SELECT count(*) FROM sessions WHERE revoked_at IS NULL") != 1 || count(t, s, "SELECT count(*) FROM mail_deliveries WHERE purpose='password_changed'") != 0 {
				t.Fatal("reset partially committed")
			}
			if _, e := s.DB.Exec(ctx, "DROP TRIGGER fail_reset_step ON "+step.table); e != nil {
				t.Fatal(e)
			}
			if _, e := s.UpdateMe(ctx, a, a.Me.User.DisplayName, a.Me.User.Locale, a.Me.User.Version, uuid.NewString()); e != nil {
				t.Fatal("old authority changed on rollback", e)
			}
			if e := s.Reset(ctx, token, fixture.Password(), "portal", uuid.NewString()); e != nil {
				t.Fatal("rolled-back token could not retry", e)
			}
		})
	}
}

func TestM01RuntimeCredentialCannotExecuteDDL(t *testing.T) {
	s := fixture.New(t)
	ctx := context.Background()
	role := "m01_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := uuid.NewString()
	schema := s.DB.Config().ConnConfig.RuntimeParams["search_path"]
	if _, e := s.DB.Exec(ctx, "CREATE ROLE "+role+" LOGIN NOINHERIT CONNECTION LIMIT 32 PASSWORD '"+password+"'"); e != nil {
		t.Fatal("fixture role creation failed")
	}
	defer func() { s.DB.Exec(ctx, "DROP OWNED BY "+role); s.DB.Exec(ctx, "DROP ROLE "+role) }()
	if _, e := s.DB.Exec(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+role+"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA "+schema+" TO "+role+"; REVOKE INSERT,UPDATE,DELETE ON "+schema+".schema_migrations FROM "+role); e != nil {
		t.Fatal(e)
	}
	pc := s.DB.Config().Copy()
	pc.ConnConfig.User = role
	pc.ConnConfig.Password = password
	runtimeDB, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	defer runtimeDB.Close()
	runtimeService := *s
	runtimeService.DB = runtimeDB
	outbox := *s.Mail
	outbox.DB = runtimeDB
	runtimeService.Mail = &outbox
	if e = database.Check(ctx, runtimeDB); e != nil {
		t.Fatal(e)
	}
	p := fixture.Bootstrap(t, &runtimeService)
	a := fixture.Login(t, &runtimeService, "admin@example.invalid", p, "admin")
	member(t, &runtimeService, a, "member@example.invalid")
	workspacesService := &workspaces.Service{DB: runtimeDB, Identity: &runtimeService}
	workspace, e := workspacesService.Create(ctx, a, "Runtime-granted workspace", a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	portal := fixture.Login(t, &runtimeService, "admin@example.invalid", p, "portal")
	if _, e = workspacesService.CreateNote(ctx, portal, workspace.ID, "Runtime note", "Synthetic runtime-grant content", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	nodeService := &nodes.Service{DB: runtimeDB, Identity: &runtimeService, Workspaces: workspacesService}
	join, e := nodeService.Mint(ctx, a, workspace.ID, "Runtime-granted node", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-runtime-node", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 1024, DiskAvailableBytes: 512, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
	node, e := nodeService.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: wire.Handshake{ProtocolVersion: wire.Version, Capabilities: []string{wire.CollectFacts}, Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	task, e := nodeService.CreateTask(ctx, a, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	lease, e := nodeService.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if e != nil || lease.Lease == nil {
		t.Fatal(e)
	}
	if _, e = nodeService.Complete(ctx, node.NodeID, node.Credential, task.ID, wire.Result{LeaseToken: lease.Lease.LeaseToken, Outcome: "success", Facts: &facts}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	componentService := &components.Service{Nodes: nodeService}
	nodeService.Components = componentService
	componentJoin, e := nodeService.MintForProfile(ctx, a, workspace.ID, "Runtime component node", wire.SystemdComponentsProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	componentNode, e := nodeService.Enroll(ctx, wire.Enrollment{JoinToken: componentJoin.Token, Handshake: wire.Handshake{ProtocolVersion: wire.ManagementVersion, ExecutionProfile: wire.SystemdComponentsProfile, Capabilities: wire.Capabilities(wire.SystemdComponentsProfile), Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	componentTask, e := componentService.Request(ctx, a, componentNode.NodeID, "stop", uuid.NewString(), "Synthetic runtime component grants", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	componentLease, e := nodeService.Claim(ctx, componentNode.NodeID, componentNode.Credential, uuid.NewString())
	if e != nil || componentLease.Lease == nil {
		t.Fatal("runtime component claim failed", e)
	}
	observation := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, Managed: true, State: "stopped", ObservedAt: time.Now().UTC()}
	if _, e = nodeService.Complete(ctx, componentNode.NodeID, componentNode.Credential, componentTask.ID, wire.Result{LeaseToken: componentLease.Lease.LeaseToken, Outcome: "success", Component: &observation}, uuid.NewString()); e != nil {
		t.Fatal("runtime component completion failed", e)
	}
	if view, e := componentService.Get(ctx, portal, workspace.ID, componentNode.NodeID); e != nil || view.Observation == nil || view.Observation.State != "stopped" {
		t.Fatal("runtime component observation unavailable", e)
	}

	maintenanceJoin, e := nodeService.MintForProfile(ctx, a, workspace.ID, "Runtime maintenance node", wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	handshake := wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: wire.Capabilities(wire.SystemdMaintenanceProfile), Facts: facts}
	maintenanceNode, e := nodeService.Enroll(ctx, wire.Enrollment{JoinToken: maintenanceJoin.Token, Handshake: handshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	configuration := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	observation = wire.ComponentObservation{Component: wire.NodeExporter, Version: "1.12.0", Managed: true, State: "stopped", Config: &configuration, ObservedAt: time.Now().UTC()}
	handshake.Component = &observation
	if _, e = nodeService.Heartbeat(ctx, maintenanceNode.NodeID, maintenanceNode.Credential, handshake); e != nil {
		t.Fatal(e)
	}
	logTask, e := componentService.Request(ctx, a, maintenanceNode.NodeID, "logs", uuid.NewString(), "Synthetic runtime log grants", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	logLease, e := nodeService.Claim(ctx, maintenanceNode.NodeID, maintenanceNode.Credential, uuid.NewString())
	if e != nil || logLease.Lease == nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	logs := wire.ComponentLogs{Source: "systemd_journal", Available: true, ObservedAt: now, Since: now.Add(-time.Minute), Entries: []wire.ComponentLogEntry{{Timestamp: now, Priority: 6, Message: "SYNTHETIC_RUNTIME_LOG"}}}
	if _, e = nodeService.Complete(ctx, maintenanceNode.NodeID, maintenanceNode.Credential, logTask.ID, wire.Result{LeaseToken: logLease.Lease.LeaseToken, Outcome: "success", Component: &observation, Logs: &logs}, uuid.NewString()); e != nil {
		t.Fatal("maintenance runtime DML failed", e)
	}
	if snapshot, e := componentService.Logs(ctx, a, maintenanceNode.NodeID); e != nil || snapshot.Snapshot == nil {
		t.Fatal("runtime log grant failed", e)
	}
	for _, sql := range []string{"CREATE TABLE forbidden(id integer)", "ALTER TABLE users ADD COLUMN forbidden integer", "DROP TABLE users", "DELETE FROM schema_migrations"} {
		if _, e = runtimeDB.Exec(ctx, sql); e == nil {
			t.Fatal("runtime credential executed DDL")
		}
	}
	if count(t, s, "SELECT count(*) FROM users") != 2 {
		t.Fatal("runtime DDL attempt affected data")
	}
}

func TestM01RetentionIsBoundedAndForeignKeySafe(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	ctx := context.Background()
	a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	member(t, s, a, "accepted@example.invalid")
	expired, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "expired@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE invitations SET created_at=clock_timestamp()-interval '200 days',expires_at=clock_timestamp()-interval '199 days' WHERE id=$1", expired.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.RequestReset(ctx, "admin@example.invalid", "portal", "retention", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE password_reset_tokens SET created_at=clock_timestamp()-interval '60 days',expires_at=clock_timestamp()-interval '59 days'"); e != nil {
		t.Fatal(e)
	}
	if e = retention.Pass(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	if count(t, s, "SELECT count(*) FROM invitations WHERE id=$1 AND status='expired' AND token_hash IS NULL", expired.ID) != 1 || count(t, s, "SELECT count(*) FROM mail_deliveries WHERE state IN ('queued','sending')") != 0 {
		t.Fatal("expired authority or ciphertext was retained")
	}
	for _, q := range []string{
		"UPDATE invitations SET updated_at=clock_timestamp()-interval '100 days' WHERE status='expired'",
		"UPDATE password_reset_tokens SET revoked_at=clock_timestamp()-interval '31 days' WHERE revoked_at IS NOT NULL",
		"UPDATE mail_deliveries SET completed_at=clock_timestamp()-interval '31 days' WHERE completed_at IS NOT NULL",
		"UPDATE sessions SET created_at=clock_timestamp()-interval '20 days',last_seen_at=clock_timestamp()-interval '19 days',absolute_expires_at=clock_timestamp()-interval '18 days',revoked_at=clock_timestamp()-interval '8 days',revoke_reason='retention_fixture'",
		"UPDATE idempotency_records SET created_at=clock_timestamp()-interval '3 days',expires_at=clock_timestamp()-interval '2 days'",
	} {
		if _, e = s.DB.Exec(ctx, q); e != nil {
			t.Fatal(e)
		}
	}
	for n := 0; n < 101; n++ {
		if _, e = s.DB.Exec(ctx, "INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id) VALUES($1,clock_timestamp()-interval '181 days','system','system.retention','success',$2)", uuid.NewString(), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	if e = retention.Pass(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	if count(t, s, "SELECT count(*) FROM audit_events WHERE occurred_at<clock_timestamp()-interval '180 days'") != 1 {
		t.Fatal("audit cleanup exceeded or missed the 100-row bound")
	}
	if count(t, s, "SELECT count(*) FROM invitations WHERE status='accepted'") != 1 || count(t, s, "SELECT count(*) FROM invitations WHERE status='expired'") != 0 || count(t, s, "SELECT count(*) FROM password_reset_tokens") != 0 || count(t, s, "SELECT count(*) FROM idempotency_records") != 0 {
		t.Fatal("retention violated provenance or FK-safe removal")
	}
	if e = retention.Pass(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
}
func TestM01AuditContainsNoCredentialMaterial(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	ctx := context.Background()
	a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	i, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "privacy@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	token := fixture.InvitationToken(t, s, i.ID)
	password := fixture.Password()
	if e = s.Register(ctx, token, "Privacy", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, _, _, e = s.Login(ctx, "unknown@example.invalid", password, "portal", "", uuid.NewString())
	expect(t, e, "AUTH_INVALID_CREDENTIALS")
	var serialized string
	if e = s.DB.QueryRow(ctx, "SELECT json_agg(audit_events)::text FROM audit_events").Scan(&serialized); e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{p, password, token, "$argon2id$", "#token=", "payload_ciphertext"} {
		if strings.Contains(serialized, secret) {
			t.Fatal("audit contains credential or mail material")
		}
	}
	if count(t, s, "SELECT count(*) FROM audit_events WHERE action='auth.login_failed' AND octet_length(identifier_digest)=32") != 1 {
		t.Fatal("anonymous audit digest missing")
	}
}

func TestM01LargeListsHaveStableBoundedCursors(t *testing.T) {
	s := fixture.New(t)
	p := fixture.Bootstrap(t, s)
	a := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	ctx := context.Background()
	stamp := time.Now().UTC().Add(-time.Minute)
	for n := 0; n < 101; n++ {
		id := uuid.NewString()
		email := fmt.Sprintf("listing%03d@example.invalid", n)
		if _, e := s.DB.Exec(ctx, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,created_at) VALUES($1,$2,$2,'List fixture','en','active',$3)", id, email, stamp); e != nil {
			t.Fatal(e)
		}
		if _, e := s.DB.Exec(ctx, `INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES($1,$2,$3,$4,'en','pending',$5,$6,$7,$7,$7::timestamptz+interval '24 hours')`, uuid.NewString(), a.Me.Workspace.TenantID, a.Me.Workspace.ID, "invite-"+email, security.Digest(security.Token()), a.Me.User.ID, stamp); e != nil {
			t.Fatal(e)
		}
		id = uuid.NewString()
		if _, e := s.DB.Exec(ctx, `INSERT INTO mail_deliveries(id,purpose,recipient_email,locale,user_id,state,next_attempt_at,message_id,created_at,updated_at,completed_at) VALUES($1,'password_changed',$2,'en',$3,'cancelled',$4,$5,$4,$4,$4)`, id, email, a.Me.User.ID, stamp, id+"@example.invalid"); e != nil {
			t.Fatal(e)
		}
		if _, e := s.DB.Exec(ctx, `INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id) VALUES($1,$2,'system','system.retention','success',$3)`, uuid.NewString(), stamp, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	for _, kind := range []string{"users", "invitations", "mail-deliveries", "audit-events"} {
		q := url.Values{"limit": {"17"}}
		expected := 101
		if kind == "users" {
			q.Set("email_prefix", "listing")
		}
		if kind == "audit-events" {
			q.Set("action", "system.retention")
		}
		seen := map[string]bool{}
		for {
			page, e := s.List(ctx, kind, q)
			if e != nil {
				t.Fatal(e)
			}
			if len(page.Items) > 17 {
				t.Fatal("page limit exceeded")
			}
			for _, item := range page.Items {
				id := item["id"].(string)
				if seen[id] {
					t.Fatal("cursor repeated an item")
				}
				seen[id] = true
			}
			if page.NextCursor == nil {
				break
			}
			q.Set("cursor", *page.NextCursor)
		}
		if len(seen) != expected {
			t.Fatalf("%s expected %d unique rows, observed %d", kind, expected, len(seen))
		}
		_, e := s.List(ctx, kind, url.Values{"cursor": {"invalid"}})
		expect(t, e, "INVALID_CURSOR")
		_, e = s.List(ctx, kind, url.Values{"limit": {"101"}})
		expect(t, e, "VALIDATION_FAILED")
	}
	page, e := s.List(ctx, "users", url.Values{"email_prefix": {"listing%"}})
	if e != nil || len(page.Items) != 0 {
		t.Fatal("prefix interpreted SQL wildcard")
	}
}

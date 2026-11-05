package identity_test

import (
	"context"
	"sync"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

const adminEmail = "admin@example.invalid"

// signIn returns a session token and its authenticated identity.
func signIn(t *testing.T, s *identity.Service, password, audience string) (string, identity.Auth) {
	t.Helper()
	_, token, _, e := s.Login(context.Background(), adminEmail, password, audience, "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Authenticate(context.Background(), token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return token, a
}

func TestChangePasswordKeepsOnlyTheCallingSession(t *testing.T) {
	s := fixture.New(t)
	old := fixture.Bootstrap(t, s)
	ctx := context.Background()
	callerToken, caller := signIn(t, s, old, "admin")
	otherToken, _ := signIn(t, s, old, "portal")
	if e := s.RequestReset(ctx, adminEmail, "portal", "192.0.2.1", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	reset := fixture.ResetToken(t, s, adminEmail)
	logins := count(t, s, `SELECT count(*) FROM audit_events WHERE action='auth.login_succeeded'`)

	next := fixture.Password()
	issued, e := s.ChangePassword(ctx, caller, old, next, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if issued.Token == "" || issued.Token == callerToken || issued.Me.User.ID != caller.Me.User.ID || issued.Me.Audience != "admin" {
		t.Fatal("a new session for the caller", issued.Me)
	}
	if issued.Me.User.Version <= caller.Me.User.Version {
		t.Fatal("the account version moves on", issued.Me.User.Version, caller.Me.User.Version)
	}
	if _, e := s.Authenticate(ctx, issued.Token, "admin"); e != nil {
		t.Fatal("the new session works", e)
	}
	for token, audience := range map[string]string{callerToken: "admin", otherToken: "portal"} {
		if _, e := s.Authenticate(ctx, token, audience); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatalf("earlier %s session must end, got %v", audience, e)
		}
	}
	if n := count(t, s, `SELECT count(*) FROM sessions WHERE revoke_reason='password_changed'`); n != 2 {
		t.Fatal("both earlier sessions revoked as a password change", n)
	}
	requireProblem(t, s.Reset(ctx, reset, fixture.Password(), "portal", uuid.NewString()), "TOKEN_UNUSABLE")
	if _, _, _, e := s.Login(ctx, adminEmail, old, "admin", "", uuid.NewString()); e == nil || e.Error() != "AUTH_INVALID_CREDENTIALS" {
		t.Fatal("the old password no longer signs in", e)
	}
	signIn(t, s, next, "admin")

	if n := count(t, s, `SELECT count(*) FROM audit_events WHERE action='auth.password_changed' AND outcome='success' AND actor_user_id=$1 AND audience='admin'`, caller.Me.User.ID); n != 1 {
		t.Fatal("one audit event for the change", n)
	}
	if n := count(t, s, `SELECT count(*) FROM audit_events WHERE action='auth.login_succeeded'`); n != logins+1 {
		t.Fatal("the change records no sign-in; only the later sign-in does", n, logins)
	}
	if n := count(t, s, `SELECT count(*) FROM mail_deliveries WHERE purpose='password_changed' AND user_id=$1 AND state='queued'`, caller.Me.User.ID); n != 1 {
		t.Fatal("the account is told by email", n)
	}
}

func TestChangePasswordRefusals(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	ctx := context.Background()
	token, a := signIn(t, s, password, "portal")
	unchanged := func(t *testing.T) {
		t.Helper()
		if _, e := s.Authenticate(ctx, token, "portal"); e != nil {
			t.Fatal("a refused change keeps the session", e)
		}
		if n := count(t, s, `SELECT count(*) FROM audit_events WHERE action='auth.password_changed'`); n != 0 {
			t.Fatal("no change recorded", n)
		}
	}

	t.Run("wrong current password is refused and audited", func(t *testing.T) {
		_, e := s.ChangePassword(ctx, a, password+"x", fixture.Password(), uuid.NewString())
		requireProblem(t, e, "PASSWORD_CURRENT_INVALID")
		if n := count(t, s, `SELECT count(*) FROM audit_events WHERE action='auth.password_change_failed' AND outcome='failure' AND actor_user_id=$1`, a.Me.User.ID); n != 1 {
			t.Fatal("the failed attempt is audited", n)
		}
		unchanged(t)
	})
	t.Run("the same password is refused", func(t *testing.T) {
		_, e := s.ChangePassword(ctx, a, password, password, uuid.NewString())
		requireProblem(t, e, "PASSWORD_UNCHANGED")
		unchanged(t)
	})
	t.Run("the password policy applies", func(t *testing.T) {
		_, e := s.ChangePassword(ctx, a, password, "short", uuid.NewString())
		requireProblem(t, e, "PASSWORD_POLICY_VIOLATION")
		_, e = s.ChangePassword(ctx, a, password, adminEmail, uuid.NewString())
		requireProblem(t, e, "PASSWORD_BLOCKED")
		unchanged(t)
	})
	t.Run("empty and oversized input", func(t *testing.T) {
		for _, input := range [][2]string{{"", fixture.Password()}, {password, ""}, {password, string(make([]byte, 4097))}} {
			_, e := s.ChangePassword(ctx, a, input[0], input[1], uuid.NewString())
			requireProblem(t, e, "VALIDATION_FAILED")
		}
		unchanged(t)
	})
	t.Run("a disabled account cannot change its password", func(t *testing.T) {
		admin := fixture.Login(t, s, adminEmail, password, "admin")
		_, memberPassword := member(t, s, admin, "member@example.invalid")
		account := fixture.Login(t, s, "member@example.invalid", memberPassword, "portal")
		if _, e := s.SetStatus(ctx, admin, account.Me.User.ID, "disabled", account.Me.User.Version, "Synthetic test", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		_, e := s.ChangePassword(ctx, account, memberPassword, fixture.Password(), uuid.NewString())
		requireProblem(t, e, "AUTH_SESSION_EXPIRED")
	})
}

func TestConcurrentPasswordChangesFromOneSessionApplyOnce(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	_, a := signIn(t, s, password, "admin")
	candidates := []string{fixture.Password(), fixture.Password()}
	errs := make([]error, len(candidates))
	var wg sync.WaitGroup
	for i, next := range candidates {
		wg.Go(func() {
			_, errs[i] = s.ChangePassword(context.Background(), a, password, next, uuid.NewString())
		})
	}
	wg.Wait()
	winner := -1
	for i, e := range errs {
		if e == nil {
			if winner >= 0 {
				t.Fatal("both changes applied")
			}
			winner = i
			continue
		}
		if e.Error() != "AUTH_SESSION_EXPIRED" && e.Error() != "PASSWORD_CURRENT_INVALID" {
			t.Fatal("the loser sees its session or password gone", e)
		}
	}
	if winner < 0 {
		t.Fatal("one change applies", errs)
	}
	signIn(t, s, candidates[winner], "admin")
	if n := count(t, s, `SELECT count(*) FROM audit_events WHERE action='auth.password_changed'`); n != 1 {
		t.Fatal("one change recorded", n)
	}
}

//go:build repository_integration

package identity_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualIdentityProfileRepository(t *testing.T) {
	f := repositoryfixture.NewUnbootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
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
	password := "Synthetic profile phrase " + uuid.NewString()
	email := "synthetic+" + uuid.NewString() + "@example.invalid"
	if e = s.Bootstrap(ctx, email, "Synthetic primary", password, "Synthetic installation", "Synthetic Workspace"); e != nil {
		t.Fatal(e)
	}
	var primary, workspace, phc string
	if e = f.Store.QueryRow(ctx, f.Statement("SELECT u.id::text,i.default_workspace_id::text,c.password_phc FROM users u JOIN password_credentials c ON c.user_id=u.id CROSS JOIN installation_state i WHERE u.email_canonical=$1", "SELECT u.id,i.default_workspace_id,c.password_phc FROM users u JOIN password_credentials c ON c.user_id=u.id CROSS JOIN installation_state i WHERE u.email_canonical=?", email)).Scan(&primary, &workspace, &phc); e != nil {
		t.Fatal(e)
	}
	exec := func(pg, my string, args ...any) {
		t.Helper()
		if _, e := f.Store.Exec(ctx, f.Statement(pg, my, args...)); e != nil {
			t.Fatal(e)
		}
	}
	want := func(e error, code string) {
		t.Helper()
		if e == nil || e.Error() != code {
			t.Fatalf("expected %s,got %v", code, e)
		}
	}
	events := func() int {
		t.Helper()
		var n int
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	get := func(id string) identity.User {
		t.Helper()
		u, e := s.GetUser(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	login := func(address, audience string) (identity.Auth, string) {
		t.Helper()
		_, token, _, e := s.Login(ctx, address, password, audience, "", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		a, e := s.Authenticate(ctx, token, audience)
		if e != nil {
			t.Fatal(e)
		}
		return a, token
	}
	account := func(admin bool) (string, string) {
		t.Helper()
		id, address := uuid.NewString(), "synthetic+"+uuid.NewString()+"@example.invalid"
		exec("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Synthetic actor','en','active',$4)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Synthetic actor','en','active',?)", id, address, address, admin)
		exec("INSERT INTO password_credentials(user_id,password_phc) VALUES($1,$2)", "INSERT INTO password_credentials(user_id,password_phc) VALUES(?,?)", id, phc)
		return id, address
	}
	admin, _ := login(email, "admin")
	t.Run("real_profile_normalization_locale_scope_and_version", func(t *testing.T) {
		a, _ := login(email, "portal")
		me, e := s.UpdateMe(ctx, a, "  Cafe\u0301 \u4e16\u754c  ", "zh-CN", a.Me.User.Version, uuid.NewString())
		if e != nil || me.User.DisplayName != "Caf\u00e9 \u4e16\u754c" || me.User.Locale != "zh-CN" || me.User.Version != a.Me.User.Version+1 || me.Workspace == nil || me.Workspace.ID != workspace {
			t.Fatal("real normalized current profile", e)
		}
		_, e = s.UpdateMe(ctx, a, "Stale", "en", a.Me.User.Version, uuid.NewString())
		want(e, "VERSION_CONFLICT")
		_, e = s.UpdateMe(ctx, a, "Unavailable switch", "fr", me.User.Version, uuid.NewString())
		want(e, "VALIDATION_FAILED")
		exec("UPDATE users SET locale='fr',version=version+1 WHERE id=$1", "UPDATE users SET locale='fr',version=version+1 WHERE id=?", primary)
		saved := get(primary)
		me, e = s.UpdateMe(ctx, a, "Keep stored unavailable locale", "fr", saved.Version, uuid.NewString())
		if e != nil || me.User.Locale != "fr" {
			t.Fatal("restored missing locale was rewritten", e)
		}
		if u := get(primary); u.DisplayName != me.User.DisplayName || u.Locale != "fr" || u.Version != me.User.Version {
			t.Fatal("profile was not persisted")
		}
	})
	t.Run("admin_audience_and_persisted_role_are_required", func(t *testing.T) {
		target, _ := account(false)
		portal, _ := login(email, "portal")
		portal.Me.User.IsPlatformAdmin = true
		_, e := s.SetStatus(ctx, portal, target, "disabled", 1, "Synthetic portal denial", uuid.NewString())
		want(e, "ACCESS_DENIED")
		actor, address := account(true)
		stale, _ := login(address, "admin")
		exec("UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", actor)
		stale.Me.User.IsPlatformAdmin = true
		_, e = s.SetStatus(ctx, stale, target, "disabled", 1, "Synthetic revoked role", uuid.NewString())
		want(e, "AUTH_SESSION_EXPIRED")
		_, e = s.SetStatus(ctx, stale, uuid.NewString(), "disabled", 1, "Synthetic missing target role denial", uuid.NewString())
		want(e, "AUTH_SESSION_EXPIRED")
		if get(target).Status != "active" {
			t.Fatal("stale/portal role changed target")
		}
	})
	t.Run("protected_missing_stale_and_same_state_target", func(t *testing.T) {
		_, e := s.GetUser(ctx, uuid.NewString())
		want(e, "RESOURCE_NOT_FOUND")
		_, e = s.SetStatus(ctx, admin, uuid.NewString(), "disabled", 1, "Synthetic missing", uuid.NewString())
		want(e, "RESOURCE_NOT_FOUND")
		_, e = s.SetStatus(ctx, admin, primary, "disabled", get(primary).Version, "Synthetic protected", uuid.NewString())
		want(e, "PROTECTED_ACCOUNT")
		target, _ := account(false)
		before := events()
		u, e := s.SetStatus(ctx, admin, target, "active", 1, "Synthetic same state", uuid.NewString())
		if e != nil || u.Version != 1 || events() != before {
			t.Fatal("same-state request changed durable state", e)
		}
		_, e = s.SetStatus(ctx, admin, target, "disabled", 2, "Synthetic stale", uuid.NewString())
		want(e, "VERSION_CONFLICT")
		if get(target).Status != "active" || events() != before {
			t.Fatal("stale request wrote state")
		}
	})
	t.Run("disable_revokes_all_entrances_reset_and_encrypted_delivery_enable_needs_login", func(t *testing.T) {
		target, address := account(true)
		_, portal := login(address, "portal")
		_, adminToken := login(address, "admin")
		if e := s.RequestReset(ctx, address, "portal", "synthetic-profile-disable", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		u, e := s.SetStatus(ctx, admin, target, "disabled", 1, "Synthetic account disable", uuid.NewString())
		if e != nil || u.Status != "disabled" || u.Version != 2 || u.Epoch != 2 {
			t.Fatal("actual disable", e)
		}
		for _, v := range []struct{ token, audience string }{{portal, "portal"}, {adminToken, "admin"}} {
			_, e := s.Authenticate(ctx, v.token, v.audience)
			want(e, "AUTH_SESSION_EXPIRED")
		}
		var resets, mailCount int
		if e = f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM password_reset_tokens WHERE user_id=$1 AND token_hash IS NOT NULL", "SELECT count(*) FROM password_reset_tokens WHERE user_id=? AND token_hash IS NOT NULL", target)).Scan(&resets); e != nil {
			t.Fatal(e)
		}
		if e = f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM mail_deliveries WHERE user_id=$1 AND (state<>'cancelled' OR payload_ciphertext IS NOT NULL OR payload_nonce IS NOT NULL OR payload_key_id IS NOT NULL OR lease_token IS NOT NULL)", "SELECT count(*) FROM mail_deliveries WHERE user_id=? AND (state<>'cancelled' OR payload_ciphertext IS NOT NULL OR payload_nonce IS NOT NULL OR payload_key_id IS NOT NULL OR lease_token IS NOT NULL)", target)).Scan(&mailCount); e != nil || resets != 0 || mailCount != 0 {
			t.Fatal("disabled account retained authority", e)
		}
		u, e = s.SetStatus(ctx, admin, target, "active", u.Version, "Synthetic account enable", uuid.NewString())
		if e != nil || u.Version != 3 || u.Epoch != 2 {
			t.Fatal("enable revived old epoch", e)
		}
		_, e = s.Authenticate(ctx, portal, "portal")
		want(e, "AUTH_SESSION_EXPIRED")
		login(address, "portal")
	})
	t.Run("audit_failure_rolls_back_profile_status_and_authorities", func(t *testing.T) {
		target, address := account(false)
		a, token := login(address, "portal")
		if e := s.RequestReset(ctx, address, "portal", "synthetic-profile-fault", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		before := events()
		me, e := s.UpdateMe(ctx, a, "Must roll back", "zh-CN", 1, "invalid-request-id")
		want(e, "STORAGE_UNAVAILABLE")
		if me.User.ID != "" {
			t.Fatal("failed profile returned success data")
		}
		u, e := s.SetStatus(ctx, admin, target, "disabled", 1, "Synthetic audit failure", "invalid-request-id")
		want(e, "STORAGE_UNAVAILABLE")
		if u.ID != "" {
			t.Fatal("failed status returned success data")
		}
		actual := get(target)
		if actual.Status != "active" || actual.Version != 1 || actual.Epoch != 1 || actual.DisplayName != "Synthetic actor" || actual.Locale != "en" || events() != before {
			t.Fatal("failed audit leaked profile/status change")
		}
		if _, e = s.Authenticate(ctx, token, "portal"); e != nil {
			t.Fatal("failed disable revoked old session", e)
		}
		var live int
		if e = f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM password_reset_tokens WHERE user_id=$1 AND token_hash IS NOT NULL", "SELECT count(*) FROM password_reset_tokens WHERE user_id=? AND token_hash IS NOT NULL", target)).Scan(&live); e != nil || live != 1 {
			t.Fatal("failed disable erased reset token", e)
		}
	})
	t.Run("concurrent_profile_and_status_writes_have_exact_versions", func(t *testing.T) {
		target, address := account(false)
		a, _ := login(address, "portal")
		result := make(chan error, 2)
		start := make(chan struct{})
		for _, name := range []string{"Synthetic profile A", "Synthetic profile B"} {
			go func() { <-start; _, e := s.UpdateMe(ctx, a, name, "en", 1, uuid.NewString()); result <- e }()
		}
		close(start)
		check := func() {
			t.Helper()
			accepted, conflict := 0, 0
			for range 2 {
				e := <-result
				if e == nil {
					accepted++
				} else if e.Error() == "VERSION_CONFLICT" {
					conflict++
				} else {
					t.Fatal(e)
				}
			}
			if accepted != 1 || conflict != 1 {
				t.Fatal("CAS did not serialize actual writes")
			}
		}
		check()
		actual := get(target)
		if actual.Version != 2 {
			t.Fatal("concurrent profile version")
		}
		start = make(chan struct{})
		for range 2 {
			go func() {
				<-start
				_, e := s.SetStatus(ctx, admin, target, "disabled", actual.Version, "Synthetic concurrent disable", uuid.NewString())
				result <- e
			}()
		}
		close(start)
		check()
		if actual = get(target); actual.Version != 3 || actual.Status != "disabled" || actual.Epoch != 2 {
			t.Fatal("concurrent disable lost state/version")
		}
	})
	t.Run("cancelled_waiter_and_revoked_actor_cannot_mutate", func(t *testing.T) {
		target, address := account(false)
		a, token := login(address, "portal")
		waiting, stop := context.WithCancel(ctx)
		defer stop()
		result := make(chan error, 1)
		e := f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			var id string
			if e := tx.QueryRow(ctx, f.Statement("SELECT id::text FROM users WHERE id=$1 FOR UPDATE", "SELECT id FROM users WHERE id=? FOR UPDATE", target)).Scan(&id); e != nil {
				return e
			}
			go func() {
				_, e := s.SetStatus(waiting, admin, target, "disabled", 1, "Synthetic cancelled waiter", uuid.NewString())
				result <- e
			}()
			select {
			case e := <-result:
				t.Fatal("status escaped target fence", e)
			case <-time.After(150 * time.Millisecond):
			}
			stop()
			select {
			case e := <-result:
				want(e, "STORAGE_UNAVAILABLE")
			case <-time.After(time.Second):
				t.Fatal("cancelled waiter did not finish")
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		if get(target).Status != "active" || get(target).Version != 1 {
			t.Fatal("cancelled waiter changed state")
		}
		if e = s.Logout(ctx, token, "portal", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		_, e = s.UpdateMe(ctx, a, "Revoked actor", "en", 1, uuid.NewString())
		want(e, "AUTH_SESSION_EXPIRED")
	})
}

//go:build repository_integration

package identity_test

import (
	"bytes"
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
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

func TestActualIdentityRecoveryRepository(t *testing.T) {
	f := repositoryfixture.NewUnbootstrapped(t)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
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
	templates, e := mail.LoadTemplates()
	if e != nil {
		t.Fatal(e)
	}
	s := &identity.Service{Storage: f.Store, Passwords: passwords, HMACKey: key}
	if f.PostgreSQL != nil {
		s.Storage, s.DB = nil, f.PostgreSQL
	}
	policy := config.DefaultRuntimePolicy()
	policy.Identity.MaxLiveResetTokens = 2
	policy.Identity.ResetTTLSeconds = 120
	policy.Identity.ResetJitterMilliseconds = 0
	policy.Identity.ResetMinimumMilliseconds = 350
	if e = policy.Validate(); e != nil {
		t.Fatal(e)
	}
	s.Config.Runtime = &policy
	s.Config.Server.PortalOrigin = "https://portal.example.invalid"
	s.Config.Server.AdminOrigin = "https://admin.example.invalid"
	s.Config.SMTP.FromAddress = "platform@example.invalid"
	s.Mail = &mail.Outbox{Storage: f.Store, Config: s.Config, Keys: keys, Templates: templates}
	password := "Synthetic recovery phrase " + uuid.NewString()
	email := "synthetic+" + uuid.NewString() + "@example.invalid"
	if e = s.Bootstrap(ctx, email, "Synthetic operator", password, "Synthetic installation", "Synthetic Workspace"); e != nil {
		t.Fatal(e)
	}
	var primary, tenant, workspace, phc string
	if e = f.Store.QueryRow(ctx, f.Statement("SELECT u.id::text,i.tenant_id::text,i.default_workspace_id::text,c.password_phc FROM users u JOIN password_credentials c ON c.user_id=u.id CROSS JOIN installation_state i WHERE u.email_canonical=$1", "SELECT u.id,i.tenant_id,i.default_workspace_id,c.password_phc FROM users u JOIN password_credentials c ON c.user_id=u.id CROSS JOIN installation_state i WHERE u.email_canonical=?", email)).Scan(&primary, &tenant, &workspace, &phc); e != nil {
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
	count := func(table string) int {
		t.Helper()
		var n int
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT COUNT(*) FROM "+table, "SELECT COUNT(*) FROM "+table)).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	account := func(admin bool) (string, string) {
		t.Helper()
		id, address := uuid.NewString(), "synthetic+"+uuid.NewString()+"@example.invalid"
		exec("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Synthetic account','en','active',$4)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Synthetic account','en','active',?)", id, address, address, admin)
		exec("INSERT INTO password_credentials(user_id,password_phc) VALUES($1,$2)", "INSERT INTO password_credentials(user_id,password_phc) VALUES(?,?)", id, phc)
		return id, address
	}
	request := func(address, audience string) string {
		t.Helper()
		if e := s.RequestReset(ctx, address, audience, "synthetic-"+uuid.NewString(), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		var id, key string
		var nonce, cipher []byte
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT id::text,payload_key_id,payload_nonce,payload_ciphertext FROM mail_deliveries WHERE recipient_email=$1 AND purpose='password_reset' AND state='queued' ORDER BY created_at DESC,id DESC LIMIT 1", "SELECT id,payload_key_id,payload_nonce,payload_ciphertext FROM mail_deliveries WHERE recipient_email=? AND purpose='password_reset' AND state='queued' ORDER BY created_at DESC,id DESC LIMIT 1", address)).Scan(&id, &key, &nonce, &cipher); e != nil {
			t.Fatal(e)
		}
		raw, e := keys.Decrypt(key, id, "password_reset", nonce, cipher)
		if e != nil {
			t.Fatal(e)
		}
		var payload mail.Payload
		if e = json.Unmarshal(raw, &payload); e != nil {
			t.Fatal(e)
		}
		origin := s.Config.Server.PortalOrigin
		if audience == "admin" {
			origin = s.Config.Server.AdminOrigin
		}
		if !strings.HasPrefix(payload.Link, origin+"/reset-password#token=") {
			t.Fatal("reset link origin/audience drift")
		}
		token := strings.TrimPrefix(payload.Link, origin+"/reset-password#token=")
		if !security.ValidToken(token) || bytes.Contains(cipher, []byte(token)) {
			t.Fatal("reset bearer was not encrypted")
		}
		return token
	}
	login := func(address, pass, audience string) string {
		t.Helper()
		_, token, _, e := s.Login(ctx, address, pass, audience, "", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		return token
	}
	t.Run("missing_outbox_password_key_and_ambiguous_authority_refuse", func(t *testing.T) {
		before, events := count("password_reset_tokens"), count("audit_events")
		for _, change := range []func(*identity.Service){func(v *identity.Service) { v.Mail = nil }, func(v *identity.Service) { v.HMACKey = nil }} {
			bad := *s
			change(&bad)
			want(bad.RequestReset(ctx, email, "portal", "synthetic-missing", uuid.NewString()), "IDENTITY_RECOVERY_CONFIGURATION_REQUIRED")
		}
		bad := *s
		bad.Passwords = nil
		want(bad.RecoverAdmin(ctx, "unused", "Synthetic reason"), "IDENTITY_PASSWORD_CONFIGURATION_REQUIRED")
		if f.PostgreSQL != nil {
			bad = *s
			bad.Storage = f.Store
			want(bad.RequestReset(ctx, email, "portal", "synthetic-conflict", uuid.NewString()), "IDENTITY_STORAGE_CONFIGURATION_CONFLICT")
		}
		if count("password_reset_tokens") != before || count("audit_events") != events {
			t.Fatal("configuration refusal mutated recovery state")
		}
	})
	t.Run("real_encrypted_request_quiet_cap_and_generic_ineligible_paths", func(t *testing.T) {
		user, address := account(false)
		before := count("mail_deliveries")
		start := time.Now()
		token := request(address, "portal")
		if time.Since(start) < config.Milliseconds(policy.Identity.ResetMinimumMilliseconds) {
			t.Fatal("minimum generic delay ignored")
		}
		var stored []byte
		var duration float64
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT token_hash,EXTRACT(EPOCH FROM(expires_at-created_at))::double precision FROM password_reset_tokens WHERE user_id=$1 AND token_hash=$2", "SELECT token_hash,TIMESTAMPDIFF(MICROSECOND,created_at,expires_at)/1000000.0 FROM password_reset_tokens WHERE user_id=? AND token_hash=?", user, security.Digest(token))).Scan(&stored, &duration); e != nil || !bytes.Equal(stored, security.Digest(token)) || duration != float64(policy.Identity.ResetTTLSeconds) {
			t.Fatal("reset digest/TTL policy persistence", e)
		}
		if e := s.RequestReset(ctx, address, "portal", "synthetic-quiet", uuid.NewString()); e != nil || count("mail_deliveries") != before+1 {
			t.Fatal("quiet request disclosed or duplicated delivery", e)
		}
		for range 3 {
			exec("UPDATE rate_limit_buckets SET last_admitted_at=clock_timestamp()-interval '2 minutes',attempt_count=0", "UPDATE rate_limit_buckets SET last_admitted_at=TIMESTAMPADD(MINUTE,-2,UTC_TIMESTAMP(6)),attempt_count=0")
			if e := s.RequestReset(ctx, address, "portal", "synthetic-cap", uuid.NewString()); e != nil {
				t.Fatal(e)
			}
		}
		if count("mail_deliveries") != before+policy.Identity.MaxLiveResetTokens {
			t.Fatal("nondefault live reset cap ignored")
		}
		before = count("mail_deliveries")
		exec("UPDATE users SET status='disabled' WHERE id=$1", "UPDATE users SET status='disabled' WHERE id=?", user)
		for _, input := range []struct{ email, audience string }{{"unknown@example.invalid", "portal"}, {address, "portal"}, {address, "admin"}} {
			if e := s.RequestReset(ctx, input.email, input.audience, "synthetic-generic-"+uuid.NewString(), uuid.NewString()); e != nil {
				t.Fatal("generic ineligible response", e)
			}
		}
		_, active := account(false)
		if e := s.RequestReset(ctx, active, "admin", "synthetic-admin-denial", uuid.NewString()); e != nil || count("mail_deliveries") != before {
			t.Fatal("ineligible Admin delivered reset", e)
		}
	})
	t.Run("real_reset_revokes_both_entrances_and_erases_old_delivery_secrets", func(t *testing.T) {
		user, address := account(true)
		portal := login(address, password, "portal")
		admin := login(address, password, "admin")
		token := request(address, "portal")
		exec("UPDATE rate_limit_buckets SET last_admitted_at=clock_timestamp()-interval '2 minutes',attempt_count=0", "UPDATE rate_limit_buckets SET last_admitted_at=TIMESTAMPADD(MINUTE,-2,UTC_TIMESTAMP(6)),attempt_count=0")
		other := request(address, "admin")
		newer := "Synthetic changed phrase " + uuid.NewString()
		before := count("sessions")
		if e := s.Reset(ctx, token, newer, "portal", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if count("sessions") != before {
			t.Fatal("reset automatically logged in")
		}
		for _, v := range []struct{ token, audience string }{{portal, "portal"}, {admin, "admin"}} {
			_, e := s.Authenticate(ctx, v.token, v.audience)
			want(e, "AUTH_SESSION_EXPIRED")
		}
		want(s.Reset(ctx, token, newer, "portal", uuid.NewString()), "TOKEN_UNUSABLE")
		want(s.Reset(ctx, other, newer, "admin", uuid.NewString()), "TOKEN_UNUSABLE")
		var epoch, version, credential int64
		var verified bool
		var current string
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT u.auth_epoch,u.version,c.version,u.email_verified_at IS NOT NULL,c.password_phc FROM users u JOIN password_credentials c ON c.user_id=u.id WHERE u.id=$1", "SELECT u.auth_epoch,u.version,c.version,u.email_verified_at IS NOT NULL,c.password_phc FROM users u JOIN password_credentials c ON c.user_id=u.id WHERE u.id=?", user)).Scan(&epoch, &version, &credential, &verified, &current); e != nil || epoch != 2 || version != 2 || credential != 2 || !verified {
			t.Fatal("reset state/version contract", e)
		}
		if ok, e := passwords.Verify(ctx, current, newer); e != nil || !ok {
			t.Fatal("actual changed Argon2 credential", e)
		}
		var live, uncleared int
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT COUNT(*) FROM password_reset_tokens WHERE user_id=$1 AND token_hash IS NOT NULL", "SELECT COUNT(*) FROM password_reset_tokens WHERE user_id=? AND token_hash IS NOT NULL", user)).Scan(&live); e != nil || live != 0 {
			t.Fatal("other reset bearers retained", e)
		}
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT COUNT(*) FROM mail_deliveries WHERE user_id=$1 AND purpose='password_reset' AND (state<>'cancelled' OR payload_ciphertext IS NOT NULL OR payload_nonce IS NOT NULL OR payload_key_id IS NOT NULL OR lease_token IS NOT NULL)", "SELECT COUNT(*) FROM mail_deliveries WHERE user_id=? AND purpose='password_reset' AND (state<>'cancelled' OR payload_ciphertext IS NOT NULL OR payload_nonce IS NOT NULL OR payload_key_id IS NOT NULL OR lease_token IS NOT NULL)", user)).Scan(&uncleared); e != nil || uncleared != 0 {
			t.Fatal("old encrypted delivery authority survived", e)
		}
		login(address, newer, "portal")
	})
	t.Run("unusable_and_blocked_tokens_preserve_account", func(t *testing.T) {
		user, address := account(false)
		token := request(address, "portal")
		events := count("audit_events")
		want(s.Reset(ctx, token, password, "admin", uuid.NewString()), "TOKEN_UNUSABLE")
		want(s.Reset(ctx, security.Token(), password, "portal", uuid.NewString()), "TOKEN_UNUSABLE")
		want(s.Reset(ctx, token, "short", "portal", uuid.NewString()), "PASSWORD_POLICY_VIOLATION")
		want(s.Reset(ctx, token, "passwordpasswordpassword", "portal", uuid.NewString()), "PASSWORD_BLOCKED")
		exec("UPDATE users SET status='disabled' WHERE id=$1", "UPDATE users SET status='disabled' WHERE id=?", user)
		want(s.Reset(ctx, token, password, "portal", uuid.NewString()), "TOKEN_UNUSABLE")
		exec("UPDATE users SET status='active' WHERE id=$1", "UPDATE users SET status='active' WHERE id=?", user)
		exec("UPDATE password_reset_tokens SET created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 second' WHERE user_id=$1", "UPDATE password_reset_tokens SET created_at=TIMESTAMPADD(HOUR,-1,UTC_TIMESTAMP(6)),expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE user_id=?", user)
		want(s.Reset(ctx, token, password, "portal", uuid.NewString()), "TOKEN_UNUSABLE")
		if count("audit_events") != events {
			t.Fatal("token refusal mutated account audit")
		}
		login(address, password, "portal")
	})
	t.Run("concurrent_consumers_have_one_actual_commit_and_notice", func(t *testing.T) {
		user, address := account(false)
		token := request(address, "portal")
		newer := "Synthetic concurrent phrase " + uuid.NewString()
		start := make(chan struct{})
		result := make(chan error, 2)
		for range 2 {
			go func() { <-start; result <- s.Reset(ctx, token, newer, "portal", uuid.NewString()) }()
		}
		close(start)
		accepted, denied := 0, 0
		for range 2 {
			e := <-result
			if e == nil {
				accepted++
			} else if e.Error() == "TOKEN_UNUSABLE" {
				denied++
			} else {
				t.Fatal(e)
			}
		}
		var notices int
		if e := f.Store.QueryRow(ctx, f.Statement("SELECT count(*) FROM mail_deliveries WHERE user_id=$1 AND purpose='password_changed'", "SELECT count(*) FROM mail_deliveries WHERE user_id=? AND purpose='password_changed'", user)).Scan(&notices); e != nil || accepted != 1 || denied != 1 || notices != 1 {
			t.Fatal("reset not single-use/atomic", e)
		}
		login(address, newer, "portal")
	})
	t.Run("held_user_lock_crossing_token_expiry_refuses", func(t *testing.T) {
		user, address := account(false)
		token := request(address, "portal")
		result := make(chan error, 1)
		outer := ctx
		e := f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			var locked string
			if e := tx.QueryRow(ctx, f.Statement("SELECT id::text FROM users WHERE id=$1 FOR UPDATE", "SELECT id FROM users WHERE id=? FOR UPDATE", user)).Scan(&locked); e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, f.Statement("UPDATE password_reset_tokens SET expires_at=clock_timestamp()+interval '100 milliseconds' WHERE user_id=$1 AND token_hash=$2", "UPDATE password_reset_tokens SET expires_at=TIMESTAMPADD(MICROSECOND,100000,UTC_TIMESTAMP(6)) WHERE user_id=? AND token_hash=?", user, security.Digest(token))); e != nil {
				return e
			}
			go func() {
				result <- s.Reset(outer, token, "Synthetic expired phrase "+uuid.NewString(), "portal", uuid.NewString())
			}()
			select {
			case e := <-result:
				t.Fatal("reset escaped held actor lock", e)
			case <-time.After(250 * time.Millisecond):
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		want(<-result, "TOKEN_UNUSABLE")
		login(address, password, "portal")
	})
	t.Run("actual_audit_and_notice_failures_roll_back_all_authority", func(t *testing.T) {
		user, address := account(false)
		session := login(address, password, "portal")
		token := request(address, "portal")
		events, deliveries := count("audit_events"), count("mail_deliveries")
		assertOld := func() {
			t.Helper()
			if _, e := s.Authenticate(ctx, session, "portal"); e != nil {
				t.Fatal("failed reset revoked old session", e)
			}
			var epoch, version int64
			var hash []byte
			if e := f.Store.QueryRow(ctx, f.Statement("SELECT u.auth_epoch,c.version,t.token_hash FROM users u JOIN password_credentials c ON c.user_id=u.id JOIN password_reset_tokens t ON t.user_id=u.id WHERE u.id=$1", "SELECT u.auth_epoch,c.version,t.token_hash FROM users u JOIN password_credentials c ON c.user_id=u.id JOIN password_reset_tokens t ON t.user_id=u.id WHERE u.id=?", user)).Scan(&epoch, &version, &hash); e != nil || epoch != 1 || version != 1 || !bytes.Equal(hash, security.Digest(token)) || count("audit_events") != events || count("mail_deliveries") != deliveries {
				t.Fatal("failure leaked partial reset", e)
			}
		}
		want(s.Reset(ctx, token, "Synthetic failure phrase "+uuid.NewString(), "portal", "invalid-request-id"), "STORAGE_UNAVAILABLE")
		assertOld()
		if f.Store.Dialect() == transaction.PostgreSQL {
			exec(`CREATE FUNCTION m20_reset_notice_fault() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.purpose='password_changed' THEN RAISE EXCEPTION 'synthetic_notice_failure'; END IF; RETURN NEW; END$$`, "")
			exec("CREATE TRIGGER m20_reset_notice_fault BEFORE INSERT ON mail_deliveries FOR EACH ROW EXECUTE FUNCTION m20_reset_notice_fault()", "")
		} else {
			exec("", `CREATE TRIGGER m20_reset_notice_fault BEFORE INSERT ON mail_deliveries FOR EACH ROW BEGIN IF NEW.purpose='password_changed' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic_notice_failure'; END IF; END`)
		}
		want(s.Reset(ctx, token, "Synthetic notice failure phrase "+uuid.NewString(), "portal", uuid.NewString()), "STORAGE_UNAVAILABLE")
		assertOld()
		if f.Store.Dialect() == transaction.PostgreSQL {
			exec("DROP TRIGGER m20_reset_notice_fault ON mail_deliveries", "")
			exec("DROP FUNCTION m20_reset_notice_fault()", "")
		} else {
			exec("", "DROP TRIGGER m20_reset_notice_fault")
		}
		if e := s.Reset(ctx, token, "Synthetic explicit diagnosed retry "+uuid.NewString(), "portal", uuid.NewString()); e != nil {
			t.Fatal("diagnosed rollback token could not be reused", e)
		}
	})
	t.Run("operator_recovery_and_cancelled_restore_then_real_invalidation", func(t *testing.T) {
		session := login(email, password, "admin")
		token := request(email, "portal")
		before := count("mail_deliveries")
		newer := "Synthetic operator recovery " + uuid.NewString()
		if e := s.RecoverAdmin(ctx, newer, "Synthetic recovery \u4e16\u754c"); e != nil {
			t.Fatal(e)
		}
		_, e := s.Authenticate(ctx, session, "admin")
		want(e, "AUTH_SESSION_EXPIRED")
		want(s.Reset(ctx, token, password, "portal", uuid.NewString()), "TOKEN_UNUSABLE")
		if count("mail_deliveries") != before {
			t.Fatal("operator recovery sent mail")
		}
		session = login(email, newer, "admin")
		exec("UPDATE rate_limit_buckets SET last_admitted_at=clock_timestamp()-interval '2 minutes',attempt_count=0", "UPDATE rate_limit_buckets SET last_admitted_at=TIMESTAMPADD(MINUTE,-2,UTC_TIMESTAMP(6)),attempt_count=0")
		token = request(email, "portal")
		id := uuid.NewString()
		exec("INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES($1,$2,$3,$4,'en','pending',$5,$6,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '1 hour')", "INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES(?,?,?,?,'en','pending',?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)))", id, tenant, workspace, "synthetic+"+uuid.NewString()+"@example.invalid", security.Digest(security.Token()), primary)
		exec("INSERT INTO idempotency_records(actor_user_id,operation_id,idempotency_key,request_hash,response_status,response_body,created_at,expires_at) VALUES($1,'createInvitation',$2,$3,201,'{}',clock_timestamp(),clock_timestamp()+interval '1 hour')", "INSERT INTO idempotency_records(actor_user_id,operation_id,idempotency_key,request_hash,response_status,response_body,created_at,expires_at) VALUES(?,'createInvitation',?,?,201,'{}',UTC_TIMESTAMP(6),TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)))", primary, uuid.NewString(), security.Digest(security.Token()))
		events := count("audit_events")
		cancelled, stop := context.WithCancel(ctx)
		e = f.Store.Within(cancelled, false, func(ctx context.Context, tx transaction.Executor) error {
			if e := s.InvalidateAccountAuthoritiesWithin(ctx, tx, "Synthetic cancelled restore"); e != nil {
				return e
			}
			stop()
			return nil
		})
		stop()
		if !errors.Is(e, context.Canceled) {
			t.Fatal("cancelled restore authority committed", e)
		}
		if _, e = s.Authenticate(ctx, session, "admin"); e != nil || count("audit_events") != events || count("idempotency_records") != 1 {
			t.Fatal("cancelled invalidation escaped rollback", e)
		}
		if e = f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			return s.InvalidateAccountAuthoritiesWithin(ctx, tx, "Synthetic actual restore")
		}); e != nil {
			t.Fatal(e)
		}
		_, e = s.Authenticate(ctx, session, "admin")
		want(e, "AUTH_SESSION_EXPIRED")
		want(s.Reset(ctx, token, password, "portal", uuid.NewString()), "TOKEN_UNUSABLE")
		var invitations, liveMail, liveTokens int
		for _, v := range []struct {
			pg, my string
			dest   *int
		}{{"SELECT count(*) FROM invitations WHERE status='pending' OR token_hash IS NOT NULL", "SELECT count(*) FROM invitations WHERE status='pending' OR token_hash IS NOT NULL", &invitations}, {"SELECT count(*) FROM mail_deliveries WHERE state IN('queued','sending') OR payload_ciphertext IS NOT NULL OR lease_token IS NOT NULL", "SELECT count(*) FROM mail_deliveries WHERE state IN('queued','sending') OR payload_ciphertext IS NOT NULL OR lease_token IS NOT NULL", &liveMail}, {"SELECT count(*) FROM password_reset_tokens WHERE token_hash IS NOT NULL", "SELECT count(*) FROM password_reset_tokens WHERE token_hash IS NOT NULL", &liveTokens}} {
			if e = f.Store.QueryRow(ctx, f.Statement(v.pg, v.my)).Scan(v.dest); e != nil {
				t.Fatal(e)
			}
		}
		if invitations != 0 || liveMail != 0 || liveTokens != 0 || count("idempotency_records") != 0 || count("audit_events") != events+1 {
			t.Fatal("actual restore retained authority")
		}
	})
}

//go:build repository_integration

package http_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

type currentMailLogs struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *currentMailLogs) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}
func (b *currentMailLogs) contains(value string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Contains(b.Buffer.Bytes(), []byte(value))
}

func TestM01CurrentHTTPMailCipherKeyFailures(t *testing.T) {
	var captured currentMailLogs
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&captured, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	s, _, _, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	sink := currentMailSMTP(t, s)
	i := currentHTTPInvite(admin, "valid-cipher@example.invalid")
	validToken := currentEmailDelivery(t, s, "invitation", "valid-cipher@example.invalid")
	if active, e := s.Mail.DispatchOne(t.Context()); e != nil || !active {
		t.Fatal("CURRENT_MAIL_VALID_ENCRYPTED_DISPATCH_FAILED")
	}
	currentMailMessageIdentity(t, currentMailReceive(t, sink), validToken)
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE invitation_id=$1 AND state='sent' AND payload_ciphertext IS NULL", 1, i["id"])
	secrets := []string{password, validToken}
	for _, kind := range []string{"cipher_flip", "nonce_flip", "cipher_short", "aad_identity", "aad_purpose", "key_missing"} {
		email := kind + "@example.invalid"
		i := currentHTTPInvite(admin, email)
		token := fixture.InvitationToken(t, s, i["id"].(string))
		secrets = append(secrets, token)
		var delivery, key string
		var nonce, cipher []byte
		if s.DB.QueryRow(t.Context(), "SELECT id::text,payload_key_id,payload_nonce,payload_ciphertext FROM mail_deliveries WHERE invitation_id=$1", i["id"]).Scan(&delivery, &key, &nonce, &cipher) != nil {
			t.Fatal("CURRENT_MAIL_PRIVATE_CIPHER_FIXTURE_UNAVAILABLE")
		}
		plain, e := s.Mail.Keys.Decrypt(key, delivery, "invitation", nonce, cipher)
		if e != nil {
			t.Fatal("CURRENT_MAIL_VALID_AEAD_READ_FAILED")
		}
		switch kind {
		case "cipher_flip":
			cipher[0] ^= 1
		case "nonce_flip":
			nonce[0] ^= 1
		case "cipher_short":
			cipher = cipher[:1]
		case "aad_identity":
			key, nonce, cipher, e = s.Mail.Keys.Encrypt(uuid.NewString(), "invitation", plain)
		case "aad_purpose":
			key, nonce, cipher, e = s.Mail.Keys.Encrypt(delivery, "password_reset", plain)
		case "key_missing":
			key = "temporarily-missing"
		}
		if e != nil {
			t.Fatal("CURRENT_MAIL_AEAD_FAULT_FIXTURE_FAILED")
		}
		currentHTTPExec(t, s, "UPDATE mail_deliveries SET payload_key_id=$2,payload_nonce=$3,payload_ciphertext=$4 WHERE id=$1", delivery, key, nonce, cipher)
		if active, e := s.Mail.DispatchOne(t.Context()); e != nil || !active {
			t.Fatal("CURRENT_MAIL_AEAD_REFUSAL_DISPATCH_FAILED")
		}
		category := "mail_ciphertext_invalid"
		if kind == "key_missing" {
			category = "mail_key_missing"
		}
		currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE id=$1 AND state='failed' AND attempt_count=1 AND error_category=$2 AND payload_key_id IS NULL AND payload_nonce IS NULL AND payload_ciphertext IS NULL AND lease_token IS NULL AND completed_at IS NOT NULL", 1, delivery, category)
		currentHTTPCount(t, s, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='mail.failed' AND outcome='failure'", 1, delivery)
		currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries d WHERE to_jsonb(d)::text LIKE $1", 0, "%"+token+"%")
		currentHTTPCount(t, s, "SELECT count(*) FROM audit_events a WHERE to_jsonb(a)::text LIKE $1", 0, "%"+token+"%")
		if len(sink.Messages) != 0 {
			t.Fatal("CURRENT_MAIL_INVALID_PAYLOAD_REACHED_SMTP")
		}
	}
	// Reintroduce the missing decryption key through a real protected keyring.
	keys := map[string]string{}
	for name, value := range s.Mail.Keys.Keys {
		keys[name] = value
	}
	keys["temporarily-missing"] = keys[s.Mail.Keys.Active]
	raw, e := json.Marshal(mail.Keyring{Active: s.Mail.Keys.Active, Keys: keys})
	path := filepath.Join(t.TempDir(), "restored-keyring.json")
	if e != nil || os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("CURRENT_MAIL_KEY_RESTORATION_FIXTURE_FAILED")
	}
	s.Mail.Keys, e = mail.LoadKeyring(path)
	if e != nil {
		t.Fatal("CURRENT_MAIL_RESTORED_KEYRING_REFUSED")
	}
	if active, e := s.Mail.DispatchOne(t.Context()); e != nil || active || len(sink.Messages) != 0 {
		t.Fatal("CURRENT_MAIL_TERMINAL_FAILURE_SILENTLY_REQUEUED")
	}
	for _, secret := range secrets {
		if captured.contains(secret) {
			t.Fatal("CURRENT_MAIL_SECRET_RETAINED_IN_STRUCTURED_LOG")
		}
	}
	t.Log("CURRENT_M01_MAIL_AEAD valid_verified_tls_render=true faults=6 exact_failed_categories=true terminal_cipher_and_lease_clear=true no_invalid_smtp=true protected_key_restoration_does_not_requeue=true raw_tokens_absent_in_rows_audit_logs=true")
}

func TestM01CurrentHTTPMailObsoleteSourceCancellation(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	sink := currentMailSMTP(t, s)
	for _, kind := range []string{"workspace_disabled", "tenant_disabled", "generation_changed", "invitation_revoked", "invitation_expired", "reset_revoked", "user_disabled", "reset_expired", "reset_consumed"} {
		reset := kind == "user_disabled" || kind == "reset_revoked" || kind == "reset_expired" || kind == "reset_consumed"
		email := kind + "@example.invalid"
		var source, user, token string
		if reset {
			user, _ = currentHTTPMember(t, s, portal, admin, email)
			portal.mutate("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": email}, 202, "AcceptedResponse")
			token = fixture.ResetToken(t, s, email)
			if s.DB.QueryRow(t.Context(), "SELECT id::text FROM password_reset_tokens WHERE user_id=$1 AND token_hash=$2", user, security.Digest(token)).Scan(&source) != nil {
				t.Fatal("CURRENT_MAIL_RESET_SOURCE_UNAVAILABLE")
			}
		} else {
			i := currentHTTPInvite(admin, email)
			source = i["id"].(string)
			token = fixture.InvitationToken(t, s, source)
		}
		var delivery string
		if s.DB.QueryRow(t.Context(), "SELECT id::text FROM mail_deliveries WHERE recipient_email=$1 AND state='queued'", email).Scan(&delivery) != nil {
			t.Fatal("CURRENT_MAIL_OBSOLETE_DELIVERY_UNAVAILABLE")
		}
		// Explicit isolated source/clock fault fixtures; no customer state changes.
		switch kind {
		case "workspace_disabled":
			currentHTTPExec(t, s, "UPDATE workspaces SET status='disabled'")
		case "tenant_disabled":
			currentHTTPExec(t, s, "UPDATE tenants SET status='disabled'")
		case "generation_changed":
			currentHTTPExec(t, s, "UPDATE invitations SET token_generation=token_generation+1,token_hash=$2 WHERE id=$1", source, security.Digest(security.Token()))
		case "invitation_revoked":
			currentHTTPExec(t, s, "UPDATE invitations SET status='revoked',token_hash=NULL,revoked_at=clock_timestamp() WHERE id=$1", source)
		case "invitation_expired":
			currentHTTPExec(t, s, "UPDATE invitations SET created_at=clock_timestamp()-interval '2 days',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", source)
		case "reset_revoked":
			currentHTTPExec(t, s, "UPDATE password_reset_tokens SET token_hash=NULL,revoked_at=clock_timestamp() WHERE id=$1", source)
		case "user_disabled":
			currentHTTPExec(t, s, "UPDATE users SET status='disabled' WHERE id=$1", user)
		case "reset_expired":
			currentHTTPExec(t, s, "UPDATE password_reset_tokens SET created_at=clock_timestamp()-interval '2 days',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", source)
		case "reset_consumed":
			currentHTTPExec(t, s, "UPDATE password_reset_tokens SET token_hash=NULL,consumed_at=clock_timestamp() WHERE id=$1", source)
		}
		query := "SELECT expires_at FROM invitations WHERE id=$1"
		if reset {
			query = "SELECT expires_at FROM password_reset_tokens WHERE id=$1"
		}
		var before, after time.Time
		if s.DB.QueryRow(t.Context(), query, source).Scan(&before) != nil {
			t.Fatal("CURRENT_MAIL_SOURCE_EXPIRY_UNAVAILABLE")
		}
		if active, e := s.Mail.DispatchOne(t.Context()); e != nil || !active {
			t.Fatal("CURRENT_MAIL_OBSOLETE_DISPATCH_FAILED")
		}
		currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE id=$1 AND state='cancelled' AND attempt_count=1 AND error_category='token_obsolete' AND payload_key_id IS NULL AND payload_nonce IS NULL AND payload_ciphertext IS NULL AND lease_token IS NULL", 1, delivery)
		currentHTTPCount(t, s, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='mail.cancelled'", 1, delivery)
		if reset {
			currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/password-resets", map[string]any{"token": token, "password": fixture.Password()}, 400, "Problem"), "TOKEN_UNUSABLE")
		} else {
			currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/invitation-validations", map[string]any{"token": token}, 400, "Problem"), "TOKEN_UNUSABLE")
		}
		if s.DB.QueryRow(t.Context(), query, source).Scan(&after) != nil || !before.Equal(after) || len(sink.Messages) != 0 {
			t.Fatal("CURRENT_MAIL_OBSOLETE_SEND_OR_EXPIRY_EXTENSION")
		}
		currentHTTPExec(t, s, "UPDATE workspaces SET status='active'")
		currentHTTPExec(t, s, "UPDATE tenants SET status='active'")
		if kind == "user_disabled" {
			currentHTTPExec(t, s, "UPDATE users SET status='active' WHERE id=$1", user)
		}
	}
	t.Log("CURRENT_M01_MAIL_OBSOLETE source_variants=9 cancelled_cipher_and_lease_cleared=true audit_once=true no_smtp_receipt=true old_public_tokens_refused=true expiry_unchanged=true isolated_state_clock_fixtures=true")
}

func TestM01CurrentHTTPMailOutageBoundedAdminRecovery(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	_, memberPassword := currentHTTPMember(t, s, portal, admin, "mail-observer@example.invalid")
	currentHTTPLogin(portal, "mail-observer@example.invalid", memberPassword)
	sink := currentMailSMTP(t, s)
	sink.SetTemporaryFailure(true)
	i := currentHTTPInvite(admin, "outage-mail@example.invalid")
	if i["delivery"].(map[string]any)["state"] != "queued" {
		t.Fatal("CURRENT_MAIL_ACCEPTANCE_CLAIMED_SMTP_DELIVERY")
	}
	portal.mutate("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": "admin@example.invalid"}, 202, "AcceptedResponse")
	maximum := s.Mail.Config.Policy().Mail.MaxAttempts
	for attempt := 1; attempt <= maximum; attempt++ {
		for count := 0; count < 2; count++ {
			if active, e := s.Mail.DispatchOne(t.Context()); e != nil || !active {
				t.Fatal("CURRENT_MAIL_OUTAGE_DISPATCH_FAILED")
			}
		}
		state := "queued"
		if attempt == maximum {
			state = "failed"
		}
		currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE state=$1 AND attempt_count=$2 AND error_category='smtp_unavailable'", 2, state, attempt)
		list := admin.request("GET", "/api/v1/admin/mail-deliveries", nil, 200, "MailListResponse", nil)
		found := 0
		for _, raw := range list["items"].([]any) {
			row := raw.(map[string]any)
			if row["state"] == state && row["attempt_count"] == float64(attempt) && row["error_category"] == "smtp_unavailable" {
				found++
			}
		}
		if found != 2 || len(sink.Messages) != 0 {
			t.Fatal("CURRENT_MAIL_ADMIN_OUTAGE_METADATA_OR_SMTP_INCORRECT")
		}
		currentHTTPExec(t, s, "UPDATE mail_deliveries SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE state='queued'")
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE state='failed' AND payload_ciphertext IS NULL AND lease_token IS NULL", 2)
	currentHTTPProblem(t, portal.request("GET", "/api/v1/admin/mail-deliveries", nil, 404, "Problem", nil), "RESOURCE_NOT_FOUND")
	sink.SetTemporaryFailure(false)
	if active, e := s.Mail.DispatchOne(t.Context()); e != nil || active || len(sink.Messages) != 0 {
		t.Fatal("CURRENT_MAIL_TERMINAL_OUTAGE_SILENTLY_REPLAYED")
	}
	t.Log("CURRENT_M01_MAIL_OUTAGE real_tls_transient_relay_fixture=true invite_201_and_reset_202_only_accepted=true exact_two_bounded_failures=true admin_metadata_schema_checked=true portal_member_admin_route_404=true relay_recovery_no_terminal_replay=true full_control_restart_not_tested=true")
}

//go:build repository_integration

package http_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func unicodeCredentialState(t *testing.T, s *identity.Service, user string) (string, int64, int64) {
	t.Helper()
	var phc string
	var version, epoch int64
	if s.DB.QueryRow(t.Context(), "SELECT c.password_phc,c.version,u.auth_epoch FROM password_credentials c JOIN users u ON u.id=c.user_id WHERE u.id=$1", user).Scan(&phc, &version, &epoch) != nil {
		t.Fatal("UNICODE_CREDENTIAL_STATE_UNAVAILABLE")
	}
	return phc, version, epoch
}

func unicodeInvalidJSON(t *testing.T, body map[string]any) []byte {
	t.Helper()
	body["password"] = "invalid-byte-fixture"
	raw, e := json.Marshal(body)
	if e != nil {
		t.Fatal("UNICODE_INVALID_JSON_FIXTURE_FAILED")
	}
	i := bytes.Index(raw, []byte("invalid-byte-fixture"))
	if i < 0 {
		t.Fatal("UNICODE_INVALID_JSON_MARKER_MISSING")
	}
	// Explicit malformed-wire fixture; do not let json.Marshal repair UTF-8.
	raw[i] = 0xff
	return raw
}

func TestM01CurrentHTTPUnicodePasswordLifecycle(t *testing.T) {
	s, server, _, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	for _, v := range []struct{ name, raw, canonical, replacement, replacementCanonical string }{
		{"latin", strings.Repeat("e\u0301", 128), strings.Repeat("\u00e9", 128), strings.Repeat("\u1100\u1161\u11a8", 128), strings.Repeat("\uac01", 128)},
		{"hangul", strings.Repeat("\u1100\u1161\u11a8", 128), strings.Repeat("\uac01", 128), " " + strings.Repeat("z", 126) + " ", " " + strings.Repeat("z", 126) + " "},
	} {
		portal := newClient(t, server, "portal", admin.schemas)
		email := "unicode-" + v.name + "@example.invalid"
		invite := currentHTTPInvite(admin, email)
		token := fixture.InvitationToken(t, s, invite["id"].(string))
		portal.mutate("POST", "/api/v1/auth/register", map[string]any{"token": token, "display_name": "Member e\u0301", "password": v.raw}, 201, "RegisterResponse")
		portal.request("GET", "/api/v1/me", nil, 401, "Problem", nil)
		var user, name, role, state string
		var cleared bool
		if s.DB.QueryRow(t.Context(), `SELECT u.id::text,u.display_name,m.role,i.status,i.token_hash IS NULL FROM users u JOIN workspace_memberships m ON m.user_id=u.id JOIN invitations i ON i.email_canonical=u.email_canonical WHERE u.email_canonical=$1`, email).Scan(&user, &name, &role, &state, &cleared) != nil || name != "Member \u00e9" || role != "member" || state != "accepted" || !cleared {
			t.Fatal("UNICODE_REGISTRATION_POSTIMAGE_INVALID")
		}
		currentHTTPCount(t, s, "SELECT count(*) FROM password_credentials WHERE user_id=$1", 1, user)
		currentHTTPCount(t, s, "SELECT count(*) FROM workspace_memberships WHERE user_id=$1", 1, user)
		currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE user_id=$1", 0, user)
		if portal.cookies["__Host-portal_session"] != nil {
			t.Fatal("UNICODE_REGISTRATION_AUTO_LOGGED_IN")
		}
		oldPHC, oldVersion, oldEpoch := unicodeCredentialState(t, s, user)
		if !strings.HasPrefix(oldPHC, "$argon2id$v=19$m=19456,t=2,p=1$") {
			t.Fatal("UNICODE_PASSWORD_COST_CHANGED")
		}
		for _, value := range []string{v.raw, v.canonical} {
			currentHTTPLogin(portal, email, value)
		}
		currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL", 1, user)
		portal.mutate("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": email}, 202, "AcceptedResponse")
		reset := fixture.ResetToken(t, s, email)
		portal.mutate("POST", "/api/v1/auth/password-resets", map[string]any{"token": reset, "password": v.replacement}, 200, "ResetResponse")
		newPHC, newVersion, newEpoch := unicodeCredentialState(t, s, user)
		if newPHC == oldPHC || newVersion != oldVersion+1 || newEpoch != oldEpoch+1 {
			t.Fatal("UNICODE_RESET_CREDENTIAL_EPOCH_NOT_ATOMIC")
		}
		currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL", 0, user)
		currentHTTPCount(t, s, "SELECT count(*) FROM password_reset_tokens WHERE user_id=$1 AND consumed_at IS NOT NULL AND token_hash IS NULL", 1, user)
		currentHTTPCount(t, s, "SELECT count(*) FROM audit_events WHERE subject_user_id=$1 AND action='auth.password_reset'", 1, user)
		portal.request("GET", "/api/v1/me", nil, 401, "Problem", nil)
		currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": v.raw}, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
		for _, value := range []string{v.replacement, v.replacementCanonical} {
			currentHTTPLogin(portal, email, value)
		}
		if v.name == "hangul" {
			currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": strings.TrimSpace(v.replacement)}, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
		}
		before := currentHTTPImage(t, s)
		currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/password-resets", map[string]any{"token": reset, "password": v.replacementCanonical}, 400, "Problem"), "TOKEN_UNUSABLE")
		currentHTTPUnchanged(t, s, before)
		currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL", 1, user)
		me := portal.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
		if me["user"].(map[string]any)["id"] != user || me["workspace"].(map[string]any)["role"] != "member" {
			t.Fatal("UNICODE_LOGIN_MEMBER_SCOPE_CHANGED")
		}
	}
	t.Log("UNICODE_HTTP_LIFECYCLE families=2 NFC_NFD_create_login_reset=true normalized_display_name=true registration_sessions=0 membership_once=true reset_epoch_and_revocation=true significant_spaces=true replay_refused=true")
}

func TestM01CurrentHTTPUnicodePasswordLimits(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	email := "unicode-limits@example.invalid"
	invite := currentHTTPInvite(admin, email)
	token := fixture.InvitationToken(t, s, invite["id"].(string))
	invalid := []struct {
		value  string
		status int
		code   string
	}{
		{strings.Repeat("a", 14), 422, "PASSWORD_POLICY_VIOLATION"},
		{strings.Repeat("a", 129), 422, "PASSWORD_POLICY_VIOLATION"},
		{strings.Repeat("🧪", 129), 422, "PASSWORD_POLICY_VIOLATION"},
		{strings.Repeat("a", 4097), 400, "VALIDATION_FAILED"},
	}
	before := currentHTTPImage(t, s)
	for _, bad := range invalid {
		currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/register", map[string]any{"token": token, "display_name": "Boundary member", "password": bad.value}, bad.status, "Problem"), bad.code)
		currentHTTPUnchanged(t, s, before)
	}
	currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/register", unicodeInvalidJSON(t, map[string]any{"token": token, "display_name": "Boundary member"}), 400, "Problem"), "VALIDATION_FAILED")
	currentHTTPUnchanged(t, s, before)
	currentHTTPCount(t, s, "SELECT count(*) FROM audit_events WHERE action='invitation.accepted'", 0)
	maximum := strings.Repeat("🧪", 128)
	portal.mutate("POST", "/api/v1/auth/register", map[string]any{"token": token, "display_name": "Boundary member", "password": maximum}, 201, "RegisterResponse")
	portal.request("GET", "/api/v1/me", nil, 401, "Problem", nil)
	currentHTTPLogin(portal, email, maximum)
	me := portal.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	user := me["user"].(map[string]any)["id"].(string)
	currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": strings.Repeat("🧪", 127) + "🧬"}, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	portal.mutate("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": email}, 202, "AcceptedResponse")
	reset := fixture.ResetToken(t, s, email)
	before = currentHTTPImage(t, s)
	for _, bad := range invalid {
		currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/password-resets", map[string]any{"token": reset, "password": bad.value}, bad.status, "Problem"), bad.code)
		currentHTTPUnchanged(t, s, before)
	}
	currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/password-resets", unicodeInvalidJSON(t, map[string]any{"token": reset}), 400, "Problem"), "VALIDATION_FAILED")
	currentHTTPUnchanged(t, s, before)
	currentHTTPCount(t, s, "SELECT count(*) FROM audit_events WHERE action='auth.password_reset'", 0)
	spaced := " " + strings.Repeat("q", 126) + " "
	portal.mutate("POST", "/api/v1/auth/password-resets", map[string]any{"token": reset, "password": spaced}, 200, "ResetResponse")
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL", 0, user)
	currentHTTPLogin(portal, email, spaced)
	currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": strings.TrimSpace(spaced)}, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL", 1, user)
	t.Log("UNICODE_HTTP_LIMITS create_refusals=5 reset_refusals=5 authority_unchanged=true mutation_audit_unchanged=true maximum_128_runes_512_bytes=true last_character_not_truncated=true spaces_significant=true explicit_recovery=true")
}

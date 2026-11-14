//go:build repository_integration

package http_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// Read only synthetic private outbox state. No SMTP worker or real recipient.
func currentEmailDelivery(t *testing.T, s *identity.Service, purpose, recipient string) string {
	t.Helper()
	var delivery, key, actual string
	var nonce, cipher []byte
	if s.DB.QueryRow(t.Context(), "SELECT id::text,payload_key_id,payload_nonce,payload_ciphertext,recipient_email FROM mail_deliveries WHERE purpose=$1 AND recipient_email=$2 AND state='queued'", purpose, recipient).Scan(&delivery, &key, &nonce, &cipher, &actual) != nil || actual != recipient {
		t.Fatal("CURRENT_EMAIL_CANONICAL_RECIPIENT_UNAVAILABLE")
	}
	raw, e := s.Mail.Keys.Decrypt(key, delivery, purpose, nonce, cipher)
	var payload mail.Payload
	if e != nil || json.Unmarshal(raw, &payload) != nil {
		t.Fatal("CURRENT_EMAIL_PRIVATE_LINK_UNAVAILABLE")
	}
	link, e := url.Parse(payload.Link)
	path := "/register"
	if purpose == "password_reset" {
		path = "/reset-password"
	}
	if e != nil || link.Scheme+"://"+link.Host != s.Config.Server.PortalOrigin || link.Path != path || link.User != nil || link.RawQuery != "" || !strings.HasPrefix(link.Fragment, "token=") {
		t.Fatal("CURRENT_EMAIL_LINK_AUTHORITY_INVALID")
	}
	token := strings.TrimPrefix(link.Fragment, "token=")
	if !security.ValidToken(token) {
		t.Fatal("CURRENT_EMAIL_PRIVATE_TOKEN_INVALID")
	}
	return token
}

func TestM01CurrentHTTPEmailCanonicalIdentityVariants(t *testing.T) {
	s, server, _, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	seen := map[string]bool{}
	for _, v := range []struct{ raw, canonical string }{
		{" \tAlias@EXAMPLE.invalid\r\n", "alias@example.invalid"},
		{"AL.IAS@Example.invalid", "al.ias@example.invalid"},
		{"Alias+tag@EXAMPLE.invalid", "alias+tag@example.invalid"},
		{"Alias@XN--BCHER-KVA.invalid", "alias@xn--bcher-kva.invalid"},
	} {
		portal := newClient(t, server, "portal", admin.schemas)
		invite := currentHTTPInvite(admin, v.raw)
		if invite["email"] != v.canonical {
			t.Fatal("CURRENT_EMAIL_INVITATION_CANONICAL_RESPONSE_INVALID")
		}
		token := currentEmailDelivery(t, s, "invitation", v.canonical)
		if token != fixture.InvitationToken(t, s, invite["id"].(string)) {
			t.Fatal("CURRENT_EMAIL_INVITATION_TOKEN_BINDING_INVALID")
		}
		credential := fixture.Password()
		portal.mutate("POST", "/api/v1/auth/register", map[string]any{"token": token, "display_name": "Synthetic email member", "password": credential}, 201, "RegisterResponse")
		portal.request("GET", "/api/v1/me", nil, 401, "Problem", nil)
		var user, display, role, state string
		var cleared bool
		if s.DB.QueryRow(t.Context(), `SELECT u.id::text,u.email_display,m.role,i.status,i.token_hash IS NULL FROM users u JOIN workspace_memberships m ON m.user_id=u.id JOIN invitations i ON i.email_canonical=u.email_canonical WHERE u.email_canonical=$1`, v.canonical).Scan(&user, &display, &role, &state, &cleared) != nil || display != v.canonical || role != "member" || state != "accepted" || !cleared || seen[user] {
			t.Fatal("CURRENT_EMAIL_FIXED_DISTINCT_IDENTITY_POSTIMAGE_INVALID")
		}
		seen[user] = true
		currentHTTPCount(t, s, "SELECT count(*) FROM password_credentials WHERE user_id=$1", 1, user)
		currentHTTPCount(t, s, "SELECT count(*) FROM workspace_memberships WHERE user_id=$1", 1, user)
		currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE user_id=$1", 0, user)
		for _, input := range []string{v.raw, v.canonical} {
			result := portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": input, "password": credential}, 200, "LoginResponse")
			actual := result["user"].(map[string]any)
			if actual["id"] != user || actual["email"] != v.canonical || result["workspace"].(map[string]any)["role"] != "member" {
				t.Fatal("CURRENT_EMAIL_LOGIN_VARIANT_MISBOUND")
			}
		}
		portal.mutate("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": v.raw}, 202, "AcceptedResponse")
		reset := currentEmailDelivery(t, s, "password_reset", v.canonical)
		currentHTTPCount(t, s, "SELECT count(*) FROM password_reset_tokens WHERE user_id=$1 AND token_hash=$2 AND audience='portal'", 1, user, security.Digest(reset))
		currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL", 1, user)
	}
	if len(seen) != 4 {
		t.Fatal("CURRENT_EMAIL_ALIAS_FOLDING_DETECTED")
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM users", 5)
	currentHTTPCount(t, s, "SELECT count(*) FROM invitations WHERE status='accepted'", 4)
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries", 8)
	t.Log("CURRENT_M01_EMAIL_CANONICAL variants=4 explicit_logins=8 distinct_users=true fixed_registration_email=true canonical_encrypted_recipients=8 recovery_token_binding=true no_auto_session=true no_smtp_worker=true")
}

func TestM01CurrentHTTPEmailUnsupportedNoMisdelivery(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	before := currentHTTPImage(t, s)
	var audits int
	if s.DB.QueryRow(t.Context(), "SELECT count(*) FROM audit_events").Scan(&audits) != nil {
		t.Fatal("CURRENT_EMAIL_AUDIT_BASELINE_UNAVAILABLE")
	}
	for _, input := range []string{"\u212Aate@example.invalid", "\u0130@example.invalid", "a@\u212A.example.invalid", "a@\u0130.example.invalid", "\u7528\u6237@example.invalid", "a@b\u00fccher.invalid", "\"alias\"@example.invalid", "a(comment)@example.invalid", "a b@example.invalid", strings.Repeat(" ", 321-len("a@example.invalid")) + "a@example.invalid"} {
		for _, operation := range []struct {
			client *client
			path   string
			body   map[string]any
		}{
			{admin, "/api/v1/admin/invitations", map[string]any{"email": input, "locale": "en"}},
			{portal, "/api/v1/auth/login", map[string]any{"email": input, "password": password}},
			{portal, "/api/v1/auth/password-reset-requests", map[string]any{"email": input}},
		} {
			currentHTTPProblem(t, operation.client.mutate("POST", operation.path, operation.body, 400, "Problem"), "VALIDATION_FAILED")
			currentHTTPUnchanged(t, s, before)
			currentHTTPCount(t, s, "SELECT count(*) FROM audit_events", audits)
		}
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries", 0)
	invite := currentHTTPInvite(admin, " Fixed@EXAMPLE.invalid ")
	token := currentEmailDelivery(t, s, "invitation", "fixed@example.invalid")
	if token != fixture.InvitationToken(t, s, invite["id"].(string)) {
		t.Fatal("CURRENT_EMAIL_FIXED_TOKEN_MISMATCH")
	}
	before = currentHTTPImage(t, s)
	if s.DB.QueryRow(t.Context(), "SELECT count(*) FROM audit_events").Scan(&audits) != nil {
		t.Fatal("CURRENT_EMAIL_REGISTRATION_AUDIT_BASELINE_UNAVAILABLE")
	}
	body := map[string]any{"token": token, "display_name": "Synthetic fixed member", "password": fixture.Password(), "email": "override@example.invalid"}
	currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/register", body, 400, "Problem"), "VALIDATION_FAILED")
	currentHTTPUnchanged(t, s, before)
	currentHTTPCount(t, s, "SELECT count(*) FROM audit_events", audits)
	delete(body, "email")
	portal.mutate("POST", "/api/v1/auth/register", body, 201, "RegisterResponse")
	portal.request("GET", "/api/v1/me", nil, 401, "Problem", nil)
	currentHTTPLogin(portal, " \tFIXED@Example.invalid\r\n", body["password"].(string))
	currentHTTPCount(t, s, "SELECT count(*) FROM users WHERE email_canonical='fixed@example.invalid' AND email_display='fixed@example.invalid'", 1)
	currentHTTPCount(t, s, "SELECT count(*) FROM users", 2)
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE recipient_email='fixed@example.invalid'", 1)
	t.Log("CURRENT_M01_EMAIL_UNSUPPORTED unsupported_inputs=10 actual_refusals=30 full_authority_and_audit_unchanged=true registration_email_overposting_400=true valid_fixed_address_registration_login_recover=true no_misdelivery=true no_smtp_worker=true")
}

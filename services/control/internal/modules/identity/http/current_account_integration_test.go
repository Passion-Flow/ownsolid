//go:build repository_integration

package http_test

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func currentHTTPExec(t *testing.T, s *identity.Service, query string, args ...any) {
	t.Helper()
	if _, e := s.DB.Exec(t.Context(), query, args...); e != nil {
		t.Fatal("CURRENT_M01_HTTP_SYNTHETIC_STATE_WRITE_FAILED")
	}
}

func currentHTTPCount(t *testing.T, s *identity.Service, query string, want int, args ...any) {
	t.Helper()
	var actual int
	if s.DB.QueryRow(t.Context(), query, args...).Scan(&actual) != nil || actual != want {
		t.Fatal("CURRENT_M01_HTTP_STATE_COUNT_MISMATCH", actual, want)
	}
}

// Full private authority rows remain in memory. Exclude coalesced session activity
// and anonymous admission/audit records; compare authority rather than timestamps
// intentionally written by authenticated reads or quiet recovery acceptance.
func currentHTTPImage(t *testing.T, s *identity.Service) [sha256.Size]byte {
	t.Helper()
	var raw string
	e := s.DB.QueryRow(t.Context(), `SELECT jsonb_build_object(
		'users',(SELECT jsonb_agg(to_jsonb(u) ORDER BY id) FROM users u),
		'credentials',(SELECT jsonb_agg(to_jsonb(c) ORDER BY user_id) FROM password_credentials c),
		'memberships',(SELECT jsonb_agg(to_jsonb(m) ORDER BY user_id,workspace_id) FROM workspace_memberships m),
		'invitations',(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM invitations i),
		'sessions',(SELECT jsonb_agg(to_jsonb(a)-'last_seen_at' ORDER BY id) FROM sessions a),
		'resets',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM password_reset_tokens r),
		'mail',(SELECT jsonb_agg(to_jsonb(d) ORDER BY id) FROM mail_deliveries d))::text`).Scan(&raw)
	if e != nil {
		t.Fatal("CURRENT_M01_HTTP_PRIVATE_POSTIMAGE_READ_FAILED")
	}
	return sha256.Sum256([]byte(raw))
}

func currentHTTPUnchanged(t *testing.T, s *identity.Service, before [sha256.Size]byte) {
	t.Helper()
	if currentHTTPImage(t, s) != before {
		t.Fatal("CURRENT_M01_HTTP_REJECTION_CHANGED_ACCOUNT_AUTHORITY")
	}
}

func currentHTTPProblem(t *testing.T, result map[string]any, code string) string {
	t.Helper()
	if result["code"] != code || result["request_id"] == "" {
		t.Fatal("CURRENT_M01_HTTP_PUBLIC_PROBLEM_MISMATCH", code)
	}
	delete(result, "request_id")
	raw, e := json.Marshal(result)
	if e != nil {
		t.Fatal("CURRENT_M01_HTTP_PROBLEM_COMPARISON_FAILED")
	}
	return string(raw)
}

func currentHTTPLogin(c *client, email, password string) {
	c.t.Helper()
	c.mutate("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": password}, 200, "LoginResponse")
}

func currentHTTPInvite(c *client, email string) map[string]any {
	c.t.Helper()
	return c.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": email, "locale": "en"}, 201, "InvitationResponse")
}

func currentHTTPMember(t *testing.T, s *identity.Service, portal, admin *client, email string) (string, string) {
	t.Helper()
	i := currentHTTPInvite(admin, email)
	password := fixture.Password()
	portal.mutate("POST", "/api/v1/auth/register", map[string]any{"token": fixture.InvitationToken(t, s, i["id"].(string)), "display_name": "Synthetic member", "password": password}, 201, "RegisterResponse")
	var id string
	if s.DB.QueryRow(t.Context(), "SELECT id::text FROM users WHERE email_canonical=$1", email).Scan(&id) != nil {
		t.Fatal("CURRENT_M01_HTTP_MEMBER_READ_FAILED")
	}
	return id, password
}

func TestM01CurrentHTTPRegistrationPolicyPostimage(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	i := currentHTTPInvite(admin, "policy-member@example.invalid")
	token := fixture.InvitationToken(t, s, i["id"].(string))
	before := currentHTTPImage(t, s)
	for _, p := range []struct{ value, code string }{{"short", "PASSWORD_POLICY_VIOLATION"}, {"passwordpassword", "PASSWORD_BLOCKED"}, {strings.Repeat("x", 129), "PASSWORD_POLICY_VIOLATION"}} {
		result := portal.mutate("POST", "/api/v1/auth/register", map[string]any{"token": token, "display_name": "Synthetic member", "password": p.value}, 422, "Problem")
		currentHTTPProblem(t, result, p.code)
		currentHTTPUnchanged(t, s, before)
	}
	currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/register", []byte(strings.Repeat(" ", httpserver.MaxJSONBytes+1)), 413, "Problem"), "PAYLOAD_TOO_LARGE")
	currentHTTPUnchanged(t, s, before)
	portal.mutate("POST", "/api/v1/auth/invitation-validations", map[string]any{"token": token}, 200, "InvitationValidationResponse")
	portal.mutate("POST", "/api/v1/auth/register", map[string]any{"token": token, "display_name": "Synthetic member", "password": fixture.Password()}, 201, "RegisterResponse")
	portal.request("GET", "/api/v1/me", nil, 401, "Problem", nil)
	currentHTTPCount(t, s, "SELECT count(*) FROM users", 2)
	currentHTTPCount(t, s, "SELECT count(*) FROM password_credentials", 2)
	currentHTTPCount(t, s, "SELECT count(*) FROM workspace_memberships", 2)
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions", 1)
	t.Log("CURRENT_M01_HTTP_REGISTRATION_POLICY short_blocked_oversized_422=true body_413=true exact_authority_preserved=true valid_retry_no_auto_session=true")
}

func TestM01CurrentHTTPInvitationPublicStates(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	tokens := []string{security.Token()}
	for _, state := range []string{"expired", "revoked", "accepted", "previous"} {
		i := currentHTTPInvite(admin, state+"@example.invalid")
		id := i["id"].(string)
		token := fixture.InvitationToken(t, s, id)
		tokens = append(tokens, token)
		switch state {
		case "expired":
			currentHTTPExec(t, s, "UPDATE invitations SET created_at=clock_timestamp()-interval '2 days',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", id)
		case "revoked":
			admin.mutate("POST", "/api/v1/admin/invitations/"+id+"/revocations", map[string]any{"version": i["version"], "reason": "Synthetic revoked link"}, 200, "InvitationResponse")
		case "accepted":
			portal.mutate("POST", "/api/v1/auth/register", map[string]any{"token": token, "display_name": "Synthetic member", "password": fixture.Password()}, 201, "RegisterResponse")
		case "previous":
			admin.mutate("POST", "/api/v1/admin/invitations/"+id+"/resends", map[string]any{"version": i["version"]}, 200, "InvitationResponse")
		}
	}
	before := currentHTTPImage(t, s)
	shape := ""
	for _, token := range tokens {
		for _, validation := range []bool{true, false} {
			path := "/api/v1/auth/invitation-validations"
			body := map[string]any{"token": token}
			if !validation {
				path = "/api/v1/auth/register"
				body["display_name"], body["password"] = "Must not register", fixture.Password()
			}
			actual := currentHTTPProblem(t, portal.mutate("POST", path, body, 400, "Problem"), "TOKEN_UNUSABLE")
			if shape != "" && actual != shape {
				t.Fatal("CURRENT_M01_HTTP_INVITATION_STATE_DISCLOSURE")
			}
			shape = actual
			currentHTTPUnchanged(t, s, before)
		}
	}
	t.Log("CURRENT_M01_HTTP_INVITATION_STATES classes=5 operations=2 uniform_problem=true exact_authority_unchanged=true")
}

func TestM01CurrentHTTPResetPublicStates(t *testing.T) {
	s, _, portal, _, _ := setup(t)
	var user string
	if s.DB.QueryRow(t.Context(), "SELECT primary_admin_user_id::text FROM installation_state WHERE id=1").Scan(&user) != nil {
		t.Fatal("CURRENT_M01_HTTP_PRIMARY_READ_FAILED")
	}
	consumed, revoked, expired, other := security.Token(), security.Token(), security.Token(), security.Token()
	insert := func(token, audience string, expired bool) {
		created, expires := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
		if expired {
			created, expires = time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
		}
		currentHTTPExec(t, s, "INSERT INTO password_reset_tokens(id,user_id,audience,token_hash,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6)", uuid.NewString(), user, audience, security.Digest(token), created, expires)
	}
	insert(consumed, "portal", false)
	insert(revoked, "portal", false)
	if s.Reset(t.Context(), consumed, fixture.Password(), "portal", uuid.NewString()) != nil {
		t.Fatal("CURRENT_M01_HTTP_REAL_RESET_TERMINAL_SETUP_FAILED")
	}
	insert(expired, "portal", true)
	insert(other, "admin", false)
	before, shape := currentHTTPImage(t, s), ""
	for _, token := range []string{security.Token(), expired, consumed, revoked, other} {
		actual := currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/password-resets", map[string]any{"token": token, "password": fixture.Password()}, 400, "Problem"), "TOKEN_UNUSABLE")
		if shape != "" && actual != shape {
			t.Fatal("CURRENT_M01_HTTP_RESET_STATE_DISCLOSURE")
		}
		shape = actual
		currentHTTPUnchanged(t, s, before)
	}
	t.Log("CURRENT_M01_HTTP_RESET_STATES classes=5 uniform_problem=true credentials_sessions_tokens_mail_unchanged=true")
}

func TestM01CurrentHTTPResetPolicyCooldown(t *testing.T) {
	s, _, portal, _, password := setup(t)
	body := map[string]any{"email": "admin@example.invalid"}
	portal.mutate("POST", "/api/v1/auth/password-reset-requests", body, 202, "AcceptedResponse")
	token := fixture.ResetToken(t, s, "admin@example.invalid")
	before := currentHTTPImage(t, s)
	for _, p := range []struct{ value, code string }{{"short", "PASSWORD_POLICY_VIOLATION"}, {"passwordpassword", "PASSWORD_BLOCKED"}, {strings.Repeat("x", 129), "PASSWORD_POLICY_VIOLATION"}} {
		currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/password-resets", map[string]any{"token": token, "password": p.value}, 422, "Problem"), p.code)
		currentHTTPUnchanged(t, s, before)
	}
	currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/password-resets", []byte(strings.Repeat(" ", httpserver.MaxJSONBytes+1)), 413, "Problem"), "PAYLOAD_TOO_LARGE")
	currentHTTPUnchanged(t, s, before)
	portal.mutate("POST", "/api/v1/auth/password-reset-requests", body, 202, "AcceptedResponse")
	currentHTTPUnchanged(t, s, before)
	currentHTTPCount(t, s, "SELECT count(*) FROM password_reset_tokens", 1)
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE purpose='password_reset'", 1)
	next := fixture.Password()
	portal.mutate("POST", "/api/v1/auth/password-resets", map[string]any{"token": token, "password": next}, 200, "ResetResponse")
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 401, "Problem")
	currentHTTPLogin(portal, "admin@example.invalid", next)
	t.Log("CURRENT_M01_HTTP_RESET_POLICY short_blocked_oversized_422=true body_413=true quiet_cooldown_no_new_authority=true original_token_valid_retry=true")
}

func TestM01CurrentHTTPProfileUnauthorizedLists(t *testing.T) {
	s, server, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	user, memberPassword := currentHTTPMember(t, s, portal, admin, "profile-member@example.invalid")
	currentHTTPLogin(portal, "profile-member@example.invalid", memberPassword)
	me := portal.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	version := me["user"].(map[string]any)["version"]
	updated := portal.mutate("PATCH", "/api/v1/me", map[string]any{"display_name": "Synthetic <script>profile</script>", "locale": "zh-CN", "version": version}, 200, "MeResponse")
	before := currentHTTPImage(t, s)
	for _, field := range []string{"email", "is_platform_admin", "user_id", "unknown_field"} {
		body := map[string]any{"display_name": "Must not persist", "locale": "en", "version": updated["user"].(map[string]any)["version"], field: "injected"}
		currentHTTPProblem(t, portal.mutate("PATCH", "/api/v1/me", body, 400, "Problem"), "VALIDATION_FAILED")
		currentHTTPUnchanged(t, s, before)
	}
	currentHTTPProblem(t, portal.mutate("PATCH", "/api/v1/me", map[string]any{"display_name": "Stale", "locale": "en", "version": version}, 409, "Problem"), "VERSION_CONFLICT")
	currentHTTPUnchanged(t, s, before)
	actual, e := s.GetUser(t.Context(), user)
	if e != nil || actual.IsPlatformAdmin || actual.Email != "profile-member@example.invalid" || actual.Locale != "zh-CN" || actual.DisplayName != "Synthetic <script>profile</script>" {
		t.Fatal("CURRENT_M01_HTTP_PROFILE_WHITELIST_POSTIMAGE_FAILED")
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM audit_events WHERE action='account.updated' AND actor_user_id=$1 AND subject_user_id=$1 AND request_id IS NOT NULL", 1, user)
	anonymous := newClient(t, server, "admin", portal.schemas)
	for _, path := range []string{"users?limit=100", "users/" + user, "invitations?limit=100", "mail-deliveries?limit=100", "audit-events?limit=100"} {
		anonymous.request("GET", "/api/v1/admin/"+path, nil, 401, "Problem", nil)
		portal.request("GET", "/api/v1/admin/"+path, nil, 404, "Problem", nil)
	}
	t.Log("CURRENT_M01_HTTP_PROFILE whitelist_injections=4 stale_version_409=true persisted_profile_audit=true unauthorized_lists_detail=true")
}

func TestM01CurrentHTTPSessionExpiryFixation(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	for _, c := range []*client{portal, admin} {
		name := "__Host-" + c.audience + "_session"
		currentHTTPLogin(c, "admin@example.invalid", password)
		token := c.cookies[name].Value
		hash := security.Digest(token)
		touch := s.Config.Policy().Identity.SessionTouchSeconds
		currentHTTPExec(t, s, "UPDATE sessions SET created_at=clock_timestamp()-interval '2 hours',last_seen_at=clock_timestamp()-$2::bigint*interval '1 second' WHERE token_hash=$1", hash, touch/2)
		var recent, unchanged, touched time.Time
		if s.DB.QueryRow(t.Context(), "SELECT last_seen_at FROM sessions WHERE token_hash=$1", hash).Scan(&recent) != nil {
			t.Fatal("CURRENT_M01_HTTP_SESSION_ACTIVITY_READ_FAILED")
		}
		c.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
		if s.DB.QueryRow(t.Context(), "SELECT last_seen_at FROM sessions WHERE token_hash=$1", hash).Scan(&unchanged) != nil || !recent.Equal(unchanged) {
			t.Fatal("CURRENT_M01_HTTP_RECENT_ACTIVITY_NOT_COALESCED")
		}
		currentHTTPExec(t, s, "UPDATE sessions SET last_seen_at=clock_timestamp()-$2::bigint*interval '1 second' WHERE token_hash=$1", hash, touch+1)
		c.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
		if s.DB.QueryRow(t.Context(), "SELECT last_seen_at FROM sessions WHERE token_hash=$1", hash).Scan(&touched) != nil || !touched.After(unchanged) {
			t.Fatal("CURRENT_M01_HTTP_OLD_ACTIVITY_NOT_REFRESHED")
		}
		currentHTTPExec(t, s, "UPDATE sessions SET created_at=clock_timestamp()-$2::bigint*interval '1 second'-interval '2 hours',last_seen_at=clock_timestamp()-$2::bigint*interval '1 second' WHERE token_hash=$1", hash, int(s.Idle(c.audience)/time.Second)+1)
		c.request("GET", "/api/v1/me", nil, 401, "Problem", nil)
		if c.cookies[name] != nil {
			t.Fatal("CURRENT_M01_HTTP_IDLE_COOKIE_NOT_CLEARED")
		}
		currentHTTPLogin(c, "admin@example.invalid", password)
		hash = security.Digest(c.cookies[name].Value)
		currentHTTPExec(t, s, "UPDATE sessions SET created_at=clock_timestamp()-interval '2 days',last_seen_at=clock_timestamp(),absolute_expires_at=clock_timestamp()-interval '1 second' WHERE token_hash=$1", hash)
		c.request("GET", "/api/v1/me", nil, 401, "Problem", nil)
		if c.cookies[name] != nil {
			t.Fatal("CURRENT_M01_HTTP_ABSOLUTE_COOKIE_NOT_CLEARED")
		}
		chosen := security.Token()
		c.cookies[name] = &http.Cookie{Name: name, Value: chosen}
		currentHTTPLogin(c, "admin@example.invalid", password)
		previous := c.cookies[name].Value
		if previous == chosen || !security.ValidToken(previous) {
			t.Fatal("CURRENT_M01_HTTP_CHOSEN_SESSION_FIXATED")
		}
		currentHTTPLogin(c, "admin@example.invalid", password)
		if c.cookies[name].Value == previous {
			t.Fatal("CURRENT_M01_HTTP_EXISTING_SESSION_NOT_ROTATED")
		}
		currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE token_hash=$1", 0, security.Digest(chosen))
		currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE token_hash=$1 AND revoked_at IS NULL", 0, security.Digest(previous))
		for _, old := range []string{chosen, previous} {
			if _, e := s.Authenticate(t.Context(), old, c.audience); e == nil {
				t.Fatal("CURRENT_M01_HTTP_PREVIOUS_OR_CHOSEN_AUTHORITY_VALID")
			}
		}
	}
	t.Log("CURRENT_M01_HTTP_SESSION audiences=2 idle_absolute_refused_cookie_cleared=true coalesced_touch=true chosen_and_existing_rotation=true")
}

func TestM01CurrentHTTPLogoutStatesAudiences(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(portal, "admin@example.invalid", password)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	old := portal.cookies["__Host-portal_session"].Value
	portal.mutate("POST", "/api/v1/auth/logout", nil, 204, "")
	if portal.cookies["__Host-portal_session"] != nil || portal.cookies["__Host-portal_csrf_context"] != nil {
		t.Fatal("CURRENT_M01_HTTP_LOGOUT_COOKIES_RETAINED")
	}
	for range 2 {
		portal.cookies["__Host-portal_session"] = &http.Cookie{Name: "__Host-portal_session", Value: old}
		portal.mutate("POST", "/api/v1/auth/logout", nil, 204, "")
	}
	portal.mutate("POST", "/api/v1/auth/logout", nil, 204, "")
	admin.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	currentHTTPCount(t, s, "SELECT count(*) FROM audit_events WHERE action='auth.logout'", 1)
	currentHTTPLogin(portal, "admin@example.invalid", password)
	expired := portal.cookies["__Host-portal_session"].Value
	currentHTTPExec(t, s, "UPDATE sessions SET created_at=clock_timestamp()-interval '2 days',absolute_expires_at=clock_timestamp()-interval '1 second' WHERE token_hash=$1", security.Digest(expired))
	for range 2 {
		portal.cookies["__Host-portal_session"] = &http.Cookie{Name: "__Host-portal_session", Value: expired}
		portal.mutate("POST", "/api/v1/auth/logout", nil, 204, "")
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM audit_events WHERE action='auth.logout'", 2)
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE audience='admin' AND revoked_at IS NULL", 1)
	if _, e := s.Authenticate(t.Context(), old, "portal"); e == nil {
		t.Fatal("CURRENT_M01_HTTP_LOGOUT_OLD_SESSION_VALID")
	}
	t.Log("CURRENT_M01_HTTP_LOGOUT missing_expired_revoked_repeated_204=true audience_isolated=true cookies_cleared=true one_audit_per_actual_revocation=true")
}

func TestM01CurrentHTTPDisableEnableConflicts(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	user, memberPassword := currentHTTPMember(t, s, portal, admin, "disable-member@example.invalid")
	currentHTTPLogin(portal, "disable-member@example.invalid", memberPassword)
	old := portal.cookies["__Host-portal_session"].Value
	portal.mutate("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": "disable-member@example.invalid"}, 202, "AcceptedResponse")
	reset := fixture.ResetToken(t, s, "disable-member@example.invalid")
	before, e := s.GetUser(t.Context(), user)
	if e != nil {
		t.Fatal("CURRENT_M01_HTTP_DISABLE_MEMBER_READ_FAILED")
	}
	path := "/api/v1/admin/users/" + user + "/status"
	disabled := admin.mutate("PATCH", path, map[string]any{"version": before.Version, "status": "disabled", "reason": "Synthetic disabling reason"}, 200, "UserResponse")
	image := currentHTTPImage(t, s)
	currentHTTPProblem(t, admin.mutate("PATCH", path, map[string]any{"version": before.Version, "status": "active", "reason": "Must not overwrite newer state"}, 409, "Problem"), "VERSION_CONFLICT")
	currentHTTPUnchanged(t, s, image)
	admin.mutate("PATCH", path, map[string]any{"version": disabled["version"], "status": "active", "reason": "Synthetic enabling reason"}, 200, "UserResponse")
	after, e := s.GetUser(t.Context(), user)
	if e != nil || after.Status != "active" || after.Epoch != before.Epoch+1 || after.Version != before.Version+2 {
		t.Fatal("CURRENT_M01_HTTP_DISABLE_ENABLE_POSTIMAGE_FAILED")
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL", 0, user)
	currentHTTPCount(t, s, "SELECT count(*) FROM password_reset_tokens WHERE user_id=$1 AND token_hash IS NOT NULL", 0, user)
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE user_id=$1 AND purpose='password_reset' AND state='cancelled' AND payload_ciphertext IS NULL", 1, user)
	currentHTTPCount(t, s, "SELECT count(*) FROM audit_events WHERE subject_user_id=$1 AND target_id=$1 AND actor_user_id=(SELECT primary_admin_user_id FROM installation_state WHERE id=1) AND audience='admin' AND ((action='account.disabled' AND reason='Synthetic disabling reason') OR (action='account.enabled' AND reason='Synthetic enabling reason'))", 2, user)
	if _, e := s.Authenticate(t.Context(), old, "portal"); e == nil {
		t.Fatal("CURRENT_M01_HTTP_ENABLE_RESURRECTED_SESSION")
	}
	portal.request("GET", "/api/v1/me", nil, 401, "Problem", nil)
	currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/password-resets", map[string]any{"token": reset, "password": fixture.Password()}, 400, "Problem"), "TOKEN_UNUSABLE")
	currentHTTPLogin(portal, "disable-member@example.invalid", memberPassword)
	t.Log("CURRENT_M01_HTTP_DISABLE_ENABLE old_authority_revoked=true epoch_increment_once=true stale_409_no_mutation=true reasons_actor_audit=true fresh_login_required=true")
}

func TestM01CurrentHTTPResendPostimageConflicts(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	i := currentHTTPInvite(admin, "resend-postimage@example.invalid")
	id := i["id"].(string)
	old := fixture.InvitationToken(t, s, id)
	path, key := "/api/v1/admin/invitations/"+id+"/resends", uuid.NewString()
	body := map[string]any{"version": i["version"]}
	admin.refresh()
	updated := admin.request("POST", path, body, 200, "InvitationResponse", map[string]string{"Idempotency-Key": key})
	if updated["version"].(float64) != i["version"].(float64)+1 {
		t.Fatal("CURRENT_M01_HTTP_RESEND_VERSION_NOT_ADVANCED")
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM invitations WHERE id=$1 AND token_generation=2 AND version=2 AND expires_at>clock_timestamp()", 1, id)
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE invitation_id=$1", 2, id)
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE invitation_id=$1 AND state='cancelled' AND payload_ciphertext IS NULL AND payload_key_id IS NULL", 1, id)
	image := currentHTTPImage(t, s)
	admin.refresh()
	admin.request("POST", path, body, 200, "InvitationResponse", map[string]string{"Idempotency-Key": key})
	currentHTTPUnchanged(t, s, image)
	admin.refresh()
	currentHTTPProblem(t, admin.request("POST", path, map[string]any{"version": updated["version"]}, 409, "Problem", map[string]string{"Idempotency-Key": key}), "IDEMPOTENCY_CONFLICT")
	currentHTTPProblem(t, admin.mutate("POST", path, body, 409, "Problem"), "VERSION_CONFLICT")
	currentHTTPUnchanged(t, s, image)
	currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/invitation-validations", map[string]any{"token": old}, 400, "Problem"), "TOKEN_UNUSABLE")
	currentHTTPExec(t, s, "UPDATE invitations SET created_at=clock_timestamp()-interval '2 days',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", id)
	updated = admin.mutate("POST", path, map[string]any{"version": updated["version"]}, 200, "InvitationResponse")
	currentHTTPCount(t, s, "SELECT count(*) FROM invitations WHERE id=$1 AND token_generation=3 AND version=3 AND expires_at>clock_timestamp() AND expires_at<=clock_timestamp()+$2::bigint*interval '1 second'", 1, id, s.Config.Policy().Identity.InvitationTTLSeconds)
	currentHTTPExec(t, s, "UPDATE invitations SET expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", id)
	newer := currentHTTPInvite(admin, "resend-postimage@example.invalid")
	var version int64
	if s.DB.QueryRow(t.Context(), "SELECT version FROM invitations WHERE id=$1", id).Scan(&version) != nil {
		t.Fatal("CURRENT_M01_HTTP_EXPIRED_VERSION_READ_FAILED")
	}
	image = currentHTTPImage(t, s)
	currentHTTPProblem(t, admin.mutate("POST", path, map[string]any{"version": version}, 409, "Problem"), "INVITATION_EXISTS")
	currentHTTPUnchanged(t, s, image)
	portal.mutate("POST", "/api/v1/auth/register", map[string]any{"token": fixture.InvitationToken(t, s, newer["id"].(string)), "display_name": "Synthetic newer member", "password": fixture.Password()}, 201, "RegisterResponse")
	image = currentHTTPImage(t, s)
	currentHTTPProblem(t, admin.mutate("POST", path, map[string]any{"version": version}, 409, "Problem"), "ACCOUNT_EXISTS")
	currentHTTPUnchanged(t, s, image)
	t.Log("CURRENT_M01_HTTP_RESEND generation_version_expiry=true old_mail_cancelled=true exact_replay_no_new_mail=true stale_changed_payload_409=true expired_conflicts_preserve_state=true")
}

// These additional private rows complement currentHTTPImage. Admission counters
// and coalesced session activity are intentional effects, not business authority.
// Neither snapshot nor any account/token contents are emitted to test logs.
func currentHTTPBoundaryLedgerImage(t *testing.T, s *identity.Service) [sha256.Size]byte {
	t.Helper()
	var raw string
	if s.DB.QueryRow(t.Context(), `SELECT jsonb_build_object(
		'tenants',(SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM tenants v),
		'workspaces',(SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM workspaces v),
		'installation',(SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM installation_state v),
		'audit',(SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM audit_events v),
		'idempotency',(SELECT jsonb_agg(to_jsonb(v) ORDER BY actor_user_id,operation_id,idempotency_key) FROM idempotency_records v))::text`).Scan(&raw) != nil {
		t.Fatal("CURRENT_M01_HTTP_BOUNDARY_LEDGER_READ_FAILED")
	}
	return sha256.Sum256([]byte(raw))
}

func TestM01CurrentHTTPEmailConflictVariants(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	member, _ := currentHTTPMember(t, s, portal, admin, "conflict-existing@example.invalid")
	pending := currentHTTPInvite(admin, "conflict-pending@example.invalid")
	currentHTTPCount(t, s, "SELECT count(*) FROM users WHERE id=$1 AND NOT is_platform_admin AND email_canonical=$2", 1, member, "conflict-existing@example.invalid")
	currentHTTPCount(t, s, "SELECT count(*) FROM invitations WHERE id=$1 AND status='pending' AND email_canonical=$2", 1, pending["id"], "conflict-pending@example.invalid")
	currentHTTPCount(t, s, `SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relname IN ('users_email_canonical_key','invitations_pending_email_idx') AND i.indisunique AND i.indisvalid`, 2)
	admin.refresh()
	before, ledger := currentHTTPImage(t, s), currentHTTPBoundaryLedgerImage(t, s)
	keys := map[string]bool{}
	for _, target := range []struct{ email, code string }{
		{"conflict-existing@example.invalid", "ACCOUNT_EXISTS"},
		{"conflict-pending@example.invalid", "INVITATION_EXISTS"},
	} {
		var canonicalProblem string
		for _, email := range []string{target.email, strings.ToUpper(target.email)} {
			key := uuid.NewString()
			if keys[key] {
				t.Fatal("CURRENT_M01_HTTP_CONFLICT_FRESH_KEY_REQUIRED")
			}
			keys[key] = true
			currentHTTPCount(t, s, "SELECT count(*) FROM idempotency_records WHERE idempotency_key=$1", 0, key)
			result := admin.request("POST", "/api/v1/admin/invitations", map[string]any{"email": email, "locale": "en"}, 409, "Problem", map[string]string{"Idempotency-Key": key})
			problem := currentHTTPProblem(t, result, target.code)
			if canonicalProblem != "" && canonicalProblem != problem {
				t.Fatal("CURRENT_M01_HTTP_CONFLICT_CASE_VARIANT_RESPONSE_CHANGED")
			}
			canonicalProblem = problem
			currentHTTPUnchanged(t, s, before)
			if currentHTTPBoundaryLedgerImage(t, s) != ledger {
				t.Fatal("CURRENT_M01_HTTP_CONFLICT_CHANGED_LEDGER_OR_SCOPE")
			}
			currentHTTPCount(t, s, "SELECT count(*) FROM idempotency_records WHERE idempotency_key=$1", 0, key)
		}
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM users", 2)
	currentHTTPCount(t, s, "SELECT count(*) FROM invitations", 2)
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries", 2)
	t.Log("CURRENT_M01_HTTP_EMAIL_CONFLICTS ordinary_account_and_separate_pending=true canonical_and_uppercase=4 fresh_keys=4 conflicts_409=true canonical_unique_indexes=2 exact_authority_ledger_scope_unchanged=true no_new_mail_or_idempotency=true")
}

func TestM01CurrentHTTPKnownResourceScope(t *testing.T) {
	s, server, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	administrator := admin.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)["user"].(map[string]any)["id"].(string)
	member, memberPassword := currentHTTPMember(t, s, portal, admin, "scope-member@example.invalid")
	other, _ := currentHTTPMember(t, s, portal, admin, "scope-other@example.invalid")
	pending := currentHTTPInvite(admin, "scope-pending@example.invalid")
	invitation := pending["id"].(string)
	currentHTTPLogin(portal, "scope-member@example.invalid", memberPassword)
	me := portal.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	user, workspace := me["user"].(map[string]any), me["workspace"].(map[string]any)
	if user["id"] != member || user["is_platform_admin"] != false || workspace["role"] != "member" || member == other || member == administrator || other == administrator {
		t.Fatal("CURRENT_M01_HTTP_SCOPE_REAL_DISTINCT_MEMBER_REQUIRED")
	}
	for _, id := range []string{administrator, other} {
		if admin.request("GET", "/api/v1/admin/users/"+id, nil, 200, "UserResponse", nil)["id"] != id {
			t.Fatal("CURRENT_M01_HTTP_SCOPE_AUTHORIZED_TARGET_CONTROL_FAILED")
		}
	}
	missing := uuid.NewString()
	currentHTTPCount(t, s, "SELECT (SELECT count(*) FROM users WHERE id=$1)+(SELECT count(*) FROM invitations WHERE id=$1)", 0, missing)
	portal.refresh()
	privateValues := []string{administrator, member, other, invitation, workspace["id"].(string), workspace["tenant_id"].(string), "admin@example.invalid", "scope-member@example.invalid", "scope-other@example.invalid", "scope-pending@example.invalid", password, memberPassword, portal.cookies["__Host-portal_session"].Value}
	noDisclosure := func(problem string) {
		t.Helper()
		for _, value := range privateValues {
			if value == "" || strings.Contains(problem, value) {
				t.Fatal("CURRENT_M01_HTTP_SCOPE_PRIVATE_VALUE_DISCLOSED")
			}
		}
	}
	validCSRF := func() {
		t.Helper()
		if !security.VerifyCSRF(s.HMACKey, "portal", portal.cookies["__Host-portal_csrf_context"].Value, portal.cookies["__Host-portal_session"].Value, portal.csrf, time.Now(), time.Duration(s.Config.Policy().Identity.CSRFSeconds)*time.Second) {
			t.Fatal("CURRENT_M01_HTTP_SCOPE_VALID_MEMBER_CSRF_REQUIRED")
		}
	}
	before, ledger := currentHTTPImage(t, s), currentHTTPBoundaryLedgerImage(t, s)
	unchanged := func() {
		t.Helper()
		currentHTTPUnchanged(t, s, before)
		if currentHTTPBoundaryLedgerImage(t, s) != ledger {
			t.Fatal("CURRENT_M01_HTTP_SCOPE_CHANGED_LEDGER_OR_AUTHORITY")
		}
	}
	var absentProblem string
	refuse := func(method, path string, body any) {
		t.Helper()
		validCSRF()
		result := portal.request(method, path, body, 404, "Problem", map[string]string{"Idempotency-Key": uuid.NewString()})
		problem := currentHTTPProblem(t, result, "RESOURCE_NOT_FOUND")
		noDisclosure(problem)
		if absentProblem != "" && absentProblem != problem {
			t.Fatal("CURRENT_M01_HTTP_SCOPE_RESOURCE_EXISTENCE_DISCLOSED")
		}
		absentProblem = problem
		unchanged()
	}
	for _, path := range []string{
		"users?email_prefix=scope-other&limit=1",
		"invitations?status=pending&limit=1",
		"mail-deliveries?purpose=invitation&limit=1",
		"audit-events?limit=1",
	} {
		refuse("GET", "/api/v1/admin/"+path, nil)
	}
	for _, id := range []string{administrator, other, invitation, missing} {
		refuse("GET", "/api/v1/admin/users/"+id, nil)
		refuse("PATCH", "/api/v1/admin/users/"+id+"/status", map[string]any{"version": 1, "status": "disabled", "reason": "Must not disable known target"})
		refuse("POST", "/api/v1/admin/invitations/"+id+"/resends", map[string]any{"version": pending["version"]})
		refuse("POST", "/api/v1/admin/invitations/"+id+"/revocations", map[string]any{"version": pending["version"], "reason": "Must not revoke known target"})
	}
	var validationProblem string
	inject := func(field string, value any) {
		t.Helper()
		validCSRF()
		body := map[string]any{"display_name": "Must not change private identity", "locale": "en", "version": user["version"], field: value}
		result := portal.request("PATCH", "/api/v1/me", body, 400, "Problem", nil)
		problem := currentHTTPProblem(t, result, "VALIDATION_FAILED")
		noDisclosure(problem)
		if validationProblem != "" && validationProblem != problem {
			t.Fatal("CURRENT_M01_HTTP_SCOPE_OVERPOSTING_DISCLOSED_TARGET")
		}
		validationProblem = problem
		unchanged()
	}
	for _, id := range []string{administrator, other, invitation} {
		for _, field := range []string{"id", "user_id", "invitation_id"} {
			inject(field, id)
		}
	}
	for _, field := range []struct {
		name  string
		value any
	}{
		{"is_platform_admin", true},
		{"role", "owner"},
		{"tenant_id", workspace["tenant_id"]},
		{"workspace_id", workspace["id"]},
		{"email", "admin@example.invalid"},
	} {
		inject(field.name, field.value)
	}
	// A Portal token never becomes an Admin session by changing its cookie name.
	// Real Admin authentication rejects this before an authorization403 is possible.
	replay := newClient(t, server, "admin", portal.schemas)
	var replayProblem string
	for _, id := range []string{administrator, other, invitation, missing} {
		replay.cookies["__Host-admin_session"] = &http.Cookie{Name: "__Host-admin_session", Value: portal.cookies["__Host-portal_session"].Value}
		result := replay.request("GET", "/api/v1/admin/users/"+id, nil, 401, "Problem", nil)
		problem := currentHTTPProblem(t, result, "AUTH_SESSION_EXPIRED")
		noDisclosure(problem)
		if replayProblem != "" && replayProblem != problem || replay.cookies["__Host-admin_session"] != nil {
			t.Fatal("CURRENT_M01_HTTP_SCOPE_AUDIENCE_REPLAY_NOT_UNIFORMLY_CLEARED")
		}
		replayProblem = problem
		unchanged()
	}
	if portal.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)["user"].(map[string]any)["id"] != member {
		t.Fatal("CURRENT_M01_HTTP_SCOPE_MEMBER_SESSION_CHANGED")
	}
	unchanged()
	t.Log("CURRENT_M01_HTTP_KNOWN_SCOPE actual_admin_other_user_pending_invitation=true missing_id_control=true protected_lists=4 path_mutations=16 uniform_portal_404=20 valid_csrf=true overposting_400=14 audience_replay_401=4 exact_authority_ledger_scope_unchanged=true authorized_admin_and_member_controls=true")
}

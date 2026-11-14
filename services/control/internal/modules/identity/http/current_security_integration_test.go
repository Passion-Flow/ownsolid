//go:build repository_integration

package http_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/pprof"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

const currentSecuritySamples = 12
const currentSecurityOperationSeconds = 20

func currentSecurityStack(raw, class string) bool {
	for _, block := range strings.Split(raw, "\n\n") {
		for _, line := range strings.Split(block, "\n") {
			if !strings.HasPrefix(line, "# labels: ") {
				continue
			}
			var labels map[string]string
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "# labels: ")), &labels) == nil && labels["m01_credential_class"] == class && strings.Contains(block, "identity.(*Service).Login") && strings.Contains(block, "identity/security.(*Passwords).Verify") && strings.Contains(block, "argon2.deriveKey") {
				return true
			}
		}
	}
	return false
}

func TestM01CurrentHashVerificationProfiles(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), currentSecurityOperationSeconds*time.Second)
	defer cancel()
	var phc string
	if s.DB.QueryRow(ctx, "SELECT password_phc FROM password_credentials").Scan(&phc) != nil {
		t.Fatal("CURRENT_M01_HASH_PRIVATE_FORMAT_READ_FAILED")
	}
	// Fixed credential-format parameters, never deployment-specific settings.
	format := "$argon2id$v=19$m=19456,t=2,p=1$"
	if !strings.HasPrefix(phc, format) || !strings.HasPrefix(s.Passwords.Dummy, format) {
		t.Fatal("CURRENT_M01_REAL_DUMMY_HASH_COST_MISMATCH")
	}
	for _, item := range []struct{ class, email string }{{"known_wrong", "admin@example.invalid"}, {"unknown", "unknown@example.invalid"}} {
		observed := 0
		samples := make([]float64, 0, currentSecuritySamples)
		for range currentSecuritySamples {
			start := time.Now()
			result := make(chan error, 1)
			callContext, callCancel := context.WithCancel(ctx)
			joined, matched := false, false
			t.Cleanup(func() {
				callCancel()
				if !joined {
					select {
					case <-result:
					case <-time.After(2 * time.Second):
						t.Error("CURRENT_M01_HASH_WORKER_CLEANUP_JOIN_TIMEOUT")
					}
				}
			})
			go pprof.Do(callContext, pprof.Labels("m01_credential_class", item.class), func(call context.Context) {
				_, _, _, e := s.Login(call, item.email, password+"incorrect", "portal", "", uuid.NewString())
				result <- e
			})
			for !joined {
				select {
				case e := <-result:
					joined = true
					if e == nil || e.Error() != "AUTH_INVALID_CREDENTIALS" {
						t.Fatal("CURRENT_M01_HASH_GENERIC_REJECTION_REQUIRED")
					}
				case <-ctx.Done():
					// The actual password worker has no asynchronous escape; join it
					// before reporting a failed bounded observation.
					select {
					case <-result:
						joined = true
					case <-time.After(2 * time.Second):
						t.Error("CURRENT_M01_HASH_WORKER_JOIN_TIMEOUT")
					}
					t.Fatal("CURRENT_M01_HASH_OBSERVATION_CONTEXT_ENDED")
				case <-time.After(time.Millisecond):
					if !matched {
						var snapshot bytes.Buffer
						if pprof.Lookup("goroutine").WriteTo(&snapshot, 1) != nil || snapshot.Len() > 1024*1024 {
							t.Fatal("CURRENT_M01_HASH_STACK_CAPTURE_FAILED")
						}
						matched = currentSecurityStack(snapshot.String(), item.class)
					}
				}
			}
			callCancel()
			if matched {
				observed++
			}
			samples = append(samples, float64(time.Since(start).Microseconds())/1000)
		}
		if observed == 0 {
			t.Fatal("CURRENT_M01_ACTUAL_LABELED_ARGON2_VERIFY_STACK_NOT_OBSERVED", item.class)
		}
		sort.Float64s(samples)
		t.Logf("CURRENT_M01_HASH_PROFILE class=%s attempts=%d actual_labeled_argon2_verify_stacks=%d min_ms=%.3f p50_ms=%.3f p95_ms=%.3f max_ms=%.3f quantile=nearest_rank instrumented_local_only=true", item.class, len(samples), observed, samples[0], samples[(len(samples)-1)/2], samples[len(samples)-1], samples[len(samples)-1])
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM sessions", 0)
}

func TestM01CurrentHTTPCookieContract(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	for _, c := range []*client{portal, admin} {
		currentHTTPLogin(c, "admin@example.invalid", password)
		c.refresh()
		for _, item := range []struct {
			suffix string
			site   http.SameSite
			age    int
		}{{"session", http.SameSiteLaxMode, int(s.Absolute(c.audience) / time.Second)}, {"csrf_context", http.SameSiteStrictMode, s.Config.Policy().Identity.CSRFSeconds}} {
			cookie := c.cookies["__Host-"+c.audience+"_"+item.suffix]
			if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != item.site || cookie.MaxAge != item.age || !security.ValidToken(cookie.Value) {
				t.Fatal("CURRENT_M01_EXACT_COOKIE_ATTRIBUTES_REQUIRED", item.suffix)
			}
		}
		r, e := http.NewRequestWithContext(t.Context(), "POST", c.s.URL+"/api/v1/auth/logout", nil)
		if e != nil {
			t.Fatal("CURRENT_M01_COOKIE_LOGOUT_REQUEST_FAILED")
		}
		r.Host = c.audience + ".example.invalid"
		r.Header.Set("Origin", "https://"+r.Host)
		r.Header.Set("X-CSRF-Token", c.csrf)
		for _, cookie := range c.cookies {
			r.AddCookie(cookie)
		}
		response, e := c.http.Do(r)
		if e != nil {
			t.Fatal("CURRENT_M01_COOKIE_LOGOUT_TRANSPORT_FAILED")
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != 204 || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || response.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CURRENT_M01_COOKIE_LOGOUT_RESPONSE_FAILED")
		}
		cleared := map[string]bool{}
		for _, cookie := range response.Cookies() {
			site := http.SameSiteLaxMode
			if strings.HasSuffix(cookie.Name, "_csrf_context") {
				site = http.SameSiteStrictMode
			}
			if cookie.Value != "" || cookie.MaxAge >= 0 || !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != site || cleared[cookie.Name] {
				t.Fatal("CURRENT_M01_EXACT_COOKIE_CLEAR_ATTRIBUTES_REQUIRED")
			}
			cleared[cookie.Name] = true
		}
		if len(cleared) != 2 || !cleared["__Host-"+c.audience+"_session"] || !cleared["__Host-"+c.audience+"_csrf_context"] {
			t.Fatal("CURRENT_M01_BOTH_IDENTITY_COOKIES_MUST_CLEAR")
		}
	}
	t.Log("CURRENT_M01_COOKIE_CONTRACT audiences=2 exact_names_paths_flags_ages_samesite=true both_clear_headers=true verified_tls=true no_cors=true")
}

func TestM01CurrentHTTPRecoveryTimingEligibility(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	for _, email := range []string{"eligible@example.invalid", "disabled@example.invalid", "admin-ineligible@example.invalid"} {
		id, _ := currentHTTPMember(t, s, portal, admin, email)
		if email == "disabled@example.invalid" {
			u, e := s.GetUser(t.Context(), id)
			if e != nil {
				t.Fatal("CURRENT_M01_RECOVERY_DISABLED_FIXTURE_READ_FAILED")
			}
			admin.mutate("PATCH", "/api/v1/admin/users/"+id+"/status", map[string]any{"status": "disabled", "version": u.Version, "reason": "Synthetic recovery eligibility"}, 200, "UserResponse")
		}
	}
	for _, item := range []struct {
		class, email string
		client       *client
	}{{"eligible", "eligible@example.invalid", portal}, {"unknown", "unknown@example.invalid", portal}, {"disabled", "disabled@example.invalid", portal}, {"admin_ineligible", "admin-ineligible@example.invalid", admin}} {
		start := time.Now()
		result := item.client.mutate("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": item.email}, 202, "AcceptedResponse")
		elapsed := time.Since(start)
		if len(result) != 1 || result["accepted"] != true || elapsed < time.Duration(s.Config.Policy().Identity.ResetMinimumMilliseconds)*time.Millisecond {
			t.Fatal("CURRENT_M01_RECOVERY_UNIFORM_BODY_OR_MINIMUM_DELAY_FAILED", item.class)
		}
		t.Logf("CURRENT_M01_RECOVERY_TIMING class=%s elapsed_ms=%.3f configured_minimum_ms=%d generic_202=true", item.class, float64(elapsed.Microseconds())/1000, s.Config.Policy().Identity.ResetMinimumMilliseconds)
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM password_reset_tokens", 1)
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE purpose='password_reset' AND recipient_email='eligible@example.invalid' AND state='queued'", 1)
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE purpose='password_reset' AND recipient_email<>'eligible@example.invalid'", 0)
	t.Log("CURRENT_M01_RECOVERY_ELIGIBILITY classes=4 eligible_token_and_mail_only=true no_account_delivery_metadata=true")
}

func TestM01CurrentHTTPCSRFBindings(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	member, _ := currentHTTPMember(t, s, portal, admin, "csrf-member@example.invalid")
	currentHTTPLogin(portal, "admin@example.invalid", password)
	pending := currentHTTPInvite(admin, "csrf-pending@example.invalid")
	id := pending["id"].(string)
	inviteToken := fixture.InvitationToken(t, s, id)
	portal.mutate("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": "admin@example.invalid"}, 202, "AcceptedResponse")
	reset := fixture.ResetToken(t, s, "admin@example.invalid")
	user, e := s.GetUser(t.Context(), member)
	if e != nil {
		t.Fatal("CURRENT_M01_CSRF_FIXTURE_MEMBER_READ_FAILED")
	}
	me := portal.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	routes := []struct {
		client       *client
		method, path string
		body         any
	}{
		{portal, "POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}},
		{portal, "POST", "/api/v1/auth/logout", nil},
		{portal, "POST", "/api/v1/auth/invitation-validations", map[string]any{"token": inviteToken}},
		{portal, "POST", "/api/v1/auth/register", map[string]any{"token": inviteToken, "display_name": "Synthetic CSRF member", "password": fixture.Password()}},
		{portal, "POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": "admin@example.invalid"}},
		{portal, "POST", "/api/v1/auth/password-resets", map[string]any{"token": reset, "password": fixture.Password()}},
		{portal, "PATCH", "/api/v1/me", map[string]any{"display_name": "Must not change", "locale": "en", "version": me["user"].(map[string]any)["version"]}},
		{admin, "POST", "/api/v1/admin/invitations", map[string]any{"email": "csrf-new@example.invalid", "locale": "en"}},
		{admin, "POST", "/api/v1/admin/invitations/" + id + "/resends", map[string]any{"version": pending["version"]}},
		{admin, "POST", "/api/v1/admin/invitations/" + id + "/revocations", map[string]any{"version": pending["version"], "reason": "Must not revoke"}},
		{admin, "PATCH", "/api/v1/admin/users/" + member + "/status", map[string]any{"version": user.Version, "status": "disabled", "reason": "Must not disable"}},
	}
	for _, c := range []*client{portal, admin} {
		c.refresh()
	}
	before := currentHTTPImage(t, s)
	var auditCount int
	if s.DB.QueryRow(t.Context(), "SELECT count(*) FROM audit_events").Scan(&auditCount) != nil {
		t.Fatal("CURRENT_M01_CSRF_AUDIT_BASELINE_FAILED")
	}
	for _, route := range routes {
		c := route.client
		contextName := "__Host-" + c.audience + "_csrf_context"
		originalContext, originalToken := c.cookies[contextName], c.csrf
		session := c.cookies["__Host-"+c.audience+"_session"].Value
		for _, variant := range []string{"missing_token", "forged_token", "missing_context", "wrong_context", "wrong_audience", "wrong_session", "expired"} {
			c.cookies[contextName], c.csrf = originalContext, originalToken
			switch variant {
			case "missing_token":
				c.csrf = ""
			case "forged_token":
				c.csrf = security.Token()
			case "missing_context":
				delete(c.cookies, contextName)
			case "wrong_context":
				c.cookies[contextName] = &http.Cookie{Name: contextName, Value: security.Token()}
			case "wrong_audience", "wrong_session", "expired":
				audience, boundSession, now := c.audience, session, time.Now()
				if variant == "wrong_audience" {
					audience = "admin"
					if c.audience == "admin" {
						audience = "portal"
					}
				} else if variant == "wrong_session" {
					boundSession = security.Token()
				} else {
					now = now.Add(-time.Duration(s.Config.Policy().Identity.CSRFSeconds+1) * time.Second)
				}
				c.csrf, _ = security.CSRF(s.HMACKey, audience, originalContext.Value, boundSession, now, time.Duration(s.Config.Policy().Identity.CSRFSeconds)*time.Second)
			}
			currentHTTPProblem(t, c.request(route.method, route.path, route.body, 403, "Problem", nil), "CSRF_INVALID")
			currentHTTPUnchanged(t, s, before)
			currentHTTPCount(t, s, "SELECT count(*) FROM audit_events", auditCount)
		}
		c.cookies[contextName], c.csrf = originalContext, originalToken
	}
	portal.mutate("PATCH", "/api/v1/me", map[string]any{"display_name": "Valid same-origin update", "locale": "en", "version": me["user"].(map[string]any)["version"]}, 200, "MeResponse")
	t.Log("CURRENT_M01_CSRF_BINDINGS routes=11 valid_business_bodies=true variants=7 refusals=77 authority_and_business_audit_unchanged=true valid_same_origin_success=true")
}

func TestM01CurrentHTTPAuditTransportSecrecy(t *testing.T) {
	var captured bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&captured, nil)))
	t.Cleanup(func() { slog.SetDefault(originalLogger) })
	s, server, portal, admin, password := setup(t)
	secrets := []string{password}
	remember := func(c *client) {
		for _, cookie := range c.cookies {
			secrets = append(secrets, cookie.Value)
		}
		secrets = append(secrets, c.csrf)
	}
	currentHTTPLogin(admin, "admin@example.invalid", password)
	remember(admin)
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "unknown@example.invalid", "password": password}, 401, "Problem")
	i := currentHTTPInvite(admin, "audit-member@example.invalid")
	id := i["id"].(string)
	secrets = append(secrets, fixture.InvitationToken(t, s, id))
	i = admin.mutate("POST", "/api/v1/admin/invitations/"+id+"/resends", map[string]any{"version": i["version"]}, 200, "InvitationResponse")
	token, memberPassword := fixture.InvitationToken(t, s, id), fixture.Password()
	secrets = append(secrets, token, memberPassword)
	var delivery, key string
	var nonce, cipher []byte
	if s.DB.QueryRow(t.Context(), "SELECT id::text,payload_key_id,payload_nonce,payload_ciphertext FROM mail_deliveries WHERE invitation_id=$1 AND state='queued'", id).Scan(&delivery, &key, &nonce, &cipher) != nil {
		t.Fatal("CURRENT_M01_AUDIT_PRIVATE_MAIL_READ_FAILED")
	}
	raw, e := s.Mail.Keys.Decrypt(key, delivery, "invitation", nonce, cipher)
	var payload mail.Payload
	if e != nil || json.Unmarshal(raw, &payload) != nil {
		t.Fatal("CURRENT_M01_AUDIT_PRIVATE_MAIL_DECRYPT_FAILED")
	}
	link, e := url.Parse(payload.Link)
	if e != nil || link.Scheme+"://"+link.Host != s.Config.Server.PortalOrigin {
		t.Fatal("CURRENT_M01_CONFIGURED_MAIL_ORIGIN_REQUIRED")
	}
	secrets = append(secrets, payload.Link, base64.StdEncoding.EncodeToString(cipher), hex.EncodeToString(cipher))
	portal.mutate("POST", "/api/v1/auth/register", map[string]any{"token": token, "display_name": "Synthetic audit member", "password": memberPassword}, 201, "RegisterResponse")
	currentHTTPLogin(portal, "audit-member@example.invalid", memberPassword)
	remember(portal)
	me := portal.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	user := me["user"].(map[string]any)
	bodyMarker := "SubmittedBodyMarker-" + uuid.NewString()
	secrets = append(secrets, bodyMarker)
	portal.mutate("PATCH", "/api/v1/me", map[string]any{"display_name": bodyMarker, "locale": "en", "version": user["version"]}, 200, "MeResponse")
	portal.mutate("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": "audit-member@example.invalid"}, 202, "AcceptedResponse")
	reset, next := fixture.ResetToken(t, s, "audit-member@example.invalid"), fixture.Password()
	secrets = append(secrets, reset, next)
	portal.mutate("POST", "/api/v1/auth/password-resets", map[string]any{"token": reset, "password": next}, 200, "ResetResponse")
	currentHTTPLogin(portal, "audit-member@example.invalid", next)
	remember(portal)
	member, e := s.GetUser(t.Context(), user["id"].(string))
	if e != nil {
		t.Fatal("CURRENT_M01_AUDIT_MEMBER_READ_FAILED")
	}
	disabled := admin.mutate("PATCH", "/api/v1/admin/users/"+member.ID+"/status", map[string]any{"version": member.Version, "status": "disabled", "reason": "Synthetic audit disable"}, 200, "UserResponse")
	admin.mutate("PATCH", "/api/v1/admin/users/"+member.ID+"/status", map[string]any{"version": disabled["version"], "status": "active", "reason": "Synthetic audit enable"}, 200, "UserResponse")
	currentHTTPLogin(portal, "audit-member@example.invalid", next)
	remember(portal)
	portal.mutate("POST", "/api/v1/auth/logout", nil, 204, "")
	i = currentHTTPInvite(admin, "audit-revoked@example.invalid")
	secrets = append(secrets, fixture.InvitationToken(t, s, i["id"].(string)))
	admin.mutate("POST", "/api/v1/admin/invitations/"+i["id"].(string)+"/revocations", map[string]any{"version": i["version"], "reason": "Synthetic audit revoke"}, 200, "InvitationResponse")
	before := currentHTTPImage(t, s)
	for _, header := range []string{"Forwarded", "X-Forwarded-Host", "X-Forwarded-For", "X-Real-IP"} {
		portal.request("GET", "/health/live", nil, 403, "Problem", map[string]string{header: "untrusted.example.invalid"})
		currentHTTPUnchanged(t, s, before)
	}
	r, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL+"/health/live?input="+bodyMarker, nil)
	r.Host = "unknown.example.invalid"
	response, e := portal.http.Do(r)
	if e != nil {
		t.Fatal("CURRENT_M01_UNKNOWN_HOST_PROBE_FAILED")
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 421 {
		t.Fatal("CURRENT_M01_UNKNOWN_HOST_NOT_REJECTED")
	}
	currentHTTPUnchanged(t, s, before)
	public := admin.request("GET", "/api/v1/admin/audit-events?limit=100", nil, 200, "AuditListResponse", nil)
	publicRaw, e := json.Marshal(public)
	if e != nil {
		t.Fatal("CURRENT_M01_PUBLIC_AUDIT_ENCODING_FAILED")
	}
	var phc string
	if s.DB.QueryRow(t.Context(), "SELECT password_phc FROM password_credentials WHERE user_id=$1", member.ID).Scan(&phc) != nil {
		t.Fatal("CURRENT_M01_AUDIT_PRIVATE_CREDENTIAL_READ_FAILED")
	}
	secrets = append(secrets, phc)
	server.Close()
	// All HTTP writers have joined before inspecting the private logger buffer.
	logRaw := captured.String()
	for _, secret := range secrets {
		if secret != "" && (strings.Contains(logRaw, secret) || strings.Contains(string(publicRaw), secret)) {
			t.Fatal("CURRENT_M01_APPLICATION_OR_PUBLIC_AUDIT_SECRET_DISCLOSURE")
		}
	}
	requestIDs := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(logRaw))
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event["msg"] != "HTTP_REQUEST_TRANSPORT" {
			t.Fatal("CURRENT_M01_STRUCTURED_APPLICATION_LOG_REQUIRED")
		}
		id, ok := event["request_id"].(string)
		if _, e := uuid.Parse(id); !ok || e != nil || requestIDs[id] {
			t.Fatal("CURRENT_M01_UNIQUE_APPLICATION_REQUEST_ID_REQUIRED")
		}
		requestIDs[id] = true
	}
	if scanner.Err() != nil || len(requestIDs) == 0 {
		t.Fatal("CURRENT_M01_APPLICATION_LOG_CAPTURE_FAILED")
	}
	var primary string
	if s.DB.QueryRow(t.Context(), "SELECT primary_admin_user_id::text FROM installation_state WHERE id=1").Scan(&primary) != nil {
		t.Fatal("CURRENT_M01_AUDIT_PRIMARY_READ_FAILED")
	}
	rows, e := s.DB.Query(t.Context(), "SELECT action,audience,actor_user_id::text,subject_user_id::text,request_id::text,target_type,target_id::text FROM audit_events WHERE action<>'installation.bootstrapped' ORDER BY occurred_at,id")
	if e != nil {
		t.Fatal("CURRENT_M01_AUDIT_CORRELATION_READ_FAILED")
	}
	defer rows.Close()
	actions := map[string]int{}
	systemRequests := map[string]bool{}
	for rows.Next() {
		var action, audience, request string
		var actor, subject, targetType, target *string
		if rows.Scan(&action, &audience, &actor, &subject, &request, &targetType, &target) != nil {
			t.Fatal("CURRENT_M01_BUSINESS_AUDIT_ROW_READ_FAILED")
		}
		if _, e := uuid.Parse(request); e != nil {
			t.Fatal("CURRENT_M01_AUDIT_REQUEST_ID_REQUIRED")
		}
		if audience == "system" {
			// Existing mail cancellation owns a fresh operational request ID.
			// It must identify the exact terminal delivery, never a fabricated
			// HTTP actor or a made-up correlation with the browser request.
			if action != "mail.cancelled" || actor != nil || subject != nil || targetType == nil || *targetType != "mail_delivery" || target == nil || requestIDs[request] || systemRequests[request] {
				t.Fatal("CURRENT_M01_SYSTEM_CANCELLATION_AUDIT_CONTEXT_FAILED")
			}
			currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE id=$1 AND state='cancelled' AND payload_key_id IS NULL AND payload_ciphertext IS NULL", 1, *target)
			systemRequests[request] = true
			actions[action]++
			continue
		}
		if !requestIDs[request] {
			t.Fatal("CURRENT_M01_BUSINESS_AUDIT_REQUEST_CORRELATION_FAILED")
		}
		actions[action]++
		switch action {
		case "invitation.created", "invitation.resent", "invitation.revoked", "account.disabled", "account.enabled":
			if actor == nil || *actor != primary || audience != "admin" {
				t.Fatal("CURRENT_M01_ADMIN_AUDIT_ACTOR_MISMATCH")
			}
		case "invitation.accepted", "auth.password_reset":
			if actor != nil || subject == nil || *subject != member.ID || audience != "portal" {
				t.Fatal("CURRENT_M01_PUBLIC_FLOW_AUDIT_SUBJECT_MISMATCH")
			}
		case "auth.login_failed", "auth.recovery_requested":
			if actor != nil || subject != nil {
				t.Fatal("CURRENT_M01_ANONYMOUS_AUDIT_INVENTED_ACTOR")
			}
		case "auth.login_succeeded", "account.updated", "auth.logout":
			if actor == nil || subject == nil || *actor != *subject {
				t.Fatal("CURRENT_M01_AUTHENTICATED_AUDIT_ACTOR_MISMATCH")
			}
		default:
			t.Fatal("CURRENT_M01_UNEXPECTED_MAIN_FLOW_AUDIT_ACTION")
		}
	}
	if rows.Err() != nil {
		t.Fatal("CURRENT_M01_AUDIT_ROWS_FAILED")
	}
	for _, action := range []string{"auth.login_succeeded", "auth.login_failed", "invitation.created", "invitation.resent", "invitation.accepted", "account.updated", "auth.recovery_requested", "auth.password_reset", "account.disabled", "account.enabled", "invitation.revoked", "auth.logout"} {
		if actions[action] == 0 {
			t.Fatal("CURRENT_M01_EXPECTED_MAIN_FLOW_AUDIT_MISSING", action)
		}
	}
	t.Logf("CURRENT_M01_AUDIT_TRANSPORT actions=%d request_logs=%d business_ids_correlated=true system_cancellations=%d exact_terminal_targets=true exact_actors_subjects=true secret_and_body_markers_absent=true forwarding_refusals=4 unknown_host_421=true configured_mail_origin=true", len(actions), len(requestIDs), len(systemRequests))
}

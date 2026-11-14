//go:build repository_integration

package http_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM01CurrentHTTPAllRouteOriginBindings(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	currentHTTPLogin(admin, "admin@example.invalid", password)
	member, _ := currentHTTPMember(t, s, portal, admin, "origin-member@example.invalid")
	currentHTTPLogin(portal, "admin@example.invalid", password)
	pending := currentHTTPInvite(admin, "origin-pending@example.invalid")
	id := pending["id"].(string)
	inviteToken := fixture.InvitationToken(t, s, id)
	portal.mutate("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": "admin@example.invalid"}, 202, "AcceptedResponse")
	reset := fixture.ResetToken(t, s, "admin@example.invalid")
	user, e := s.GetUser(t.Context(), member)
	if e != nil {
		t.Fatal("CURRENT_M01_ORIGIN_FIXTURE_MEMBER_READ_FAILED")
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
		{portal, "POST", "/api/v1/auth/register", map[string]any{"token": inviteToken, "display_name": "Synthetic Origin member", "password": fixture.Password()}},
		{portal, "POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": "admin@example.invalid"}},
		{portal, "POST", "/api/v1/auth/password-resets", map[string]any{"token": reset, "password": fixture.Password()}},
		{portal, "PATCH", "/api/v1/me", map[string]any{"display_name": "Must not change", "locale": "en", "version": me["user"].(map[string]any)["version"]}},
		{admin, "POST", "/api/v1/admin/invitations", map[string]any{"email": "origin-new@example.invalid", "locale": "en"}},
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
		t.Fatal("CURRENT_M01_ORIGIN_AUDIT_BASELINE_FAILED")
	}
	for _, route := range routes {
		c := route.client
		var payload []byte
		if route.body != nil {
			payload, e = json.Marshal(route.body)
			if e != nil {
				t.Fatal("CURRENT_M01_ORIGIN_VALID_BODY_ENCODING_FAILED")
			}
		}
		for _, variant := range []string{"missing_origin", "hostile_origin", "duplicate_origin", "cross_site"} {
			r, e := http.NewRequestWithContext(t.Context(), route.method, c.s.URL+route.path, bytes.NewReader(payload))
			if e != nil {
				t.Fatal("CURRENT_M01_ORIGIN_REQUEST_CONSTRUCTION_FAILED")
			}
			r.Host = c.audience + ".example.invalid"
			if route.body != nil {
				r.Header.Set("Content-Type", "application/json")
			}
			r.Header.Set("X-CSRF-Token", c.csrf)
			r.Header.Set("Idempotency-Key", uuid.NewString())
			for _, cookie := range c.cookies {
				r.AddCookie(cookie)
			}
			switch variant {
			case "missing_origin":
				// A genuinely absent wire header, not an empty present field.
				r.Header.Del("Origin")
			case "hostile_origin":
				r.Header.Set("Origin", "https://hostile.example.invalid")
			case "duplicate_origin":
				r.Header.Add("Origin", "https://"+r.Host)
				r.Header.Add("Origin", "https://hostile.example.invalid")
			case "cross_site":
				r.Header.Set("Origin", "https://"+r.Host)
				r.Header.Set("Sec-Fetch-Site", "cross-site")
			}
			response, e := c.http.Do(r)
			if e != nil {
				t.Fatal("CURRENT_M01_ORIGIN_VERIFIED_REQUEST_FAILED")
			}
			raw, readError := io.ReadAll(io.LimitReader(response.Body, httpserver.MaxJSONBytes+1))
			response.Body.Close()
			if readError != nil || len(raw) > httpserver.MaxJSONBytes || response.StatusCode != 403 || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Request-ID") == "" || response.Header.Get("Content-Type") != "application/problem+json" || response.Header.Get("Access-Control-Allow-Origin") != "" || len(response.Cookies()) != 0 {
				t.Fatal("CURRENT_M01_ORIGIN_PUBLIC_REFUSAL_HEADERS_FAILED", variant)
			}
			var problem map[string]any
			if json.Unmarshal(raw, &problem) != nil || c.schemas["Problem"].Validate(problem) != nil {
				t.Fatal("CURRENT_M01_ORIGIN_PROBLEM_SCHEMA_FAILED")
			}
			currentHTTPProblem(t, problem, "ORIGIN_REJECTED")
			currentHTTPUnchanged(t, s, before)
			currentHTTPCount(t, s, "SELECT count(*) FROM audit_events", auditCount)
		}
	}
	portal.mutate("PATCH", "/api/v1/me", map[string]any{"display_name": "Valid Origin profile", "locale": "en", "version": me["user"].(map[string]any)["version"]}, 200, "MeResponse")
	admin.mutate("PATCH", "/api/v1/admin/users/"+member+"/status", map[string]any{"version": user.Version, "status": "active", "reason": "Valid same-origin no change"}, 200, "UserResponse")
	t.Log("CURRENT_M01_ORIGIN_BINDINGS routes=11 valid_business_bodies_and_csrf=true variants=4 refusals=44 absent_header_on_wire=true exact_authority_and_audit_unchanged=true both_audience_valid_operations=true")
}

func TestM01CurrentHTTPResetDestinationInputs(t *testing.T) {
	s, _, portal, _, _ := setup(t)
	before := currentHTTPImage(t, s)
	var auditCount int
	if s.DB.QueryRow(t.Context(), "SELECT count(*) FROM audit_events").Scan(&auditCount) != nil {
		t.Fatal("CURRENT_M01_DESTINATION_AUDIT_BASELINE_FAILED")
	}
	for _, field := range []string{"return_to", "redirect_uri", "url", "origin"} {
		body := map[string]any{"email": "admin@example.invalid", field: "https://hostile.example.invalid/reset-password"}
		currentHTTPProblem(t, portal.mutate("POST", "/api/v1/auth/password-reset-requests", body, 400, "Problem"), "VALIDATION_FAILED")
		currentHTTPUnchanged(t, s, before)
		currentHTTPCount(t, s, "SELECT count(*) FROM audit_events", auditCount)
	}
	portal.mutate("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": "admin@example.invalid"}, 202, "AcceptedResponse")
	var delivery, key string
	var nonce, cipher []byte
	if s.DB.QueryRow(t.Context(), "SELECT id::text,payload_key_id,payload_nonce,payload_ciphertext FROM mail_deliveries WHERE purpose='password_reset' AND state='queued'").Scan(&delivery, &key, &nonce, &cipher) != nil {
		t.Fatal("CURRENT_M01_DESTINATION_PRIVATE_MAIL_READ_FAILED")
	}
	raw, e := s.Mail.Keys.Decrypt(key, delivery, "password_reset", nonce, cipher)
	var payload mail.Payload
	if e != nil || json.Unmarshal(raw, &payload) != nil {
		t.Fatal("CURRENT_M01_DESTINATION_PRIVATE_MAIL_DECRYPT_FAILED")
	}
	link, e := url.Parse(payload.Link)
	if e != nil || link.Scheme+"://"+link.Host != s.Config.Server.PortalOrigin || link.Path != "/reset-password" || link.RawQuery != "" || link.User != nil || !strings.HasPrefix(link.Fragment, "token=") {
		t.Fatal("CURRENT_M01_RESET_DESTINATION_MUST_USE_CONFIGURED_ORIGIN")
	}
	token := strings.TrimPrefix(link.Fragment, "token=")
	if !security.ValidToken(token) {
		t.Fatal("CURRENT_M01_RESET_DESTINATION_TOKEN_FRAGMENT_INVALID")
	}
	currentHTTPCount(t, s, "SELECT count(*) FROM password_reset_tokens WHERE token_hash=$1 AND audience='portal' AND user_id=(SELECT primary_admin_user_id FROM installation_state WHERE id=1)", 1, security.Digest(token))
	currentHTTPCount(t, s, "SELECT count(*) FROM password_reset_tokens", 1)
	currentHTTPCount(t, s, "SELECT count(*) FROM mail_deliveries", 1)
	t.Log("CURRENT_M01_RESET_DESTINATION unknown_destination_fields=4 refusals_400_no_mutation=true valid_202_exact_one_delivery_token=true configured_origin_path_fragment=true no_query_or_user_info=true")
}

package http

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	ssohttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/google/uuid"
)

// A zero domain owner is deliberately unready. These cases prove the real common
// HTTP guards precede candidate dispatch; no mock can issue an authenticated result.
func TestSAMLCompletionKeepsOrdinaryHTTPBoundary(t *testing.T) {
	const origin = "https://portal.example.invalid"
	for _, item := range []struct {
		name, expected string
		change         func(*http.Request, httpserver.CookiePolicy)
	}{
		{"selected_owner_remains_unready", "SERVICE_NOT_READY", func(*http.Request, httpserver.CookiePolicy) {}},
		{"missing_origin", "ORIGIN_REJECTED", func(r *http.Request, _ httpserver.CookiePolicy) { r.Header.Del("Origin") }},
		{"hostile_origin", "ORIGIN_REJECTED", func(r *http.Request, _ httpserver.CookiePolicy) {
			r.Header.Set("Origin", "https://other.example.invalid")
		}},
		{"null_origin", "ORIGIN_REJECTED", func(r *http.Request, _ httpserver.CookiePolicy) { r.Header.Set("Origin", "null") }},
		{"duplicate_origin", "ORIGIN_REJECTED", func(r *http.Request, _ httpserver.CookiePolicy) { r.Header.Add("Origin", origin) }},
		{"cross_site_fetch", "ORIGIN_REJECTED", func(r *http.Request, _ httpserver.CookiePolicy) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
		{"duplicate_fetch", "ORIGIN_REJECTED", func(r *http.Request, _ httpserver.CookiePolicy) { r.Header.Add("Sec-Fetch-Site", "same-origin") }},
		{"missing_csrf", "CSRF_INVALID", func(r *http.Request, _ httpserver.CookiePolicy) { r.Header.Del("X-CSRF-Token") }},
		{"wrong_csrf", "CSRF_INVALID", func(r *http.Request, _ httpserver.CookiePolicy) { r.Header.Set("X-CSRF-Token", security.Token()) }},
		{"duplicate_csrf", "CSRF_INVALID", func(r *http.Request, _ httpserver.CookiePolicy) {
			r.Header.Add("X-CSRF-Token", r.Header.Get("X-CSRF-Token"))
		}},
		{"duplicate_context_cookie", "CSRF_INVALID", func(r *http.Request, c httpserver.CookiePolicy) {
			r.AddCookie(&http.Cookie{Name: c.Name("portal", httpserver.CSRFContextCookie), Value: security.Token()})
		}},
		{"duplicate_session_cookie", "AUTH_REQUIRED", func(r *http.Request, c httpserver.CookiePolicy) {
			r.AddCookie(&http.Cookie{Name: c.Name("portal", httpserver.SessionCookie), Value: security.Token()})
		}},
	} {
		t.Run(item.name, func(t *testing.T) {
			key := make([]byte, 32)
			if _, e := rand.Read(key); e != nil {
				t.Fatal(e)
			}
			accounts := &identity.Service{HMACKey: key}
			base := &sso.Service{Identity: accounts}
			h := &Handler{Service: accounts, SSO: &ssohttp.Handler{Service: base, SAMLCompletion: &sso.SAMLCompletion{}}}
			flow := uuid.NewString()
			path := "/api/v1/sso/saml/connectors/" + uuid.NewString() + "/flows/" + flow + "/complete"
			r := httptest.NewRequest(http.MethodPost, origin+path, strings.NewReader("{}"))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", flow)
			r.Header.Set("Origin", origin)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			cookies, e := httpserver.NewCookiePolicy(config.TransportDirectHTTPS)
			if e != nil {
				t.Fatal(e)
			}
			session, contextCookie := security.Token(), security.Token()
			r.AddCookie(&http.Cookie{Name: cookies.Name("portal", httpserver.SessionCookie), Value: session})
			r.AddCookie(&http.Cookie{Name: cookies.Name("portal", httpserver.CSRFContextCookie), Value: contextCookie})
			r.AddCookie(&http.Cookie{Name: cookies.Name("portal", httpserver.SSOFlowCookie), Value: security.Token()})
			token, _ := security.CSRF(key, "portal", contextCookie, session, time.Now(), config.Seconds(accounts.Config.Policy().Identity.CSRFSeconds))
			r.Header.Set("X-CSRF-Token", token)
			item.change(r, cookies)
			w := httptest.NewRecorder()
			out, status, e := h.api(w, r, "portal", origin, uuid.NewString())
			if e == nil || e.Error() != item.expected || out != nil || status != 0 || len(w.Result().Cookies()) != 0 {
				t.Fatal("ordinary invariant or fail-closed result changed", e)
			}
		})
	}
}

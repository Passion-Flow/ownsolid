package http_test

import (
	"net/http"
	"testing"

	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

const passwordChanges = "/api/v1/me/password-changes"

func problemCode(t *testing.T, body map[string]any, code string) {
	t.Helper()
	if body["code"] != code {
		t.Fatalf("expected %s, got %v", code, body["code"])
	}
}

func TestPasswordChangeRotatesTheCallersCookieOnly(t *testing.T) {
	_, _, portal, admin, password := setup(t)
	login := map[string]any{"email": "admin@example.invalid", "password": password}
	admin.mutate("POST", "/api/v1/auth/login", login, 200, "LoginResponse")
	portal.mutate("POST", "/api/v1/auth/login", login, 200, "LoginResponse")
	before := admin.cookies["__Host-admin_session"].Value

	problemCode(t, admin.mutate("POST", passwordChanges, map[string]any{"current_password": password + "x", "new_password": fixture.Password()}, 403, "Problem"), "PASSWORD_CURRENT_INVALID")
	problemCode(t, admin.mutate("POST", passwordChanges, map[string]any{"current_password": password, "new_password": password}, 422, "Problem"), "PASSWORD_UNCHANGED")
	admin.mutate("POST", passwordChanges, map[string]any{"current_password": password}, 400, "Problem")
	admin.mutate("POST", passwordChanges, map[string]any{"current_password": password, "new_password": fixture.Password(), "user_id": "other"}, 400, "Problem")
	if admin.cookies["__Host-admin_session"].Value != before {
		t.Fatal("a refused change keeps the session cookie")
	}

	next := fixture.Password()
	me := admin.mutate("POST", passwordChanges, map[string]any{"current_password": password, "new_password": next}, 200, "MeResponse")
	if me["audience"] != "admin" {
		t.Fatal("the response is the caller's identity", me)
	}
	if admin.cookies["__Host-admin_session"].Value == before {
		t.Fatal("the change replaces this browser's session cookie")
	}
	admin.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	portal.request("GET", "/api/v1/me", nil, 401, "Problem", nil)
	stale := admin.cookies["__Host-admin_session"]
	admin.cookies["__Host-admin_session"] = &http.Cookie{Name: stale.Name, Value: before}
	admin.request("GET", "/api/v1/me", nil, 401, "Problem", nil)

	portal.mutate("POST", "/api/v1/auth/login", login, 401, "Problem")
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": next}, 200, "LoginResponse")
	portal.mutate("POST", passwordChanges, map[string]any{"current_password": next, "new_password": fixture.Password()}, 200, "MeResponse")
}

func TestPasswordChangeAttemptsShareTheSignInLimit(t *testing.T) {
	policy := config.DefaultRuntimePolicy()
	policy.Rates.LoginEmailCount = 2
	_, _, portal, _, password := setup(t, policy)
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	wrong := map[string]any{"current_password": password + "x", "new_password": fixture.Password()}
	for range 2 {
		portal.mutate("POST", passwordChanges, wrong, 403, "Problem")
	}
	problemCode(t, portal.mutate("POST", passwordChanges, map[string]any{"current_password": password, "new_password": fixture.Password()}, 429, "Problem"), "RATE_LIMITED")
	portal.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
}

func TestPasswordChangeNeedsNoLicense(t *testing.T) {
	s := fixture.NewUnlicensed(t)
	password := fixture.Bootstrap(t, s)
	handler := transport.New(s)
	if e := handler.LicenseInitializationError(); e != nil {
		t.Fatal(e)
	}
	admin := newClient(t, accountTLSServer(t, handler), "admin", schemas(t))
	admin.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	// New business is refused here, so the change below passes on its own exception.
	admin.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": "someone@example.invalid", "locale": "en"}, 403, "")
	admin.mutate("POST", passwordChanges, map[string]any{"current_password": password, "new_password": fixture.Password()}, 200, "MeResponse")
	admin.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
}

package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

const (
	preferencesPath = "/api/v1/preferences"
	tenantSettings  = "/api/v1/admin/tenant-settings"
)

func accountLocale(t *testing.T, s *identity.Service) string {
	t.Helper()
	var locale string
	if e := s.DB.QueryRow(context.Background(), "SELECT locale FROM users WHERE email_canonical='admin@example.invalid'").Scan(&locale); e != nil {
		t.Fatal(e)
	}
	return locale
}

func TestLanguageFollowsTheAccountAndItsEmails(t *testing.T) {
	s, _, _, admin, password := setup(t)
	admin.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	saved := admin.request("GET", preferencesPath, nil, 200, "", nil)
	if saved["locale"] != "auto" {
		t.Fatal("nobody has chosen yet", saved)
	}
	before := accountLocale(t, s)
	version := admin.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)["user"].(map[string]any)["version"].(float64)

	saved = admin.mutate("PATCH", preferencesPath, map[string]any{"locale": "zh-CN", "expected_revision": saved["revision"]}, 200, "")
	if saved["locale"] != "zh-CN" || saved["theme"] != "system" {
		t.Fatal("the change names only the language", saved)
	}
	if accountLocale(t, s) != "zh-CN" {
		t.Fatal("emails follow the chosen language")
	}
	if got := admin.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)["user"].(map[string]any)["version"].(float64); got != version+1 {
		t.Fatal("the account version moves with its language", got, version)
	}

	// With no tenant default, a browser's languages cannot reach an email: the language stays.
	saved = admin.mutate("PATCH", preferencesPath, map[string]any{"locale": "auto", "expected_revision": saved["revision"]}, 200, "")
	if saved["locale"] != "auto" || accountLocale(t, s) != "zh-CN" {
		t.Fatal("automatic keeps the account's email language without a default", saved)
	}
	// With one, going back to automatic takes it.
	admin.mutate("PATCH", tenantSettings, map[string]any{"default_locale": "en", "expected_revision": 1}, 200, "")
	saved = admin.mutate("PATCH", preferencesPath, map[string]any{"locale": "zh-CN", "expected_revision": saved["revision"]}, 200, "")
	saved = admin.mutate("PATCH", preferencesPath, map[string]any{"locale": "auto", "expected_revision": saved["revision"]}, 200, "")
	if accountLocale(t, s) != "en" {
		t.Fatal("automatic takes the tenant's default", before)
	}

	for _, bad := range []map[string]any{
		{"locale": "fr", "expected_revision": saved["revision"]},
		{"locale": nil, "expected_revision": saved["revision"]},
		{"locale": "", "expected_revision": saved["revision"]},
	} {
		admin.mutate("PATCH", preferencesPath, bad, 400, "")
	}
	admin.mutate("PATCH", preferencesPath, map[string]any{"locale": "en", "expected_revision": 1}, 409, "")
}

func TestTenantDefaultLanguage(t *testing.T) {
	s, server, portal, admin, password := setup(t)
	visitor := newClient(t, server, "portal", nil)
	publicDefault := func() any {
		return visitor.request("GET", "/runtime-config.json", nil, 200, "", nil)["default_locale"]
	}
	if publicDefault() != "auto" {
		t.Fatal("no default until an administrator sets one")
	}
	login := map[string]any{"email": "admin@example.invalid", "password": password}
	admin.request("GET", tenantSettings, nil, 401, "", nil)
	admin.mutate("POST", "/api/v1/auth/login", login, 200, "LoginResponse")
	portal.mutate("POST", "/api/v1/auth/login", login, 200, "LoginResponse")

	current := admin.request("GET", tenantSettings, nil, 200, "", nil)
	if current["default_locale"] != "auto" || current["revision"] != float64(1) {
		t.Fatal("initial settings", current)
	}
	portal.request("GET", tenantSettings, nil, 404, "", nil)
	portal.mutate("PATCH", tenantSettings, map[string]any{"default_locale": "en", "expected_revision": 1}, 404, "")

	updated := admin.mutate("PATCH", tenantSettings, map[string]any{"default_locale": "zh-CN", "expected_revision": 1}, 200, "")
	if updated["default_locale"] != "zh-CN" || updated["revision"] != float64(2) {
		t.Fatal("saved settings", updated)
	}
	if publicDefault() != "zh-CN" {
		t.Fatal("pages before sign-in read the default")
	}
	problemCode(t, admin.mutate("PATCH", tenantSettings, map[string]any{"default_locale": "en", "expected_revision": 1}, 409, ""), "PREFERENCE_REVISION_CONFLICT")
	for _, bad := range []map[string]any{
		{"default_locale": "fr", "expected_revision": 2},
		{"default_locale": "en"},
		{"default_locale": "en", "expected_revision": 2, "tenant_id": "other"},
	} {
		admin.mutate("PATCH", tenantSettings, bad, 400, "")
	}
	var audited int
	if e := s.DB.QueryRow(context.Background(), "SELECT count(*) FROM audit_events WHERE action='tenant.default_locale_changed' AND outcome='success'").Scan(&audited); e != nil || audited != 1 {
		t.Fatal("one audit event per change", audited, e)
	}
}

// attemptPatch sends a write from a goroutine, where the test client's fatal checks cannot run.
func attemptPatch(c *client, path string, body map[string]any) string {
	raw, e := json.Marshal(body)
	if e != nil {
		return e.Error()
	}
	r, e := http.NewRequest("PATCH", c.s.URL+path, bytes.NewReader(raw))
	if e != nil {
		return e.Error()
	}
	r.Host = c.audience + ".example.invalid"
	r.Header.Set("Origin", "https://"+r.Host)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", c.csrf)
	for _, cookie := range c.cookies {
		r.AddCookie(cookie)
	}
	response, e := c.http.Do(r)
	if e != nil {
		return e.Error()
	}
	defer response.Body.Close()
	return strconv.Itoa(response.StatusCode)
}

func TestConcurrentTenantSettingsApplyOnce(t *testing.T) {
	_, _, _, admin, password := setup(t)
	admin.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	admin.refresh()
	const writers = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[string]int{}
	for range writers {
		wg.Go(func() {
			status := attemptPatch(admin, tenantSettings, map[string]any{"default_locale": "en", "expected_revision": 1})
			mu.Lock()
			defer mu.Unlock()
			codes[status]++
		})
	}
	wg.Wait()
	if codes["200"] != 1 || codes["409"] != writers-1 {
		t.Fatal("exactly one writer wins and the others see a conflict", codes)
	}
}

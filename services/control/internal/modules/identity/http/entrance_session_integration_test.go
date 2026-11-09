package http_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/google/uuid"
)

func TestBrowserEntranceSessionIsolation(t *testing.T) {
	s, server, portal, admin, password := setup(t)
	const email = "admin@example.invalid"
	const mePath = "/api/v1/me"
	const sessionPath = "/api/v1/me/session"
	credentials := map[string]any{"email": email, "password": password}

	var installationTenant string
	if err := s.DB.QueryRow(t.Context(), "SELECT tenant_id::text FROM installation_state WHERE id=1").Scan(&installationTenant); err != nil {
		t.Fatal(err)
	}
	foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO tenants(id,name,status) VALUES($1,'Other tenant','active')", []any{foreignTenant}},
		{"INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other workspace','active')", []any{foreignWorkspace, foreignTenant}},
		{`INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role)
			SELECT primary_admin_user_id,$1,$2,'owner' FROM installation_state WHERE id=1`, []any{foreignTenant, foreignWorkspace}},
	} {
		if _, err := s.DB.Exec(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}

	requireEntrance := func(c *client, data map[string]any) {
		t.Helper()
		workspace := data["workspace"].(map[string]any)
		if data["audience"] != c.audience || workspace["tenant_id"] != installationTenant {
			t.Fatalf("%s session used the wrong audience or tenant", c.audience)
		}
	}
	for _, c := range []*client{admin, portal} {
		requireEntrance(c, c.mutate(http.MethodPost, "/api/v1/auth/login", credentials, http.StatusOK, "LoginResponse"))
		requireEntrance(c, c.request(http.MethodGet, mePath, nil, http.StatusOK, "MeResponse", nil))
		c.request(http.MethodGet, sessionPath, nil, http.StatusOK, "SessionStatusResponse", nil)
	}
	if admin.cookies["__Host-admin_session"].Value == portal.cookies["__Host-portal_session"].Value {
		t.Fatal("entrances received the same session token")
	}

	for _, source := range []*client{admin, portal} {
		destination := admin
		if source == admin {
			destination = portal
		}
		token := source.cookies["__Host-"+source.audience+"_session"].Value
		replay := newClient(t, server, destination.audience, destination.schemas)
		renameCookie := func() {
			name := "__Host-" + destination.audience + "_session"
			replay.cookies[name] = &http.Cookie{Name: name, Value: token}
		}
		for _, path := range []string{mePath, sessionPath} {
			renameCookie()
			problem := replay.request(http.MethodGet, path, nil, http.StatusUnauthorized, "Problem", nil)
			if problem["code"] != "AUTH_SESSION_EXPIRED" {
				t.Fatalf("%s accepted a session issued to %s", destination.audience, source.audience)
			}
		}
		renameCookie()
		replay.mutate(http.MethodPost, "/api/v1/auth/logout", nil, http.StatusNoContent, "")
		requireEntrance(source, source.request(http.MethodGet, mePath, nil, http.StatusOK, "MeResponse", nil))
		requireEntrance(destination, destination.request(http.MethodGet, mePath, nil, http.StatusOK, "MeResponse", nil))
	}
}

func TestSharedHostPortsKeepBrowserAuthoritySeparateHTTPS(t *testing.T) {
	s, _, _, _, password := setup(t)
	portal, admin := httptest.NewUnstartedServer(nil), httptest.NewUnstartedServer(nil)
	t.Cleanup(portal.Close)
	t.Cleanup(admin.Close)
	s.Config.Server.PortalOrigin = "https://" + portal.Listener.Addr().String()
	s.Config.Server.AdminOrigin = "https://" + admin.Listener.Addr().String()
	handler := transport.New(s)
	portal.Config.Handler, admin.Config.Handler = handler, handler
	portal.StartTLS()
	admin.StartTLS()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	portal.Client().Jar, admin.Client().Jar = jar, jar
	request := func(server *httptest.Server, c *http.Client, method, path, origin, csrf string, payload map[string]string, override *http.Cookie, want int) map[string]any {
		t.Helper()
		var body []byte
		if payload != nil {
			var err error
			body, err = json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
		}
		r, err := http.NewRequestWithContext(t.Context(), method, server.URL+cellroute.Public(path, wire.LocalScope()), bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		if payload != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if override != nil {
			r.AddCookie(override)
		}
		res, err := c.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.TLS == nil || len(res.TLS.VerifiedChains) == 0 || res.StatusCode != want {
			t.Fatalf("%s %s returned %d, expected %d over verified TLS", method, path, res.StatusCode, want)
		}
		if res.StatusCode == http.StatusNoContent {
			return nil
		}
		var result map[string]any
		if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode >= http.StatusBadRequest {
			return result
		}
		data, ok := result["data"].(map[string]any)
		if !ok {
			t.Fatal("expected the standard identity data envelope")
		}
		return data
	}

	const csrfPath, loginPath, mePath, logoutPath = "/api/v1/auth/csrf", "/api/v1/auth/login", "/api/v1/me", "/api/v1/auth/logout"
	const portalCookie, adminCookie = "__Host-portal_session", "__Host-admin_session"
	csrf := func(server *httptest.Server) string {
		t.Helper()
		return request(server, server.Client(), http.MethodGet, csrfPath, "", "", nil, nil, 200)["token"].(string)
	}
	login := map[string]string{"email": "admin@example.invalid", "password": password}
	for _, entry := range []struct {
		server   *httptest.Server
		audience string
	}{{portal, "portal"}, {admin, "admin"}} {
		who := request(entry.server, entry.server.Client(), http.MethodPost, loginPath, entry.server.URL, csrf(entry.server), login, nil, 200)
		if who["audience"] != entry.audience {
			t.Fatalf("configured port resolved to the wrong audience for %s", entry.audience)
		}
	}
	for _, entry := range []struct {
		server  *httptest.Server
		present string
		foreign string
	}{{portal, portalCookie, adminCookie}, {admin, adminCookie, portalCookie}} {
		u, err := url.Parse(entry.server.URL)
		if err != nil {
			t.Fatal(err)
		}
		tokens := map[string]string{}
		for _, cookie := range jar.Cookies(u) {
			tokens[cookie.Name] = cookie.Value
		}
		if tokens[entry.present] == "" || tokens[entry.foreign] == "" || tokens[entry.present] == tokens[entry.foreign] {
			t.Fatal("both entrance cookies must share this host's jar")
		}
		noJar := &http.Client{Transport: entry.server.Client().Transport}
		foreign := &http.Cookie{Name: entry.present, Value: tokens[entry.foreign]}
		problemCode(t, request(entry.server, noJar, http.MethodGet, mePath, "", "", nil, foreign, 401), "AUTH_SESSION_EXPIRED")
	}
	adminCSRF, portalCSRF := csrf(admin), csrf(portal)
	problemCode(t, request(admin, admin.Client(), http.MethodPost, logoutPath, portal.URL, adminCSRF, nil, nil, 403), "ORIGIN_REJECTED")
	problemCode(t, request(admin, admin.Client(), http.MethodPost, logoutPath, admin.URL, portalCSRF, nil, nil, 403), "CSRF_INVALID")
	request(admin, admin.Client(), http.MethodPost, logoutPath, admin.URL, adminCSRF, nil, nil, 204)
	problemCode(t, request(admin, admin.Client(), http.MethodGet, mePath, "", "", nil, nil, 401), "AUTH_REQUIRED")
	if request(portal, portal.Client(), http.MethodGet, mePath, "", "", nil, nil, 200)["audience"] != "portal" {
		t.Fatal("Admin logout changed the Portal session")
	}
}

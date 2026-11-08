package http_test

import (
	"net/http"
	"testing"

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

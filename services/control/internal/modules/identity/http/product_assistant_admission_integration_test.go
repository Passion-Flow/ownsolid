//go:build assistant_http_dispatch

package http_test

import (
	"maps"
	"net/http"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/google/uuid"
)

func TestProductAssistantIdentityAdmissionHTTPS(t *testing.T) {
	const path = "/api/v1/product-assistant/commands"
	body := map[string]string{"command": "open nodes"}
	noAudit := func(t *testing.T, service *identity.Service, requestID string) {
		t.Helper()
		var count int
		if err := service.DB.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE request_id=$1", requestID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("expected no audit event for a refused request, got %d: %v", count, err)
		}
	}

	t.Run("current and revoked session", func(t *testing.T) {
		s, server, _, admin, password := setup(t)
		me := admin.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
		user := me["user"].(map[string]any)["id"].(string)
		admin.refresh()
		forged := uuid.NewString()
		result := admin.request(http.MethodPost, path, body, 200, "", map[string]string{"X-Request-ID": forged})
		requestID := admin.requestID
		if result["command"] != "open_nodes" || result["path"] != "/nodes" || requestID == forged {
			t.Fatal("dispatcher lost the authorized result or trusted a caller request ID")
		}
		var count int
		if err := s.DB.QueryRow(t.Context(), `SELECT count(*) FROM audit_events a
			JOIN installation_state i ON i.id=1 AND i.tenant_id=a.tenant_id AND i.default_workspace_id=a.workspace_id
			WHERE a.request_id=$1 AND a.subject_user_id=$2 AND a.actor_user_id IS NULL
			AND a.action='product_assistant.open_nodes' AND a.audience='admin' AND a.outcome='success'
			AND a.chain_position IS NOT NULL
			AND a.metadata='{"actor_kind":"product_assistant","actor_id":"ownsolid.product-assistant"}'::jsonb`,
			requestID, user).Scan(&count); err != nil || count != 1 {
			t.Fatalf("expected one chained event for the server request ID, got %d: %v", count, err)
		}
		noAudit(t, s, forged)

		stale := newClient(t, server, "admin", admin.schemas)
		stale.cookies, stale.csrf = maps.Clone(admin.cookies), admin.csrf
		admin.mutate(http.MethodPost, "/api/v1/auth/logout", nil, 204, "")
		refused := stale.request(http.MethodPost, path, body, 401, "Problem", nil)
		problemCode(t, refused, "AUTH_SESSION_EXPIRED")
		noAudit(t, s, stale.requestID)
	})

	t.Run("browser guards before license", func(t *testing.T) {
		s, _, admin, _ := licenseClients(t, nil)
		admin.refresh()
		for _, test := range []struct {
			code    string
			headers map[string]string
		}{
			{"ORIGIN_REJECTED", map[string]string{"Origin": "https://portal.example.invalid", "X-CSRF-Token": ""}},
			{"CSRF_INVALID", map[string]string{"X-CSRF-Token": ""}},
			{"LICENSE_NEW_BUSINESS_UNAVAILABLE", nil},
		} {
			refused := admin.request(http.MethodPost, path, body, 403, "Problem", test.headers)
			problemCode(t, refused, test.code)
			noAudit(t, s, admin.requestID)
		}
	})
}

//go:build !assistant_http_dispatch

package http_test

import (
	"net/http"
	"testing"

	assistant "github.com/Passion-Flow/ownsolid/services/control/internal/modules/productagent/http"
)

func TestProductAssistantUnregisteredHTTPS(t *testing.T) {
	const path = "/api/v1/product-assistant/commands"
	if !assistant.Owns(path) {
		t.Fatal("the fixture must exercise the staged adapter path")
	}
	s, _, portal, admin, password := setup(t)
	credentials := map[string]string{"email": "admin@example.invalid", "password": password}
	for _, entry := range []struct {
		name    string
		browser *client
		command string
	}{
		{"Admin", admin, "open nodes"},
		{"Portal", portal, "open applications"},
	} {
		t.Run(entry.name, func(t *testing.T) {
			entry.browser.mutate(http.MethodPost, "/api/v1/auth/login", credentials, 200, "LoginResponse")
			entry.browser.request(http.MethodGet, "/api/v1/me", nil, 200, "MeResponse", nil)
			problemCode(t, entry.browser.mutate(http.MethodPost, path, map[string]string{
				"command": entry.command,
			}, 404, "Problem"), "RESOURCE_NOT_FOUND")
		})
	}
	var events int
	if err := s.DB.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE action LIKE 'product_assistant.%'").Scan(&events); err != nil ||
		events != 0 {
		t.Fatalf("the unregistered path created %d assistant events: %v", events, err)
	}
}

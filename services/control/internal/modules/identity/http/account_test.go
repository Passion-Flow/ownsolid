package http

import "testing"

func TestAccountRoutesExcludeFutureOwners(t *testing.T) {
	for _, path := range []string{"/api/v1/me", "/api/v1/auth/login", "/api/v1/auth/password-resets", "/api/v1/admin/invitations", "/api/v1/license/status"} {
		if !accountAPI(path) {
			t.Fatal("missing account route", path)
		}
	}
	for _, path := range []string{"/api/v1/auth/sso/providers", "/api/v1/admin/nodes", "/api/v1/workspaces", "/agent/v1/tasks", "/api/v1/admin/users-extra"} {
		if accountAPI(path) {
			t.Fatal("future route admitted", path)
		}
	}
}

package http

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"strings"
)

// workspaceAPI identifies the namespace, not the capabilities available below it.
func workspaceAPI(path string) bool {
	for _, root := range []string{"/api/v1/workspaces", "/api/v1/admin/workspaces"} {
		if path == root {
			return true
		}
		if !strings.HasPrefix(path, root+"/") {
			continue
		}
		p := strings.Split(strings.TrimPrefix(path, root+"/"), "/")
		if len(p) == 1 {
			return true
		}
		if len(p) == 2 {
			return p[0] == "operations" || p[1] == "members" || p[1] == "notes"
		}
		if len(p) == 3 {
			return p[1] == "members" || p[1] == "notes"
		}
		return len(p) == 4 && p[1] == "notes" && p[3] == "export"
	}
	return false
}
func accountAPI(path string) bool {
	for _, auth := range []string{"csrf", "login", "logout", "register", "invitation-validations", "password-reset-requests", "password-resets"} {
		if path == "/api/v1/auth/"+auth {
			return true
		}
	}
	if path == "/api/v1/me" || path == "/api/v1/license/status" || strings.HasPrefix(path, "/api/v1/admin/license") {
		return true
	}
	for _, root := range []string{"/api/v1/admin/users", "/api/v1/admin/invitations", "/api/v1/admin/audit-events", "/api/v1/admin/mail-deliveries"} {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}
// Only deployment-selected public origins are exposed; provider policy and keys remain private.
func ssoCallbackOrigins(c config.Config) []string {
	origins := []string{}
	for _, origin := range []string{c.Server.PortalOrigin, c.Server.AdminOrigin} {
		if !config.SSOOrigin(origin) {
			return []string{}
		}
		if len(origins) == 0 || origins[0] != origin {
			origins = append(origins, origin)
		}
	}
	return origins
}

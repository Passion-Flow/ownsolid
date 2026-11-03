//go:build repository_integration

package main

import (
	"testing"

	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// This exercises actual TLS/authenticated routing on the account22 graph. The
// release owner is deliberately absent: readiness/availability must stay closed,
// while a valid Admin request must reach its inherited M17 route (not a 404).
func TestReleasesCacheV1HTTPInheritsReleaseRouteAndPreservesEntrances(t *testing.T) {
	accounts := fixture.NewM16(t)
	password := fixture.Bootstrap(t, accounts)
	profile := config.ReleasesCacheV1Profile
	accounts.Config.Server.ApplicationProfile = &profile
	accounts.Config.ReleaseViewConfigFile = "/run/projectone/admin-view.json"
	h := identityhttp.New(accounts)
	if e := h.InitializationError(); e != nil {
		t.Fatal(e)
	}
	server := httpfixture.NewTLSServer(t, h)
	defer server.Close()
	admin := httpfixture.New(t, server, "admin", nil)
	portal := httpfixture.New(t, server, "portal", nil)
	path := "/api/v1/admin/releases/current"
	check := func(client *httpfixture.Client, status int, code string) {
		t.Helper()
		out := client.Call("GET", path, nil, status, "")
		if out["code"] != code || len(out) != 5 {
			t.Fatal("release route/error boundary changed", out)
		}
	}
	check(admin, 401, "AUTH_REQUIRED")
	for _, client := range []*httpfixture.Client{admin, portal} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	}
	check(admin, 503, "RELEASE_VIEW_UNAVAILABLE")
	check(portal, 404, "RESOURCE_NOT_FOUND")
	if e := h.VerifyStored(t.Context()); e == nil {
		t.Fatal("absent runtime owners passed readiness")
	}
}

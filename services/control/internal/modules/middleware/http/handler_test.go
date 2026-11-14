package http

import (
	"net/http/httptest"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"testing"
)

func TestAudienceAndExactMiddlewareRoute(t *testing.T) {
	a := identity.Auth{}
	a.Me.Audience = "portal"
	for _, path := range []string{"/api/v1/admin/workspaces/w/middleware", "/api/v1/workspaces/w/other", "/api/v1/workspaces/w/middlewarex", "/api/v1/other/workspaces/w/middleware"} {
		if _, _, ok := route(path, a); ok {
			t.Fatal("foreign route accepted", path)
		}
	}
	w, parts, ok := route("/api/v1/workspaces/w/middleware/profiles", a)
	if !ok || w != "w" || len(parts) != 1 || parts[0] != "profiles" {
		t.Fatal("exact route lost")
	}
}

func TestUnwiredOwnerIsExplicitUnavailable(t *testing.T) {
	a := identity.Auth{}
	a.Me.Audience = "portal"
	_, _, e := (&Handler{}).Handle(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/workspaces/w/middleware", nil), a, "request")
	if e == nil || e.Error() != "SERVICE_NOT_READY" {
		t.Fatal("missing owner inferred success", e)
	}
}

func TestExactPublicMiddlewareOwnership(t *testing.T) {
	for _, path := range []string{"/api/v1/workspaces/w/middleware", "/api/v1/admin/workspaces/w/middleware/operations/x"} {
		if !Owns(path) {
			t.Fatal("owning route lost", path)
		}
	}
	for _, path := range []string{"/workspaces/w/middleware", "/api/v1/workspaces//middleware", "/api/v1/workspaces/w/middlewarex", "/api/v1/admin/admin/workspaces/w/middleware", "/api/v1/nodes/w/middleware"} {
		if Owns(path) {
			t.Fatal("foreign route owned", path)
		}
	}
}

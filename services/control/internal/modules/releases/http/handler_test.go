package http

import (
	"net/http/httptest"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"strings"
	"testing"
)

func TestReleaseHTTPFixedReadonlyAdminInputs(t *testing.T) {
	a := identity.Auth{}
	a.Me.Audience = "admin"
	a.Me.User.IsPlatformAdmin = true
	var h *Handler
	for _, path := range []string{CurrentPath + "?scope_id=foreign", CurrentPath + "?cursor=x", HistoryPath + "?limit=1", HistoryPath + "?cursor=x&cursor=y", HistoryPath + "?cursor="} {
		r := httptest.NewRequest("GET", path, nil)
		if _, _, e := h.Handle(r, a, ""); e == nil || e.Error() != "VALIDATION_FAILED" {
			t.Fatal(path, e)
		}
	}
	r := httptest.NewRequest("GET", CurrentPath, nil)
	if _, _, e := h.Handle(r, a, ""); e == nil || e.Error() != "RELEASE_VIEW_UNAVAILABLE" {
		t.Fatal(e)
	}
	a.Me.Audience = "portal"
	if _, _, e := h.Handle(r, a, ""); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal(e)
	}
	a.Me.Audience = "admin"
	r = httptest.NewRequest("POST", HistoryPath, strings.NewReader("{}"))
	if _, _, e := h.Handle(r, a, ""); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatal(e)
	}
}

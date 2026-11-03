package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
)

func TestExactDiagnosticRouteRejectsAmbiguousInputs(t *testing.T) {
	h := &Handler{Service: &monitoring.Service{}}
	a := identity.Auth{}
	a.Me.Audience, a.Me.User.IsPlatformAdmin = "admin", true
	for _, item := range []struct{ method, target, code string }{
		{http.MethodPost, Path, "RESOURCE_NOT_FOUND"},
		{http.MethodHead, Path, "RESOURCE_NOT_FOUND"},
		{http.MethodGet, Path + "/other", "RESOURCE_NOT_FOUND"},
		{http.MethodGet, Path, "VALIDATION_FAILED"},
		{http.MethodGet, Path + "?workspace_id=a&workspace_id=b", "VALIDATION_FAILED"},
		{http.MethodGet, Path + "?workspace_id=a&unknown=value", "VALIDATION_FAILED"},
		{http.MethodGet, Path + "?workspace_id=a&node_cursor=", "VALIDATION_FAILED"},
		{http.MethodGet, Path + "?workspace_id=a&node_cursor=x&node_cursor=y", "VALIDATION_FAILED"},
		{http.MethodGet, Path + "?workspace_id=%zz", "VALIDATION_FAILED"},
	} {
		r := httptest.NewRequest(item.method, "https://admin.example.invalid"+item.target, nil)
		out, status, e := h.Handle(r, a, "untrusted-request-id")
		if out != nil || status != 0 || e == nil || e.Error() != item.code {
			t.Fatal("ambiguous route projection", item, status, e)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "https://admin.example.invalid"+Path+"?workspace_id=a", strings.NewReader("unexpected"))
	if out, status, e := h.Handle(r, a, "id"); out != nil || status != 0 || e == nil || e.Error() != "VALIDATION_FAILED" {
		t.Fatal("GET body admitted", e)
	}
	r = httptest.NewRequest(http.MethodGet, "https://admin.example.invalid"+Path+"?workspace_id=a", nil)
	r.URL.RawPath = "/api/v1/admin/%6donitoring"
	if out, status, e := h.Handle(r, a, "id"); out != nil || status != 0 || e == nil || e.Error() != "VALIDATION_FAILED" {
		t.Fatal("encoded alias admitted", e)
	}
	for _, audience := range []string{"portal", ""} {
		a.Me.Audience = audience
		if out, status, e := h.Handle(r, a, "id"); out != nil || status != 0 || e == nil || e.Error() != "ACCESS_DENIED" {
			t.Fatal("foreign audience projected", e)
		}
	}
	a.Me.Audience, a.Me.User.IsPlatformAdmin = "admin", false
	if out, status, e := h.Handle(r, a, "id"); out != nil || status != 0 || e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal("role bypass", e)
	}
	var absent *Handler
	if out, status, e := absent.Handle(r, a, "id"); out != nil || status != 0 || e == nil || e.Error() != "MONITORING_OWNER_REQUIRED" {
		t.Fatal("absent owner projection", e)
	}
}

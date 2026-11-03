package http

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/cloudinventory"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestAWSHTTPRejectsMalformedBeforePersistence(t *testing.T) {
	h := &Handler{Service: &cloudinventory.Service{Identity: &identity.Service{Config: config.Config{}}}}
	a := identity.Auth{}
	a.Me.Audience = "portal"
	for _, path := range []string{"/api/v1/workspaces/w/aws", "/api/v1/admin/workspaces/w/aws/imports"} {
		if !Owns(path) {
			t.Fatal("route missing")
		}
	}
	for _, path := range []string{"/workspaces/w/aws", "/api/v1/workspaces/w/aws-other", "/api/v1/workspaces/w/nodes"} {
		if Owns(path) {
			t.Fatal("route overclaimed")
		}
	}
	for _, body := range []string{`null`, `{}`, `{"connection":"fixture","region":"us-east-1","idempotency_key":"invalid"}`, `{"connection":"fixture","region":"us-east-1","idempotency_key":"11111111-1111-4111-8111-111111111111","tenant_id":"foreign"}`, `{"connection":"a","connection":"b","region":"us-east-1","idempotency_key":"x"}`, `{"Connection":"a","region":"us-east-1","idempotency_key":"x"}`} {
		r := httptest.NewRequest("POST", "/api/v1/workspaces/w/aws/imports", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		value, _, e := h.Handle(r, a, "11111111-1111-4111-8111-111111111111")
		if e == nil || value != nil && body == "null" {
			t.Fatal("invalid request accepted", body, e)
		}
	}
	r := httptest.NewRequest("GET", "/api/v1/admin/workspaces/w/aws", nil)
	_, _, e := h.Handle(r, a, "")
	if e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal("audience crossing", e)
	}
	for _, query := range []string{"?after=a&after=b", "?tenant_id=foreign", "?limit=false"} {
		r = httptest.NewRequest("GET", "/api/v1/workspaces/w/aws"+query, nil)
		_, _, e = h.Handle(r, a, "")
		if e == nil || e.Error() != "VALIDATION_FAILED" {
			t.Fatal("invalid query", e)
		}
	}
}

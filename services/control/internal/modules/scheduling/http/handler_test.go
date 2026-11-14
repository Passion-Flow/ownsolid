package http

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/google/uuid"
)

func TestQuotaReadRejectsAmbiguousSelectorsAndWrongEntrance(t *testing.T) {
	id := uuid.NewString()
	portal := "/api/v1/workspaces/" + id + "/scheduling/quota"
	admin := "/api/v1/admin/scheduling/quota"
	for _, test := range []struct {
		name, method, path, audience, body, code string
		admin                                    bool
	}{
		{name: "wrong portal audience", path: portal, audience: "admin", admin: true, code: "RESOURCE_NOT_FOUND"},
		{name: "wrong admin audience", path: admin + "?workspace_id=" + id, audience: "portal", code: "RESOURCE_NOT_FOUND"},
		{name: "nonadmin", path: admin + "?workspace_id=" + id, audience: "admin", code: "RESOURCE_NOT_FOUND"},
		{name: "mutation", method: "POST", path: portal, audience: "portal", code: "RESOURCE_NOT_FOUND"},
		{name: "trailing slash", path: portal + "/", audience: "portal", code: "RESOURCE_NOT_FOUND"},
		{name: "escaped selector", path: strings.Replace(portal, "/scheduling/", "/%73cheduling/", 1), audience: "portal", code: "RESOURCE_NOT_FOUND"},
		{name: "invalid selector", path: "/api/v1/workspaces/invalid/scheduling/quota", audience: "portal", code: "VALIDATION_FAILED"},
		{name: "unrelated route", path: "/api/v1/workspaces/" + id + "/notes", audience: "portal", code: "RESOURCE_NOT_FOUND"},
		{name: "portal tenant selector", path: portal + "?tenant_id=" + id, audience: "portal", code: "VALIDATION_FAILED"},
		{name: "portal workspace selector", path: portal + "?workspace_id=" + id, audience: "portal", code: "VALIDATION_FAILED"},
		{name: "missing admin selector", path: admin, audience: "admin", admin: true, code: "VALIDATION_FAILED"},
		{name: "duplicate selector", path: admin + "?workspace_id=" + id + "&workspace_id=" + id, audience: "admin", admin: true, code: "VALIDATION_FAILED"},
		{name: "unknown selector", path: admin + "?workspace_id=" + id + "&tenant_id=" + id, audience: "admin", admin: true, code: "VALIDATION_FAILED"},
		{name: "malformed query", path: portal + "?%zz=1", audience: "portal", code: "VALIDATION_FAILED"},
		{name: "missing installed authority", path: portal, audience: "portal", code: "STORAGE_UNAVAILABLE"},
		{name: "get body", path: portal, audience: "portal", body: "{}", code: "VALIDATION_FAILED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			method := test.method
			if method == "" {
				method = http.MethodGet
			}
			r := httptest.NewRequest(method, test.path, nil)
			if test.body != "" {
				r = httptest.NewRequest(method, test.path, bytes.NewBufferString(test.body))
			}
			actor := identity.Auth{}
			actor.Me.Audience, actor.Me.User.IsPlatformAdmin = test.audience, test.admin
			w := httptest.NewRecorder()
			out, status, err := (&Handler{}).Handle(w, r, actor, "")
			if err == nil || err.Error() != test.code || out != nil || status != 0 {
				t.Fatalf("invalid request exposed data: %+v, %d, %v", out, status, err)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private read may be cached")
			}
		})
	}
}

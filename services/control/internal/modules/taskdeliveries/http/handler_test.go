package http

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/taskdeliveries"
)

func TestTaskDeliveryHTTPRejectsScopeOverridesAndAmbiguousRetry(t *testing.T) {
	h := &Handler{Service: &taskdeliveries.Service{}}
	var a identity.Auth
	a.Me.Audience = "admin"
	a.Me.User.IsPlatformAdmin = true
	base := "/api/v1/admin/nodes/11111111-1111-4111-8111-111111111111/task-deliveries"
	scope := "workspace_id=22222222-2222-4222-8222-222222222222"
	for _, tc := range []struct{ method, tail, query, body, code string }{
		{"GET", "", scope + "&tenant_id=33333333-3333-4333-8333-333333333333", "", "VALIDATION_FAILED"},
		{"GET", "", scope + "&limit=01", "", "VALIDATION_FAILED"},
		{"GET", "", scope + "&cursor=a&cursor=b", "", "VALIDATION_FAILED"},
		{"GET", "", scope, "{}", "VALIDATION_FAILED"},
		{"GET", "", scope + "&" + scope, "", "VALIDATION_FAILED"},
		{"GET", "/", scope, "", "RESOURCE_NOT_FOUND"},
		{"POST", "/33333333-3333-4333-8333-333333333333/retry", scope, `{"generation":1,"reason":"first","reason":"second"}`, "VALIDATION_FAILED"},
		{"POST", "/33333333-3333-4333-8333-333333333333/retry", scope, `{"generation":1,"reason":"retry","tenant_id":"override"}`, "VALIDATION_FAILED"},
		{"POST", "/33333333-3333-4333-8333-333333333333/retry", scope, `{"generation":null,"reason":"retry"}`, "VALIDATION_FAILED"},
		{"POST", "/33333333-3333-4333-8333-333333333333/retry", scope, `{"generation":1,"reason":"retry"}`, "TASK_BUS_NOT_ADMITTED"},
	} {
		r := httptest.NewRequest(tc.method, base+tc.tail+"?"+tc.query, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "44444444-4444-4444-8444-444444444444")
		v, status, e := h.Handle(r, a, "55555555-5555-4555-8555-555555555555")
		if e == nil || e.Error() != tc.code || v != nil || status != 0 {
			t.Fatalf("%s %s projected value/status: %v %d", tc.method, tc.tail, e, status)
		}
	}
	a.Me.Audience = "portal"
	for _, path := range []string{base + "?" + scope, "/api/v1/admin/task-delivery-quarantine", "/api/v1/workspaces/22222222-2222-4222-8222-222222222222/nodes/11111111-1111-4111-8111-111111111111/task-deliveries/33333333-3333-4333-8333-333333333333/retry"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"generation":1,"reason":"retry"}`))
		r.Header.Set("Content-Type", "application/json")
		if v, status, e := h.Handle(r, a, "55555555-5555-4555-8555-555555555555"); e == nil || e.Error() != "RESOURCE_NOT_FOUND" || v != nil || status != 0 {
			t.Fatal("portal crossed Admin mutation/quarantine authority", e, status)
		}
	}
}

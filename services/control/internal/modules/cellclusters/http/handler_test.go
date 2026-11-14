package http

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/google/uuid"
)

func TestHandlerRoutes(t *testing.T) {
	admin := identity.Auth{}
	admin.Me.Audience, admin.Me.User.IsPlatformAdmin, admin.Me.User.ID = "admin", true, uuid.NewString()
	cluster, node := uuid.NewString(), uuid.NewString()
	body := `{"reason":"Synthetic"}`
	// A route that reaches the service answers with its refusal: no service is configured.
	reached := "KUBERNETES_CELLS_UNCONFIGURED"
	for _, c := range []struct {
		method, path, body, key, code string
	}{
		{"GET", Root, "", "", reached},
		{"POST", Root, `{"name":"c","server_node_id":"` + node + `","worker_node_ids":[],"reason":"r"}`, uuid.NewString(), reached},
		{"POST", Root, `{"name":"c","server_node_id":"` + node + `","worker_node_ids":[],"reason":"r"}`, "", "VALIDATION_FAILED"},
		{"POST", Root, `{"name":"c","server_node_id":"` + node + `","worker_node_ids":[],"reason":"r"}`, "not-a-key", "VALIDATION_FAILED"},
		{"GET", Root + "/" + cluster, "", "", reached},
		{"GET", Root + "/not-an-id", "", "", "RESOURCE_NOT_FOUND"},
		{"GET", Root + "/" + cluster + "/members?state=ready&limit=10", "", "", reached},
		{"GET", Root + "/" + cluster + "?verbose=true", "", "", "VALIDATION_FAILED"},
		{"POST", Root + "/" + cluster + "/members", `{"worker_node_ids":["` + node + `"],"reason":"r"}`, "", reached},
		{"POST", Root + "/" + cluster + "/members/" + node + "/retry", body, "", reached},
		{"POST", Root + "/" + cluster + "/members/" + node + "/retry?force=true", body, "", "VALIDATION_FAILED"},
		{"POST", Root + "/" + cluster + "/members/not-an-id/retry", body, "", "RESOURCE_NOT_FOUND"},
		{"POST", Root + "/" + cluster + "/observations", body, "", reached},
		{"DELETE", Root + "/" + cluster, "", "", "RESOURCE_NOT_FOUND"},
		{"GET", Root + "/" + cluster + "/observations", "", "", "RESOURCE_NOT_FOUND"},
		{"POST", Root + "/" + cluster + "/members/" + node + "/retry", `{"reason":"r","extra":1}`, "", "VALIDATION_FAILED"},
	} {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		if c.body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if c.key != "" {
			r.Header.Set("Idempotency-Key", c.key)
		}
		_, _, e := (&Handler{}).Handle(r, admin, uuid.NewString())
		if e == nil || e.Error() != c.code {
			t.Errorf("%s %s: wanted %s, observed %v", c.method, c.path, c.code, e)
		}
	}
	portal := admin
	portal.Me.Audience = "portal"
	if _, _, e := (&Handler{}).Handle(httptest.NewRequest("GET", Root, nil), portal, uuid.NewString()); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal("a portal session reached cell clusters", e)
	}
	if !Owns(Root) || !Owns(Root+"/x") || Owns("/api/v1/admin/cell-clustersx") || Owns("/api/v1/admin/nodes") {
		t.Fatal("Owns")
	}
}

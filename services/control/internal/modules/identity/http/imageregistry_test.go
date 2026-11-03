package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/registryconfig"
)

func TestRegistryRuntimePolicyUsesOnlyTypedPublicBudgets(t *testing.T) {
	c := config.Config{}
	if registryPublicPolicy(c) != (registryRuntimePolicy{}) {
		t.Fatal("omitted registry enabled")
	}
	c.ImageRegistry = &registryconfig.Config{RequestSeconds: 31, OperationSeconds: 73, Endpoint: "https://private.example.invalid", CredentialFile: "/private/credential", StoreRoot: "/private/store"}
	value := registryPublicPolicy(c)
	if value != (registryRuntimePolicy{true, 31000, 73000}) {
		t.Fatal("nondefault registry policy", value)
	}
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	var public map[string]any
	if e = json.Unmarshal(raw, &public); e != nil || len(public) != 3 {
		t.Fatal("public registry shape", e)
	}
	for _, private := range []string{"private", "credential", "endpoint", "store", "cidr"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private registry configuration leaked")
		}
	}
	for _, item := range []struct {
		path    string
		seconds int
	}{
		{"/api/v1/admin/image-registry/pins", 73},
		{"/api/v1/workspaces/w/image-registry/pins", 73},
		{"/agent/v1/nodes/n/tasks/t/image-registry/manifest", 31},
		{"/agent/v1/nodes/n/tasks/t/image-registry/ranges", 31},
		{"/agent/v1/nodes/n/tasks/t/image-registry/upload", 0},
		{"/api/v1/admin/models", 0},
	} {
		r := httptest.NewRequest(http.MethodPost, item.path, nil)
		if actual := registryRequestBudget(c, r); actual != item.seconds {
			t.Fatal(item.path, actual)
		}
	}
	c.ImageRegistry = nil
	if registryRequestBudget(c, httptest.NewRequest("GET", "/api/v1/admin/image-registry/pins", nil)) != 0 {
		t.Fatal("disabled registry changed request budget")
	}
}

func TestRegistryEntrancesRetainMiddlewareAndMachineDispatch(t *testing.T) {
	c := config.Config{}
	c.Server.AdminOrigin = "https://admin.example.invalid"
	c.Server.PortalOrigin = "https://portal.example.invalid"
	i := &identity.Service{Config: c, HMACKey: make([]byte, 32)}
	h := &Handler{Service: i, active: make(chan struct{}, 1)}
	// This unit owner has no node/registry/database implementation. A dispatch
	// regression to generic node handling would panic instead of passing safely.
	for _, item := range []struct {
		name, host, path, origin string
		cookie                   bool
		status                   int
		code                     string
	}{
		{"machine_admin", "admin.example.invalid", "/agent/v1/nodes/n/tasks/t/image-registry/manifest", "", false, 404, "RESOURCE_NOT_FOUND"},
		{"machine_portal", "portal.example.invalid", "/agent/v1/nodes/n/tasks/t/image-registry/ranges", "", false, 404, "RESOURCE_NOT_FOUND"},
		{"admin_wrong_entrance", "portal.example.invalid", "/api/v1/admin/image-registry/pins", "", false, 404, "RESOURCE_NOT_FOUND"},
		{"human_missing_session", "admin.example.invalid", "/api/v1/admin/image-registry/pins", "", false, 401, "AUTH_REQUIRED"},
		{"human_wrong_origin", "admin.example.invalid", "/api/v1/admin/image-registry/pins", "https://foreign.example.invalid", true, 403, "ORIGIN_REJECTED"},
		{"human_missing_csrf", "admin.example.invalid", "/api/v1/admin/image-registry/pins", "https://admin.example.invalid", true, 403, "CSRF_INVALID"},
	} {
		t.Run(item.name, func(t *testing.T) {
			method := http.MethodPost
			if item.name == "human_missing_session" {
				method = http.MethodGet
			}
			r := httptest.NewRequest(method, "https://"+item.host+item.path, strings.NewReader("{}"))
			r.TLS.ServerName = item.host
			r.Header.Set("Content-Type", "application/json")
			if item.origin != "" {
				r.Header.Set("Origin", item.origin)
			}
			if item.cookie {
				r.AddCookie(&http.Cookie{Name: "__Host-admin_session", Value: security.Token()})
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var body struct {
				Code string `json:"code"`
			}
			if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
				t.Fatal(e)
			}
			if w.Code != item.status || body.Code != item.code {
				t.Fatal("actual entrance refusal", w.Code, body.Code)
			}
		})
	}
}

func TestRegistryLicenseRecoveryRoutesAreClosed(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	for _, action := range []string{"remove", "reconcile-removal", "revoke", "release-offline", "release-deployment", "abandon", "discard"} {
		path := "/api/v1/admin/image-registry/pins/" + id + "/" + action
		for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodPut} {
			allowed, e := recoveryMutation(httptest.NewRequest(method, path, nil))
			if e != nil || allowed != (method == http.MethodPost) {
				t.Fatal("fixed cleanup exception", method, path, e)
			}
		}
		for _, suffix := range []string{"/", "/extra"} {
			allowed, e := recoveryMutation(httptest.NewRequest(http.MethodPost, path+suffix, nil))
			if e != nil || allowed {
				t.Fatal("noncanonical cleanup exception", path+suffix, e)
			}
		}
	}
	for _, path := range []string{"/api/v1/admin/image-registry/pins", "/api/v1/admin/image-registry/pins/" + id + "/reconcile", "/api/v1/admin/image-registry/pins/" + id + "/grant", "/api/v1/admin/image-registry/pins/" + id + "/offline", "/api/v1/admin/image-registry/pins/invalid/remove"} {
		allowed, e := recoveryMutation(httptest.NewRequest(http.MethodPost, path, nil))
		if e != nil || allowed {
			t.Fatal("new business bypassed license", path, e)
		}
	}
}

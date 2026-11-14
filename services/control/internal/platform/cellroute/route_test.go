package cellroute

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

const scoped = "/sites/default/cells/default"

func TestResolve(t *testing.T) {
	local := wire.LocalScope()
	for _, c := range []struct {
		in, out string
		ok      bool
	}{
		{"/api/v1" + scoped + "/admin/nodes", "/api/v1/admin/nodes", true},
		{"/api/v1" + scoped + "/workspaces/w/nodes/n", "/api/v1/workspaces/w/nodes/n", true},
		{"/agent/v1" + scoped + "/nodes/n/heartbeat", "/agent/v1/nodes/n/heartbeat", true},
		{"/gateway/v1" + scoped + "/instances/g/admissions", "/gateway/v1/instances/g/admissions", true},
		{"/api/v1/auth/login", "/api/v1/auth/login", true},
		{"/api/v1/workspaces/w", "/api/v1/workspaces/w", true},
		{"/api/v1/workspaces/w/members", "/api/v1/workspaces/w/members", true},
		{"/agent/v1/enroll", "/agent/v1/enroll", true},
		{"/health/live", "/health/live", true},
		{"/v1/chat/completions", "/v1/chat/completions", true},
		{"/api/v1/admin/nodes", "", false},
		{"/api/v1/workspaces/w/nodes", "", false},
		{"/agent/v1/nodes/n/heartbeat", "", false},
		{"/gateway/v1/instances/g/admissions", "", false},
		{"/api/v1/sites/other/cells/default/admin/nodes", "", false},
		{"/api/v1/sites/default/cells/other/admin/nodes", "", false},
		{"/api/v1" + scoped + "/auth/login", "", false},
		{"/api/v1" + scoped + "/workspaces/w/members", "", false},
		{"/agent/v1" + scoped + "/enroll", "", false},
	} {
		out, ok := Resolve(c.in, local)
		if ok != c.ok || c.ok && out != c.out {
			t.Errorf("%s: got %q %v", c.in, out, ok)
		}
	}
}

func TestPublicInvertsResolve(t *testing.T) {
	local := wire.LocalScope()
	for _, path := range []string{"/api/v1/admin/nodes", "/api/v1/workspaces/w/nodes/n", "/agent/v1/nodes/n/heartbeat", "/gateway/v1/instances/g/admissions", "/api/v1/auth/login", "/agent/v1/enroll"} {
		if got, ok := Resolve(Public(path, local), local); !ok || got != path {
			t.Errorf("%s: %q %v", path, got, ok)
		}
	}
	if got := Public("/api/v1/admin/nodes?limit=1", local); got != "/api/v1"+scoped+"/admin/nodes?limit=1" {
		t.Fatal(got)
	}
}

// Global lists every OpenAPI path family the global layer keeps. A new contract path has
// to be scoped or added here, so no resource lands in the global layer by accident.
var global = []string{
	"/api/v1/account/", "/api/v1/capabilities", "/api/v1/admin/audit-events", "/api/v1/admin/invitations", "/api/v1/admin/license",
	"/api/v1/admin/mail-deliveries", "/api/v1/admin/releases/", "/api/v1/admin/sso/", "/api/v1/admin/tenant-settings", "/api/v1/admin/users",
	"/api/v1/admin/workspaces", "/api/v1/auth/", "/api/v1/license/", "/api/v1/localization/", "/api/v1/me",
	"/api/v1/notifications", "/api/v1/preferences", "/api/v1/saved-views", "/api/v1/workspaces", "/agent/v1/enroll", "/health/",
	"/v1/chat/completions", "/v1/models",
}

func TestEveryContractPathHasOneLayer(t *testing.T) {
	files, e := filepath.Glob("../../../../../api/openapi/v1/*/openapi.json")
	if e != nil || len(files) < 20 {
		t.Fatal(e, len(files))
	}
	for _, file := range files {
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		var contract struct {
			Paths map[string]json.RawMessage `json:"paths"`
		}
		if e = json.Unmarshal(raw, &contract); e != nil {
			t.Fatal(file, e)
		}
		for path := range contract.Paths {
			if !strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/agent/") && !strings.HasPrefix(path, "/gateway/") && !strings.HasPrefix(path, "/health/") && !strings.HasPrefix(path, "/v1/") {
				path = apiRoot + path // these contracts name /api/v1 as their server
			}
			if strings.Contains(path, "{site_id}") {
				inner := strings.Replace(path, "/sites/{site_id}/cells/{cell_id}", "", 1)
				if inner == path || !CellOwned(inner) {
					t.Errorf("%s: scoped path is not cell-owned", path)
				}
				continue
			}
			if CellOwned(path) {
				t.Errorf("%s: cell-owned path has no scope", path)
			}
			known := false
			for _, prefix := range global {
				known = known || strings.HasPrefix(path, prefix)
			}
			if !known {
				t.Errorf("%s: choose a layer for this path", path)
			}
		}
	}
}

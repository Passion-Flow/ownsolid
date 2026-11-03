package http_test

import (
	"github.com/google/uuid"
	"strings"
	"testing"
)

// Real SQL/session/CSRF/license middleware, with no admitted S3 owner. This
// verifies composition refusal and never stands in for real object acceptance.
func TestM22RootArtifactRoutesRetainSessionCSRFAndLicenseGates(t *testing.T) {
	_, _, admin, portal := licenseClients(t, nil)
	base := "/api/v1/admin/artifact-objects?workspace_id=" + uuid.NewString() + "&limit=2"
	anonymous := newClient(t, admin.s, "admin", nil)
	if p := anonymous.request("GET", base, nil, 401, "", nil); p["code"] != "AUTH_REQUIRED" {
		t.Fatal("anonymous module access", p)
	}
	if p := portal.request("GET", base, nil, 404, "", nil); p["code"] != "RESOURCE_NOT_FOUND" {
		t.Fatal("entrance crossing", p)
	}
	if p := admin.request("GET", base, nil, 503, "", nil); p["code"] != "OBJECT_NOT_ADMITTED" {
		t.Fatal("generic Workspace route swallowed owner", p)
	}
	path := "/api/v1/admin/artifact-objects?workspace_id=" + uuid.NewString()
	admin.refresh()
	if p := admin.request("POST", path, map[string]any{}, 403, "", map[string]string{"Origin": "https://foreign.example.invalid"}); p["code"] != "ORIGIN_REJECTED" {
		t.Fatal(p)
	}
	if p := admin.request("POST", path, map[string]any{}, 403, "", map[string]string{"X-CSRF-Token": "invalid"}); p["code"] != "CSRF_INVALID" {
		t.Fatal(p)
	}
	if p := admin.mutate("POST", path, map[string]any{}, 403, ""); !strings.HasPrefix(p["code"].(string), "LICENSE_") {
		t.Fatal("license admission bypassed", p)
	}
	stop := "/api/v1/admin/artifact-objects/" + uuid.NewString() + "/abort?workspace_id=" + uuid.NewString()
	if p := admin.mutate("POST", stop, map[string]any{"reason": "Stop synthetic unknown object"}, 503, ""); p["code"] != "OBJECT_NOT_ADMITTED" {
		t.Fatal("expired license blocked safe owner refusal", p)
	}
	runtime := admin.request("GET", "/runtime-config.json", nil, 200, "", nil)["artifact_storage"].(map[string]any)
	if runtime["enabled"] != false || runtime["transfer_timeout_milliseconds"] != float64(0) {
		t.Fatal("disabled runtime truthiness", runtime)
	}
	admin.mutate("POST", "/api/v1/auth/logout", nil, 204, "")
	if p := admin.request("GET", base, nil, 401, "", nil); p["code"] != "AUTH_REQUIRED" {
		t.Fatal("logout left artifact session authority", p)
	}
}

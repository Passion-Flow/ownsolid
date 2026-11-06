package http

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArtifactRecoveryLicenseExceptionsAreExactAndRetainOwnerChecks(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	for _, item := range []struct {
		method, path string
		safe         bool
	}{
		{"POST", "/api/v1/admin/artifact-objects/" + id + "/abort", true},
		{"POST", "/api/v1/workspaces/" + id + "/artifact-objects/" + id + "/delete", true},
		{"DELETE", "/api/v1/admin/artifact-objects/" + id + "/grants/" + id, true},
		{"POST", "/api/v1/admin/artifact-objects/" + id + "/ingestions/" + id + "/cancel", true},
		{"POST", "/api/v1/admin/artifact-objects/" + id + "/complete", false},
		{"POST", "/api/v1/admin/artifact-objects/" + id + "/reconcile", true},
		{"POST", "/api/v1/admin/artifact-objects/" + id + "/model-import", false},
		{"POST", "/api/v1/admin/artifact-objects/not-an-id/abort", false},
		{"POST", "/api/v1/admin/artifact-objects/" + id + "/abort/", false},
		{"PUT", "/api/v1/admin/artifact-objects/" + id + "/abort", false},
	} {
		allowed, e := recoveryMutation(httptest.NewRequest(item.method, item.path, nil))
		if e != nil || allowed != item.safe {
			t.Fatal(item, e, allowed)
		}
	}
}

// Pacing is in the same list: it protects control, so the license never blocks it.
func TestNodeLicenseExceptionCoversOnlyTakingANodeOutOfService(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	for _, item := range []struct {
		method, path, body string
		safe               bool
	}{
		{"PATCH", "/api/v1/admin/nodes/" + id, `{"name":"a","status":"disabled","version":1,"reason":"r"}`, true},
		{"PATCH", "/api/v1/admin/nodes/" + id, `{"name":"a","status":"removed","version":1,"reason":"r"}`, true},
		{"PATCH", "/api/v1/admin/nodes/" + id, `{"name":"a","status":"active","version":1,"reason":"r"}`, false},
		{"PATCH", "/api/v1/admin/nodes/not-an-id", `{"name":"a","status":"disabled","version":1,"reason":"r"}`, false},
		{"PATCH", "/api/v1/admin/nodes/" + id + "/tasks", `{"name":"a","status":"disabled","version":1,"reason":"r"}`, false},
		{"POST", "/api/v1/admin/nodes/" + id, `{"name":"a","status":"disabled","version":1,"reason":"r"}`, false},
		{"PUT", "/api/v1/admin/agent-pacing", `{}`, true},
		{"PATCH", "/api/v1/admin/agent-pacing", `{}`, false},
	} {
		request := httptest.NewRequest(item.method, item.path, strings.NewReader(item.body))
		request.Header.Set("Content-Type", "application/json")
		allowed, _ := recoveryMutation(request)
		if allowed != item.safe {
			t.Fatal(item, allowed)
		}
	}
}

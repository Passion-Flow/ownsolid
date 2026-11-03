package http

import (
	"net/http/httptest"
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
		{"POST", "/api/v1/admin/artifact-objects/" + id + "/reconcile", false},
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

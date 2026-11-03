package http

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestObjectReconcileLicenseRecoveryRequiresExactExistingPath(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	for _, root := range []string{"/api/v1/admin/artifact-objects", "/api/v1/workspaces/" + id + "/artifact-objects"} {
		path := root + "/" + id + "/reconcile"
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"reason":"Observe an existing accepted operation"}`))
		if safe, err := recoveryMutation(r); err != nil || !safe {
			t.Fatal("exact existing recovery route refused", err)
		}
		for _, bad := range []string{root, path + "/", path + "/create", strings.Replace(path, id, "not-a-uuid", 1)} {
			if safe, err := recoveryMutation(httptest.NewRequest("POST", bad, nil)); err != nil || safe {
				t.Fatal("new or noncanonical work received a recovery exception", bad, err)
			}
		}
		if safe, err := recoveryMutation(httptest.NewRequest("PUT", path, nil)); err != nil || safe {
			t.Fatal("wrong method received a recovery exception", err)
		}
		r.URL.RawPath = r.URL.Path
		if safe, err := recoveryMutation(r); err != nil || safe {
			t.Fatal("encoded path received a recovery exception", err)
		}
	}
}

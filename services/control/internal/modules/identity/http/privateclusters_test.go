package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrivateClusterRecoveryRoutesNeverAdmitNewWork(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	admin := "/api/v1/admin/private-clusters/connections/" + id
	portal := "/api/v1/workspaces/" + id + "/private-clusters/connections/" + id
	for _, root := range []string{admin, portal} {
		for _, action := range []string{"reconcile", "cancel", "cleanup"} {
			path := root + "/runs/" + id + "/" + action
			if !privateClusterRecoveryMutation(httptest.NewRequest(http.MethodPost, path, nil)) {
				t.Fatal("owned safety route not classified", path)
			}
			for _, invalid := range []string{path + "/extra", strings.Replace(path, "/"+id+"/runs/", "/foreign/runs/", 1)} {
				if privateClusterRecoveryMutation(httptest.NewRequest(http.MethodPost, invalid, nil)) {
					t.Fatal("foreign safety route admitted", invalid)
				}
			}
			r := httptest.NewRequest(http.MethodPost, path, nil)
			r.URL.RawPath = path
			if privateClusterRecoveryMutation(r) || privateClusterRecoveryMutation(httptest.NewRequest(http.MethodGet, path, nil)) {
				t.Fatal("encoded path or method changed safety classification")
			}
		}
		for _, action := range []string{"runs", "continue", "observe", "rebind"} {
			for _, path := range []string{root + "/" + action, root + "/runs/" + id + "/" + action} {
				if privateClusterRecoveryMutation(httptest.NewRequest(http.MethodPost, path, nil)) {
					t.Fatal("new work admitted", path)
				}
			}
		}
	}
	if !privateClusterRecoveryMutation(httptest.NewRequest(http.MethodPost, admin+"/disable", nil)) ||
		privateClusterRecoveryMutation(httptest.NewRequest(http.MethodPost, portal+"/disable", nil)) ||
		privateClusterRecoveryMutation(httptest.NewRequest(http.MethodPost, "/api/v1/admin/private-clusters/connections", nil)) {
		t.Fatal("connection disable/bind boundary changed")
	}
	if value := (&Handler{}).privateClusterPublicPolicy(); value.Enabled || value.OperationTimeoutMilliseconds != 0 || value.MaximumResponseBytes != 0 {
		t.Fatal("missing owner reported as enabled")
	}
}

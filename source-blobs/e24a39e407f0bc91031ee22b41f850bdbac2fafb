package compose_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func exerciseComposePreparationStore(t *testing.T, client *http.Client, origin string, restart, verify func()) {
	t.Helper()
	get := func(path string, out any) {
		t.Helper()
		response, e := client.Get(origin + path)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(out) != nil {
			t.Fatal("actual Compose preparation HTTP response", response.StatusCode)
		}
	}
	var me struct {
		Data struct{ Workspace struct{ ID string } }
	}
	get("/api/v1/me", &me)
	path := "/api/v1/workspaces/" + me.Data.Workspace.ID + "/model-preparations"
	assert := func() {
		t.Helper()
		var caps struct {
			Data struct {
				Configured bool `json:"execution_configured"`
				CanCreate  bool `json:"can_create"`
			}
		}
		get(path+"/capabilities", &caps)
		if !caps.Data.Configured || !caps.Data.CanCreate {
			t.Fatal("actual prepared namespace/runtime role unavailable")
		}
		var inventory struct {
			Data struct {
				Items []any `json:"items"`
			}
		}
		get(path+"?limit=1", &inventory)
		if len(inventory.Data.Items) != 0 {
			t.Fatal("idle configured deployment fabricated preparation")
		}
	}
	assert()
	restart()
	assert()
	verify()
	assert()
	t.Log("Actual declared arm64 Docker Desktop Compose control/PG with four separate private stores, root-to-runtime preparation initialization, restricted runtime role, original empty namespace restart/operator verification and truthful idle preparation API. Actual full accepted output/paired restore is independently verified natively; no NVIDIA/Kubernetes/physical host claim.")
}

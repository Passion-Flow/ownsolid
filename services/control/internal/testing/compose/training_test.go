package compose_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func exerciseComposeTrainingStore(t *testing.T, client *http.Client, origin string, restart, verify func()) {
	t.Helper()
	var me struct {
		Data struct{ Workspace struct{ ID string } }
	}
	get := func(path string, out any) {
		t.Helper()
		response, e := client.Get(origin + path)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(out) != nil {
			t.Fatal("actual Compose training HTTP response", response.StatusCode)
		}
	}
	get("/api/v1/me", &me)
	path := "/api/v1/workspaces/" + me.Data.Workspace.ID + "/training/capabilities"
	assert := func() {
		t.Helper()
		var envelope struct {
			Data struct {
				Enabled   bool `json:"enabled"`
				CanCreate bool `json:"can_create"`
			}
		}
		get(path, &envelope)
		if !envelope.Data.Enabled || !envelope.Data.CanCreate {
			t.Fatal("actual private Compose training storage unavailable")
		}
	}
	assert()
	restart()
	assert()
	verify()
	assert()
	t.Log("Actual declared Docker Desktop Linux arm64 control with real private training namespace, root-to-runtime initialization, restricted runtime role, native operator verification and restart passed. Physical NVIDIA execution and retained-output paired restore are separate profiles.")
}

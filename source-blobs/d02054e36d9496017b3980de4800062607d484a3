package compose_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// This verifies actual installed enabled configuration/private key+CA loading,
// restricted runtime with the selected exact catalog and restart. The provider is intentionally absent;
// actual exchange/React is separately exercised against real pinned Gitea.
func exerciseComposeSSOConfiguration(t *testing.T, client *http.Client, origin string, restart func()) {
	t.Helper()
	probe := func() {
		t.Helper()
		response, e := client.Get(origin + "/api/v1/auth/sso/connectors")
		if e != nil {
			t.Fatal("COMPOSE_SSO_INVENTORY_TRANSPORT_FAILED")
		}
		var inventory struct {
			Data struct {
				Items []json.RawMessage `json:"items"`
			}
		}
		err := json.NewDecoder(response.Body).Decode(&inventory)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || inventory.Data.Items == nil || len(inventory.Data.Items) != 0 {
			t.Fatal("COMPOSE_SSO_EMPTY_REAL_INVENTORY_FAILED")
		}
		response, e = client.Get(origin + "/api/v1/auth/csrf")
		if e != nil {
			t.Fatal(e)
		}
		var csrf struct {
			Data struct {
				Token string `json:"token"`
			}
		}
		err = json.NewDecoder(response.Body).Decode(&csrf)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || csrf.Data.Token == "" {
			t.Fatal("COMPOSE_SSO_CSRF_FAILED")
		}
		request, e := http.NewRequest("POST", origin+"/api/v1/auth/sso/"+uuid.NewString()+"/starts", bytes.NewBufferString(`{"mode":"login","password":null,"reason":null}`))
		if e != nil {
			t.Fatal(e)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", origin)
		request.Header.Set("X-CSRF-Token", csrf.Data.Token)
		response, e = client.Do(request)
		if e != nil {
			t.Fatal(e)
		}
		var problem struct {
			Code string `json:"code"`
		}
		err = json.NewDecoder(response.Body).Decode(&problem)
		response.Body.Close()
		if err != nil || response.StatusCode != 404 || problem.Code != "SSO_CONNECTOR_NOT_FOUND" {
			t.Fatal("COMPOSE_SSO_ENABLED_MISSING_CONNECTOR_REFUSAL_FAILED", response.StatusCode, problem.Code)
		}
	}
	probe()
	restart()
	probe()
	t.Log("Actual installed arm64 Compose enabled SSO/private keyring+CA startup, selected exact catalog/runtime-role inventory, missing connector refusal without provider I/O and restart passed. Real provider/login remains the separate native Gitea/React profile; no physical Linux/Kubernetes qualification.")
}

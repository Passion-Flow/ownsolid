package distribution_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

func TestConsumedOperationKeyRefusedAtMachineEntrances(t *testing.T) {
	c := setup(t)
	a := c.access(t)
	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	machine := keyPurposeMachineProbe(t, server, a.gateway.Gateway.ID, *a.gateway.Secret)
	ctx := t.Context()
	if err := c.s.WithTransientKey(ctx, a.owner, keyInput(a.alias), 2*time.Second, uuid.NewString(), func(raw string) error {
		models := machine("models", wire.ModelsRequest{APIKey: raw}, http.StatusOK, "")
		bindings := models["models"].([]any)
		if len(bindings) != 1 || bindings[0].(map[string]any)["id"] != a.alias.ID {
			t.Fatal("operation key discovery lost its bound alias")
		}
		input := a.input()
		input.APIKey = raw
		machine("admissions", input, http.StatusOK, "")
		machine("models", wire.ModelsRequest{APIKey: raw}, http.StatusUnauthorized, "API_KEY_INVALID")
		retry := input
		retry.RequestID = uuid.NewString()
		machine("admissions", retry, http.StatusUnauthorized, "API_KEY_INVALID")
		receipt := machine("outcomes", success(input.RequestID), http.StatusOK, "")
		if receipt["request_id"] != input.RequestID {
			t.Fatal("consumed key prevented reporting the accepted operation")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func keyPurposeMachineProbe(t *testing.T, server *httptest.Server, gateway, credential string) func(string, any, int, string) map[string]any {
	t.Helper()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	successSchema := map[string]string{
		"models": "GatewayModelsAuthorizationResponse", "admissions": "GatewayAdmissionResponse",
		"outcomes": "GatewayOutcomeAcknowledgmentResponse",
	}
	path := httpfixture.AdminOrigin + cellroute.Public("/gateway/v1/instances/"+gateway+"/", agentwire.LocalScope())
	return func(operation string, payload any, status int, code string) map[string]any {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal("machine request encoding failed")
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, path+operation, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+credential)
		request.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var envelope map[string]any
		if json.NewDecoder(response.Body).Decode(&envelope) != nil {
			t.Fatal("invalid machine response JSON")
		}
		if response.StatusCode != status {
			t.Fatalf("%s returned HTTP %d, expected %d", operation, response.StatusCode, status)
		}
		schema := successSchema[operation]
		if status >= http.StatusBadRequest {
			schema = "Problem"
			if envelope["code"] != code {
				t.Fatalf("%s returned %v, expected %s", operation, envelope["code"], code)
			}
		}
		if schemas[schema].Validate(envelope) != nil {
			t.Fatalf("%s response violated %s", operation, schema)
		}
		if status >= http.StatusBadRequest {
			return nil
		}
		return envelope["data"].(map[string]any)
	}
}

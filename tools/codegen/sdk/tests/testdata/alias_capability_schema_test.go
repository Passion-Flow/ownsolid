package distribution_test

import (
	"encoding/json"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

// The SDK harness overlays this test into the owning package to reuse the
// real JSON Schema compiler without changing the distribution source tree.
func TestAliasCapabilityReadSchemas(t *testing.T) {
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	alias := map[string]any{
		"id":            "12345678-0000-4000-8000-000000000001",
		"workspace_id":  "12345678-0000-4000-8000-000000000002",
		"gateway_id":    "12345678-0000-4000-8000-000000000003",
		"node_id":       "12345678-0000-4000-8000-000000000004",
		"deployment_id": "12345678-0000-4000-8000-000000000005",
		"name":          "verified-alias", "client_origin": "https://gateway.example.invalid",
		"deployment_revision": 1, "revision": 1, "held_requests": 0,
		"disabled_at": nil, "created_at": "2026-11-01T00:00:00Z",
		"available": true, "unavailable_code": nil,
		"description": "", "description_revision": 1,
	}
	with := func(capabilities any, present bool) map[string]any {
		t.Helper()
		raw, err := json.Marshal(alias)
		var value map[string]any
		if err != nil || json.Unmarshal(raw, &value) != nil {
			t.Fatal("invalid synthetic alias")
		}
		if present {
			value["capabilities"] = capabilities
		}
		return value
	}
	verified := map[string]any{"text_generation_verified": true, "context_token_ceiling": 1024}
	for _, test := range []struct {
		name           string
		capabilities   any
		present, valid bool
	}{
		{"unknown omitted", nil, false, true},
		{"verified current target", verified, true, true},
		{"null is not unknown", nil, true, false},
		{"empty is not unknown", map[string]any{}, true, false},
		{"missing ceiling", map[string]any{"text_generation_verified": true}, true, false},
		{"missing verification", map[string]any{"context_token_ceiling": 1024}, true, false},
		{"false is not verified", map[string]any{"text_generation_verified": false, "context_token_ceiling": 1024}, true, false},
		{"zero ceiling", map[string]any{"text_generation_verified": true, "context_token_ceiling": 0}, true, false},
		{"fractional ceiling", map[string]any{"text_generation_verified": true, "context_token_ceiling": 1.5}, true, false},
		{"unknown adapter claim", map[string]any{"text_generation_verified": true, "context_token_ceiling": 1024, "tools": true}, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := with(test.capabilities, test.present)
			for schema, body := range map[string]any{
				"ModelAliasRead":         value,
				"ModelAliasReadResponse": map[string]any{"data": value},
				"ModelAliasPageResponse": map[string]any{"data": map[string]any{"items": []any{value}, "next_cursor": nil}},
			} {
				err := schemas[schema].Validate(body)
				if (err == nil) != test.valid {
					t.Fatalf("%s validity is %v, want %v: %v", schema, err == nil, test.valid, err)
				}
			}
		})
	}
	for _, name := range []string{"ModelAlias", "ModelAliasResponse"} {
		body := any(with(verified, true))
		if name == "ModelAliasResponse" {
			body = map[string]any{"data": body}
		}
		if err := schemas[name].Validate(body); err == nil {
			t.Fatalf("capabilities entered the closed mutation component %s", name)
		}
	}
	unpaired := with(nil, false)
	delete(unpaired, "description_revision")
	if schemas["ModelAliasRead"].Validate(unpaired) == nil {
		t.Fatal("read projection lost the existing description pairing")
	}
}

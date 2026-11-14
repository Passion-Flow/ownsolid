package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

func TestMongoDBSourceChangedWireAndProblemSchemaRefusals(t *testing.T) {
	const code = "MONGODB_SOURCE_CHANGED"
	w := httptest.NewRecorder()
	Problem(w, "9fe472ea-d595-424a-bbef-aea6ee6a3f4f", errors.New(code))
	var body map[string]any
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || w.Code != http.StatusConflict || body["code"] != code || len(body) != 5 || w.Header().Get("Retry-After") != "" || PublicErrorCode(errors.New(code)) != code {
		t.Fatal("source change lost stable conflict/no-DTO semantics")
	}
	for _, name := range []string{"applications", "components", "deployments", "distribution", "knowledge", "model-lifecycle", "nodes", "notifications", "plugins", "preferences", "sso", "training", "workflows"} {
		t.Run(name, func(t *testing.T) {
			raw, e := os.ReadFile(filepath.Join("../../../../../api/openapi/v1", name, "openapi.json"))
			if e != nil {
				t.Fatal(e)
			}
			var document map[string]any
			if json.Unmarshal(raw, &document) != nil {
				t.Fatal("invalid OpenAPI JSON")
			}
			problem := document["components"].(map[string]any)["schemas"].(map[string]any)["Problem"]
			compiler := jsonschema.NewCompiler()
			compiler.AssertFormat()
			id := "https://projectone.invalid/m21/" + name + "/problem"
			if e = compiler.AddResource(id, problem); e != nil {
				t.Fatal(e)
			}
			schema, e := compiler.Compile(id)
			if e != nil {
				t.Fatal(e)
			}
			if e = schema.Validate(body); e != nil {
				t.Fatal("actual conflict body violates generated consumer", e)
			}
			for _, change := range []struct {
				key   string
				value any
			}{{"code", code + "_UNKNOWN"}, {"status", "409"}, {"source_uri", "redacted.invalid"}, {"data", map[string]any{"draft_id": "not-a-success-response"}}} {
				altered := map[string]any{}
				for k, v := range body {
					altered[k] = v
				}
				altered[change.key] = change.value
				if schema.Validate(altered) == nil {
					t.Fatal("unsafe/unknown problem payload accepted", change.key)
				}
			}
		})
	}
}

func TestMongoDBSourceChangedVersionedAPIAndRequestRefusals(t *testing.T) {
	raw, e := os.ReadFile("../../../../../api/mongodb/v1/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	var source map[string]any
	if yaml.Unmarshal(raw, &source) != nil {
		t.Fatal("invalid versioned MongoDB OpenAPI")
	}
	normalized, e := json.Marshal(source)
	if e != nil {
		t.Fatal(e)
	}
	var document map[string]any
	if json.Unmarshal(normalized, &document) != nil {
		t.Fatal("invalid normalized contract")
	}
	paths := document["paths"].(map[string]any)
	for _, entry := range [][2]string{{"/preflight", "post"}, {"/imports", "post"}, {"/imports/{id}", "get"}} {
		responses := paths[entry[0]].(map[string]any)[entry[1]].(map[string]any)["responses"].(map[string]any)
		if !strings.Contains(responses["409"].(map[string]any)["description"].(string), "MONGODB_SOURCE_CHANGED") {
			t.Fatal("source conflict not documented", entry)
		}
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	const id = "https://projectone.invalid/m21/openapi"
	if e = compiler.AddResource(id, document); e != nil {
		t.Fatal(e)
	}
	schema, e := compiler.Compile(id + "#/components/schemas/ImportRequest")
	if e != nil {
		t.Fatal(e)
	}
	request := map[string]any{"source_id": "m21_synthetic", "prompt_field": "question", "completion_field": "answer", "conversion": "string", "reason": "Review configured source"}
	if e = schema.Validate(request); e != nil {
		t.Fatal("valid scoped source request rejected", e)
	}
	for _, key := range []string{"uri", "query", "aggregation", "write"} {
		request[key] = "must refuse"
		if schema.Validate(request) == nil {
			t.Fatal("request-selected source capability admitted", key)
		}
		delete(request, key)
	}
}

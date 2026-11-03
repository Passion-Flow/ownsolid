package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/servingcheck"
)

// Invoked by the frozen argument-contract root; this is not a second parser.
func servingExpectationSchemaControls(t *testing.T) {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..")
	raw, e := os.ReadFile(filepath.Join(root, "api/configuration/v1/control-serving-expectation/schema.json"))
	if e != nil {
		t.Fatal(e)
	}
	var document any
	if e = json.Unmarshal(raw, &document); e != nil {
		t.Fatal(e)
	}
	compiler := jsonschema.NewCompiler()
	const id = "https://projectone.invalid/configuration/control-serving-expectation/v1"
	if e = compiler.AddResource(id, document); e != nil {
		t.Fatal(e)
	}
	schema, e := compiler.Compile(id)
	if e != nil {
		t.Fatal(e)
	}
	raw, e = os.ReadFile(filepath.Join(root, "services/control/config/examples/control/serving-expectation.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	var example map[string]any
	if e = json.Unmarshal(raw, &example); e != nil || schema.Validate(example) != nil {
		t.Fatal("versioned structural example invalid", e)
	}
	if _, e = servingcheck.ParseExpected(raw); e != nil {
		t.Fatal("example disagrees with native canonical owner", e)
	}
	// The schema must require every field and refuse nulls and wrong types.
	for key := range example {
		for _, action := range []string{"missing", "null", "wrong_type"} {
			t.Run("schema_"+key+"_"+action, func(t *testing.T) {
				candidate := make(map[string]any, len(example))
				for k, value := range example {
					candidate[k] = value
				}
				switch action {
				case "missing":
					delete(candidate, key)
				case "null":
					candidate[key] = nil
				case "wrong_type":
					candidate[key] = false
				}
				encoded, e := json.Marshal(candidate)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = servingcheck.ParseExpected(encoded); e == nil || schema.Validate(candidate) == nil {
					t.Fatal("schema or native owner accepted an invalid required field")
				}
			})
		}
	}
	for _, mode := range []string{"http", "trusted_proxy_https"} {
		t.Run("schema_"+mode, func(t *testing.T) {
			candidate := make(map[string]any, len(example))
			for k, value := range example {
				candidate[k] = value
			}
			candidate["transport_mode"], candidate["tls_certificate_file"], candidate["tls_private_key_file"] = mode, "", ""
			if mode == "http" {
				candidate["portal_origin"], candidate["admin_origin"] = "http://portal.example.invalid", "http://admin.example.invalid"
			} else {
				candidate["trusted_proxy_cidrs"] = []any{"127.0.0.1/32"}
			}
			encoded, e := json.Marshal(candidate)
			if e != nil || schema.Validate(candidate) != nil {
				t.Fatal("structural mode example invalid", e)
			}
			if _, e = servingcheck.ParseExpected(encoded); e != nil {
				t.Fatal("mode example disagrees with native canonical owner", e)
			}
			candidate["tls_private_key_file"] = "/synthetic/unowned-key"
			if schema.Validate(candidate) == nil {
				t.Fatal("non-direct mode accepted a TLS key reference")
			}
		})
	}
}

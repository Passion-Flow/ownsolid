package http

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"reflect"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

func TestLicenseHTTPFormAndDiagnosticLimitsMatchActualProtocol(t *testing.T) {
	raw, e := os.ReadFile("../../../../../../api/openapi/v1/licensing/openapi.json")
	if e != nil {
		t.Fatal(e)
	}
	var contract struct {
		Limits struct {
			Maximum int `json:"maximum_diagnostics_bytes"`
		} `json:"x-license-wire-limits"`
		Components struct {
			Schemas map[string]struct {
				Required   []string `json:"required"`
				Properties map[string]struct {
					Maximum int `json:"maxLength"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if e = json.Unmarshal(raw, &contract); e != nil {
		t.Fatal(e)
	}
	request := contract.Components.Schemas["ImportLicense"]
	if request.Properties["artifact"].Maximum != wire.MaximumArtifactBytes || request.Properties["reason"].Maximum != wire.MaximumReasonRunes || contract.Limits.Maximum != licensing.MaximumDiagnosticsBytes {
		t.Fatal("published protocol limit drift")
	}
	typ := reflect.TypeOf(licensing.ImportInput{})
	if len(request.Required) != typ.NumField() {
		t.Fatal("required request fields drift")
	}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Tag.Get("json")
		if _, ok := request.Properties[name]; !ok {
			t.Fatal("actual field missing from contract", name)
		}
	}
}

func TestLicenseInspectionContractMatchesActualFields(t *testing.T) {
	raw, e := os.ReadFile("../../../../../../api/openapi/v1/licensing/openapi.json")
	if e != nil {
		t.Fatal(e)
	}
	var contract struct {
		Components struct {
			Schemas map[string]struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if e = json.Unmarshal(raw, &contract); e != nil {
		t.Fatal(e)
	}
	terms := licensing.FileTerms{Summary: licensing.Summary{}}
	for schema, value := range map[string]any{"LicenseInspection": licensing.Inspection{}, "LicenseFileTerms": terms, "LicenseCheck": licensing.Check{}, "InspectLicense": licensing.InspectInput{}, "LicenseState": licensing.State{}} {
		encoded, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		var fields map[string]json.RawMessage
		if e = json.Unmarshal(encoded, &fields); e != nil {
			t.Fatal(e)
		}
		declared := contract.Components.Schemas[schema]
		if len(fields) != len(declared.Properties) || len(fields) != len(declared.Required) {
			t.Fatal("field count drift", schema, len(fields), len(declared.Properties), len(declared.Required))
		}
		for name := range fields {
			if _, ok := declared.Properties[name]; !ok {
				t.Fatal("actual field missing from contract", schema, name)
			}
		}
	}
}

// Central License admission runs before every owning human mutation handler;
// therefore every Go Problem schema must describe those exact public failures.
func TestAllHumanHTTPProblemsIncludeCentralLicenseErrors(t *testing.T) {
	paths, e := filepath.Glob("../../../../../../api/openapi/v1/*/openapi.json")
	if e != nil || len(paths) == 0 {
		t.Fatal("HTTP contracts missing", e)
	}
	expected := map[string]int{"LICENSE_NEW_BUSINESS_UNAVAILABLE": 403, "LICENSE_STORED_STATE_INVALID": 503, "LICENSE_CONFIGURATION_REQUIRED": 503}
	checked := map[string]bool{}
	for _, path := range paths {
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		var value struct {
			Components struct {
				Schemas map[string]json.RawMessage `json:"schemas"`
			} `json:"components"`
		}
		if json.Unmarshal(raw, &value) != nil {
			t.Fatal("invalid HTTP contract", path)
		}
		encoded, ok := value.Components.Schemas["Problem"]
		if !ok {
			continue
		}
		var problem struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if json.Unmarshal(encoded, &problem) != nil {
			t.Fatal("invalid Problem schema", path)
		}
		code, ok := problem.Properties["code"]
		if !ok {
			continue
		}
		var enum struct {
			Enum []string `json:"enum"`
		}
		if json.Unmarshal(code, &enum) != nil {
			t.Fatal("invalid Problem code enum", path)
		}
		checked[filepath.Base(filepath.Dir(path))] = true
		for code := range expected {
			count := 0
			for _, actual := range enum.Enum {
				if actual == code {
					count++
				}
			}
			if count != 1 {
				t.Errorf("%s: central error %s appears %d times", path, code, count)
			}
		}
	}
	for _, required := range []string{"identity", "workspaces", "nodes", "components", "deployments", "training", "licensing"} {
		if !checked[required] {
			t.Error("required owning Problem contract missing", required)
		}
	}
	for code, status := range expected {
		response := httptest.NewRecorder()
		httpserver.Problem(response, "synthetic-contract-request", errors.New(code))
		var problem struct {
			Code string `json:"code"`
		}
		if response.Code != status || json.Unmarshal(response.Body.Bytes(), &problem) != nil || problem.Code != code {
			t.Error("actual public License status/code drift", code, response.Code)
		}
	}
}

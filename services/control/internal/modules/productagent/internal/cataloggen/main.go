// Command cataloggen produces the closed set of reviewed assistant tools.
package main

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	contractPath       = "../../../../../api/openapi/v1/applications/openapi.json"
	overlayPath        = "tool_catalog_overlay.json"
	catalogPath        = "gen/tools.json"
	portalScheme       = "PortalSession"
	legacyPortalScheme = "portalCookie"
)

type review struct {
	OperationID string `json:"operation_id"`
	Path        string `json:"path"`
	Risk        string `json:"risk"`
	Permission  string `json:"permission"`
}

type operation struct {
	ID        string                `json:"operationId"`
	Audiences []string              `json:"x-audiences"`
	Security  []map[string][]string `json:"security"`
	Method    string                `json:"-"`
	Path      string                `json:"-"`
}

type securityScheme struct {
	Ref  string `json:"$ref"`
	Type string `json:"type"`
	In   string `json:"in"`
	Name string `json:"name"`
}

type entry struct {
	review    `json:",inline"`
	Method    string   `json:"method"`
	Audiences []string `json:"audiences"`
}

func generate(contract, overlay []byte) ([]byte, error) {
	var reviews map[string]review
	if err := json.Unmarshal(overlay, &reviews, json.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if len(reviews) == 0 {
		return nil, fmt.Errorf("reviewed tool overlay is empty")
	}
	var spec struct {
		Paths      map[string]map[string]jsontext.Value `json:"paths"`
		Components struct {
			SecuritySchemes map[string]securityScheme `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(contract, &spec); err != nil {
		return nil, err
	}
	operations := make(map[string]operation)
	for path, item := range spec.Paths {
		for verb, raw := range item {
			switch verb {
			case "get", "head", "post", "put", "patch", "delete", "options", "trace":
			default:
				continue
			}
			var op operation
			if err := json.Unmarshal(raw, &op); err != nil {
				return nil, err
			}
			if op.ID == "" {
				continue
			}
			if _, exists := operations[op.ID]; exists {
				return nil, fmt.Errorf("duplicate operation %q", op.ID)
			}
			op.Method, op.Path = strings.ToUpper(verb), path
			operations[op.ID] = op
		}
	}
	catalog := make(map[string]entry, len(reviews))
	seen := make(map[string]bool, len(reviews))
	for id, policy := range reviews {
		op, ok := operations[policy.OperationID]
		if id == "" || !ok || seen[op.ID] {
			return nil, fmt.Errorf("invalid or duplicate reviewed operation for %q", id)
		}
		if !admittedPortalRead(op, policy, spec.Components.SecuritySchemes) {
			return nil, fmt.Errorf("reviewed portal read %q no longer matches its policy", op.ID)
		}
		seen[op.ID] = true
		catalog[id] = entry{review: policy, Method: op.Method, Audiences: op.Audiences}
	}
	output := struct {
		Notice string           `json:"generated"`
		Tools  map[string]entry `json:"tools"`
	}{"Code generated from OpenAPI and the tool overlay. DO NOT EDIT.", catalog}
	body, err := json.Marshal(output, json.Deterministic(true), jsontext.WithIndent("  "))
	return append(body, '\n'), err
}

func admittedPortalRead(op operation, policy review, schemes map[string]securityScheme) bool {
	if policy.Risk != "read" || policy.Permission != "workspace_member" || op.Method != http.MethodGet ||
		op.Path != policy.Path || !strings.HasPrefix(op.Path, "/api/v1/") || len(op.Audiences) != 1 || op.Audiences[0] != "portal" ||
		len(op.Security) != 1 || len(op.Security[0]) != 1 {
		return false
	}
	if schemes[portalScheme] != (securityScheme{Type: "apiKey", In: "cookie", Name: "__Host-portal_session"}) {
		return false
	}
	scopes, canonical := op.Security[0][portalScheme]
	if !canonical {
		// The application contract still uses an undeclared legacy reference.
		// A declared legacy scheme must go through an explicit review.
		if _, declared := schemes[legacyPortalScheme]; declared {
			return false
		}
		scopes = op.Security[0][legacyPortalScheme]
	}
	return scopes != nil && len(scopes) == 0
}

func generateFromPackage(dir string) ([]byte, error) {
	contract, err := os.ReadFile(filepath.Join(dir, contractPath))
	if err != nil {
		return nil, err
	}
	overlay, err := os.ReadFile(filepath.Join(dir, overlayPath))
	if err != nil {
		return nil, err
	}
	return generate(contract, overlay)
}

func main() {
	body, err := generateFromPackage(".")
	if err == nil {
		err = os.WriteFile(catalogPath, body, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

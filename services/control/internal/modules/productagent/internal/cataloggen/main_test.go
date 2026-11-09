package main

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGeneratedCatalogMatchesContract(t *testing.T) {
	const packageDir = "../.."
	generated, err := generateFromPackage(packageDir)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(filepath.Join(packageDir, catalogPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(committed, generated) {
		t.Fatal("review the API change and run go generate in the productagent package")
	}
	var catalog struct {
		Notice string           `json:"generated"`
		Tools  map[string]entry `json:"tools"`
	}
	if err := json.Unmarshal(generated, &catalog, json.RejectUnknownMembers(true)); err != nil {
		t.Fatalf("catalog contains fields outside the reviewed metadata: %v", err)
	}
	want := map[string]entry{
		"list_applications": {
			review: review{"ListApplications", "/api/v1/sites/{site_id}/cells/{cell_id}/applications", "read", "workspace_member"},
			Method: "GET", Audiences: []string{"portal"},
		},
		"list_conversations": {
			review: review{"ListApplicationConversations", "/api/v1/sites/{site_id}/cells/{cell_id}/application-conversations", "read", "workspace_member"},
			Method: "GET", Audiences: []string{"portal"},
		},
	}
	if !reflect.DeepEqual(catalog.Tools, want) {
		t.Fatal("the two reviewed Portal reads changed or contain additional data")
	}
}

func TestCurrentReadsRejectMisplacedPortalSession(t *testing.T) {
	const packageDir = "../.."
	raw, err := os.ReadFile(filepath.Join(packageDir, contractPath))
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := os.ReadFile(filepath.Join(packageDir, overlayPath))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths      map[string]map[string]jsontext.Value `json:"paths"`
		Components jsontext.Value                       `json:"components"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	const base = "/api/v1/sites/{site_id}/cells/{cell_id}/"
	for _, route := range []string{"applications", "application-conversations"} {
		item := spec.Paths[base+route]
		original := item["get"]
		var fields map[string]jsontext.Value
		if err := json.Unmarshal(original, &fields); err != nil {
			t.Fatal(err)
		}
		for _, tt := range []struct {
			name, operationSecurity, pathSecurity string
		}{
			{name: "missing"},
			{name: "under path item", pathSecurity: `[{"PortalSession":[]}]`},
			{name: "undeclared legacy reference", operationSecurity: `[{"portalCookie":[]}]`},
		} {
			t.Run(route+"/"+tt.name, func(t *testing.T) {
				delete(fields, "security")
				if tt.operationSecurity != "" {
					fields["security"] = jsontext.Value(tt.operationSecurity)
				}
				if tt.pathSecurity != "" {
					item["security"] = jsontext.Value(tt.pathSecurity)
				}
				updated, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				item["get"] = updated
				contract, err := json.Marshal(spec)
				if err != nil {
					t.Fatal(err)
				}
				item["get"] = original
				delete(item, "security")
				if _, err := generate(contract, overlay); err == nil || !strings.Contains(err.Error(), string(fields["operationId"])) {
					t.Fatalf("expected the reviewed operation to be refused, got %v", err)
				}
			})
		}
	}
}

func TestCatalogRejectsUnreviewedChanges(t *testing.T) {
	const overlay = `{"list":{"operation_id":"List","path":"/api/v1/list","risk":"read","permission":"workspace_member"}}`
	const operation = `"get":{"operationId":"List","x-audiences":["portal"],"security":[{"PortalSession":[]}]}`
	const scheme = `"PortalSession":{"type":"apiKey","in":"cookie","name":"__Host-portal_session"}`
	contract := func(path, methods, schemes string) string {
		return `{"paths":{"` + path + `":{` + methods + `}},"components":{"securitySchemes":{` + schemes + `}}}`
	}
	const path = "/api/v1/list"
	canonical := contract(path, operation, scheme)
	legacyOperation := strings.Replace(operation, "PortalSession", "portalCookie", 1)
	if _, err := generate([]byte(canonical), []byte(overlay)); err != nil {
		t.Fatal(err)
	}
	for name, changed := range map[string]string{
		"renamed path":       contract("/api/v1/renamed", operation, scheme),
		"write method":       contract(path, strings.Replace(operation, `"get"`, `"post"`, 1), scheme),
		"missing operation":  contract(path, strings.Replace(operation, `"List"`, `"Other"`, 1), scheme),
		"duplicate ID":       contract(path, operation+`,`+strings.Replace(operation, `"get"`, `"head"`, 1), scheme),
		"duplicate member":   contract(path, operation+`,`+operation, scheme),
		"admin audience":     contract(path, strings.Replace(operation, `["portal"]`, `["portal","admin"]`, 1), scheme),
		"anonymous access":   contract(path, strings.Replace(operation, `[{"PortalSession":[]}]`, `[{}]`, 1), scheme),
		"wrong credential":   contract(path, strings.Replace(operation, "PortalSession", "apiKey", 1), scheme),
		"missing scheme":     contract(path, operation, ""),
		"legacy reference":   contract(path, legacyOperation, scheme),
		"legacy declaration": contract(path, legacyOperation, scheme+`,`+strings.Replace(scheme, "PortalSession", "portalCookie", 1)),
		"header scheme":      contract(path, operation, strings.Replace(scheme, `"cookie"`, `"header"`, 1)),
		"other cookie":       contract(path, operation, strings.Replace(scheme, "__Host-portal_session", "__Host-admin_session", 1)),
		"other type":         contract(path, operation, strings.Replace(scheme, `"apiKey"`, `"http"`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := generate([]byte(changed), []byte(overlay)); err == nil {
				t.Fatal("accepted a changed operation without review")
			}
		})
	}
	for _, changed := range []string{
		strings.Replace(overlay, `"risk":"read"`, `"risk":"read","risk":"read"`, 1),
		strings.Replace(overlay, `"risk":"read"`, `"risk":"read","Risk":"write"`, 1),
		strings.Replace(overlay, `"risk":"read"`, `"risk":"read","unexpected":true`, 1),
	} {
		if _, err := generate([]byte(canonical), []byte(changed)); err == nil {
			t.Fatal("accepted ambiguous or unknown overlay fields")
		}
	}
}

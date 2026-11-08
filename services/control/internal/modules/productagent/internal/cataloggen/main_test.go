package main

import (
	"bytes"
	"os"
	"path/filepath"
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
	for _, valid := range []string{canonical, contract(path, legacyOperation, scheme)} {
		if _, err := generate([]byte(valid), []byte(overlay)); err != nil {
			t.Fatal(err)
		}
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

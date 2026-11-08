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
	const overlay = `{"list":{"operation_id":"List","risk":"read","permission":"workspace_member"}}`
	const operation = `"get":{"operationId":"List","x-audiences":["portal"],"security":[{"portalCookie":[]}]}`
	contract := func(methods string) []byte { return []byte(`{"paths":{"/api/v1/list":{` + methods + `}}}`) }
	if _, err := generate(contract(operation), []byte(overlay)); err != nil {
		t.Fatal(err)
	}
	for name, changed := range map[string]string{
		"write method":      strings.Replace(operation, `"get"`, `"post"`, 1),
		"missing operation": strings.Replace(operation, `"List"`, `"Other"`, 1),
		"duplicate ID":      operation + `,` + strings.Replace(operation, `"get"`, `"head"`, 1),
		"admin audience":    strings.Replace(operation, `["portal"]`, `["portal","admin"]`, 1),
		"anonymous access":  strings.Replace(operation, `[{"portalCookie":[]}]`, `[{}]`, 1),
		"wrong credential":  strings.Replace(operation, `"portalCookie"`, `"apiKey"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := generate(contract(changed), []byte(overlay)); err == nil {
				t.Fatal("accepted a changed operation without review")
			}
		})
	}
}

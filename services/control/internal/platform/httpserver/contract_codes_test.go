package httpserver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
)

// A code a contract declares is a promise to the client, which explains each one to the
// person (UX-107). Every declared code must leave the server as itself, with the status the
// contract names; anything unknown would turn into STORAGE_UNAVAILABLE on the way out.
func TestDeclaredErrorCodesLeaveTheServerAsDeclared(t *testing.T) {
	files, e := filepath.Glob("../../../../../api/openapi/v1/*/openapi.json")
	if e != nil || len(files) < 20 {
		t.Fatal(e, len(files))
	}
	var wrong []string
	declared := 0
	for _, file := range files {
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		var contract struct {
			Paths map[string]map[string]json.RawMessage `json:"paths"`
		}
		if e = json.Unmarshal(raw, &contract); e != nil {
			t.Fatal(file, e)
		}
		for path, operations := range contract.Paths {
			for method, body := range operations {
				var operation struct {
					Codes map[string][]string `json:"x-error-codes"`
				}
				if json.Unmarshal(body, &operation) != nil {
					continue
				}
				for status, codes := range operation.Codes {
					want, e := strconv.Atoi(status)
					if e != nil {
						t.Fatal(file, path, status)
					}
					for _, code := range codes {
						declared++
						got, actual := PublicProblem(errors.New(code))
						if got != code || actual != want {
							wrong = append(wrong, filepath.Base(filepath.Dir(file))+" "+method+" "+path+" "+code+" "+status+" leaves as "+got+" "+strconv.Itoa(actual))
						}
					}
				}
			}
		}
	}
	sort.Strings(wrong)
	if declared == 0 || len(wrong) > 0 {
		t.Fatalf("%d of %d declared codes leave differently:\n%v", len(wrong), declared, wrong)
	}
}

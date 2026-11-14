package main

import (
	"bytes"
	"testing"
)

func TestPostgresRestoreRootsInspectionOnlyExplicitArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--config", "/private/control"}, {"--request", "/private/request"}, {"--config", "relative", "--request", "/private/request"}, {"--config", "/private/control", "--request", ""}, {"--config", "/private/control", "--request", "/private/request", "--request", "/private/other"}, {"--config", "/private/control", "--request", "/private/request", "--env-file", ""}, {"--config", "/private/control", "--request", "/private/request", "--paths", "/arbitrary"}, {"--config", "/private/control", "--request", "/private/request", "extra"}} {
		if _, e := postgresRestoreRootsInspectionPaths(postgresRestoreRootsInspectCommand, args); e == nil {
			t.Fatal("ambiguous arguments accepted", args)
		}
		var out bytes.Buffer
		if e := runPostgresRestoreRootsInspection(postgresRestoreRootsInspectCommand, args, &out); e == nil || out.Len() != 0 {
			t.Fatal("refusal published output", e)
		}
	}
	args := []string{"--config", "/private/control", "--request", "/private/request", "--env-file", "/private/runtime.env", "--policy-env-file", "/private/policy.env"}
	if _, e := postgresRestoreRootsInspectionPaths(postgresRestoreRootsInspectCommand, args); e != nil {
		t.Fatal(e)
	}
	if _, e := postgresRestoreRootsInspectionPaths("restore-kubernetes-postgres", args); e == nil {
		t.Fatal("wrong command accepted")
	}
}

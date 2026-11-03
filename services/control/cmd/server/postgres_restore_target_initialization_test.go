package main

import (
	"bytes"
	"testing"
)

func TestPostgresRestoreTargetInitializationCommandsUseOnlyExplicitInputs(t *testing.T) {
	for _, command := range []string{postgresTargetInitializeCommand, postgresTargetInspectCommand} {
		for _, args := range [][]string{nil, {"--request", "/private/request"}, {"--config", "/private/control"}, {"--config", "relative", "--request", "/private/request"}, {"--config", "/private/control", "--request", ""}, {"--config", "/private/control", "--request", "/private/request", "--request", "/private/other"}, {"--config", "/private/control", "--request", "/private/request", "--config", "/private/other"}, {"--config", "/private/control", "--request", "/private/request", "--unknown", "true"}, {"--config", "/private/control", "--request", "/private/request", "--env-file", ""}, {"--config", "/private/control", "--request", "/private/request", "extra"}, {"--config", "/private/control", "--request", "/private/request", "--policy-env-file", "/private/policy", "--policy-env-file", "/private/other"}} {
			if _, e := postgresTargetInitializationPaths(command, args); e == nil {
				t.Fatal("ambiguous inputs accepted", command, args)
			}
		}
		if _, e := postgresTargetInitializationPaths(command, []string{"--config", "/private/control", "--request", "/private/request", "--env-file", "/private/operator.env", "--policy-env-file", "/private/policy.env"}); e != nil {
			t.Fatal("explicit original inputs", e)
		}
		var output bytes.Buffer
		if e := runPostgresTargetInitialization(command, nil, &output); e == nil || output.Len() != 0 {
			t.Fatal("invalid input reached output/files", e)
		}
	}
	if _, e := postgresTargetInitializationPaths("arbitrary-sql", []string{"--config", "/private/control", "--request", "/private/request"}); e == nil {
		t.Fatal("arbitrary command accepted")
	}
}

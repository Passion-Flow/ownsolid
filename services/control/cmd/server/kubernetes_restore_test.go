package main

import "testing"

func TestPostgresRestoreCommandsRefuseAmbiguousInputsBeforeFiles(t *testing.T) {
	for _, command := range []string{postgresRestoreCommand, postgresRestoreFilesCommand, postgresRestoreVerifyCommand} {
		for _, args := range [][]string{nil, {"--request", "relative"}, {"--request", "/private/request", "--request", "/private/other"}, {"--request", "/private/request", "--config", "/private/config", "--unknown", "true"}, {"--request", "/private/request", "--config", "/private/config", "extra"}, {"--request", "/private/request", "--config", ""}, {"--request", "/private/request", "--policy-env-file", "/private/policy", "--policy-env-file", "/private/other"}} {
			if _, e := postgresRestorePaths(command, args); e == nil {
				t.Fatalf("ambiguous %s inputs accepted", command)
			}
		}
	}
	for _, command := range []string{postgresRestoreCommand, postgresRestoreVerifyCommand} {
		if _, e := postgresRestorePaths(command, []string{"--request", "/private/request", "--config", "/private/operator", "--env-file", "/private/operator.env", "--policy-env-file", "/private/policy"}); e != nil {
			t.Fatal(e)
		}
		if _, e := postgresRestorePaths(command, []string{"--request", "/private/request"}); e == nil {
			t.Fatal("operator omitted")
		}
	}
	if _, e := postgresRestorePaths(postgresRestoreFilesCommand, []string{"--request", "/private/request", "--policy-env-file", "/private/policy"}); e != nil {
		t.Fatal(e)
	}
	if _, e := postgresRestorePaths(postgresRestoreFilesCommand, []string{"--request", "/private/request", "--config", "/private/operator"}); e == nil {
		t.Fatal("file reader accepted SQL configuration")
	}
}

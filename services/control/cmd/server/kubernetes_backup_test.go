package main

import (
	"strings"
	"testing"
)

func TestPostgresBackupCommandsRefuseAmbiguousInputsBeforeFiles(t *testing.T) {
	for _, c := range []string{postgresBackupCommand, postgresBackupReadbackCommand, postgresBackupVerifyCommand} {
		for _, args := range [][]string{nil, {"--request", "relative"}, {"--request", "/private/request", "--request", "/private/other"}, {"--request", "/private/request", "--config", "/private/config", "--unknown", "true"}, {"--request", "/private/request", "--manifest-sha256", "false"}, {"--request", "/private/request", "--config", "/private/config", "extra"}} {
			if _, e := postgresBackupPaths(c, args); e == nil {
				t.Fatalf("ambiguous %s inputs accepted", c)
			}
		}
	}
	if _, e := postgresBackupPaths(postgresBackupCommand, []string{"--config", "/private/operator", "--request", "/private/request", "--policy-env-file", "/private/policy.env"}); e != nil {
		t.Fatal(e)
	}
	for _, c := range []string{postgresBackupReadbackCommand, postgresBackupVerifyCommand} {
		if _, e := postgresBackupPaths(c, []string{"--request", "/private/request", "--manifest-sha256", strings.Repeat("a", 64)}); e != nil {
			t.Fatal(e)
		}
		if _, e := postgresBackupPaths(c, []string{"--request", "/private/request", "--manifest-sha256", strings.Repeat("a", 64), "--config", "/private/operator"}); e == nil {
			t.Fatal("reader accepted SQL configuration")
		}
	}
}

package main

import (
	"io"
	"strings"
	"testing"
)

func TestExpectedDeploymentFlagIsRestrictedToBackupHistory(t *testing.T) {
	for _, args := range [][]string{
		{"backup-history", "--config", "/site.yaml", "--expected-deployment-id="},
		{"status", "--config", "/site.yaml", "--expected-deployment-id", "0bc2c190-6fce-4136-a30a-9a32dbfc504b"},
	} {
		if err := run(args, io.Discard); err == nil || !strings.HasPrefix(err.Error(), "usage:") {
			t.Fatalf("invalid invocation reached input loading: %v", err)
		}
	}
}

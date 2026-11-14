package main

import (
	"context"
	"testing"
)

func TestRegistryCLIRejectsContradictoryArguments(t *testing.T) {
	for _, args := range [][]string{
		{"registry-import-offline"},
		{"registry-import-offline", "--manifest-file", "relative", "--archive-file", "/private/image.tar"},
		{"registry-reconcile-offline", "--manifest-file", "/private/manifest.json", "--archive-file", "/private/image.tar"},
		{"registry-reconcile-task", "--task-id", "../task"},
		{"registry-reconcile-task", "--task-id", "12345678-1234-1234-1234-123456789abc", "--disk-path", "/"},
		{"registry-reconcile-task", "--task-id", "12345678-1234-1234-1234-123456789abc", "--confirm-fenced-node", "12345678-1234-1234-1234-123456789abc"},
		{"run", "--task-id", "12345678-1234-1234-1234-123456789abc"},
		{"facts", "--manifest-file", "/private/manifest.json"},
	} {
		if e := execute(context.Background(), args); e == nil || e.Error() != "AGENT_ARGUMENT_INVALID" {
			t.Fatal("invalid registry CLI accepted", args[0], e)
		}
	}
}

func TestRegistryCLIUsesRealProtectedConfigurationBoundary(t *testing.T) {
	for _, args := range [][]string{
		{"registry-import-offline", "--manifest-file", "/private/manifest.json", "--archive-file", "/private/image.tar"},
		{"registry-reconcile-offline", "--manifest-file", "/private/manifest.json"},
		{"registry-reconcile-task", "--task-id", "12345678-1234-1234-1234-123456789abc"},
	} {
		if e := execute(context.Background(), args); e == nil || e.Error() != "PROTECTED_FILE_INVALID" {
			t.Fatal("registry entrance missed protected loader", args[0], e)
		}
	}
}

func TestFencedCleanupAcceptsSameExplicitEnvironmentBoundary(t *testing.T) {
	for _, command := range []string{"remove-fenced-inference", "remove-fenced-training", "remove-fenced-preparation"} {
		if e := execute(context.Background(), []string{command, "--config", "/definitely-absent/agent.json", "--env-file", "/definitely-absent/agent.env"}); e == nil || e.Error() != "PROTECTED_FILE_INVALID" {
			t.Fatal("cleanup missed shared protected configuration boundary", command, e)
		}
	}
	if e := execute(context.Background(), []string{"facts", "--env-file", "/definitely-absent/agent.env"}); e == nil || e.Error() != "AGENT_ARGUMENT_INVALID" {
		t.Fatal("facts adopted policy", e)
	}
}

package main

import (
	"os"
	"strings"
	"testing"
)

func TestPolicyEnvironmentFlagsRejectBeforeConfigurationOrDatabase(t *testing.T) {
	previous := os.Args
	defer func() { os.Args = previous }()
	for _, args := range [][]string{
		{"serve", "--env-file="}, {"serve", "--env-file", "relative-private-input"},
		{"migrate", "--env-file", "/unavailable-private-input"},
		{"bootstrap", "--env-file", "/unavailable-private-input"},
		{"bootstrap-packet", "--env-file", "/unavailable-private-input"},
		{"unknown", "--env-file", "/unavailable-private-input"},
	} {
		os.Args = append([]string{"control"}, args...)
		if e := run(); e == nil || e.Error() != "POLICY_ENVIRONMENT_FLAG_INVALID" {
			t.Fatal("must reject before reading any configuration/database", e)
		}
	}
	os.Args = []string{"control", "serve", "--env-file", "/unavailable-private-input"}
	if e := run(); e == nil || !strings.Contains(e.Error(), "CONFIG_REQUIRED") {
		t.Fatal("valid optional flag must preserve required configuration", e)
	}
}

func TestDatasetOperatorEnvironmentAdmission(t *testing.T) {
	previous := os.Args
	defer func() { os.Args = previous }()
	for _, command := range []string{"export-dataset-files", "import-dataset-files", "verify-dataset-store", "initialize-dataset-store"} {
		os.Args = []string{"control", command, "--env-file", "/unavailable-private-input"}
		if e := run(); e == nil || e.Error() != "CONFIG_REQUIRED" {
			t.Fatal("dataset operator mapping rejected before config", command, e)
		}
		os.Args = []string{"control", command, "--env-file", "relative"}
		if e := run(); e == nil || e.Error() != "POLICY_ENVIRONMENT_FLAG_INVALID" {
			t.Fatal(command, e)
		}
	}
}

func TestTrainingOperatorEnvironmentAdmission(t *testing.T) {
	previous := os.Args
	defer func() { os.Args = previous }()
	for _, command := range []string{"export-training-files", "import-training-files", "verify-training-files", "initialize-training-store", "check-training-store-initialization", "record-training-store-initialization"} {
		os.Args = []string{"control", command, "--env-file", "/unavailable-private-input"}
		if e := run(); e == nil || e.Error() != "CONFIG_REQUIRED" {
			t.Fatal(command, e)
		}
		os.Args = []string{"control", command, "--env-file", "relative"}
		if e := run(); e == nil || e.Error() != "POLICY_ENVIRONMENT_FLAG_INVALID" {
			t.Fatal(command, e)
		}
	}
}

func TestPreparationOperatorEnvironmentAdmission(t *testing.T) {
	previous := os.Args
	defer func() { os.Args = previous }()
	for _, command := range []string{"export-preparation-files", "import-preparation-files", "verify-preparation-files", "initialize-preparation-store", "check-preparation-store-initialization", "record-preparation-store-initialization"} {
		os.Args = []string{"control", command, "--env-file", "/unavailable-private-input"}
		if e := run(); e == nil || e.Error() != "CONFIG_REQUIRED" {
			t.Fatal(command, e)
		}
		os.Args = []string{"control", command, "--env-file", "relative"}
		if e := run(); e == nil || e.Error() != "POLICY_ENVIRONMENT_FLAG_INVALID" {
			t.Fatal(command, e)
		}
	}
}

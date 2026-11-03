package main

import (
	"os"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
)

func TestRuntimeConfigurationCommandBoundaryRefusesBeforeConfigurationOrDatabase(t *testing.T) {
	original := os.Args
	defer func() { os.Args = original }()
	for _, command := range []string{databaseaccess.NodesUpgradeCommand, databaseaccess.WorkspacesUpgradeCommand, databaseaccess.MigrationCommand, "prepare-runtime-access", "check-runtime-access", databaseaccess.ReconcileMySQLCommand, databaseaccess.ResumeMySQLCommand} {
		os.Args = []string{"server", command, "--config", "missing-must-not-be-opened"}
		if e := run(); e == nil || safeError(e) != "RUNTIME_ACCESS_CONFIGURATION_REQUIRED" {
			t.Fatal("missing runtime reference reached configuration or DB", command, e)
		}
	}
	// Restored-runtime commands have the stricter fixed argument shape checked
	// before generic runtime configuration admission or any configuration I/O.
	for _, command := range []string{"prepare-restored-runtime-access", "check-restored-runtime-access", "reconcile-restored-runtime-access", "resume-restored-runtime-access"} {
		os.Args = []string{"server", command, "--config", "missing-must-not-be-opened"}
		if e := run(); e == nil || safeError(e) != "MYSQL_NATIVE_OPERATOR_ARGUMENT_INVALID" {
			t.Fatal("incomplete restored-runtime arguments reached configuration or DB", command, e)
		}
	}
	for _, command := range []string{"migrate", "serve", "initialize-model-store"} {
		os.Args = []string{"server", command, "--config", "missing-must-not-be-opened", "--runtime-config", "SYNTHETIC_PRIVATE_UNUSED_REFERENCE"}
		if e := run(); e == nil || safeError(e) != "RUNTIME_ACCESS_COMMAND_INVALID" {
			t.Fatal("unused runtime reference silently accepted", command, e)
		}
	}
	os.Args = []string{"server", databaseaccess.MigrationCommand, "--config", "missing-must-not-be-opened", "--runtime-config", "SYNTHETIC_PRIVATE_UNUSED_REFERENCE", "unowned-positional-value"}
	if e := run(); e == nil || safeError(e) != "OPERATOR_ARGUMENT_INVALID" {
		t.Fatal("positional override silently ignored", e)
	}
}

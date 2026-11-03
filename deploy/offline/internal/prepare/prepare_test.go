package prepare

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

func TestRestoredApplicationRetryDispatchAcceptsOnlyFixedEntrances(t *testing.T) {
	for _, command := range []string{"retry-restored-database", "retry-restored-application"} {
		t.Run(command, func(t *testing.T) {
			if !Supported(command) {
				t.Fatal("fixed retry entrance missing")
			}
			_, e := Run(context.Background(), command, configuration.Config{})
			if e == nil {
				t.Fatal("retry accepted missing owning configuration")
			}
		})
	}
	for _, command := range []string{"retry-runtime", "retry-restored-control", "retry-restored-arbitrary"} {
		if Supported(command) {
			t.Fatal("arbitrary retry entrance accepted")
		}
	}
}

func TestRestoredApplicationCommandRequiresExplicitConfiguredBoundary(t *testing.T) {
	if !Supported("start-restored-application") || Supported("start-restored-arbitrary-application") {
		t.Fatal("fixed restored application entrance required")
	}
	if _, e := Run(context.Background(), "start-restored-application", configuration.Config{}); e == nil || e.Error() != "OFFLINE_SCOPE_RESTORED_APPLICATION_CONFIGURATION_REQUIRED" {
		t.Fatal("implicit application configuration", e)
	}
}

func TestRestoredCurrentStoresCommandRequiresExplicitConfiguredBoundary(t *testing.T) {
	for _, command := range []string{"inspect-restored-current-stores", "qualify-restored-current-stores", "requalify-restored-current-stores", "reconcile-restored-current-stores", "abort-restored-current-stores"} {
		if !Supported(command) {
			t.Fatal("current stores command missing", command)
		}
		if _, e := Run(context.Background(), command, configuration.Config{}); e == nil || e.Error() != "OFFLINE_SCOPE_CURRENT_STORES_CONFIGURATION_REQUIRED" {
			t.Fatal("implicit current stores boundary", command, e)
		}
	}
	if Supported("qualify-restored-arbitrary-store") {
		t.Fatal("arbitrary store command accepted")
	}
}

func TestRestoredLiveDatabaseCommandsRequireExplicitConfiguredBoundary(t *testing.T) {
	for _, command := range []string{"inspect-restored-live-database", "qualify-restored-live-database", "requalify-restored-live-database", "reconcile-restored-live-database", "abort-restored-live-database"} {
		if !Supported(command) {
			t.Fatal("fixed command missing", command)
		}
		if _, e := Run(context.Background(), command, configuration.Config{}); e == nil || e.Error() != "OFFLINE_SCOPE_LIVE_DATABASE_CONFIGURATION_REQUIRED" {
			t.Fatal("implicit live database boundary", command, e)
		}
	}
	if Supported("qualify-restored-arbitrary-task") {
		t.Fatal("arbitrary command accepted")
	}
}

func TestRestoredDependencyCommandRequiresExplicitConfiguredBoundary(t *testing.T) {
	if !Supported("start-restored-database") || Supported("start-restored-arbitrary-service") {
		t.Fatal("fixed restored dependency command")
	}
	if _, e := Run(context.Background(), "start-restored-database", configuration.Config{}); e == nil || e.Error() != "OFFLINE_SCOPE_RESTORED_DEPENDENCY_CONFIGURATION_REQUIRED" {
		t.Fatal("unconfigured restored dependency admitted", e)
	}
}

func TestPreparationRejectsInvalidPinsBeforeCreatingAnyOutput(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "must-not-create")
	c := configuration.Defaults()
	c.DestinationRoot = destination
	c.ExpectedArchiveSHA256 = "invalid"
	c.ExpectedManifestSHA256 = strings.Repeat("a", 64)
	for _, command := range []string{"inspect", "unpack", "install-production"} {
		if _, e := Run(context.Background(), command, c); e == nil {
			t.Fatal("unsafe operation accepted", command)
		}
		if _, e := os.Stat(destination); !os.IsNotExist(e) {
			t.Fatal("output created on invalid input")
		}
	}
}

func TestControlReplacementCommandsRequireExplicitOwnedEngineContext(t *testing.T) {
	for _, command := range []string{"replace-restored-control", "reconcile-restored-control", "recover-restored-control"} {
		if !Supported(command) {
			t.Fatal("command missing", command)
		}
		if _, e := Run(context.Background(), command, configuration.Config{}); e == nil {
			t.Fatal("implicit replacement accepted", command)
		}
	}
	if Supported("replace-control-arbitrary") {
		t.Fatal("arbitrary replacement accepted")
	}
}

func TestModelTaskCommandsRequireExplicitEngineUtility(t *testing.T) {
	for _, command := range []string{"create-runtime-access-task", "reconcile-runtime-access-task", "start-runtime-access-task", "stop-runtime-access-task", "remove-runtime-access-task", "reconcile-runtime-access-action", "create-runtime-access-check-task", "reconcile-runtime-access-check-task", "start-runtime-access-check-task", "stop-runtime-access-check-task", "remove-runtime-access-check-task", "reconcile-runtime-access-check-action"} {
		if !Supported(command) {
			t.Fatal("access command missing", command)
		}
		if _, e := Run(context.Background(), command, configuration.Defaults()); e == nil {
			t.Fatal("implicit access context", command)
		}
	}
	for _, command := range []string{"create-database-schema-task", "reconcile-database-schema-task", "start-database-schema-task", "stop-database-schema-task", "remove-database-schema-task", "reconcile-database-schema-action"} {
		if !Supported(command) {
			t.Fatal("schema command missing", command)
		}
		if _, e := Run(context.Background(), command, configuration.Defaults()); e == nil {
			t.Fatal("implicit schema context", command)
		}
	}
	for _, command := range []string{"create-model-store-task", "reconcile-model-store-task", "start-model-store-task", "stop-model-store-task", "remove-model-store-task", "reconcile-model-store-action", "start-database", "create-database-migration-task", "reconcile-database-migration-task", "start-database-migration-task", "stop-database-migration-task", "remove-database-migration-task", "reconcile-database-migration-action"} {
		if !Supported(command) {
			t.Fatal("model task command missing", command)
		}
		if _, e := Run(context.Background(), command, configuration.Defaults()); e == nil {
			t.Fatal("implicit model task context", command)
		}
	}
}

func TestBootstrapTaskCommandsRequireExplicitUtility(t *testing.T) {
	for _, command := range []string{"create-bootstrap-task", "reconcile-bootstrap-task", "start-bootstrap-task", "stop-bootstrap-task", "remove-bootstrap-task", "reconcile-bootstrap-action", "create-bootstrap-check-task", "reconcile-bootstrap-check-task", "start-bootstrap-check-task", "stop-bootstrap-check-task", "remove-bootstrap-check-task", "reconcile-bootstrap-check-action", "create-bootstrap-resume-task", "reconcile-bootstrap-resume-task", "start-bootstrap-resume-task", "stop-bootstrap-resume-task", "remove-bootstrap-resume-task", "reconcile-bootstrap-resume-action"} {
		if !Supported(command) {
			t.Fatal(command)
		}
		if _, e := Run(context.Background(), command, configuration.Config{}); e == nil {
			t.Fatal("unconfigured bootstrap accepted", command)
		}
	}
}

func TestFixedStoreCommandsRequireExplicitConfiguredContext(t *testing.T) {
	for command := range storeCommands {
		if !Supported(command) {
			t.Fatal(command)
		}
		if _, e := Run(context.Background(), command, configuration.Config{}); e == nil {
			t.Fatal("unconfigured store accepted", command)
		}
	}
	if Supported("create-arbitrary-store-task") {
		t.Fatal("arbitrary task exposed")
	}
}

func TestRestoreTargetRequiresExplicitReadOnlyOperationContext(t *testing.T) {
	if !Supported("inspect-restore-target") || !RestoreSourceCommand("inspect-restore-target") || BackupCommand("inspect-restore-target") {
		t.Fatal("restore target operation boundary")
	}
	if _, e := RunRestoreTarget(context.Background(), configuration.Defaults(), configuration.RestoreSourceDefaults()); e == nil || e.Error() != "OFFLINE_SCOPE_RESTORE_TARGET_CONFIGURATION_REQUIRED" {
		t.Fatal("unconfigured target admitted", e)
	}
}

func TestRestoreSQLCommandsRequireExplicitConfiguredScope(t *testing.T) {
	for _, command := range []string{"restore-sql", "reconcile-restore-sql", "abort-restore-sql"} {
		if !Supported(command) || !RestoreSQLCommand(command) || RestoreReservationCommand(command) {
			t.Fatal("closed SQL command boundary", command)
		}
		if _, e := RunRestoreSQL(context.Background(), command, configuration.Defaults(), configuration.RestoreRequest{}); e == nil || e.Error() != "OFFLINE_SCOPE_RESTORE_SQL_CONFIGURATION_REQUIRED" {
			t.Fatal("unconfigured SQL scope accepted", e)
		}
	}
}

func TestRestoredDatabaseCommandsRequireExplicitConfiguredScope(t *testing.T) {
	for _, command := range []string{"qualify-restored-database", "reconcile-restored-database", "abort-restored-database"} {
		if !Supported(command) || !RestoreDatabaseCommand(command) || RestoreSQLCommand(command) {
			t.Fatal("closed restored database boundary", command)
		}
		if _, e := RunRestoreDatabase(context.Background(), command, configuration.Defaults(), configuration.RestoreRequest{}); e == nil || e.Error() != "OFFLINE_SCOPE_RESTORE_DATABASE_CONFIGURATION_REQUIRED" {
			t.Fatal("unconfigured restored database admitted", e)
		}
	}
}

func TestRestoreStoresCommandsRequireExplicitConfiguredScope(t *testing.T) {
	for _, command := range []string{"restore-stores", "reconcile-restored-stores", "abort-restored-stores"} {
		if !Supported(command) || !RestoreStoresCommand(command) || RestoreDatabaseCommand(command) || RestoreSQLCommand(command) {
			t.Fatal("closed store command boundary", command)
		}
		if _, e := RunRestoreStores(context.Background(), command, configuration.Defaults(), configuration.RestoreRequest{}); e == nil || e.Error() != "OFFLINE_SCOPE_RESTORE_STORES_CONFIGURATION_REQUIRED" {
			t.Fatal("unconfigured store scope admitted", e)
		}
	}
}

// Package prepare dispatches verified preparation and isolated runtime lifecycle effects.
package prepare

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

func Supported(command string) bool {
	if command == "inspect-compatible-restore-target" {
		return true
	}
	if RuntimeAbsenceRecoveryCommand(command) {
		return true
	}
	if scope.CumulativeUpgradeCommand(command) {
		return true
	}
	if ProcessRecoveryCommand(command) {
		return true
	}
	if JournalCapacityCommand(command) {
		return true
	}
	if command == "retry-restored-database" || command == "retry-restored-application" {
		return true
	}
	if command == "start-restored-application" {
		return true
	}
	if scope.CurrentStoresCommand(command) {
		return true
	}
	if command == "inspect-restored-current-stores" {
		return true
	}
	if scope.LiveDatabaseCommand(command) {
		return true
	}
	if command == "inspect-restored-live-database" {
		return true
	}
	if command == "start-restored-database" {
		return true
	}
	if scope.ControlReplacementCommand(command) || RestoredTLSCommand(command) || BackupCommand(command) || RestoreSourceCommand(command) || RestoreReservationCommand(command) || RestoreSQLCommand(command) || RestoreDatabaseCommand(command) || RestoreStoresCommand(command) {
		return true
	}
	if _, ok := storeCommands[command]; ok {
		return true
	}
	switch command {
	case "create-bootstrap-task", "reconcile-bootstrap-task", "start-bootstrap-task", "stop-bootstrap-task", "remove-bootstrap-task", "reconcile-bootstrap-action", "create-bootstrap-check-task", "reconcile-bootstrap-check-task", "start-bootstrap-check-task", "stop-bootstrap-check-task", "remove-bootstrap-check-task", "reconcile-bootstrap-check-action", "create-bootstrap-resume-task", "reconcile-bootstrap-resume-task", "start-bootstrap-resume-task", "stop-bootstrap-resume-task", "remove-bootstrap-resume-task", "reconcile-bootstrap-resume-action":
		return true
	case "create-runtime-access-task", "reconcile-runtime-access-task", "start-runtime-access-task", "stop-runtime-access-task", "remove-runtime-access-task", "reconcile-runtime-access-action", "create-runtime-access-check-task", "reconcile-runtime-access-check-task", "start-runtime-access-check-task", "stop-runtime-access-check-task", "remove-runtime-access-check-task", "reconcile-runtime-access-check-action":
		return true
	case "create-database-schema-task", "reconcile-database-schema-task", "start-database-schema-task", "stop-database-schema-task", "remove-database-schema-task", "reconcile-database-schema-action":
		return true
	case "reconcile-release-view", "inspect", "unpack", "verify-prepared", "init-scope", "status", "preflight-engine", "import-images", "reconcile-images", "preflight-compose", "plan-compose", "prepare-storage-inputs", "reconcile-storage-inputs", "initialize-storage", "reconcile-storage", "create-runtime", "reconcile-runtime", "start-runtime", "stop-runtime", "reconcile-process", "restart-runtime", "create-model-store-task", "reconcile-model-store-task", "start-model-store-task", "stop-model-store-task", "remove-model-store-task", "reconcile-model-store-action", "start-database", "create-database-migration-task", "reconcile-database-migration-task", "start-database-migration-task", "stop-database-migration-task", "remove-database-migration-task", "reconcile-database-migration-action":
		return true
	}
	return false
}
func Run(ctx context.Context, command string, c configuration.Config) (any, error) {
	if command == "inspect-compatible-restore-target" {
		return nil, errors.New("OFFLINE_OPERATOR_RESTORE_COMPATIBILITY_OPERATION_REQUIRED")
	}
	if RuntimeAbsenceRecoveryCommand(command) {
		return nil, errors.New("OFFLINE_OPERATOR_RUNTIME_RECOVERY_OPERATION_REQUIRED")
	}
	if command == "reconcile-release-view" {
		return scope.ReconcileReleaseView(ctx, c)
	}
	if ProcessRecoveryCommand(command) {
		return nil, errors.New("OFFLINE_OPERATOR_PROCESS_RECOVERY_OPERATION_REQUIRED")
	}
	if command == "retry-restored-database" {
		return scope.RetryRestoredDatabase(ctx, c)
	}
	if command == "retry-restored-application" {
		return scope.RetryRestoredApplication(ctx, c)
	}
	if command == "start-restored-application" {
		return scope.StartRestoredApplication(ctx, c)
	}
	if scope.CurrentStoresCommand(command) {
		return scope.RunRestoredCurrentStores(ctx, c, command)
	}
	if command == "inspect-restored-current-stores" {
		return scope.InspectRestoredCurrentStores(ctx, c)
	}
	if scope.LiveDatabaseCommand(command) {
		return scope.RunRestoredLiveDatabase(ctx, c, command)
	}
	if command == "inspect-restored-live-database" {
		return scope.InspectRestoredLiveDatabase(ctx, c)
	}
	if command == "start-restored-database" {
		return scope.StartRestoredDatabase(ctx, c)
	}
	if scope.ControlReplacementCommand(command) {
		if command == "recover-restored-control" {
			return scope.RecoverRestoredControl(ctx, c)
		}
		return scope.ReplaceRestoredControl(ctx, c, command == "reconcile-restored-control")
	}
	if fixed, ok := storeCommands[command]; ok {
		return runStoreCommand(ctx, c, fixed)
	}
	switch command {
	case "create-bootstrap-task":
		return scope.CreateBootstrapTask(ctx, c, engine.DatabaseBootstrap, false)
	case "reconcile-bootstrap-task":
		return scope.CreateBootstrapTask(ctx, c, engine.DatabaseBootstrap, true)
	case "start-bootstrap-task":
		return scope.BootstrapTaskAction(ctx, c, engine.DatabaseBootstrap, "execute", false)
	case "stop-bootstrap-task":
		return scope.BootstrapTaskAction(ctx, c, engine.DatabaseBootstrap, "stopped", false)
	case "remove-bootstrap-task":
		return scope.BootstrapTaskAction(ctx, c, engine.DatabaseBootstrap, "removed", false)
	case "reconcile-bootstrap-action":
		return scope.BootstrapTaskAction(ctx, c, engine.DatabaseBootstrap, "", true)
	case "create-bootstrap-check-task":
		return scope.CreateBootstrapTask(ctx, c, engine.DatabaseBootstrapReconcile, false)
	case "reconcile-bootstrap-check-task":
		return scope.CreateBootstrapTask(ctx, c, engine.DatabaseBootstrapReconcile, true)
	case "start-bootstrap-check-task":
		return scope.BootstrapTaskAction(ctx, c, engine.DatabaseBootstrapReconcile, "execute", false)
	case "stop-bootstrap-check-task":
		return scope.BootstrapTaskAction(ctx, c, engine.DatabaseBootstrapReconcile, "stopped", false)
	case "remove-bootstrap-check-task":
		return scope.BootstrapTaskAction(ctx, c, engine.DatabaseBootstrapReconcile, "removed", false)
	case "reconcile-bootstrap-check-action":
		return scope.BootstrapTaskAction(ctx, c, engine.DatabaseBootstrapReconcile, "", true)
	case "create-bootstrap-resume-task":
		return scope.CreateBootstrapTask(ctx, c, engine.DatabaseBootstrapResume, false)
	case "reconcile-bootstrap-resume-task":
		return scope.CreateBootstrapTask(ctx, c, engine.DatabaseBootstrapResume, true)
	case "start-bootstrap-resume-task":
		return scope.BootstrapTaskAction(ctx, c, engine.DatabaseBootstrapResume, "execute", false)
	case "stop-bootstrap-resume-task":
		return scope.BootstrapTaskAction(ctx, c, engine.DatabaseBootstrapResume, "stopped", false)
	case "remove-bootstrap-resume-task":
		return scope.BootstrapTaskAction(ctx, c, engine.DatabaseBootstrapResume, "removed", false)
	case "reconcile-bootstrap-resume-action":
		return scope.BootstrapTaskAction(ctx, c, engine.DatabaseBootstrapResume, "", true)
	case "create-runtime-access-task":
		return scope.CreateDatabaseAccessTask(ctx, c, engine.DatabaseRuntimePrepare, false)
	case "reconcile-runtime-access-task":
		return scope.CreateDatabaseAccessTask(ctx, c, engine.DatabaseRuntimePrepare, true)
	case "start-runtime-access-task":
		return scope.DatabaseAccessTaskAction(ctx, c, engine.DatabaseRuntimePrepare, "execute", false)
	case "stop-runtime-access-task":
		return scope.DatabaseAccessTaskAction(ctx, c, engine.DatabaseRuntimePrepare, "stopped", false)
	case "remove-runtime-access-task":
		return scope.DatabaseAccessTaskAction(ctx, c, engine.DatabaseRuntimePrepare, "removed", false)
	case "reconcile-runtime-access-action":
		return scope.DatabaseAccessTaskAction(ctx, c, engine.DatabaseRuntimePrepare, "", true)
	case "create-runtime-access-check-task":
		return scope.CreateDatabaseAccessTask(ctx, c, engine.DatabaseRuntimeCheck, false)
	case "reconcile-runtime-access-check-task":
		return scope.CreateDatabaseAccessTask(ctx, c, engine.DatabaseRuntimeCheck, true)
	case "start-runtime-access-check-task":
		return scope.DatabaseAccessTaskAction(ctx, c, engine.DatabaseRuntimeCheck, "execute", false)
	case "stop-runtime-access-check-task":
		return scope.DatabaseAccessTaskAction(ctx, c, engine.DatabaseRuntimeCheck, "stopped", false)
	case "remove-runtime-access-check-task":
		return scope.DatabaseAccessTaskAction(ctx, c, engine.DatabaseRuntimeCheck, "removed", false)
	case "reconcile-runtime-access-check-action":
		return scope.DatabaseAccessTaskAction(ctx, c, engine.DatabaseRuntimeCheck, "", true)
	case "create-database-schema-task":
		return scope.CreateDatabaseSchemaTask(ctx, c, false)
	case "reconcile-database-schema-task":
		return scope.CreateDatabaseSchemaTask(ctx, c, true)
	case "start-database-schema-task":
		return scope.DatabaseSchemaTaskAction(ctx, c, "execute", false)
	case "stop-database-schema-task":
		return scope.DatabaseSchemaTaskAction(ctx, c, "stopped", false)
	case "remove-database-schema-task":
		return scope.DatabaseSchemaTaskAction(ctx, c, "removed", false)
	case "reconcile-database-schema-action":
		return scope.DatabaseSchemaTaskAction(ctx, c, "", true)
	case "start-database-migration-task":
		return scope.DatabaseTaskAction(ctx, c, "execute", false)
	case "stop-database-migration-task":
		return scope.DatabaseTaskAction(ctx, c, "stopped", false)
	case "remove-database-migration-task":
		return scope.DatabaseTaskAction(ctx, c, "removed", false)
	case "reconcile-database-migration-action":
		return scope.DatabaseTaskAction(ctx, c, "", true)
	case "create-database-migration-task":
		return scope.CreateDatabaseMigrationTask(ctx, c, false)
	case "reconcile-database-migration-task":
		return scope.CreateDatabaseMigrationTask(ctx, c, true)
	case "start-database":
		return scope.StartDatabase(ctx, c)
	case "start-model-store-task":
		return scope.ModelTaskAction(ctx, c, "execute", false)
	case "stop-model-store-task":
		return scope.ModelTaskAction(ctx, c, "stopped", false)
	case "remove-model-store-task":
		return scope.ModelTaskAction(ctx, c, "removed", false)
	case "reconcile-model-store-action":
		return scope.ModelTaskAction(ctx, c, "", true)
	case "create-model-store-task":
		return scope.CreateModelTask(ctx, c, false)
	case "reconcile-model-store-task":
		return scope.CreateModelTask(ctx, c, true)
	case "inspect":
		return bundle.Inspect(ctx, c.Verification, c.BundleFile, c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
	case "unpack":
		return bundle.Unpack(ctx, c.Verification, c.BundleFile, c.DestinationRoot, c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
	case "verify-prepared":
		return bundle.VerifyPrepared(ctx, c.Verification, c.DestinationRoot, c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
	case "init-scope":
		return scope.Create(ctx, c)
	case "status":
		return scope.Read(ctx, c)
	case "plan-compose":
		return scope.PlanCompose(ctx, c)
	case "prepare-storage-inputs":
		return scope.PrepareStorageInputs(ctx, c, false)
	case "reconcile-storage-inputs":
		return scope.PrepareStorageInputs(ctx, c, true)
	case "initialize-storage":
		return scope.InitializeStorage(ctx, c, false)
	case "reconcile-storage":
		return scope.InitializeStorage(ctx, c, true)
	case "create-runtime":
		return scope.CreateRuntime(ctx, c, false)
	case "reconcile-runtime":
		return scope.CreateRuntime(ctx, c, true)
	case "start-runtime":
		return scope.ProcessRuntime(ctx, c, "running", false)
	case "stop-runtime":
		return scope.ProcessRuntime(ctx, c, "stopped", false)
	case "restart-runtime":
		return scope.RestartRuntime(ctx, c)
	case "reconcile-process":
		return scope.ProcessRuntime(ctx, c, "", true)
	case "preflight-compose":
		return compose.Preflight(ctx, c)
	case "import-images":
		return scope.Images(ctx, c, false)
	case "reconcile-images":
		return scope.Images(ctx, c, true)
	case "preflight-engine":
		if c.Engine == nil {
			return nil, errors.New("OFFLINE_OPERATOR_ENGINE_CONFIGURATION_REQUIRED")
		}
		client, e := engine.New(*c.Engine)
		if e != nil {
			return nil, e
		}
		defer client.Close()
		return client.Preflight(ctx)
	default:
		return nil, errors.New("OFFLINE_OPERATOR_COMMAND_INVALID")
	}
}

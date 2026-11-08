package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	bootstrap "github.com/Passion-Flow/ownsolid/services/control/internal/operations/bootstrap"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	recovery "github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/storeinitialization"
	"strings"
	"syscall"
	"unicode/utf8"

	"golang.org/x/term"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	lifecyclewire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, safeError(e))
		reportProcessFailure(os.Stderr, e)
		os.Exit(1)
	}
}
func safeError(e error) string {
	v := e.Error()
	if len(v) > 80 || strings.ContainsAny(v, " \n:/=") {
		return "OPERATION_FAILED"
	}
	return v
}
func run() (result error) {
	if len(os.Args) < 2 {
		return errors.New("COMMAND_REQUIRED")
	}
	command := os.Args[1]
	if damengRecoveryCommand(command) {
		return runDamengRecoveryCommand(command, os.Args[2:], os.Stdout)
	}
	if command == postgresRestoreRootsInspectCommand {
		return runPostgresRestoreRootsInspection(command, os.Args[2:], os.Stdout)
	}
	if command == postgresTargetInitializeCommand || command == postgresTargetInspectCommand {
		return runPostgresTargetInitialization(command, os.Args[2:], os.Stdout)
	}
	if command == postgresRestoreCommand || command == postgresRestoreFilesCommand || command == postgresRestoreVerifyCommand {
		return runPostgresRestore(command, os.Args[2:], os.Stdout)
	}
	if command == postgresBackupCommand || command == postgresBackupReadbackCommand || command == postgresBackupVerifyCommand {
		return runPostgresBackup(command, os.Args[2:], os.Stdout)
	}
	if command == postgresMaintenanceCheckCommand {
		return runPostgresMaintenanceCheck(os.Args[2:], os.Stdout)
	}
	if command == agentGatewayCommand {
		return runAgentGateway(os.Args[2:])
	}
	if command == healthProbeCommand {
		return runHealthProbeCommand(os.Args[2:], os.Stdout)
	}
	if command == servingCheckCommand {
		return runServingCheckCommand(os.Args[2:], os.Stdout)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	path := flags.String("config", "", "Configuration file")
	policyEnvironment := flags.String("env-file", "", "Optional protected serving resource-policy environment file")
	bootstrapPacket := flags.String("bootstrap-packet", "", "Protected prepared administrator credential packet")
	bootstrapInput := flags.String("bootstrap-input", "", "Protected versioned administrator request")
	runtimeConfig := flags.String("runtime-config", "", "Protected application configuration for isolated database runtime access")
	restoredContext := flags.String("restore-context", "", "Protected versioned independently bound new-target restore context")
	credentialOutput := flags.String("credential-output", "", "Exclusive protected credential intent; never overwritten")
	ssoKeyringOutput := flags.String("sso-keyring-output", "", "New protected SSO keyring file in an existing private directory; never overwritten")
	ssoKeyringPrevious := flags.String("sso-keyring-previous", "", "Optional protected previous keyring; all old keys are retained")
	backupRoot := flags.String("backup-root", "", "New immutable file/native backup root or explicit restore source")
	isolatedRestore := flags.Bool("confirm-isolated-mysql-restore", false, "Explicitly select only a new isolated MySQL restore target; never production")
	modelStore := flags.String("model-store-root", "", "Existing empty model storage directory")
	objectSnapshot := flags.String("object-snapshot", "", "Existing private immutable artifact snapshot file; never extracted")
	objectScope := flags.String("object-workspace-id", "", "Exact installed object Workspace for independent snapshot readback")
	objectID := flags.String("object-id", "", "Exact stored object identity for independent snapshot readback")
	confirmObjectStage := flags.Bool("confirm-empty-object-stage", false, "Initialize only the configured existing empty protected object staging directory")
	ownerUID := flags.Int("owner-uid", -1, "Explicit storage runtime UID")
	ownerGID := flags.Int("owner-gid", -1, "Explicit storage runtime GID")
	confirmEmpty := flags.Bool("confirm-empty-model-store", false, "Initialize only the explicitly selected existing empty directory")
	datasetStore := flags.String("dataset-store-root", "", "Explicit existing empty dataset store; must equal configured artifact_root")
	confirmDataset := flags.Bool("confirm-empty-dataset-store", false, "Initialize only an explicitly selected existing empty dataset directory")
	trainingStore := flags.String("training-store-root", "", "Explicit existing empty training store; must equal configured artifact_root")
	confirmTraining := flags.Bool("confirm-empty-training-store", false, "Initialize only an explicitly selected existing empty training directory")
	preparationStore := flags.String("preparation-store-root", "", "Explicit existing empty preparation store; must equal configured artifact_root")
	confirmPreparation := flags.Bool("confirm-empty-preparation-store", false, "Initialize only an explicitly selected existing empty preparation directory")
	nodeID := flags.String("node-id", "", "Exact inference node ID to fence")
	receiptFile := flags.String("receipt-file", "", "Protected local root removal receipt")
	challengeFile := flags.String("challenge-file", "", "New protected exact workload cleanup challenge output")
	preparation := flags.String("preparation-id", "", "Exact current model preparation to reconcile")
	trainingJob := flags.String("training-job-id", "", "Exact current training job to reconcile")
	reason := flags.String("reason", "", "Operator reason")
	if e := flags.Parse(os.Args[2:]); e != nil {
		return e
	}
	if e := validateSchemaUpgradeFlags(command, flags, os.Args[2:]); e != nil {
		return e
	}
	if e := validateMySQLNativeFlags(flags, command, *path, *runtimeConfig, *backupRoot, *restoredContext, *reason, *isolatedRestore); e != nil {
		return e
	}
	if e := validatePolicyEnvironmentFlags(command, flags, *policyEnvironment); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("OPERATOR_ARGUMENT_INVALID")
	}
	if command == configuredModelInitializationCommand {
		if e := validateConfiguredModelInitializationFlags(flags, *path, *modelStore, *ownerUID, *ownerGID, *confirmEmpty); e != nil {
			return e
		}
	}
	if command == storeinitialization.CheckModelCommand || command == storeinitialization.RecordModelCommand {
		if e := validateConfiguredModelRecoveryFlags(flags, command, *path, *modelStore, *ownerUID, *ownerGID, *reason); e != nil {
			return e
		}
	}
	if e := validateBootstrapPacketFlags(flags, command, *path, *runtimeConfig, *bootstrapInput, *credentialOutput, *bootstrapPacket); e != nil {
		return e
	}
	if command == bootstrap.PreparePacketCommand {
		if e := bootstrap.PreparePacket(*bootstrapInput, *credentialOutput); e != nil {
			return e
		}
		fmt.Println(bootstrap.PacketCreated)
		return nil
	}
	runtimeContextCommand := schemaUpgradeCommand(command) || command == "prepare-runtime-access" || command == "check-runtime-access" || databaseaccess.MySQLRestoredCommand(command) || command == databaseaccess.ReconcileMySQLCommand || command == databaseaccess.ResumeMySQLCommand || command == databaseaccess.MigrationCommand || command == bootstrap.PrepareMySQLPacketCommand || bootstrap.PacketCommand(command)
	if !runtimeContextCommand && *runtimeConfig != "" {
		return errors.New("RUNTIME_ACCESS_COMMAND_INVALID")
	}
	if runtimeContextCommand && *runtimeConfig == "" {
		return errors.New("RUNTIME_ACCESS_CONFIGURATION_REQUIRED")
	}
	if !databaseaccess.MySQLRestoredCommand(command) && !mysqlNativeRestoreCommand(command) && *restoredContext != "" {
		return errors.New("RUNTIME_ACCESS_RESTORE_COMMAND_INVALID")
	}
	if command == "initialize-sso-keyring" {
		if e := sso.InitializeKeyring(*ssoKeyringOutput, *ssoKeyringPrevious); e != nil {
			return e
		}
		fmt.Println("SSO_PRIVATE_KEYRING_CREATED_CONFIGURATION_UNCHANGED")
		return nil
	}
	if command == "initialize-model-store" {
		if !*confirmEmpty {
			return errors.New("EMPTY_MODEL_STORE_CONFIRMATION_REQUIRED")
		}
		if e := models.InitializeStore(*modelStore, *ownerUID, *ownerGID); e != nil {
			return e
		}
		fmt.Println("EMPTY_MODEL_STORE_INITIALIZED")
		return nil
	}
	if *path == "" {
		return errors.New("CONFIG_REQUIRED")
	}
	var c config.Config
	var e error
	if command == configuredModelInitializationCommand {
		c, e = config.LoadProtected(*path)
	} else if schemaUpgradeCommand(command) {
		c, e = config.LoadProtectedWithEnvironment(*path, *policyEnvironment)
	} else {
		c, e = config.LoadWithEnvironment(*path, *policyEnvironment)
	}
	if e != nil {
		return e
	}
	if schemaUpgradeCommand(command) && c.DatabaseEngine() != config.PostgreSQLDatabaseEngine {
		return errors.New("SCHEMA_UPGRADE_POSTGRESQL_REQUIRED")
	}
	if c.DatabaseEngine() == config.MySQLDatabaseEngine && command != "serve" {
		c, e = config.LoadProtected(*path)
		if e != nil {
			return e
		}
		if c.DatabaseEngine() != config.MySQLDatabaseEngine {
			return errors.New("MYSQL_OPERATOR_CONFIGURATION_CHANGED")
		}
	}
	if command == configuredModelInitializationCommand && c.DatabaseEngine() != config.MySQLDatabaseEngine {
		if e = initializeConfiguredModelStore(c, *modelStore, *ownerUID, *ownerGID, *confirmEmpty); e != nil {
			return e
		}
		fmt.Println("EMPTY_MODEL_STORE_INITIALIZED")
		return nil
	}
	if e = validateVerificationStore(c, command, *modelStore, *datasetStore, *trainingStore, *preparationStore); e != nil {
		return e
	}
	if e = validateSelectedCommand(c, command); e != nil {
		return e
	}
	var runtimeInput databaseaccess.RuntimeInput
	var mysqlRuntimeInput databaseaccess.MySQLRuntimeInput
	if runtimeContextCommand {
		if c.DatabaseEngine() == config.MySQLDatabaseEngine {
			mysqlRuntimeInput, e = databaseaccess.LoadMySQLRuntime(*runtimeConfig)
		} else {
			runtimeInput, e = databaseaccess.LoadRuntimeWithEnvironment(*runtimeConfig, *policyEnvironment)
		}
		if e != nil {
			return e
		}
	}
	if command == databaseaccess.MigrationCommand && c.DatabaseEngine() == config.MySQLDatabaseEngine {
		if e = databaseaccess.ValidateMySQLMigrationContext(c, mysqlRuntimeInput); e != nil {
			return e
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	startup, stop := context.WithTimeout(ctx, config.Seconds(c.Policy().HTTP.StartupSeconds))
	defer stop()
	selected, e := openSelectedDatabase(startup, c, command)
	if e != nil {
		return e
	}
	defer func() { result = errors.Join(result, selected.Close()) }()
	db, lease := selected.postgres, selected.pgLease
	if selected.mysql != nil && mysqlMigrationCommand(command) {
		mode := mysqlmigrate.Apply
		if command == "reconcile-mysql-migrations" {
			mode = mysqlmigrate.Reconcile
		}
		var r mysqlmigrate.Report
		if command == databaseaccess.MigrationCommand {
			r, e = databaseaccess.MigrateMySQLWithRuntimeContext(ctx, selected.mysql, c, selected.catalog, selected.options, mysqlRuntimeInput)
		} else {
			r, e = mysqlmigrate.Run(ctx, selected.mysql, selected.catalog, selected.options, mode, nil)
		}
		if e != nil {
			return e
		}
		if !r.Ready || !r.LockReleased {
			return errors.New("MYSQL_MIGRATION_NOT_VERIFIED")
		}
		fmt.Println("MIGRATION_COMPLETE")
		return nil
	}
	if command == "verify-mysql-installation" {
		if e = selected.VerifyOperator(startup); e != nil {
			return e
		}
		fmt.Println("MYSQL_INSTALLATION_METADATA_VERIFIED")
		return nil
	}
	if schemaUpgradeCommand(command) {
		var status string
		if command == databaseaccess.UpgradeInspectCommand {
			status, e = databaseaccess.InspectUpgrade(startup, db, lease, runtimeInput)
		} else {
			status, e = databaseaccess.Upgrade(startup, db, lease, runtimeInput)
		}
		if e != nil {
			return e
		}
		// Publish no terminal success while the original lease release is unknown.
		if e = selected.Close(); e != nil {
			return e
		}
		fmt.Println(status)
		return nil
	}
	if command == "migrate" || command == databaseaccess.MigrationCommand {
		if command == databaseaccess.MigrationCommand {
			e = databaseaccess.MigrateWithRuntimeContext(startup, db, lease, runtimeInput)
		} else if e = database.Migrate(startup, lease); e != nil {
			e = errors.New("MIGRATION_FAILED")
		}
		if e != nil {
			return e
		}
		fmt.Println("MIGRATION_COMPLETE")
		return nil
	}
	if mysqlNativeRestoreCommand(command) {
		status, e := selected.nativeMySQLCommand(ctx, c, command, *backupRoot, *restoredContext, *reason, *isolatedRestore)
		if e != nil {
			return e
		}
		fmt.Println(status)
		return nil
	}
	if e = selected.Verify(startup); e != nil {
		return e
	}
	if mysqlNativeCommand(command) {
		status, e := selected.nativeMySQLCommand(ctx, c, command, *backupRoot, *restoredContext, *reason, *isolatedRestore)
		if e != nil {
			return e
		}
		fmt.Println(status)
		return nil
	}
	if command == "prepare-runtime-access" || command == "check-runtime-access" || databaseaccess.MySQLRestoredCommand(command) || command == databaseaccess.ReconcileMySQLCommand || command == databaseaccess.ResumeMySQLCommand {
		var status string
		if selected.mysql != nil && databaseaccess.MySQLRestoredCommand(command) {
			status, e = selected.restoredMySQLAccess(ctx, c, mysqlRuntimeInput, *restoredContext, command)
		} else if selected.mysql != nil {
			operator, oe := selected.mysqlOperator(startup, c)
			if oe != nil {
				return oe
			}
			switch command {
			case "prepare-runtime-access":
				status, e = databaseaccess.PrepareMySQL(startup, operator, mysqlRuntimeInput)
			case "check-runtime-access":
				status, e = databaseaccess.CheckMySQL(startup, operator, mysqlRuntimeInput)
			case databaseaccess.ReconcileMySQLCommand:
				status, e = databaseaccess.ReconcileMySQL(startup, operator, mysqlRuntimeInput)
			case databaseaccess.ResumeMySQLCommand:
				status, e = databaseaccess.ResumeMySQL(startup, operator, mysqlRuntimeInput)
			default:
				return errors.New("MYSQL_PRIVILEGED_OPERATOR_COMMAND_UNSUPPORTED")
			}
		} else if command == "prepare-restored-runtime-access" {
			r, e := databaseaccess.LoadRestoredContext(*restoredContext)
			if e != nil {
				return e
			}
			status, e = databaseaccess.PrepareRestored(startup, db, lease, runtimeInput, r)
		} else if command == "prepare-runtime-access" {
			status, e = databaseaccess.Prepare(startup, db, lease, runtimeInput)
		} else {
			status, e = databaseaccess.Check(startup, db, lease, runtimeInput)
		}
		if e != nil {
			return e
		}
		if status != databaseaccess.Verified {
			return errors.New(status)
		}
		fmt.Println(status)
		return nil
	}
	if command == "check-schema" {
		fmt.Println("SCHEMA_CURRENT")
		return nil
	}
	if storeinitialization.MySQLSupported(command) {
		selectedRoot, confirmed := *datasetStore, *confirmDataset
		if strings.Contains(command, "training") {
			selectedRoot, confirmed = *trainingStore, *confirmTraining
		}
		if strings.Contains(command, "preparation") {
			selectedRoot, confirmed = *preparationStore, *confirmPreparation
		}
		if command == storeinitialization.ConfiguredModelCommand || command == storeinitialization.CheckModelCommand || command == storeinitialization.RecordModelCommand {
			selectedRoot, confirmed = *modelStore, *confirmEmpty
		}
		opts := storeinitialization.Options{Command: command, Root: selectedRoot, UID: *ownerUID, GID: *ownerGID, Reason: *reason, ConfirmEmpty: confirmed}
		var status string
		if c.DatabaseEngine() == config.MySQLDatabaseEngine {
			operator, operatorError := selected.mysqlOperator(startup, c)
			if operatorError != nil {
				return operatorError
			}
			status, e = storeinitialization.RunMySQL(startup, operator, c, opts)
		} else {
			status, e = storeinitialization.Run(startup, db, lease, c, opts)
		}
		if e != nil {
			return e
		}
		if status != storeinitialization.Verified {
			return errors.New(status)
		}
		fmt.Println(status)
		return nil
	}
	if command == bootstrap.PrepareMySQLPacketCommand {
		passwords, e := security.NewPasswords(c.Security.PasswordBlocklistFile, c.Policy().Identity)
		if e != nil {
			return e
		}
		service := selected.identity(c)
		service.Passwords = passwords
		operator, e := selected.mysqlOperator(startup, c)
		if e != nil {
			return e
		}
		if e = bootstrap.PrepareMySQLPacket(startup, service, operator, *bootstrapInput, *credentialOutput, mysqlRuntimeInput); e != nil {
			return e
		}
		fmt.Println(bootstrap.MySQLPacketCreated)
		return nil
	}
	if bootstrap.PacketCommand(command) {
		passwords, e := security.NewPasswords(c.Security.PasswordBlocklistFile, c.Policy().Identity)
		if e != nil {
			return e
		}
		service := selected.identity(c)
		service.Passwords = passwords
		var status string
		if selected.mysql != nil {
			operator, oe := selected.mysqlOperator(startup, c)
			if oe != nil {
				return oe
			}
			status, e = bootstrap.RunMySQLPacket(startup, service, operator, command, *bootstrapPacket, mysqlRuntimeInput)
		} else {
			status, e = bootstrap.RunPacket(startup, service, lease, command, *bootstrapPacket, runtimeInput)
		}
		if e != nil {
			return e
		}
		if status != bootstrap.Verified {
			return errors.New(status)
		}
		fmt.Println(status)
		return nil
	}
	if command == "bootstrap-admin-file" || command == "reconcile-bootstrap-file" || command == "resume-bootstrap-file" {
		passwords, e := security.NewPasswords(c.Security.PasswordBlocklistFile, c.Policy().Identity)
		if e != nil {
			return e
		}
		service := selected.identity(c)
		service.Passwords = passwords
		var status string
		if selected.mysql != nil {
			operator, oe := selected.mysqlOperator(startup, c)
			if oe != nil {
				return oe
			}
			status, e = bootstrap.RunMySQL(startup, service, operator, command, *bootstrapInput, *credentialOutput)
		} else {
			status, e = bootstrap.Run(startup, service, lease, command, *bootstrapInput, *credentialOutput)
		}
		if e != nil {
			return e
		}
		if status != bootstrap.Verified {
			return errors.New(status)
		}
		fmt.Println(status)
		return nil
	}
	hmac, e := config.Secret(c.Security.AuthHMACKeyFile)
	if e != nil || len(hmac) < security.MinimumHMACKeyBytes || len(hmac) > security.MaximumHMACKeyBytes {
		return errors.New("AUTH_KEY_INVALID")
	}
	passwords, e := security.NewPasswords(c.Security.PasswordBlocklistFile, c.Policy().Identity)
	if e != nil {
		return e
	}
	keys, e := mail.LoadKeyring(c.Security.MailKeyringFile)
	if e != nil {
		return e
	}
	locales, e := localization.New(c.Localization, c.Policy().Localization)
	if e != nil {
		return e
	}
	templates, e := mail.LoadTemplatesWithRegistry(locales)
	if e != nil {
		return errors.New("MAIL_TEMPLATES_INVALID")
	}
	outbox := &mail.Outbox{DB: db, Keys: keys, Config: c, Templates: templates}
	if selected.mysql != nil {
		outbox.Storage = selected.store
	}
	service := selected.identity(c)
	service.Locales = locales
	service.Passwords = passwords
	service.Mail = outbox
	service.HMACKey = hmac
	switch command {
	case "export-training-files", "import-training-files":
		if !training.ValidOperatorReason(*reason) {
			return errors.New("OPERATOR_REASON_REQUIRED")
		}
		if !c.Training.Enabled() || *backupRoot == "" {
			return errors.New("TRAINING_STORAGE_REQUIRED")
		}
		lookup, stopLookup := context.WithTimeout(ctx, config.Seconds(c.Policy().HTTP.RequestSeconds))
		tenant, lookupError := installedTenant(lookup, selected.store)
		stopLookup()
		if lookupError != nil {
			return errors.New("TRAINING_INSTALLATION_REQUIRED")
		}
		trainingService := &training.Service{DB: db, Identity: service}
		operation := "export"
		if command == "import-training-files" {
			operation = "import"
			if !*confirmTraining || *trainingStore != c.Training.ArtifactRoot {
				return errors.New("TRAINING_EMPTY_STORE_CONFIRMATION_REQUIRED")
			}
			if e = trainingService.ImportFiles(ctx, *backupRoot, c.Training.ArtifactRoot); e != nil {
				return e
			}
		}
		store, e := training.OpenStore(c.Training.ArtifactRoot, tenant, c.Policy().Training.MinimumFreeBytes, trainingwire.MaximumCheckpointBytes, trainingwire.MaximumCheckpointFiles)
		if e != nil {
			return e
		}
		defer store.Close()
		trainingService.Store = store
		if operation == "export" {
			if e = trainingService.ExportFiles(ctx, *backupRoot); e != nil {
				return e
			}
		}
		if e = trainingService.AuditStoreOperation(ctx, operation, *reason); e != nil {
			return e
		}
		fmt.Println("TRAINING_FILES_" + strings.ToUpper(operation) + "_VERIFIED")
		return nil
	case "export-preparation-files", "import-preparation-files":
		if !modelpreparations.ValidOperatorReason(*reason) {
			return errors.New("OPERATOR_REASON_REQUIRED")
		}
		if !c.ModelPreparation.Enabled() || *backupRoot == "" {
			return errors.New("MODEL_PREPARATION_STORAGE_REQUIRED")
		}
		lookup, stopLookup := context.WithTimeout(ctx, config.Seconds(c.Policy().HTTP.RequestSeconds))
		tenant, lookupError := installedTenant(lookup, selected.store)
		stopLookup()
		if lookupError != nil {
			return errors.New("MODEL_PREPARATION_INSTALLATION_REQUIRED")
		}
		preparationService := &modelpreparations.Service{DB: db, Identity: service}
		operation := "export"
		if command == "import-preparation-files" {
			operation = "import"
			if !*confirmPreparation || *preparationStore != c.ModelPreparation.ArtifactRoot {
				return errors.New("EMPTY_PREPARATION_STORE_CONFIRMATION_REQUIRED")
			}
			if e = preparationService.ImportFiles(ctx, *backupRoot, c.ModelPreparation.ArtifactRoot); e != nil {
				return e
			}
		}
		store, e := modelpreparations.OpenStore(c.ModelPreparation.ArtifactRoot, tenant, c.Policy().ModelLifecycle.MinimumFreeBytes, lifecyclewire.MaximumModelBytes, lifecyclewire.MaximumModelFiles)
		if e != nil {
			return e
		}
		defer store.Close()
		preparationService.Store = store
		if operation == "export" {
			if e = preparationService.ExportFiles(ctx, *backupRoot); e != nil {
				return e
			}
		}
		if e = preparationService.AuditStoreOperation(ctx, operation, *reason); e != nil {
			return e
		}
		fmt.Println("PREPARATION_FILES_" + strings.ToUpper(operation) + "_VERIFIED")
		return nil
	case "verify-preparation-files":
		if !modelpreparations.ValidOperatorReason(*reason) {
			return errors.New("OPERATOR_REASON_REQUIRED")
		}
		if !c.ModelPreparation.Enabled() {
			return errors.New("MODEL_PREPARATION_STORAGE_REQUIRED")
		}
		lookup, stopLookup := context.WithTimeout(ctx, config.Seconds(c.Policy().HTTP.RequestSeconds))
		tenant, lookupError := installedTenant(lookup, selected.store)
		stopLookup()
		if lookupError != nil {
			return errors.New("MODEL_PREPARATION_INSTALLATION_REQUIRED")
		}
		store, e := modelpreparations.OpenStore(c.ModelPreparation.ArtifactRoot, tenant, c.Policy().ModelLifecycle.MinimumFreeBytes, lifecyclewire.MaximumModelBytes, lifecyclewire.MaximumModelFiles)
		if e != nil {
			return e
		}
		defer store.Close()
		preparationService := &modelpreparations.Service{DB: db, Identity: service, Store: store}
		if e = preparationService.AuditStoreOperation(ctx, "verify", *reason); e != nil {
			return e
		}
		fmt.Println("PREPARATION_FILES_VERIFIED")
		return nil
	case "verify-training-files":
		if !training.ValidOperatorReason(*reason) {
			return errors.New("OPERATOR_REASON_REQUIRED")
		}
		lookup, stopLookup := context.WithTimeout(ctx, config.Seconds(c.Policy().HTTP.RequestSeconds))
		tenant, lookupError := installedTenant(lookup, selected.store)
		stopLookup()
		if lookupError != nil {
			return errors.New("TRAINING_INSTALLATION_REQUIRED")
		}
		store, e := training.OpenStore(c.Training.ArtifactRoot, tenant, c.Policy().Training.MinimumFreeBytes, trainingwire.MaximumCheckpointBytes, trainingwire.MaximumCheckpointFiles)
		if e != nil {
			return e
		}
		defer store.Close()
		trainingService := &training.Service{DB: db, Identity: service, Store: store}
		if e = trainingService.AuditStoreOperation(ctx, "verify", *reason); e != nil {
			return e
		}
		fmt.Println("TRAINING_FILES_VERIFIED")
		return nil
	case "export-dataset-files", "import-dataset-files":
		if strings.TrimSpace(*reason) == "" || !utf8.ValidString(*reason) || len([]rune(*reason)) > 500 {
			return errors.New("OPERATOR_REASON_REQUIRED")
		}
		data := &datasets.Service{DB: db, Identity: service}
		if command == "export-dataset-files" {
			if e = data.OpenConfiguredStore(startup); e != nil {
				return e
			}
			if data.Store != nil {
				defer data.Store.Close()
			}
			e = data.ExportFiles(ctx, *backupRoot)
		} else {
			e = data.ImportFiles(ctx, *backupRoot, c.Datasets.ArtifactRoot)
		}
		if e != nil {
			return e
		}
		operation := "export"
		if command == "import-dataset-files" {
			operation = "import"
		}
		if e = data.AuditFileOperation(ctx, operation, *reason); e != nil {
			return e
		}
		fmt.Println("DATASET_FILE_OPERATION_COMPLETE")
		return nil
	case "verify-dataset-store":
		data := &datasets.Service{DB: db, Identity: service}
		if e = data.OpenConfiguredStore(startup); e != nil {
			return e
		}
		if data.Store != nil {
			defer data.Store.Close()
		}
		fmt.Println("DATASET_REFERENCED_FILES_VERIFIED")
		return nil
	case "bootstrap-admin":
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("INTERACTIVE_TERMINAL_REQUIRED")
		}
		reader := bufio.NewReader(os.Stdin)
		ask := func(label string) string {
			fmt.Print(label + ": ")
			line, _ := reader.ReadString('\n')
			return strings.TrimSpace(line)
		}
		email := ask("Administrator email")
		name := ask("Display name")
		tenant := ask("Installation name")
		workspace := ask("Workspace name")
		password, e := readPassword()
		if e != nil {
			return e
		}
		if e = service.Bootstrap(ctx, email, name, password, tenant, workspace); e != nil {
			return e
		}
		fmt.Println("BOOTSTRAP_COMPLETE")
		return nil
	case "recover-admin":
		if strings.TrimSpace(*reason) == "" || len([]rune(*reason)) > 500 {
			return errors.New("OPERATOR_REASON_REQUIRED")
		}
		password, e := readPassword()
		if e != nil {
			return e
		}
		if e = service.RecoverAdmin(ctx, password, *reason); e != nil {
			return e
		}
		fmt.Println("ADMIN_RECOVERY_COMPLETE")
		return nil
	case "invalidate-restored-auth":
		if strings.TrimSpace(*reason) == "" || len([]rune(*reason)) > 500 {
			return errors.New("OPERATOR_REASON_REQUIRED")
		}
		if e = recovery.InvalidateRestored(ctx, service, *reason); e != nil {
			return e
		}
		fmt.Println("RESTORED_AUTH_INVALIDATED")
		return nil
	case "fence-inference-node":
		if e = recovery.FenceInferenceNode(ctx, service, *nodeID, *reason); e != nil {
			return e
		}
		fmt.Println("INFERENCE_NODE_FENCED_OWNERSHIP_RETAINED")
		return nil
	case "reconcile-inference-removal":
		receipt, e := recovery.LoadInferenceReceipt(*receiptFile)
		if e != nil {
			return e
		}
		if e = recovery.ReconcileInferenceRemoval(ctx, service, receipt, *reason); e != nil {
			return e
		}
		fmt.Println("INFERENCE_OWNED_REMOVAL_RECONCILED")
		return nil
	case "prepare-preparation-removal":
		if e = recovery.PreparePreparationRemovalFile(ctx, service, *nodeID, *preparation, *reason, *challengeFile); e != nil {
			return e
		}
		fmt.Println("MODEL_PREPARATION_NODE_FENCED_CHALLENGE_WRITTEN_OWNERSHIP_RETAINED")
		return nil
	case "reconcile-preparation-removal":
		receipt, e := recovery.LoadPreparationRemovalReceipt(*receiptFile)
		if e != nil {
			return e
		}
		if e = recovery.ReconcilePreparationRemoval(ctx, service, receipt, *reason); e != nil {
			return e
		}
		fmt.Println("MODEL_PREPARATION_OWNED_REMOVAL_RECONCILED")
		return nil
	case "prepare-training-removal":
		if e = recovery.PrepareTrainingRemovalFile(ctx, service, *nodeID, *trainingJob, *reason, *challengeFile); e != nil {
			return e
		}
		fmt.Println("TRAINING_NODE_FENCED_CHALLENGE_WRITTEN_OWNERSHIP_RETAINED")
		return nil
	case "reconcile-training-removal":
		receipt, e := recovery.LoadTrainingRemovalReceipt(*receiptFile)
		if e != nil {
			return e
		}
		if e = recovery.ReconcileTrainingRemoval(ctx, service, receipt, *reason); e != nil {
			return e
		}
		fmt.Println("TRAINING_OWNED_REMOVAL_RECONCILED")
		return nil
	case "export-model-files", "import-model-files", "verify-model-store":
		if command != "verify-model-store" && (!utf8.ValidString(*reason) || strings.TrimSpace(*reason) == "" || len([]rune(*reason)) > 500) {
			return errors.New("OPERATOR_REASON_REQUIRED")
		}
		modelService := &models.Service{DB: db, Identity: service, RootDirectory: c.Models.ArtifactRoot}
		switch command {
		case "export-model-files":
			e = modelService.ExportFiles(ctx, *backupRoot)
		case "import-model-files":
			e = modelService.ImportFiles(ctx, *backupRoot)
		default:
			e = modelService.VerifyStoredVersions(ctx)
		}
		if e != nil {
			return e
		}
		if command != "verify-model-store" {
			operation := "export"
			if command == "import-model-files" {
				operation = "import"
			}
			if e = modelService.AuditFileOperation(ctx, operation, *reason); e != nil {
				return e
			}
		}
		fmt.Println("MODEL_FILE_OPERATION_COMPLETE")
		return nil
	case "initialize-object-stage":
		if c.ObjectStorage == nil || !c.ObjectStorage.Enabled {
			return errors.New("OBJECT_NOT_ADMITTED")
		}
		if !*confirmObjectStage {
			return errors.New("OBJECT_STAGE_CONFIRMATION_REQUIRED")
		}
		if !validArtifactOperatorReason(*reason) {
			return errors.New("OPERATOR_REASON_REQUIRED")
		}
		if e = artifactSchemaAdmission(c, selected.store); e != nil {
			return e
		}
		if e = recovery.InitializeObjectStage(startup, service, *reason); e != nil {
			return e
		}
		fmt.Println("OBJECT_STAGE_INITIALIZED")
		return nil
	case "verify-object-snapshot":
		if c.ObjectStorage == nil || !c.ObjectStorage.Enabled {
			return errors.New("OBJECT_NOT_ADMITTED")
		}
		bounded, cancel := context.WithTimeout(ctx, config.Seconds(c.ObjectStorage.TransferSeconds))
		defer cancel()
		if e = recovery.VerifyArtifactSnapshot(bounded, service, *objectScope, *objectID, *objectSnapshot, *reason); e != nil {
			return e
		}
		fmt.Println("OBJECT_SNAPSHOT_VERIFIED")
		return nil
	case "verify-object-store":
		if e = verifyArtifactStore(startup, c, selected.store, identityhttp.New(service)); e != nil {
			return e
		}
		fmt.Println("OBJECT_STORE_VERIFIED")
		return nil
	case "serve":
		return serveSelected(ctx, startup, c, selected, service, outbox)
	default:
		return errors.New("UNKNOWN_COMMAND")
	}
}
func readPassword() (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("INTERACTIVE_TERMINAL_REQUIRED")
	}
	fmt.Print("Password: ")
	a, e := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if e != nil {
		return "", e
	}
	fmt.Print("Confirm password: ")
	b, e := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if e != nil {
		return "", e
	}
	if string(a) != string(b) {
		return "", errors.New("PASSWORD_CONFIRMATION_MISMATCH")
	}
	return string(a), nil
}

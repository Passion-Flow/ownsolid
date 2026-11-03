//go:build cgo && odbc_manager

package damengbackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	accountidentity "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresbackup"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type OperatorResult struct {
	FormatVersion int    `json:"format_version"`
	OperationID   string `json:"operation_id"`
	Status        string `json:"status"`
	ProofSHA256   string `json:"proof_sha256"`
}
type operatorIntent struct {
	FormatVersion        int                     `json:"format_version"`
	Request              OperatorRequest         `json:"request"`
	RequestSHA256        string                  `json:"request_sha256"`
	EffectiveSHA256      string                  `json:"effective_sha256"`
	Configuration        ConfigurationProjection `json:"configuration"`
	SourceCompleteSHA256 string                  `json:"source_complete_sha256"`
}
type operatorComplete struct {
	FormatVersion        int                     `json:"format_version"`
	IntentSHA256         string                  `json:"intent_sha256"`
	DatabaseSHA256       string                  `json:"database_sha256"`
	StoresSHA256         string                  `json:"stores_sha256"`
	Configuration        ConfigurationProjection `json:"configuration"`
	AuthoritySHA256      string                  `json:"authority_sha256"`
	InitialProfileSHA256 string                  `json:"initial_profile_sha256"`
}
type operatorInputs struct {
	request     OperatorRequest
	context     NativeContextConfiguration
	recovery    Configuration
	connector   dameng.ConnectionConfiguration
	schema      dameng.SchemaConfiguration
	native      *Native
	kernel      *dameng.Store
	ownerOpened bool
}

func loadOperatorInputs(ctx context.Context, r OperatorRequest) (operatorInputs, error) {
	var out operatorInputs
	out.request = r
	c, e := LoadNativeContext(r.NativeContextFile, r.NativeEnvironmentFile)
	if e != nil {
		return out, e
	}
	out.context = c
	out.recovery, e = LoadConfiguration(r.RecoveryConfigurationFile, r.RecoveryEnvironmentFile)
	if e != nil {
		return out, e
	}
	out.connector, e = dameng.LoadConnectionConfiguration(c.ConnectorConfigurationFile, "")
	if e != nil {
		return out, fail("OWNER_CONNECTOR_INPUT_INVALID")
	}
	out.schema, e = dameng.LoadSchemaConfiguration(c.SchemaConfigurationFile, "")
	if e != nil {
		return out, fail("OWNER_SCHEMA_INPUT_INVALID")
	}
	if withinRoot(r.ConfigurationRoot, out.connector.PasswordFile) || withinRoot(r.ConfigurationRoot, c.ConnectorConfigurationFile) || out.connector.Policy.MaximumOpenConnections < 2 || out.connector.Policy.MaximumPhysicalConnections < 2 {
		return out, fail("SEPARATE_OWNER_CONFIGURATION_REQUIRED")
	}
	if validatePlatformInputRoots(r, c, out.recovery) != nil {
		return out, fail("OPERATOR_ROOTS_OVERLAP")
	}
	return out, nil
}
func validatePlatformInputRoots(r OperatorRequest, c NativeContextConfiguration, n Configuration) error {
	for _, p := range []string{r.NativeContextFile, c.ConnectorConfigurationFile, c.SchemaConfigurationFile, n.InventoryFile, n.Connection.PasswordFile, n.Connection.SocketFile, r.RecoveryConfigurationFile} {
		if related(r.OutputRoot, p) {
			return fail("OPERATOR_ROOTS_OVERLAP")
		}
	}
	return nil
}
func loadOperatorApplication(r OperatorRequest) (config.Config, error) {
	raw, e := ReadOperatorApplication(r.ApplicationConfigurationFile)
	if e != nil {
		return config.Config{}, fail("APPLICATION_PRIVATE_SNAPSHOT_REQUIRED")
	}
	if e = admitApplicationResolution(raw, r); e != nil {
		return config.Config{}, e
	}
	if r.ApplicationEnvironmentFile != "" {
		c, e := config.LoadProtectedWithEnvironment(r.ApplicationConfigurationFile, r.ApplicationEnvironmentFile)
		if e != nil {
			return config.Config{}, fail("APPLICATION_CONFIGURATION_INVALID")
		}
		return c, nil
	}
	c, e := config.Decode(raw)
	if e != nil {
		return config.Config{}, fail("APPLICATION_CONFIGURATION_INVALID")
	}
	return c, nil
}
func (i *operatorInputs) closeOwner() error {
	if i.kernel == nil {
		return nil
	}
	cleanup, stop := context.WithTimeout(context.Background(), time.Duration(i.connector.CleanupSeconds)*time.Second)
	defer stop()
	status, e := i.kernel.Close(cleanup)
	if e != nil || status.State != "closed" || status.PhysicalOwners != 0 || status.CleanupJobs != 0 || status.MemoryLeases != 0 || status.RetainedBytes != 0 {
		return fail("OWNER_KERNEL_CLEANUP_PENDING")
	}
	return nil
}
func (i *operatorInputs) withOwner(ctx context.Context, application config.Config, mode MaintenanceMode, operation func(context.Context, *NativeCustody, *accountidentity.Service) error) error {
	// One invocation retains exactly one kernel, including failed-open ownership.
	// A new custody borrows that same kernel after COMMIT; no alternate pool exists.
	if !i.ownerOpened {
		i.ownerOpened = true
		var e error
		i.kernel, _, e = dameng.Open(ctx, i.connector)
		if e != nil {
			return fail("OWNER_KERNEL_OPEN_FAILED")
		}
	}
	if i.kernel == nil {
		return fail("OWNER_KERNEL_REQUIRED")
	}
	store, e := transaction.FromDameng(i.kernel)
	if e != nil {
		return fail("OWNER_REPOSITORY_BINDING_FAILED")
	}
	accounts := &accountidentity.Service{Storage: store, Config: application}
	return WithNativeCustody(ctx, i.native, i.kernel, i.connector, i.schema, i.context.Snapshot, mode, func(ctx context.Context, c *NativeCustody) error { return operation(ctx, c, accounts) })
}
func readOperatorComplete(ctx context.Context, n *Native, rootPath string, p SnapshotPolicy) (operatorIntent, operatorComplete, string, error) {
	var intent operatorIntent
	var complete operatorComplete
	root, e := openPrivateRoot(rootPath)
	if e != nil {
		return intent, complete, "", e
	}
	defer root.Close()
	raw, e := readDocument(root, "operator.intent.json", &intent)
	if e != nil || intent.FormatVersion != 1 || intent.Request.OutputRoot != rootPath || intent.RequestSHA256 != fingerprint(intent.Request) || intent.Request.Validate() != nil {
		return intent, complete, "", fail("ORIGINAL_OPERATOR_INTENT_REQUIRED")
	}
	proof, e := readDocument(root, "operator.complete.json", &complete)
	if e != nil || complete.FormatVersion != 1 || complete.IntentSHA256 != sha(raw) {
		return intent, complete, "", fail("ORIGINAL_OPERATOR_COMPLETION_REQUIRED")
	}
	dbRoot, e := openPrivateRoot(filepath.Join(rootPath, "database"))
	if e != nil {
		return intent, complete, "", e
	}
	defer dbRoot.Close()
	var databaseIntent Intent
	if _, e = readDocument(dbRoot, "intent.json", &databaseIntent); e != nil || databaseIntent.Kind != "backup" || databaseIntent.Root != filepath.Join(rootPath, "database") {
		return intent, complete, "", fail("DATABASE_COMPONENT_CHANGED")
	}
	database, dbRaw, e := n.readComplete(dbRoot, databaseIntent)
	if e != nil || sha(dbRaw) != complete.DatabaseSHA256 {
		return intent, complete, "", fail("DATABASE_COMPONENT_CHANGED")
	}
	if _, _, e = readArchive(ctx, dbRoot, n.configuration.Policy.MaximumArchiveBytes, database.ArchiveSHA256); e != nil {
		return intent, complete, "", e
	}
	if e = readInitialProfileProof(root, rootPath, complete, database); e != nil {
		return intent, complete, "", e
	}
	if !reflect.DeepEqual(complete.Configuration, intent.Configuration) || complete.Configuration.Root != intent.Request.ConfigurationRoot || complete.Configuration.EffectiveSHA256 != intent.EffectiveSHA256 || !reflect.DeepEqual(complete.Configuration.Images, intent.Request.ImageRoots) {
		return intent, complete, "", fail("CONFIGURATION_COMPONENT_CHANGED")
	}
	storeRoot, e := openPrivateRoot(filepath.Join(rootPath, "stores"))
	if e != nil {
		return intent, complete, "", e
	}
	defer storeRoot.Close()
	if intent.Request.Kind == "backup" {
		var matched MatchedStores
		rawStores, e := readDocument(storeRoot, "matched.complete.json", &matched)
		if e != nil || sha(rawStores) != complete.StoresSHA256 || matched.DatabaseCompleteSHA256 != complete.DatabaseSHA256 || matched.Database != database.Database || matched.ConfigurationSHA256 != intent.EffectiveSHA256 {
			return intent, complete, "", fail("STORE_COMPONENT_CHANGED")
		}
		if len(matched.Snapshots) != 4 {
			return intent, complete, "", fail("STORE_COMPONENT_CHANGED")
		}
		for index, binding := range matched.Snapshots {
			path := filepath.Join(rootPath, "stores", storeKinds[index])
			if binding.Path != path {
				return intent, complete, "", fail("STORE_COMPONENT_CHANGED")
			}
			tree, e := postgresbackup.TreeDigest(ctx, path, p.MaximumTreeBytes, p.MaximumTreeFiles, p.MaximumTreeMetadataBytes)
			if e != nil || tree.SHA256 != binding.SHA256 || tree.Bytes != binding.Bytes || tree.Files != binding.Files {
				return intent, complete, "", fail("STORE_COMPONENT_CHANGED")
			}
		}
	}
	cfg, e := postgresbackup.TreeDigest(ctx, filepath.Join(rootPath, "configuration"), p.MaximumTreeBytes, p.MaximumTreeFiles, p.MaximumTreeMetadataBytes)
	if e != nil || cfg != complete.Configuration.Tree {
		return intent, complete, "", fail("CONFIGURATION_COMPONENT_CHANGED")
	}
	return intent, complete, sha(proof), nil
}

// RunOperator composes existing source/fresh restore owners. Read-only verification
// never dispatches import or invalidation. Unknown completion remains isolated.
func RunOperator(ctx context.Context, command string, r OperatorRequest) (result OperatorResult, resultError error) {
	empty := OperatorResult{}
	if ctx == nil || r.Validate() != nil || !OperatorCommand(command) || RefuseOperatorAmbient() != nil {
		return empty, fail("OPERATOR_INPUT_INVALID")
	}
	restore := command == RestoreCommand || command == VerifyRestoreCommand
	if restore != (r.Kind == "restore") {
		return empty, fail("OPERATOR_KIND_MISMATCH")
	}
	inputs, e := loadOperatorInputs(ctx, r)
	if e != nil {
		return empty, e
	}
	defer func() {
		if e := inputs.closeOwner(); e != nil {
			result = empty
			resultError = errors.Join(resultError, e)
		}
	}()
	ctx, stop := context.WithTimeout(ctx, time.Duration(inputs.recovery.Policy.OperationSeconds)*time.Second)
	defer stop()
	if command == VerifyBackupCommand {
		inputs.native, e = NewNativeContext(ctx, inputs.recovery, inputs.connector)
		if e != nil {
			return empty, e
		}
		intent, _, proof, e := readOperatorComplete(ctx, inputs.native, r.OutputRoot, inputs.context.Snapshot)
		if e != nil || intent.RequestSHA256 != fingerprint(r) {
			return empty, fail("ORIGINAL_OPERATOR_REQUEST_CHANGED")
		}
		return OperatorResult{1, r.OperationID, "matched_backup_verified", proof}, nil
	}
	var preparedRoot *os.Root
	sourceCompletionSHA := ""
	// Restore admits the original source configuration before target SQL. Copy
	// preserves exact absolute configuration references; new backing is explicit.
	if command == RestoreCommand {
		inputs.native, e = NewNativeContext(ctx, inputs.recovery, inputs.connector)
		if e != nil {
			return empty, e
		}
		source, proof, originalCompletionSHA, e := readOperatorComplete(ctx, inputs.native, r.SourceRoot, inputs.context.Snapshot)
		if e != nil {
			return empty, e
		}
		if source.Request.Kind != "backup" || source.Request.ConfigurationRoot != r.ConfigurationRoot || source.Request.ApplicationConfigurationFile != r.ApplicationConfigurationFile || source.Request.ApplicationEnvironmentFile != r.ApplicationEnvironmentFile || !reflect.DeepEqual(source.Request.ImageRoots, r.ImageRoots) {
			return empty, fail("ORIGINAL_CONFIGURATION_PATH_BINDING_REQUIRED")
		}
		preparedRoot, e = newRoot(r.OutputRoot)
		if e != nil {
			return empty, e
		}
		defer preparedRoot.Close()
		sourceCompletionSHA = originalCompletionSHA
		original := operatorIntent{1, r, fingerprint(r), proof.Configuration.EffectiveSHA256, proof.Configuration, sourceCompletionSHA}
		if e = writeNew(preparedRoot, "operator.intent.json", jsonBytes(original)); e != nil {
			return empty, e
		}
		if _, e = copyConfigurationProjection(ctx, filepath.Join(r.SourceRoot, "configuration"), r.ConfigurationRoot, inputs.context.Snapshot, inputs.recovery.Policy.MinimumFreeBytes); e != nil {
			return empty, e
		}
		// Configuration bytes are preserved, not rewritten into a different address,
		// credential authority, keyring or callback/origin profile.
		if proof.Configuration.Root != r.ConfigurationRoot {
			return empty, fail("CONFIGURATION_ROOT_CHANGED")
		}
	}
	application, e := loadOperatorApplication(r)
	if e != nil {
		return empty, e
	}
	if validatePlatformRoots(application, r.ConfigurationRoot, r.OutputRoot) != nil {
		return empty, fail("OPERATOR_ROOTS_OVERLAP")
	}
	projection, e := projectionReadback(ctx, application, r, inputs.context.Snapshot)
	if e != nil {
		return empty, e
	}
	if e = inputs.validateRuntimeProjection(application); e != nil {
		return empty, e
	}
	if inputs.native == nil {
		inputs.native, e = NewNativeContext(ctx, inputs.recovery, inputs.connector)
		if e != nil {
			return empty, e
		}
	}

	if command == VerifyRestoreCommand {
		return verifyRestoredOperator(ctx, &inputs, application, r, projection)
	}
	root := preparedRoot
	if root == nil {
		root, e = newRoot(r.OutputRoot)
		if e != nil {
			return empty, e
		}
		defer root.Close()
	}
	intent := operatorIntent{1, r, fingerprint(r), nativePlatformConfigurationSHA256(application), projection, sourceCompletionSHA}
	if preparedRoot == nil {
		if e = writeNew(root, "operator.intent.json", jsonBytes(intent)); e != nil {
			return empty, e
		}
	} else {
		var original operatorIntent
		if _, e = readDocument(root, "operator.intent.json", &original); e != nil || !reflect.DeepEqual(original, intent) {
			return empty, fail("RESTORED_EFFECTIVE_CONFIGURATION_CHANGED")
		}
	}
	databaseRoot, storesRoot := filepath.Join(r.OutputRoot, "database"), filepath.Join(r.OutputRoot, "stores")
	if r.Kind == "backup" {
		e = inputs.withOwner(ctx, application, SourceMaintenance, func(ctx context.Context, c *NativeCustody, accounts *accountidentity.Service) error {
			if e := checkInitialNativeProfile(ctx, c); e != nil {
				return e
			}
			if _, e := inputs.native.Backup(ctx, databaseRoot, r.Reason, c); e != nil {
				return e
			}
			if _, e := CaptureMatchedStores(ctx, c, accounts, application, databaseRoot, storesRoot); e != nil {
				return e
			}
			if _, e := copyConfigurationProjection(ctx, r.ConfigurationRoot, filepath.Join(r.OutputRoot, "configuration"), inputs.context.Snapshot, inputs.recovery.Policy.MinimumFreeBytes); e != nil {
				return e
			}
			after, e := projectionReadback(ctx, application, r, inputs.context.Snapshot)
			if e != nil || !reflect.DeepEqual(after, projection) {
				return fail("CONFIGURATION_CHANGED_DURING_BACKUP")
			}
			if e = checkInitialNativeProfile(ctx, c); e != nil {
				return e
			}
			database, completeSHA, e := completeAt(ctx, inputs.native, databaseRoot)
			if e != nil {
				return e
			}
			profile := initialProfileProof{FormatVersion: 1, Profile: initialNativeProfile, Root: r.OutputRoot, DatabaseCompleteSHA256: completeSHA, RowsSHA256: database.Database.RowsSHA256, ServerID: database.Database.ServerID, EndpointSHA256: database.Database.EndpointSHA256, Tables: initialNativeProfileTables(), BeforeExport: true, AfterCapture: true}
			if e = writeNew(root, "profile.absent.json", jsonBytes(profile)); e != nil {
				return e
			}
			return c.CheckFence(ctx)
		})
		if e != nil {
			return empty, e
		}
		complete := operatorComplete{FormatVersion: 1, IntentSHA256: fingerprint(intent), Configuration: projection}
		_, complete.DatabaseSHA256, e = completeAt(ctx, inputs.native, databaseRoot)
		if e != nil {
			return empty, e
		}
		stores, e := openPrivateRoot(storesRoot)
		if e != nil {
			return empty, e
		}
		var matched MatchedStores
		raw, e := readDocument(stores, "matched.complete.json", &matched)
		stores.Close()
		if e != nil {
			return empty, e
		}
		complete.StoresSHA256 = sha(raw)
		var profile initialProfileProof
		profileRaw, e := readDocument(root, "profile.absent.json", &profile)
		if e != nil {
			return empty, e
		}
		complete.InitialProfileSHA256 = sha(profileRaw)
		if e = writeNew(root, "operator.complete.json", jsonBytes(complete)); e != nil {
			return empty, e
		}
		return OperatorResult{1, r.OperationID, "matched_backup_complete", fingerprint(complete)}, nil
	}
	e = inputs.withOwner(ctx, application, FreshRestoreMaintenance, func(ctx context.Context, c *NativeCustody, accounts *accountidentity.Service) error {
		if _, e := inputs.native.Restore(ctx, filepath.Join(r.SourceRoot, "database"), databaseRoot, r.Reason, c); e != nil {
			return e
		}
		if e := checkInitialNativeProfile(ctx, c); e != nil {
			return e
		}
		if _, e := RestoreMatchedStores(ctx, c, accounts, application, databaseRoot, filepath.Join(r.SourceRoot, "stores"), storesRoot); e != nil {
			return e
		}
		_, e := PrepareRestoredAuthority(ctx, c, accounts, application, databaseRoot, storesRoot, filepath.Join(r.OutputRoot, "authority"), r.OperationID)
		return e
	})
	if e != nil {
		return empty, e
	}
	return verifyRestoredOperator(ctx, &inputs, application, r, projection)
}
func verifyRestoredOperator(ctx context.Context, i *operatorInputs, application config.Config, r OperatorRequest, projection ConfigurationProjection) (OperatorResult, error) {
	var result OperatorResult
	root, e := openPrivateRoot(r.OutputRoot)
	if e != nil {
		return result, e
	}
	defer root.Close()
	var intent operatorIntent
	raw, e := readDocument(root, "operator.intent.json", &intent)
	if e != nil || intent.RequestSHA256 != fingerprint(r) || intent.FormatVersion != 1 || intent.RequestSHA256 != fingerprint(intent.Request) || !reflect.DeepEqual(intent.Request, r) || intent.EffectiveSHA256 != nativePlatformConfigurationSHA256(application) || !reflect.DeepEqual(intent.Configuration, projection) {
		return result, fail("ORIGINAL_OPERATOR_INTENT_CHANGED")
	}
	source, sourceProof, sourceSHA, e := readOperatorComplete(ctx, i.native, r.SourceRoot, i.context.Snapshot)
	if e != nil || source.Request.Kind != "backup" || sourceSHA != intent.SourceCompleteSHA256 || sourceProof.Configuration.Root != projection.Root || !reflect.DeepEqual(sourceProof.Configuration, projection) {
		return result, fail("ORIGINAL_INITIAL_PROFILE_REQUIRED")
	}
	var authority RestoredAuthority
	e = i.withOwner(ctx, application, FreshRestoreMaintenance, func(ctx context.Context, c *NativeCustody, accounts *accountidentity.Service) error {
		if e := checkInitialNativeProfile(ctx, c); e != nil {
			return e
		}
		var e error
		authority, e = VerifyCommittedAuthority(ctx, c, accounts, application, filepath.Join(r.OutputRoot, "authority"))
		return e
	})
	if e != nil {
		return result, e
	}
	after, e := projectionReadback(ctx, application, r, i.context.Snapshot)
	if e != nil || !reflect.DeepEqual(after, projection) {
		return result, fail("CONFIGURATION_CHANGED_DURING_RESTORE_VERIFICATION")
	}
	complete := operatorComplete{FormatVersion: 1, IntentSHA256: sha(raw), Configuration: projection, AuthoritySHA256: fingerprint(authority), InitialProfileSHA256: sourceProof.InitialProfileSHA256}
	_, complete.DatabaseSHA256, e = completeAt(ctx, i.native, filepath.Join(r.OutputRoot, "database"))
	if e != nil {
		return result, e
	}
	files, e := openPrivateRoot(filepath.Join(r.OutputRoot, "stores"))
	if e != nil {
		return result, e
	}
	var restored RestoredStores
	fileRaw, e := readDocument(files, "files.complete.json", &restored)
	files.Close()
	if e != nil {
		return result, e
	}
	complete.StoresSHA256 = sha(fileRaw)
	if _, e = root.Lstat("operator.complete.json"); e == nil {
		var old operatorComplete
		if _, e = readDocument(root, "operator.complete.json", &old); e != nil || !reflect.DeepEqual(old, complete) {
			return result, fail("OPERATOR_COMPLETION_CHANGED")
		}
	} else {
		if e = writeNew(root, "operator.complete.json", jsonBytes(complete)); e != nil {
			return result, e
		}
	}
	return OperatorResult{1, r.OperationID, "restored_authority_verified_runtime_access_pending", fingerprint(complete)}, nil
}

// Runtime and owner use different credentials but the same declared schema,
// endpoint, SDK and native identity. This check opens no runtime pool or socket.
func (i *operatorInputs) validateRuntimeProjection(c config.Config) error {
	if c.Database.Dameng == nil {
		return fail("RUNTIME_CONNECTOR_CLOSURE_REQUIRED")
	}
	runtime, e := dameng.LoadConnectionConfiguration(c.Database.Dameng.RuntimeConfigurationFile, "")
	if e != nil {
		return fail("RUNTIME_CONNECTOR_CLOSURE_INVALID")
	}
	schema, e := dameng.LoadSchemaConfiguration(c.Database.Dameng.SchemaConfigurationFile, "")
	if e != nil || schema != i.schema || schema.Schema != runtime.Schema || schema.RuntimeUser != runtime.User || runtime.User == i.connector.User || runtime.Schema != i.connector.Schema || runtime.Transport != i.connector.Transport || runtime.SocketFile != i.connector.SocketFile || runtime.SocketOwnerUID != i.connector.SocketOwnerUID {
		return fail("RUNTIME_OWNER_BINDING_MISMATCH")
	}
	secret, e := readPrivate(runtime.PasswordFile, 65536)
	if e != nil || len(secret) == 0 {
		return fail("RUNTIME_CREDENTIAL_CLOSURE_INVALID")
	}
	clear(secret)
	var source, target dameng.NativeManifest
	a, e := readPrivate(i.connector.NativeManifestFile, maximumDocumentBytes)
	if e != nil || strictJSON(a, &source) != nil || source.Validate() != nil {
		return fail("OWNER_MANIFEST_CLOSURE_INVALID")
	}
	b, e := readPrivate(runtime.NativeManifestFile, maximumDocumentBytes)
	if e != nil || strictJSON(b, &target) != nil || target.Validate() != nil || !reflect.DeepEqual(source, target) {
		return fail("RUNTIME_NATIVE_IDENTITY_MISMATCH")
	}
	return nil
}

const initialNativeProfile = "native_four_stores_v1"

type initialProfileProof struct {
	FormatVersion          int      `json:"format_version"`
	Profile                string   `json:"profile"`
	Root                   string   `json:"root"`
	DatabaseCompleteSHA256 string   `json:"database_complete_sha256"`
	RowsSHA256             string   `json:"rows_sha256"`
	ServerID               string   `json:"server_id"`
	EndpointSHA256         string   `json:"endpoint_sha256"`
	Tables                 []string `json:"tables"`
	BeforeExport           bool     `json:"before_export"`
	AfterCapture           bool     `json:"after_capture"`
}

// These exact relations are in the closed complete serving catalog. Until their
// external state owners are composed, even historical rows refuse this profile.
func initialNativeProfileTables() []string {
	return []string{
		"ARTIFACT_OBJECTS", "ARTIFACT_OBJECT_GRANTS", "ARTIFACT_OBJECT_INGESTIONS", "ARTIFACT_OBJECT_PARTS",
		"IMAGE_REGISTRY_BLOBS", "IMAGE_REGISTRY_DEPLOYMENT_REFS", "IMAGE_REGISTRY_GRANTS", "IMAGE_REGISTRY_IMPORTS", "IMAGE_REGISTRY_MUTATIONS", "IMAGE_REGISTRY_OFFLINE_REFS", "IMAGE_REGISTRY_PINS",
		"MIDDLEWARE_INSTANCES", "MIDDLEWARE_OPERATIONS", "PRIVATE_CLUSTER_CONNECTIONS", "PRIVATE_CLUSTER_INTENTS", "PRIVATE_CLUSTER_RUNS",
	}
}
func readInitialNativeProfile(ctx context.Context, q nativeQuery, catalog []string) error {
	if ctx == nil || q == nil {
		return fail("INITIAL_PROFILE_CUSTODY_REQUIRED")
	}
	known := map[string]bool{}
	for _, table := range catalog {
		known[table] = true
	}
	for _, table := range initialNativeProfileTables() {
		if !known[table] {
			return fail("INITIAL_PROFILE_CATALOG_REQUIRED")
		}
	}
	for _, table := range initialNativeProfileTables() {
		var count int64
		if e := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+nativeQuote(table)).Scan(&count); e != nil {
			return fail("INITIAL_PROFILE_ABSENCE_UNPROVEN")
		}
		if count != 0 {
			return fail("STATE_OWNER_BACKUP_UNIMPLEMENTED")
		}
	}
	return nil
}
func checkInitialNativeProfile(ctx context.Context, c *NativeCustody) error {
	if c == nil || c.CheckFence(ctx) != nil {
		return fail("INITIAL_PROFILE_CUSTODY_REQUIRED")
	}
	if e := readInitialNativeProfile(ctx, c.query, c.tables); e != nil {
		return e
	}
	return c.CheckFence(ctx)
}
func readInitialProfileProof(root *os.Root, path string, complete operatorComplete, database Complete) error {
	var profile initialProfileProof
	raw, e := readDocument(root, "profile.absent.json", &profile)
	if e != nil || !digest.MatchString(complete.InitialProfileSHA256) || sha(raw) != complete.InitialProfileSHA256 || profile.FormatVersion != 1 || profile.Profile != initialNativeProfile || profile.Root != path || profile.DatabaseCompleteSHA256 != complete.DatabaseSHA256 || profile.RowsSHA256 != database.Database.RowsSHA256 || profile.ServerID != database.Database.ServerID || profile.EndpointSHA256 != database.Database.EndpointSHA256 || !reflect.DeepEqual(profile.Tables, initialNativeProfileTables()) || !profile.BeforeExport || !profile.AfterCapture {
		return fail("ORIGINAL_INITIAL_PROFILE_REQUIRED")
	}
	return nil
}

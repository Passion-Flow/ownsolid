package configuration

import (
	"encoding/json"
	"errors"
	"path"
	"reflect"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// These are already admitted caller facts, not another configuration loader or
// an HTTP/JSON override. Scope retains original source, target and once-only
// reservation authority; the native owner still verifies SQL and actual roots.
type NativePostgreSQLRestoreBinding struct {
	ScopeID             string                               `json:"-"`
	AttemptID           string                               `json:"-"`
	SignedSourceSHA256  string                               `json:"-"`
	ControlImageSHA256  string                               `json:"-"`
	PostgresImageSHA256 string                               `json:"-"`
	SourceRequest       wire.PostgreSQLBackupRequest         `json:"-"`
	SourceReference     wire.NativePostgreSQLBackupReference `json:"-"`
	TargetDatabase      string                               `json:"-"`
	TargetOperator      string                               `json:"-"`
	Reason              string                               `json:"-"`
	UID                 int                                  `json:"-"`
	GID                 int                                  `json:"-"`
	TargetRoots         []wire.PostgreSQLRestoreRoot         `json:"-"`
}

// BindRestore constructs the first immutable native request from the same
// protected descriptor as backup. A deployment template is never an executed
// native request, and this method must not be used to replace a started request.
func (d NativePostgreSQLRecoveryDescriptor) BindRestore(b NativePostgreSQLRestoreBinding) (wire.PostgreSQLRestoreRequest, error) {
	bad := errors.New("OFFLINE_OPERATOR_NATIVE_RESTORE_BINDING_INVALID")
	source, reference := b.SourceRequest, b.SourceReference
	raw, e := json.Marshal(source)
	if d.Validate() != nil || d.RestoreTemplate == nil || e != nil || !source.Valid() || source.ApplicationProfile != wire.MVPPostgreSQLCPUApplicationProfile || !reference.Valid() || !reference.RegistryConfigured || source.SourceScopeID == b.ScopeID || source.SourceScopeID == b.AttemptID || source.BackupID == b.AttemptID || source.Database == b.TargetDatabase || reference.RequestSHA256 != wire.Digest(append(raw, '\n')) || source.SourceManifestSHA256 != b.SignedSourceSHA256 || reference.SourceManifestSHA256 != b.SignedSourceSHA256 || d.BackupTemplate.SourceManifestSHA256 != b.SignedSourceSHA256 || d.BackupTemplate.SourceScopeID != b.ScopeID || source.TenantID != d.BackupTemplate.TenantID || source.ControlImageSHA256 != b.ControlImageSHA256 || source.PostgresImageSHA256 != b.PostgresImageSHA256 || d.BackupTemplate.ControlImageSHA256 != b.ControlImageSHA256 || d.BackupTemplate.PostgresImageSHA256 != b.PostgresImageSHA256 || b.UID < 1 || b.GID < 1 || b.UID > 2147483647 || b.GID > 2147483647 || len(b.TargetRoots) != len(d.StoreRoots) || !wire.ValidPostgreSQLRestoreRoots(b.TargetRoots) {
		return wire.PostgreSQLRestoreRequest{}, bad
	}
	for i, root := range b.TargetRoots {
		if root.Path != d.StoreRoots[i] || root.UID != uint32(b.UID) || root.GID != uint32(b.GID) {
			return wire.PostgreSQLRestoreRequest{}, bad
		}
	}
	r := *d.RestoreTemplate
	r.RestoreID, r.TargetScopeID = b.AttemptID, b.ScopeID
	r.SourceManifestSHA256, r.BackupRoot = reference.CandidateSHA256, source.BackupRoot
	r.RestoreRoot = path.Join(path.Dir(r.RestoreRoot), b.AttemptID)
	r.TargetDatabase, r.TargetOperator, r.Reason = b.TargetDatabase, b.TargetOperator, b.Reason
	r.TargetRoots = append([]wire.PostgreSQLRestoreRoot(nil), b.TargetRoots...)
	if !r.Valid() {
		return wire.PostgreSQLRestoreRequest{}, bad
	}
	return r, nil
}

type NativePostgreSQLRestoreTargetBinding struct {
	InitializationID string                                             `json:"-"`
	Binding          wire.PostgreSQLRestoreTargetInitializationBinding  `json:"-"`
	SourceDatabase   string                                             `json:"-"`
	Endpoint         wire.PostgreSQLRestoreTargetInitializationEndpoint `json:"-"`
}

type NativePostgreSQLRestoreRootsBinding struct {
	ScopeID                    string                                            `json:"-"`
	InspectionID               string                                            `json:"-"`
	Binding                    wire.PostgreSQLRestoreTargetInitializationBinding `json:"-"`
	RuntimeConfigurationSHA256 string                                            `json:"-"`
	RuntimeEnvironmentSHA256   string                                            `json:"-"`
	UID                        int                                               `json:"-"`
	GID                        int                                               `json:"-"`
}

// Build the independent readonly request before the final measured restore
// request exists. Both operations derive policy/paths from this one descriptor.
func (d NativePostgreSQLRecoveryDescriptor) BindRestoreRootsInspection(b NativePostgreSQLRestoreRootsBinding) (wire.PostgreSQLRestoreRootsInspectionRequest, error) {
	bad := errors.New("OFFLINE_OPERATOR_NATIVE_RESTORE_ROOTS_BINDING_INVALID")
	if d.Validate() != nil || d.RestoreTemplate == nil || d.InitializationPolicy == nil || b.ScopeID != d.BackupTemplate.SourceScopeID || b.UID < 1 || b.GID < 1 || b.UID > 2147483647 || b.GID > 2147483647 || (d.RestoreTemplate.RuntimeEnvironmentFile == "") != (b.RuntimeEnvironmentSHA256 == "") {
		return wire.PostgreSQLRestoreRootsInspectionRequest{}, bad
	}
	r := wire.PostgreSQLRestoreRootsInspectionRequest{FormatVersion: 1, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, InspectionID: b.InspectionID, TargetScopeID: b.ScopeID, Binding: b.Binding, RuntimeConfigurationRoot: d.RestoreTemplate.ConfigurationRoot, RuntimeConfigurationSHA256: b.RuntimeConfigurationSHA256, RuntimeEnvironmentSHA256: b.RuntimeEnvironmentSHA256, ExpectedUID: uint32(b.UID), ExpectedGID: uint32(b.GID), Policy: wire.PostgreSQLRestoreRootsInspectionPolicy{OperationSeconds: d.InitializationPolicy.OperationSeconds, MaximumRecordBytes: d.InitializationPolicy.MaximumRecordBytes}}
	if !r.Valid() {
		return wire.PostgreSQLRestoreRootsInspectionRequest{}, bad
	}
	return r, nil
}

// Original observation is one instant, not continuous custody. Scope must retain
// its original bytes/task and physical volume identities, then publish this NEW
// final request before starting NativeRestore, which relocks and compares pins.
func (d NativePostgreSQLRecoveryDescriptor) BindRestoreObservedRoots(b NativePostgreSQLRestoreBinding, request wire.PostgreSQLRestoreRootsInspectionRequest, observed wire.PostgreSQLRestoreRootsInspectionObservation) (wire.PostgreSQLRestoreRequest, error) {
	bad := errors.New("OFFLINE_OPERATOR_NATIVE_RESTORE_ROOTS_BINDING_INVALID")
	if len(b.TargetRoots) != 0 || !observed.ValidFor(request) || request.TargetScopeID != b.ScopeID || request.Binding.RestoreAttemptID != b.AttemptID || request.Binding.SourceCandidateSHA256 != b.SourceReference.CandidateSHA256 {
		return wire.PostgreSQLRestoreRequest{}, bad
	}
	expected, e := d.BindRestoreRootsInspection(NativePostgreSQLRestoreRootsBinding{ScopeID: b.ScopeID, InspectionID: request.InspectionID, Binding: request.Binding, RuntimeConfigurationSHA256: request.RuntimeConfigurationSHA256, RuntimeEnvironmentSHA256: request.RuntimeEnvironmentSHA256, UID: b.UID, GID: b.GID})
	if e != nil || expected != request {
		return wire.PostgreSQLRestoreRequest{}, bad
	}
	b.TargetRoots = append([]wire.PostgreSQLRestoreRoot(nil), observed.Roots...)
	return d.BindRestore(b)
}

// Discovery has no identity pins and grants no initializer authority. The SQL
// owner returns the actual system/OID under its original installation session.
func (d NativePostgreSQLRecoveryDescriptor) BindRestoreTargetInspection(restore wire.PostgreSQLRestoreRequest, b NativePostgreSQLRestoreTargetBinding) (wire.PostgreSQLRestoreTargetInitializationRequest, error) {
	bad := errors.New("OFFLINE_OPERATOR_NATIVE_RESTORE_TARGET_BINDING_INVALID")
	if d.Validate() != nil || d.RestoreTemplate == nil || d.InitializationPolicy == nil || !restore.Valid() || restore.ApplicationProfile != wire.MVPPostgreSQLCPUApplicationProfile || restore.TargetScopeID != d.BackupTemplate.SourceScopeID || restore.ConfigurationRoot != d.RestoreTemplate.ConfigurationRoot || restore.RuntimeConfigurationFile != d.RestoreTemplate.RuntimeConfigurationFile || restore.RuntimeEnvironmentFile != d.RestoreTemplate.RuntimeEnvironmentFile || !reflect.DeepEqual(restore.Policy, d.RestoreTemplate.Policy) || b.Binding.RestoreAttemptID != restore.RestoreID || b.Binding.SourceCandidateSHA256 != restore.SourceManifestSHA256 || b.InitializationID == restore.RestoreID {
		return wire.PostgreSQLRestoreTargetInitializationRequest{}, bad
	}
	r := wire.PostgreSQLRestoreTargetInitializationRequest{FormatVersion: 1, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, InitializationID: b.InitializationID, TargetScopeID: restore.TargetScopeID, Binding: b.Binding, SourceDatabase: b.SourceDatabase, TargetDatabase: restore.TargetDatabase, TargetOperator: restore.TargetOperator, Endpoint: b.Endpoint, InitializationRoot: path.Join(d.InitializationParentRoot, b.InitializationID), Policy: *d.InitializationPolicy}
	if !r.ValidForInspection() || r.IdentityPinned() {
		return wire.PostgreSQLRestoreTargetInitializationRequest{}, bad
	}
	return r, nil
}

// A new initializer identity has separate input/record custody from the earlier
// readonly discovery task. Pin only an uninitialized original observation; a
// matching marker, partial original intent or unknown completion is not adopted.
// This is construction before intent, never a retry or modification of intent.
func (d NativePostgreSQLRecoveryDescriptor) BindRestoreTargetInitialization(discovery wire.PostgreSQLRestoreTargetInitializationRequest, observed wire.PostgreSQLRestoreTargetInitializationObservation, initializationID string) (wire.PostgreSQLRestoreTargetInitializationRequest, error) {
	bad := errors.New("OFFLINE_OPERATOR_NATIVE_RESTORE_TARGET_BINDING_INVALID")
	if d.Validate() != nil || d.RestoreTemplate == nil || d.InitializationPolicy == nil || !discovery.ValidForInspection() || discovery.IdentityPinned() || discovery.TargetScopeID != d.BackupTemplate.SourceScopeID || discovery.InitializationRoot != path.Join(d.InitializationParentRoot, discovery.InitializationID) || discovery.Policy != *d.InitializationPolicy || !observed.ValidFor(discovery, true) || observed.State != "uninitialized" || observed.OriginalIntentPresent || observed.OriginalCompletionPresent || observed.EffectObserved || initializationID == discovery.InitializationID || initializationID == discovery.Binding.RestoreAttemptID {
		return wire.PostgreSQLRestoreTargetInitializationRequest{}, bad
	}
	r := discovery
	r.InitializationID = initializationID
	r.InitializationRoot = path.Join(d.InitializationParentRoot, initializationID)
	r.ExpectedSystemIdentifier, r.ExpectedDatabaseOID = observed.Target.SystemIdentifier, observed.Target.DatabaseOID
	if !r.Valid() {
		return wire.PostgreSQLRestoreTargetInitializationRequest{}, bad
	}
	return r, nil
}

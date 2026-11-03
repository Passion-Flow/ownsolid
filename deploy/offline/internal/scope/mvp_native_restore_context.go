package scope

import (
	"reflect"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Bind the ORIGINAL completed native runtime context, rather than generating a
// legacy SQL context from its metadata reference. The caller must first obtain
// the completion and independent observation through their original native
// owners and retain their exact private bytes. This check creates no SQL,
// signature, file-import or authority-invalidation evidence.
func bindMVPNativeRestoreRuntimeContext(plan RestorePlan, scopeID string, request wire.PostgreSQLRestoreRequest, connection restoreConnection, archiveSHA256 string, original wire.RestoredRuntimeContext, observed *wire.NativePostgreSQLRestoreReference) (wire.RestoredRuntimeContext, error) {
	bound, e := bindMVPNativeRestoreOriginalRuntimeContext(plan, scopeID, request, connection, archiveSHA256, original)
	if e != nil {
		return wire.RestoredRuntimeContext{}, e
	}
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_RUNTIME_CONTEXT_CHANGED")
	if observed == nil || !observed.Valid() || !reflect.DeepEqual(observed.Backup, *plan.Source.NativePostgreSQL) || observed.RequestSHA256 != wire.Digest(encode(request)) || observed.ManifestSHA256 != request.SourceManifestSHA256 {
		return wire.RestoredRuntimeContext{}, bad
	}
	return bound, nil
}

// Original completion custody is admitted before the RW actor is removed and
// before a fresh readonly observer can be started. This validates only its
// context; it deliberately provides no completed-restore reference by itself.
func bindMVPNativeRestoreOriginalRuntimeContext(plan RestorePlan, scopeID string, request wire.PostgreSQLRestoreRequest, connection restoreConnection, archiveSHA256 string, original wire.RestoredRuntimeContext) (wire.RestoredRuntimeContext, error) {
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_RUNTIME_CONTEXT_CHANGED")
	if !mvpNativeRestoreRequestContext(plan, scopeID, request, connection, archiveSHA256) || !original.Valid() {
		return wire.RestoredRuntimeContext{}, bad
	}
	// The native context binds the original native candidate, independently of
	// the enclosing portable inventory consumed by the Scope reservation.
	expected := wire.RestoredRuntimeContext{FormatVersion: 1, RestoreID: request.RestoreID, SourceScopeID: plan.Source.SourceScopeID, TargetScopeID: scopeID, BackupManifestSHA256: request.SourceManifestSHA256, SQLArchiveSHA256: archiveSHA256, TenantID: plan.Source.Source.TenantID, SchemaVersion: plan.Source.Source.SchemaVersion, TargetDatabase: connection.database, TargetOperator: connection.operator, Reason: plan.Request.Reason, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile}
	if !reflect.DeepEqual(original, expected) {
		return wire.RestoredRuntimeContext{}, bad
	}
	return original, nil
}

// Shared pre-effect identity check; it creates no runtime context or proof.
func mvpNativeRestoreRequestContext(plan RestorePlan, scopeID string, request wire.PostgreSQLRestoreRequest, connection restoreConnection, archiveSHA256 string) bool {
	if !validMVPNativeRestoreSource(plan.Source) {
		return false
	}
	return plan.FormatVersion == 1 && plan.Request.Validate() == nil && identityPattern.MatchString(plan.AttemptID) && identityPattern.MatchString(scopeID) && scopeID != plan.Source.SourceScopeID && plan.Source.ManifestSHA256 == plan.Request.ManifestSHA256 && plan.Source.RequestSHA256 == wire.Digest(encode(plan.Request.RestoreSourceRequest)) && request.Valid() && request.ApplicationProfile == wire.MVPPostgreSQLCPUApplicationProfile && request.RestoreID == plan.AttemptID && request.TargetScopeID == scopeID && request.SourceManifestSHA256 == plan.Source.NativePostgreSQL.CandidateSHA256 && request.Reason == plan.Request.Reason && request.TargetDatabase == connection.database && request.TargetOperator == connection.operator && wire.ValidSHA256(archiveSHA256)
}

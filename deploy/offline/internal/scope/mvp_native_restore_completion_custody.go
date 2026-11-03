package scope

import (
	"context"
	"os"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func mvpNativeRestoreCompletionName(operationID string) string {
	return "mvp-native-restore-completion-" + operationID + ".json"
}

// The original RW actor may be removed only after the caller has durably
// journaled this exact descriptor and its original task ID. This helper does
// not authorize removal, replay, stage completion or an independent observation.
func retainMVPNativeRestoreCompletion(ctx context.Context, root *os.Root, operationID string, plan RestorePlan, scopeID string, request wire.PostgreSQLRestoreRequest, connection restoreConnection, archiveSHA256 string, value engine.NativePostgreSQLRestoreCompletion) (InputFile, engine.NativePostgreSQLRestoreCompletion, error) {
	if ctx == nil || ctx.Err() != nil || root == nil || !identityPattern.MatchString(operationID) {
		return InputFile{}, engine.NativePostgreSQLRestoreCompletion{}, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_COMPLETION_CUSTODY_INVALID")
	}
	complete, e := bindMVPNativeRestoreCompletion(plan, scopeID, request, connection, archiveSHA256, value)
	if e != nil {
		return InputFile{}, engine.NativePostgreSQLRestoreCompletion{}, e
	}
	file := InputFile{Name: mvpNativeRestoreCompletionName(operationID), Bytes: int64(len(complete.Original)), SHA256: complete.OriginalSHA256}
	if e = syncPrivateRoot(root); e != nil {
		return InputFile{}, engine.NativePostgreSQLRestoreCompletion{}, e
	}
	if e = writeBackupFile(root, file.Name, complete.Original); e != nil {
		return InputFile{}, engine.NativePostgreSQLRestoreCompletion{}, e
	}
	retained, e := readMVPNativeRestoreCompletion(ctx, root, operationID, file, plan, scopeID, request, connection, archiveSHA256)
	return file, retained, e
}

// Reopen uses only original private files and the already admitted source SQL
// archive digest. It cannot contact Engine, rerun SQL or recreate a completion.
func readMVPNativeRestoreCompletion(ctx context.Context, root *os.Root, operationID string, file InputFile, plan RestorePlan, scopeID string, request wire.PostgreSQLRestoreRequest, connection restoreConnection, archiveSHA256 string) (engine.NativePostgreSQLRestoreCompletion, error) {
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_COMPLETION_CUSTODY_CHANGED")
	if ctx == nil || ctx.Err() != nil || root == nil || !identityPattern.MatchString(operationID) || file.Name != mvpNativeRestoreCompletionName(operationID) || file.Bytes < 1 || file.Bytes > request.Policy.MaximumManifestBytes || !wire.ValidSHA256(file.SHA256) {
		return engine.NativePostgreSQLRestoreCompletion{}, bad
	}
	raw, e := regular(root, file.Name, request.Policy.MaximumManifestBytes)
	if e != nil || int64(len(raw)) != file.Bytes || wire.Digest(raw) != file.SHA256 {
		return engine.NativePostgreSQLRestoreCompletion{}, bad
	}
	var complete engine.NativePostgreSQLRestoreCompletion
	if wire.StrictJSON(raw, &complete) != nil {
		return engine.NativePostgreSQLRestoreCompletion{}, bad
	}
	complete.Original, complete.OriginalSHA256 = raw, file.SHA256
	return bindMVPNativeRestoreCompletion(plan, scopeID, request, connection, archiveSHA256, complete)
}

// Join two independently retained originals. A shape-valid reference cannot
// substitute another completion or attach a new target/post-image to old SQL.
// Fresh observation bytes/time remain outside the invariant restore identity.
func bindMVPNativeRestoreCompletedObservation(plan RestorePlan, scopeID string, request wire.PostgreSQLRestoreRequest, connection restoreConnection, archiveSHA256 string, complete engine.NativePostgreSQLRestoreCompletion, observed engine.NativePostgreSQLRestoreObservation) (engine.NativePostgreSQLRestoreObservation, error) {
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_COMPLETION_OBSERVATION_CHANGED")
	c, e := bindMVPNativeRestoreCompletion(plan, scopeID, request, connection, archiveSHA256, complete)
	if e != nil {
		return engine.NativePostgreSQLRestoreObservation{}, e
	}
	o, e := bindMVPNativeRestoreObservation(observed.Original, request.Policy.MaximumManifestBytes, request.RestoreID, wire.Digest(encode(request)), plan.Source, nil)
	if e != nil {
		return engine.NativePostgreSQLRestoreObservation{}, e
	}
	r := o.NativePostgreSQL
	if r == nil || !o.OriginalCompletionPresent || !o.ReadOnly || r.CompletionSHA256 != c.OriginalSHA256 || r.ManifestSHA256 != c.ManifestSHA256 || r.IntentSHA256 != c.IntentSHA256 || r.FilesSHA256 != c.FilesSHA256 || r.TargetBindingSHA256 != c.TargetBindingSHA256 || r.PostDataSHA256 != c.PostDataSHA256 {
		return engine.NativePostgreSQLRestoreObservation{}, bad
	}
	if _, e = bindMVPNativeRestoreRuntimeContext(plan, scopeID, request, connection, archiveSHA256, c.RuntimeContext, r); e != nil {
		return engine.NativePostgreSQLRestoreObservation{}, e
	}
	return o, nil
}

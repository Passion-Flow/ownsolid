package scope

import (
	"context"
	"os"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func mvpNativeRestoreObservationName(operationID string) string {
	return "mvp-native-restore-observation-" + operationID + ".json"
}

// Each actual observation has separate exclusive custody. A repeated readonly
// check never replaces the original output or becomes immutable restore identity.
// Callers must retain the descriptor in their owning journal before removing
// the original task/output; this helper grants no cleanup or task authority.
func retainMVPNativeRestoreObservation(ctx context.Context, root *os.Root, operationID string, raw []byte, maximumBytes int64, restoreID, requestSHA256 string, source RestoreSourceResult, original *wire.NativePostgreSQLRestoreReference) (InputFile, engine.NativePostgreSQLRestoreObservation, error) {
	if ctx == nil || ctx.Err() != nil || root == nil || !identityPattern.MatchString(operationID) {
		return InputFile{}, engine.NativePostgreSQLRestoreObservation{}, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_CUSTODY_CONTEXT_INVALID")
	}
	observation, e := bindMVPNativeRestoreObservation(raw, maximumBytes, restoreID, requestSHA256, source, original)
	if e != nil {
		return InputFile{}, engine.NativePostgreSQLRestoreObservation{}, e
	}
	file := InputFile{Name: mvpNativeRestoreObservationName(operationID), Bytes: int64(len(observation.Original)), SHA256: observation.OriginalSHA256}
	if e = syncPrivateRoot(root); e != nil {
		return InputFile{}, engine.NativePostgreSQLRestoreObservation{}, e
	}
	if e = writeBackupFile(root, file.Name, observation.Original); e != nil {
		return InputFile{}, engine.NativePostgreSQLRestoreObservation{}, e
	}
	retained, e := readMVPNativeRestoreObservation(ctx, root, operationID, file, maximumBytes, restoreID, requestSHA256, source, original)
	if e != nil {
		return InputFile{}, engine.NativePostgreSQLRestoreObservation{}, e
	}
	return file, retained, nil
}

// Reopen only the exact private original selected by its recorded descriptor.
// This path never writes, syncs a missing completion or reruns an Engine task.
func readMVPNativeRestoreObservation(ctx context.Context, root *os.Root, operationID string, file InputFile, maximumBytes int64, restoreID, requestSHA256 string, source RestoreSourceResult, original *wire.NativePostgreSQLRestoreReference) (engine.NativePostgreSQLRestoreObservation, error) {
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_CUSTODY_CHANGED")
	if ctx == nil || ctx.Err() != nil || root == nil || !identityPattern.MatchString(operationID) || file.Name != mvpNativeRestoreObservationName(operationID) || file.Bytes < 1 || file.Bytes > maximumBytes || !wire.ValidSHA256(file.SHA256) {
		return engine.NativePostgreSQLRestoreObservation{}, bad
	}
	raw, e := regular(root, file.Name, maximumBytes)
	if e != nil || int64(len(raw)) != file.Bytes || wire.Digest(raw) != file.SHA256 {
		return engine.NativePostgreSQLRestoreObservation{}, bad
	}
	return bindMVPNativeRestoreObservation(raw, maximumBytes, restoreID, requestSHA256, source, original)
}

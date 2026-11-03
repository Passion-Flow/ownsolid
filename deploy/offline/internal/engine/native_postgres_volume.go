package engine

import "context"

// RemoveNativePostgreSQLVolume removes only an exact per-attempt temporary
// native operator volume after zero-consumer readback. The scope must retain
// required original artifacts/completions before calling it. Domain stores,
// live registry CAS and reusable prepared owners never match this catalog.
func (c *Client) RemoveNativePostgreSQLVolume(ctx context.Context, o Ownership) error {
	return c.removeGeneratedVolume(ctx, o, []string{NativePostgreSQLRestoreRootsInputRole, NativePostgreSQLTargetInputRole, NativePostgreSQLTargetRecordsRole, NativePostgreSQLRecoveryInputRole, NativePostgreSQLConfigurationRole, NativePostgreSQLArtifactRole, NativePostgreSQLRecoveryScratchRole, NativePostgreSQLRestoreRecordsRole, NativePostgreSQLScaffoldRole, NativePostgreSQLPreparedRole, NativePostgreSQLVerifierInputRole, NativePostgreSQLVerifierScratchRole, NativePostgreSQLVerifierPlaceholderRole})
}

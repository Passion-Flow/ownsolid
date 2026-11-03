package scope

import wire "github.com/Passion-Flow/ownsolid/api/release/v1"

// Native selection is fail-closed even if a required reference is missing.
// Shape-valid legacy metadata must never route schema29 or a native marker into
// the old independent SQL, runtime-prepare, auth-invalidate or store-import loops.
func requiresMVPNativeRestore(marker Marker, plan RestorePlan) bool {
	return marker.Profile == wire.MVPPostgreSQLCPUProfile || plan.Source.Profile == wire.MVPPostgreSQLCPUProfile || plan.Source.Source.SchemaVersion == wire.MVPPostgreSQLCPUV1SchemaVersion || plan.Source.NativePostgreSQL != nil
}

func mvpNativeRestoreRequired() error {
	return failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_REQUIRED")
}

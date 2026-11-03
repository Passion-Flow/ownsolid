package scope

import (
	"context"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Backup consumes the original historical recovery custody after all three
// native stages. Current serving/maintenance, stopped peers and installed view
// are independently admitted by the backup owner; this getter grants none of
// those authorities and never reruns old post-image checks after activation.
func (l *locked) readMVPNativeRestoredBackupOriginal(ctx context.Context) (mvpNativeRestoredBackupOriginal, error) {
	var out mvpNativeRestoredBackupOriginal
	if l.marker.Profile != wire.MVPPostgreSQLCPUProfile {
		return out, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORED_BACKUP_PROFILE_REQUIRED")
	}
	if _, ok := restoreSQLProof(l.events); !ok {
		return out, failure("OFFLINE_SCOPE_RESTORE_QUALIFICATION_SQL_REQUIRED")
	}
	if _, ok := restoreDatabaseProof(l.events); !ok {
		return out, failure("OFFLINE_SCOPE_RESTORE_STORE_DATABASE_QUALIFICATION_REQUIRED")
	}
	if _, ok := restoreStoresProof(l.events); !ok {
		return out, failure("OFFLINE_SCOPE_CURRENT_SELECTED_STORES_PROOF_REQUIRED")
	}
	plan, e := l.readBoundRestorePlan()
	if e != nil {
		return out, e
	}
	execution, e := l.readMVPNativeRestoreExecution(ctx)
	if e != nil {
		return out, e
	}
	complete, e := l.readMVPNativeRestoreOriginal(ctx, plan)
	if e != nil {
		return out, e
	}
	sql, e := l.nativeRestoreObservation(ctx, "sql")
	if e != nil {
		return out, e
	}
	database, e := l.nativeRestoreObservation(ctx, "database")
	if e != nil {
		return out, e
	}
	stores, e := l.nativeRestoreObservation(ctx, "stores")
	if e != nil {
		return out, e
	}
	if !sameMVPNativeRestore(sql.NativePostgreSQL, database.NativePostgreSQL) || !sameMVPNativeRestore(sql.NativePostgreSQL, stores.NativePostgreSQL) {
		return out, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_STATIC_IDENTITY_CHANGED")
	}
	return mvpNativeRestoredBackupOriginal{Request: execution.NativeRequest, Writer: execution.Writer, Completion: complete, Reference: *stores.NativePostgreSQL}, nil
}

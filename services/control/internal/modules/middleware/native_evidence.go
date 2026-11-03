package middleware

import (
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/pkg/maintenance"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Reference validity is shape validation, not a new native verifier or signature
// authority. The original offline owner must supply its verified signed outer
// binding and independently verified canonical native reference together.
func requiresNativeReference(r maintenance.Result) bool {
	return r.Profile == wire.MVPPostgreSQLCPUProfile || r.NativePostgreSQL != nil || r.Source != nil && r.Source.SchemaVersion == wire.MVPPostgreSQLCPUV1SchemaVersion
}

func completedNativeBackupReference(r maintenance.Result) bool {
	if !requiresNativeReference(r) {
		return true
	}
	n := r.NativePostgreSQL
	return r.Profile == wire.MVPPostgreSQLCPUProfile && wire.ValidSHA256(r.ReleaseManifestSHA256) && n != nil && n.Restore == nil && n.Backup != nil && n.Backup.Valid() && n.Backup.SourceManifestSHA256 == r.ReleaseManifestSHA256 && n.Backup.RegistryConfigured && n.Backup.Registry != nil
}

func completedNativeSourceReference(r maintenance.Result) bool {
	if !requiresNativeReference(r) {
		return true
	}
	return completedNativeBackupReference(r) && r.Source != nil && r.Source.SchemaVersion == r.NativePostgreSQL.Backup.SchemaVersion && reflect.DeepEqual(r.Stores, []string{"datasets", "models", "preparations", "training"})
}

func sameOriginalNativeBackup(a, b maintenance.Result) bool {
	if !requiresNativeReference(a) && !requiresNativeReference(b) {
		return true
	}
	return completedNativeBackupReference(a) && completedNativeBackupReference(b) && a.Profile == b.Profile && a.ReleaseManifestSHA256 == b.ReleaseManifestSHA256 && reflect.DeepEqual(a.NativePostgreSQL.Backup, b.NativePostgreSQL.Backup)
}

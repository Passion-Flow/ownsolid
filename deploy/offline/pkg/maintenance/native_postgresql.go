package maintenance

import (
	"encoding/json"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// NativePostgreSQLReference transports exactly one original canonical wire
// reference. Its JSON is the unchanged backup or restore object, without a new
// discriminator, proof field, private path or independent authority.
type NativePostgreSQLReference struct {
	Backup  *wire.NativePostgreSQLBackupReference  `json:"-"`
	Restore *wire.NativePostgreSQLRestoreReference `json:"-"`
}

func (r NativePostgreSQLReference) MarshalJSON() ([]byte, error) {
	if r.Backup != nil && r.Restore == nil && r.Backup.Valid() {
		return json.Marshal(r.Backup)
	}
	if r.Restore != nil && r.Backup == nil && r.Restore.Valid() {
		return json.Marshal(r.Restore)
	}
	return nil, errors.New("MAINTENANCE_ORIGINAL_RESULT_INVALID")
}

func (r *NativePostgreSQLReference) UnmarshalJSON(raw []byte) error {
	*r = NativePostgreSQLReference{}
	var backup wire.NativePostgreSQLBackupReference
	if wire.StrictJSON(raw, &backup) == nil && backup.Valid() {
		*r = NativePostgreSQLReference{Backup: &backup}
		return nil
	}
	var restore wire.NativePostgreSQLRestoreReference
	if wire.StrictJSON(raw, &restore) == nil && restore.Valid() {
		*r = NativePostgreSQLReference{Restore: &restore}
		return nil
	}
	return errors.New("MAINTENANCE_ORIGINAL_RESULT_INVALID")
}

// UnmarshalJSON checks the complete stored outer object before pointer decoding.
// An explicit native_postgresql:null must not bypass the union as a nil pointer.
// Original omission remains valid, and failed decoding cannot leave stale data.
func (r *Result) UnmarshalJSON(raw []byte) error {
	*r = Result{}
	type storedResult Result
	var decoded storedResult
	if wire.StrictJSON(raw, &decoded) != nil {
		return errors.New("MAINTENANCE_ORIGINAL_RESULT_INVALID")
	}
	*r = Result(decoded)
	return nil
}

func validNativeProjection(r Result) bool {
	if r.ReleaseManifestSHA256 != "" && !wire.ValidSHA256(r.ReleaseManifestSHA256) {
		return false
	}
	n := r.NativePostgreSQL
	if n == nil {
		// Absent references preserve original progress/legacy bytes. Completeness
		// and the final MVP profile remain the consuming stage's responsibility.
		return true
	}
	switch r.Command {
	case Backup, InspectBackup, ReconcileBackup, InspectRestoreSource:
		return n.Backup != nil && n.Restore == nil && n.Backup.Valid() && r.Profile == wire.MVPPostgreSQLCPUProfile && wire.ValidSHA256(r.ReleaseManifestSHA256) && r.ReleaseManifestSHA256 == n.Backup.SourceManifestSHA256
	case RestoreSQL, ReconcileSQL, VerifyRestoredDatabase, ReconcileDatabase, RestoreStores, ReconcileStores:
		// The restore reference retains the original backup's declared source.
		// Bind it to the independently verified source's signed outer result in
		// the controller; a target manifest is not that source authority.
		return n.Restore != nil && n.Backup == nil && n.Restore.Valid()
	default:
		return false
	}
}

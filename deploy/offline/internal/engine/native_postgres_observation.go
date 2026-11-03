package engine

import (
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// These envelopes transport the original native readonly observations. They do
// not inspect SQL, verify signatures or acquire any installation authority.
// Expected hashes must come from the caller's original frozen/signed context.
// requestSHA256 means the native typed request's canonical JSON plus final LF;
// it is distinct from an offline source request or exact stdout byte digest.
type NativePostgreSQLBackupObservation struct {
	Format                 string                                `json:"format"`
	BackupID               string                                `json:"backup_id"`
	RequestSHA256          string                                `json:"request_sha256"`
	ManifestSHA256         string                                `json:"manifest_sha256"`
	ArchiveSHA256          string                                `json:"archive_sha256"`
	ArchiveDirectorySHA256 string                                `json:"archive_directory_sha256"`
	ObservedAt             string                                `json:"observed_at"`
	CurrentLockHeld        bool                                  `json:"current_lock_held"`
	NativePostgreSQL       *wire.NativePostgreSQLBackupReference `json:"native_postgresql,omitempty"`
	OriginalSHA256         string                                `json:"-"`
	Original               []byte                                `json:"-"`
}

func DecodeNativePostgreSQLBackupObservation(raw []byte, maximumBytes int64, backupID, requestSHA256, candidateSHA256, signedSourceSHA256 string) (NativePostgreSQLBackupObservation, error) {
	var v NativePostgreSQLBackupObservation
	bad := failure("OFFLINE_ENGINE_NATIVE_BACKUP_OBSERVATION_INVALID")
	if maximumBytes < 1 || int64(len(raw)) > maximumBytes || !validNativeObservationContext(backupID, requestSHA256, candidateSHA256, signedSourceSHA256) || wire.StrictJSON(raw, &v) != nil || v.Format != "projectone.postgres.independent-readback.v1" || v.BackupID != backupID || v.RequestSHA256 != requestSHA256 || v.ManifestSHA256 != candidateSHA256 || !wire.ValidSHA256(v.ArchiveSHA256) || !wire.ValidSHA256(v.ArchiveDirectorySHA256) || v.CurrentLockHeld || !nativeObservationTime(v.ObservedAt) {
		return NativePostgreSQLBackupObservation{}, bad
	}
	r := v.NativePostgreSQL
	if r == nil || !r.Valid() || r.RequestSHA256 != requestSHA256 || r.CandidateSHA256 != candidateSHA256 || r.SourceManifestSHA256 != signedSourceSHA256 {
		return NativePostgreSQLBackupObservation{}, bad
	}
	v.OriginalSHA256, v.Original = wire.Digest(raw), append([]byte(nil), raw...)
	return v, nil
}

type NativePostgreSQLRestoreObservation struct {
	Format                    string                                 `json:"format"`
	RestoreID                 string                                 `json:"restore_id"`
	IntentSHA256              string                                 `json:"intent_sha256"`
	FilesSHA256               string                                 `json:"files_sha256"`
	ManifestSHA256            string                                 `json:"manifest_sha256"`
	TargetBindingSHA256       string                                 `json:"target_binding_sha256"`
	PostDataSHA256            string                                 `json:"post_data_sha256"`
	OriginalCompletionPresent bool                                   `json:"original_completion_present"`
	ReadOnly                  bool                                   `json:"read_only"`
	CurrentLockHeld           bool                                   `json:"current_lock_held"`
	ObservedAt                string                                 `json:"observed_at"`
	Qualification             string                                 `json:"qualification"`
	NativePostgreSQL          *wire.NativePostgreSQLRestoreReference `json:"native_postgresql,omitempty"`
	OriginalSHA256            string                                 `json:"-"`
	Original                  []byte                                 `json:"-"`
}

// A valid observation with no original completion retains nil NativePostgreSQL.
// It cannot qualify imports, invalidate again, or authorize mutation replay.
func DecodeNativePostgreSQLRestoreObservation(raw []byte, maximumBytes int64, restoreID, requestSHA256, candidateSHA256, signedSourceSHA256 string) (NativePostgreSQLRestoreObservation, error) {
	var v NativePostgreSQLRestoreObservation
	bad := failure("OFFLINE_ENGINE_NATIVE_RESTORE_OBSERVATION_INVALID")
	if maximumBytes < 1 || int64(len(raw)) > maximumBytes || !validNativeObservationContext(restoreID, requestSHA256, candidateSHA256, signedSourceSHA256) || wire.StrictJSON(raw, &v) != nil || v.Format != "projectone.postgres.restore-observation.v1" || v.RestoreID != restoreID || v.ManifestSHA256 != candidateSHA256 || !v.ReadOnly || v.CurrentLockHeld || !nativeObservationTime(v.ObservedAt) || !validNativeObservationDigests(v.IntentSHA256, v.FilesSHA256, v.TargetBindingSHA256, v.PostDataSHA256) {
		return NativePostgreSQLRestoreObservation{}, bad
	}
	r := v.NativePostgreSQL
	if !v.OriginalCompletionPresent {
		if r != nil {
			return NativePostgreSQLRestoreObservation{}, bad
		}
	} else if r == nil || !r.Valid() || r.RequestSHA256 != requestSHA256 || r.Backup.SourceManifestSHA256 != signedSourceSHA256 || r.ManifestSHA256 != candidateSHA256 || r.IntentSHA256 != v.IntentSHA256 || r.FilesSHA256 != v.FilesSHA256 || r.TargetBindingSHA256 != v.TargetBindingSHA256 || r.PostDataSHA256 != v.PostDataSHA256 {
		return NativePostgreSQLRestoreObservation{}, bad
	}
	v.OriginalSHA256, v.Original = wire.Digest(raw), append([]byte(nil), raw...)
	return v, nil
}

func validNativeObservationContext(identity string, hashes ...string) bool {
	if len(identity) != 32 {
		return false
	}
	for _, b := range identity {
		if b < '0' || b > '9' && b < 'a' || b > 'f' {
			return false
		}
	}
	return validNativeObservationDigests(hashes...)
}

func validNativeObservationDigests(hashes ...string) bool {
	for _, value := range hashes {
		if !wire.ValidSHA256(value) {
			return false
		}
	}
	return true
}

func nativeObservationTime(value string) bool {
	_, e := time.Parse(time.RFC3339Nano, value)
	return e == nil
}

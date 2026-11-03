package scope

import (
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This is the invariant portion of an original native restore reference for
// scope journal identity. Current readonly observation bytes and their timestamp
// are retained separately. It is not a public reference or a native verifier.
type mvpNativeRestoreIdentity struct {
	FormatVersion       int                                           `json:"format_version"`
	ApplicationProfile  string                                        `json:"application_profile"`
	Backup              wire.NativePostgreSQLBackupReference          `json:"backup"`
	RequestSHA256       string                                        `json:"request_sha256"`
	ManifestSHA256      string                                        `json:"manifest_sha256"`
	IntentSHA256        string                                        `json:"intent_sha256"`
	FilesSHA256         string                                        `json:"files_sha256"`
	CompletionSHA256    string                                        `json:"completion_sha256"`
	FilesReadbackSHA256 string                                        `json:"files_readback_sha256"`
	TargetBindingSHA256 string                                        `json:"target_binding_sha256"`
	PostDataSHA256      string                                        `json:"post_data_sha256"`
	Registry            *wire.NativePostgreSQLRegistryImportReference `json:"registry,omitempty"`
}

func mvpNativeRestoreIdentityOf(r *wire.NativePostgreSQLRestoreReference) (mvpNativeRestoreIdentity, bool) {
	if r == nil || !r.Valid() || !r.Backup.RegistryConfigured {
		return mvpNativeRestoreIdentity{}, false
	}
	backup := r.Backup
	registry := *r.Registry
	backupRegistry := *r.Backup.Registry
	backup.Registry = &backupRegistry
	return mvpNativeRestoreIdentity{FormatVersion: r.FormatVersion, ApplicationProfile: r.ApplicationProfile, Backup: backup, RequestSHA256: r.RequestSHA256, ManifestSHA256: r.ManifestSHA256, IntentSHA256: r.IntentSHA256, FilesSHA256: r.FilesSHA256, CompletionSHA256: r.CompletionSHA256, FilesReadbackSHA256: r.FilesReadbackSHA256, TargetBindingSHA256: r.TargetBindingSHA256, PostDataSHA256: r.PostDataSHA256, Registry: &registry}, true
}

// This binding consumes an independently verified source. It does not verify a
// signature or grant authority to run a native task. In particular, the signed
// release, outer backup inventory, native candidate and native request hashes
// retain their separate meanings.
func validMVPNativeRestoreSource(source RestoreSourceResult) bool {
	r := source.NativePostgreSQL
	if source.Status != "verified_independent_backup_source_not_restored" || source.Profile != wire.MVPPostgreSQLCPUProfile || !identityPattern.MatchString(source.SourceScopeID) || !identityPattern.MatchString(source.SourceAttemptID) || !wire.ValidSHA256(source.RequestSHA256) || !wire.ValidSHA256(source.ManifestSHA256) || !wire.ValidSHA256(source.PlanSHA256) || !wire.ValidSHA256(source.ReleaseManifestSHA256) || !source.Source.Valid() || source.Source.SchemaVersion != wire.MVPPostgreSQLCPUV1SchemaVersion || r == nil || !r.Valid() || !r.RegistryConfigured || r.Registry == nil || r.SourceManifestSHA256 != source.ReleaseManifestSHA256 {
		return false
	}
	// The supplementary registry does not change the four domain stores. Keep
	// the source result's existing public ordering, not the task catalog order.
	return reflect.DeepEqual(source.Stores, []string{"datasets", "models", "preparations", "training"})
}

// A current readonly observation may have a new observation digest. Every
// original import, target, post-image and completion fact must remain identical.
// Neither an invalid reference nor omission can compare equal to completion.
func sameMVPNativeRestore(a, b *wire.NativePostgreSQLRestoreReference) bool {
	x, aOK := mvpNativeRestoreIdentityOf(a)
	y, bOK := mvpNativeRestoreIdentityOf(b)
	return aOK && bOK && reflect.DeepEqual(x, y)
}

// Decode the original native output again, rather than accepting a separately
// assembled DTO. Original/OriginalSHA256 remain private in the Engine envelope.
// A missing original completion is a valid noncomplete observation, never an
// invitation to resend the restore or publish a completion record.
func bindMVPNativeRestoreObservation(raw []byte, maximumBytes int64, restoreID, requestSHA256 string, source RestoreSourceResult, original *wire.NativePostgreSQLRestoreReference) (engine.NativePostgreSQLRestoreObservation, error) {
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_OBSERVATION_CHANGED")
	if !validMVPNativeRestoreSource(source) {
		return engine.NativePostgreSQLRestoreObservation{}, bad
	}
	observation, e := engine.DecodeNativePostgreSQLRestoreObservation(raw, maximumBytes, restoreID, requestSHA256, source.NativePostgreSQL.CandidateSHA256, source.ReleaseManifestSHA256)
	if e != nil {
		return engine.NativePostgreSQLRestoreObservation{}, e
	}
	r := observation.NativePostgreSQL
	if original != nil && !sameMVPNativeRestore(original, r) {
		return engine.NativePostgreSQLRestoreObservation{}, bad
	}
	if r != nil && (!r.Backup.RegistryConfigured || !reflect.DeepEqual(r.Backup, *source.NativePostgreSQL)) {
		return engine.NativePostgreSQLRestoreObservation{}, bad
	}
	return observation, nil
}

package postgresbackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Observation responses are separate from durable readback records: completion
// hashes the earlier independent readback, so it cannot appear inside that record.
// An omitted reference keeps the historical response's exact JSON shape.
type BackupObservation struct {
	Readback
	NativePostgreSQL *release.NativePostgreSQLBackupReference `json:"native_postgresql,omitempty"`
}
type RestoreObservationWithReference struct {
	RestoreObservation
	NativePostgreSQL *release.NativePostgreSQLRestoreReference `json:"native_postgresql,omitempty"`
}

type nativeReferenceRecord struct {
	path     string
	maximum  int64
	optional bool
}

// Keep the original private-file protocol open across verification and projection.
// Ctime also catches same-byte writes with a restored mtime. No payload is copied
// or opened a second time merely to compute the bounded metadata references.
func withNativeReferenceRecords(ctx context.Context, records []nativeReferenceRecord, consume func() error) error {
	if ctx == nil || consume == nil {
		return errors.New("POSTGRES_NATIVE_REFERENCE_CONTEXT_REQUIRED")
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if len(records) == 0 {
		return consume()
	}
	record := records[0]
	if _, e := os.Lstat(record.path); record.optional && os.IsNotExist(e) {
		root, e := openPrivateRoot(filepath.Dir(record.path))
		if e != nil {
			return e
		}
		defer root.Close()
		info, e := root.Stat(".")
		if e != nil {
			return e
		}
		before, e := observeTree(info)
		if e != nil {
			return e
		}
		result := withNativeReferenceRecords(ctx, records[1:], consume)
		info, e = root.Stat(".")
		if e != nil {
			return errors.Join(result, e)
		}
		after, e := observeTree(info)
		_, missing := root.Lstat(filepath.Base(record.path))
		if e != nil || before != after || !os.IsNotExist(missing) {
			return errors.Join(result, errors.New("POSTGRES_NATIVE_REFERENCE_RECORD_CHANGED"))
		}
		return result
	}
	proof, e := fileDigest(ctx, record.path, record.maximum)
	if e != nil {
		return e
	}
	return privateArchive(ctx, record.path, record.maximum, proof.SHA256, func(file *os.File) error {
		info, e := file.Stat()
		if e != nil {
			return e
		}
		before, e := observeTree(info)
		if e != nil {
			return e
		}
		result := withNativeReferenceRecords(ctx, records[1:], consume)
		info, e = file.Stat()
		if e != nil {
			return errors.Join(result, e)
		}
		after, e := observeTree(info)
		if e != nil || before != after {
			return errors.Join(result, errors.New("POSTGRES_NATIVE_REFERENCE_RECORD_CHANGED"))
		}
		return result
	})
}

func backupReferenceRecords(root string, maximum int64) []nativeReferenceRecord {
	out := []nativeReferenceRecord{}
	for _, name := range []string{candidateName, readbackName, completeName} {
		out = append(out, nativeReferenceRecord{filepath.Join(root, name), maximum, false})
	}
	return out
}

// ObserveBackup always runs the original complete payload/native-archive verifier.
// The reference contains declarations/hashes, not an independent signed-profile
// verdict; the scope must retain its own signed release and class verification.
func ObserveBackup(ctx context.Context, r release.PostgreSQLBackupRequest, expected string) (BackupObservation, error) {
	if r.EffectiveApplicationProfile() != release.MVPPostgreSQLCPUApplicationProfile {
		b, e := Verify(ctx, r, expected, true)
		return BackupObservation{Readback: b}, e
	}
	if ctx == nil || !r.Valid() || !release.ValidSHA256(expected) {
		return BackupObservation{}, errors.New("POSTGRES_BACKUP_NATIVE_REFERENCE_INVALID")
	}
	var out BackupObservation
	e := withNativeReferenceRecords(ctx, backupReferenceRecords(r.BackupRoot, r.Policy.MaximumManifestBytes), func() error {
		b, e := Verify(ctx, r, expected, true)
		if e != nil {
			return e
		}
		reference, _, e := projectNativeBackupReference(r, expected)
		if e != nil {
			return e
		}
		out = BackupObservation{Readback: b, NativePostgreSQL: &reference}
		return nil
	})
	if e != nil {
		return BackupObservation{}, e
	}
	return out, nil
}

// This metadata-only helper is private and is called only after the same
// invocation's original complete verifier, while all referenced records stay pinned.
func projectNativeBackupReference(r release.PostgreSQLBackupRequest, expected string) (release.NativePostgreSQLBackupReference, Manifest, error) {
	var empty release.NativePostgreSQLBackupReference
	var m Manifest
	bad := errors.New("POSTGRES_BACKUP_NATIVE_REFERENCE_INVALID")
	raw, e := readPrivateDocument(filepath.Join(r.BackupRoot, candidateName), r.Policy.MaximumManifestBytes, &m)
	if e != nil || digest(raw) != expected || m.Format != "projectone.postgres.matched-backup.v1" || !reflect.DeepEqual(m.Request, r) || m.RequestSHA256 != requestDigest(r) || !matchesDatabaseProfile(r.EffectiveApplicationProfile(), m.Database) || validMVPSourceBinding(m) != nil || m.MVPSource == nil {
		return empty, m, bad
	}
	var prior Readback
	priorRaw, e := readPrivateDocument(filepath.Join(r.BackupRoot, readbackName), r.Policy.MaximumManifestBytes, &prior)
	if e != nil || prior.Format != "projectone.postgres.independent-readback.v1" || prior.BackupID != r.BackupID || prior.RequestSHA256 != m.RequestSHA256 || prior.ManifestSHA256 != expected || prior.ArchiveSHA256 != m.Archive.SHA256 || !release.ValidSHA256(prior.ArchiveDirectorySHA256) || prior.CurrentLockHeld {
		return empty, m, bad
	}
	if _, e = time.Parse(time.RFC3339Nano, prior.ObservedAt); e != nil {
		return empty, m, bad
	}
	var complete Completion
	completeRaw, e := readPrivateDocument(filepath.Join(r.BackupRoot, completeName), r.Policy.MaximumManifestBytes, &complete)
	if e != nil || complete.Format != "projectone.postgres.backup-complete.v1" || complete.BackupID != r.BackupID || complete.ManifestSHA256 != expected || complete.ReadbackSHA256 != digest(priorRaw) {
		return empty, m, bad
	}
	if _, e = time.Parse(time.RFC3339Nano, complete.CompletedAt); e != nil {
		return empty, m, bad
	}
	out := release.NativePostgreSQLBackupReference{FormatVersion: 1, ApplicationProfile: r.EffectiveApplicationProfile(), SourceManifestSHA256: r.SourceManifestSHA256, SchemaVersion: m.Database.SchemaVersion, TableCount: m.Database.Tables, RequestSHA256: m.RequestSHA256, CandidateSHA256: expected, CompletionSHA256: digest(completeRaw), ReadbackSHA256: digest(priorRaw), MVPSourceSHA256: valueDigest(m.MVPSource), RegistryConfigured: m.MVPSource.RegistryConfigured}
	if payload := m.MVPSource.Registry; payload != nil {
		receipt := payload.Receipt
		out.Registry = &release.NativePostgreSQLRegistryReference{ReceiptSHA256: valueDigest(receipt), ManifestSHA256: receipt.ManifestSHA256, PayloadTreeSHA256: payload.Tree.SHA256, Pins: receipt.Pins, Files: receipt.Files, Bytes: receipt.Bytes}
	}
	if !out.Valid() {
		return empty, m, bad
	}
	return out, m, nil
}

// ObserveRestore retains the original selected pool and installation session.
// An absent historical completion remains a valid readonly observation with no
// completed-restore reference. It never imports, invalidates, grants or publishes.
func ObserveRestore(ctx context.Context, pool *pgxpool.Pool, lease *pgxpool.Conn, operator, runtime config.Config, operatorPath, operatorEnvironment, requestPath, policyEnvironment string, r release.PostgreSQLRestoreRequest) (RestoreObservationWithReference, error) {
	if r.EffectiveApplicationProfile() != release.MVPPostgreSQLCPUApplicationProfile {
		b, e := VerifyRestore(ctx, pool, lease, operator, runtime, operatorPath, operatorEnvironment, requestPath, policyEnvironment, r)
		return RestoreObservationWithReference{RestoreObservation: b}, e
	}
	if ctx == nil || pool == nil || lease == nil || !r.Valid() {
		return RestoreObservationWithReference{}, errors.New("POSTGRES_RESTORE_NATIVE_REFERENCE_INVALID")
	}
	records := backupReferenceRecords(r.BackupRoot, r.Policy.MaximumManifestBytes)
	for _, name := range []string{restoreIntentName, restoreFilesName, "independent-files-readback.json"} {
		records = append(records, nativeReferenceRecord{filepath.Join(r.RestoreRoot, name), r.Policy.MaximumManifestBytes, false})
	}
	records = append(records, nativeReferenceRecord{filepath.Join(r.RestoreRoot, "runtime-context.json"), r.Policy.MaximumManifestBytes, true})
	records = append(records, nativeReferenceRecord{filepath.Join(r.RestoreRoot, restoreCompleteName), r.Policy.MaximumManifestBytes, true})
	var out RestoreObservationWithReference
	e := withNativeReferenceRecords(ctx, records, func() error {
		b, e := VerifyRestore(ctx, pool, lease, operator, runtime, operatorPath, operatorEnvironment, requestPath, policyEnvironment, r)
		if e != nil {
			return e
		}
		out = RestoreObservationWithReference{RestoreObservation: b}
		if !b.OriginalCompletionPresent {
			return nil
		}
		reference, e := projectNativeRestoreReference(r, runtime, b)
		if e != nil {
			return e
		}
		out.NativePostgreSQL = &reference
		return nil
	})
	if e != nil {
		return RestoreObservationWithReference{}, e
	}
	return out, nil
}

func projectNativeRestoreReference(r release.PostgreSQLRestoreRequest, runtime config.Config, observed RestoreObservation) (release.NativePostgreSQLRestoreReference, error) {
	var empty release.NativePostgreSQLRestoreReference
	bad := errors.New("POSTGRES_RESTORE_NATIVE_REFERENCE_INVALID")
	var m Manifest
	raw, e := readPrivateDocument(filepath.Join(r.BackupRoot, candidateName), r.Policy.MaximumManifestBytes, &m)
	if e != nil || digest(raw) != r.SourceManifestSHA256 || m.Request.BackupRoot != r.BackupRoot || m.Request.EffectiveApplicationProfile() != r.EffectiveApplicationProfile() {
		return empty, bad
	}
	backup, m, e := projectNativeBackupReference(m.Request, r.SourceManifestSHA256)
	if e != nil {
		return empty, e
	}
	i, f, intentSHA, filesSHA, e := readRestoreRecords(r, m, runtime)
	if e != nil {
		return empty, e
	}
	if observed.Format != "projectone.postgres.restore-observation.v1" || observed.RestoreID != r.RestoreID || observed.IntentSHA256 != intentSHA || observed.FilesSHA256 != filesSHA || observed.ManifestSHA256 != r.SourceManifestSHA256 || observed.TargetBindingSHA256 != targetBinding(i.Target) || !observed.OriginalCompletionPresent || !observed.ReadOnly || observed.CurrentLockHeld {
		return empty, bad
	}
	var c RestoreCompletion
	completeRaw, e := readPrivateDocument(filepath.Join(r.RestoreRoot, restoreCompleteName), r.Policy.MaximumManifestBytes, &c)
	if e != nil || c.Format != "projectone.postgres.restore-complete.v1" || c.RestoreID != r.RestoreID || c.IntentSHA256 != intentSHA || c.FilesSHA256 != filesSHA || c.ManifestSHA256 != r.SourceManifestSHA256 || c.ArchiveSHA256 != m.Archive.SHA256 || c.TargetBindingSHA256 != observed.TargetBindingSHA256 || c.PostDataSHA256 != observed.PostDataSHA256 || c.CurrentLockHeld || !c.RuntimeContext.Valid() || !reflect.DeepEqual(c.RuntimeContext, restoredRuntimeContext(r, m)) {
		return empty, bad
	}
	if _, e = time.Parse(time.RFC3339Nano, c.CompletedAt); e != nil {
		return empty, bad
	}
	var readback RestoreFilesReadback
	if _, e = readPrivateDocument(filepath.Join(r.RestoreRoot, "independent-files-readback.json"), r.Policy.MaximumManifestBytes, &readback); e != nil || !validRestoreFilesReadback(readback, i, intentSHA, filesSHA) || readback.TargetTreesSHA256 != valueDigest(f.TargetTrees) || readback.RoundtripSHA256 != valueDigest(f.Roundtrip) {
		return empty, bad
	}
	out := release.NativePostgreSQLRestoreReference{FormatVersion: 1, ApplicationProfile: r.EffectiveApplicationProfile(), Backup: backup, RequestSHA256: i.RequestSHA256, ManifestSHA256: r.SourceManifestSHA256, IntentSHA256: intentSHA, FilesSHA256: filesSHA, CompletionSHA256: digest(completeRaw), FilesReadbackSHA256: valueDigest(readback), ObservationSHA256: valueDigest(observed), TargetBindingSHA256: observed.TargetBindingSHA256, PostDataSHA256: observed.PostDataSHA256}
	if f.Registry != nil {
		out.Registry = &release.NativePostgreSQLRegistryImportReference{SnapshotReceiptSHA256: valueDigest(f.Registry.Snapshot), ImportReceiptSHA256: valueDigest(f.Registry), ReadbackSHA256: f.Registry.ReadbackSHA256}
	}
	if !out.Valid() {
		return empty, bad
	}
	return out, nil
}

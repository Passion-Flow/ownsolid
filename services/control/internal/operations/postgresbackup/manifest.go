package postgresbackup

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const candidateName = "candidate.json"
const completeName = "complete.json"
const archiveName = "postgres.dump"
const effectiveName = "effective-control.yaml"
const readbackName = "independent-readback.json"

type StoreReadback struct {
	Kind string       `json:"kind"`
	Tree TreeReadback `json:"tree"`
}
type FileReadback struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type Manifest struct {
	Format                 string                          `json:"format"`
	Request                release.PostgreSQLBackupRequest `json:"request"`
	RequestSHA256          string                          `json:"request_sha256"`
	CreatedAt              string                          `json:"created_at"`
	Database               DatabaseReadback                `json:"database"`
	DatabaseSnapshot       string                          `json:"database_snapshot"`
	Sources                []TreeReadback                  `json:"sources"`
	Stores                 []StoreReadback                 `json:"stores"`
	Archive                FileReadback                    `json:"archive"`
	Configuration          TreeReadback                    `json:"configuration"`
	EffectiveConfiguration FileReadback                    `json:"effective_configuration"`
	MVPSource              *MVPSourceBinding               `json:"mvp_source,omitempty"`
}
type Readback struct {
	Format                 string `json:"format"`
	BackupID               string `json:"backup_id"`
	RequestSHA256          string `json:"request_sha256"`
	ManifestSHA256         string `json:"manifest_sha256"`
	ArchiveSHA256          string `json:"archive_sha256"`
	ArchiveDirectorySHA256 string `json:"archive_directory_sha256"`
	ObservedAt             string `json:"observed_at"`
	CurrentLockHeld        bool   `json:"current_lock_held"`
}
type Completion struct {
	Format         string `json:"format"`
	BackupID       string `json:"backup_id"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ReadbackSHA256 string `json:"readback_sha256"`
	CompletedAt    string `json:"completed_at"`
	Qualification  string `json:"qualification"`
}

func requestDigest(r release.PostgreSQLBackupRequest) string {
	raw, e := canonical(r)
	if e != nil {
		return ""
	}
	return digest(raw)
}
func fileDigest(ctx context.Context, path string, maximum int64) (FileReadback, error) {
	var proof FileReadback
	root, e := openPrivateRoot(filepath.Dir(path))
	if e != nil {
		return proof, e
	}
	defer root.Close()
	info, e := root.Lstat(filepath.Base(path))
	if e != nil || !owned(info, false) || info.Size() < 1 || info.Size() > maximum {
		return proof, errors.New("POSTGRES_BACKUP_FILE_INVALID")
	}
	// privateArchive pins and hashes before and after consumption.
	file, e := root.Open(filepath.Base(path))
	if e != nil {
		return proof, errors.New("POSTGRES_BACKUP_FILE_INVALID")
	}
	sha, n, e := hashStream(ctx, nil, file, maximum)
	ce := file.Close()
	if e != nil || ce != nil || n != info.Size() {
		return proof, errors.New("POSTGRES_BACKUP_FILE_CHANGED")
	}
	if e = privateArchive(ctx, path, maximum, sha, func(*os.File) error { return nil }); e != nil {
		return proof, e
	}
	return FileReadback{sha, n}, nil
}

func snapshotTrees(ctx context.Context, r release.PostgreSQLBackupRequest) ([]StoreReadback, error) {
	p := r.Policy
	out := []StoreReadback{}
	var bytes int64
	files := 0
	for _, kind := range []string{"models", "datasets", "training", "preparations"} {
		proof, e := TreeDigest(ctx, filepath.Join(r.BackupRoot, "stores", kind), p.MaximumStoreBytes-bytes, p.MaximumStoreFiles-files, p.MaximumManifestBytes)
		if e != nil {
			return nil, e
		}
		out = append(out, StoreReadback{kind, proof})
		bytes += proof.Bytes
		files += proof.Files
	}
	return out, nil
}

func expectedRoot(root *os.Root, complete bool) error {
	return expectedRootProfile(root, complete, database.LineageName)
}

func expectedRootProfile(root *os.Root, complete bool, profile string) error {
	f, e := root.Open(".")
	if e != nil {
		return errors.New("POSTGRES_BACKUP_ROOT_INVALID")
	}
	defer f.Close()
	expected := map[string]bool{"stores": true, "configuration": true, candidateName: true, archiveName: true, effectiveName: true}
	if profile == database.LineageName {
		expected[mvpArtifactsDirectory] = true
	}
	if complete {
		expected[readbackName] = true
		expected[completeName] = true
	}
	entries, e := f.ReadDir(len(expected) + 1)
	if e != nil || len(entries) != len(expected) {
		return errors.New("POSTGRES_BACKUP_ROOT_INVENTORY_INVALID")
	}
	for _, entry := range entries {
		if !expected[entry.Name()] {
			return errors.New("POSTGRES_BACKUP_ROOT_INVENTORY_INVALID")
		}
		delete(expected, entry.Name())
	}
	stores, e := root.Open("stores")
	if e != nil {
		return errors.New("POSTGRES_BACKUP_ROOT_INVENTORY_INVALID")
	}
	defer stores.Close()
	entries, e = stores.ReadDir(5)
	if e != nil || len(entries) != 4 {
		return errors.New("POSTGRES_BACKUP_ROOT_INVENTORY_INVALID")
	}
	want := map[string]bool{"models": true, "datasets": true, "training": true, "preparations": true}
	for _, entry := range entries {
		if !entry.IsDir() || !want[entry.Name()] {
			return errors.New("POSTGRES_BACKUP_ROOT_INVENTORY_INVALID")
		}
		delete(want, entry.Name())
	}
	return nil
}

// Verify is read-only, opens no SQL connection or source store, and cannot
// publish a completion. The writer runs it in a separate pinned Control process.
func Verify(ctx context.Context, r release.PostgreSQLBackupRequest, expected string, complete bool) (Readback, error) {
	return verifyPayload(ctx, r, expected, complete, r.Policy)
}

// The historical payload request remains exact. A restore reader may use its
// own current pinned same-version/platform clients, without rewriting history.
func verifyPayload(ctx context.Context, r release.PostgreSQLBackupRequest, expected string, complete bool, tools release.PostgreSQLBackupPolicy) (Readback, error) {
	bad := errors.New("POSTGRES_BACKUP_READBACK_MISMATCH")
	if ctx == nil || !r.Valid() || !tools.Valid() || tools.ExpectedVersion != r.Policy.ExpectedVersion || !release.ValidSHA256(expected) {
		return Readback{}, bad
	}
	if e := verifyTools(ctx, tools, true); e != nil {
		return Readback{}, e
	}
	root, e := openPrivateRoot(r.BackupRoot)
	if e != nil {
		return Readback{}, e
	}
	defer root.Close()
	if e = expectedRootProfile(root, complete, r.EffectiveApplicationProfile()); e != nil {
		return Readback{}, e
	}
	var m Manifest
	raw, e := readPrivateDocument(filepath.Join(r.BackupRoot, candidateName), r.Policy.MaximumManifestBytes, &m)
	if e != nil || digest(raw) != expected || m.Format != "projectone.postgres.matched-backup.v1" || !reflect.DeepEqual(m.Request, r) || m.RequestSHA256 != requestDigest(r) || m.Database.TenantID != r.TenantID || m.Database.Database != r.Database || m.Database.Operator != r.Operator || !matchesDatabaseProfile(r.EffectiveApplicationProfile(), m.Database) || !snapshotPattern.MatchString(m.DatabaseSnapshot) || len(m.Sources) != 4 || !release.ValidSHA256(m.Database.ContentSHA256) {
		return Readback{}, bad
	}
	if e = validMVPSourceBinding(m); e != nil {
		return Readback{}, e
	}
	if _, e = time.Parse(time.RFC3339Nano, m.CreatedAt); e != nil {
		return Readback{}, bad
	}
	stores, e := snapshotTrees(ctx, r)
	if e != nil || !reflect.DeepEqual(stores, m.Stores) {
		return Readback{}, bad
	}
	configuration, e := TreeDigest(ctx, filepath.Join(r.BackupRoot, "configuration"), r.Policy.MaximumConfigurationBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes)
	if e != nil || configuration != m.Configuration {
		return Readback{}, bad
	}
	effective, e := fileDigest(ctx, filepath.Join(r.BackupRoot, effectiveName), r.Policy.MaximumConfigurationBytes)
	if e != nil || effective != m.EffectiveConfiguration {
		return Readback{}, bad
	}
	if e = verifyMVPRegistryPayload(ctx, m); e != nil {
		return Readback{}, e
	}
	archive, e := fileDigest(ctx, filepath.Join(r.BackupRoot, archiveName), r.Policy.MaximumSQLBytes)
	if e != nil || archive != m.Archive {
		return Readback{}, bad
	}
	listing := &limitedBuffer{maximum: r.Policy.MaximumManifestBytes}
	e = privateArchive(ctx, filepath.Join(r.BackupRoot, archiveName), r.Policy.MaximumSQLBytes, m.Archive.SHA256, func(f *os.File) error {
		return runNative(ctx, tools, tools.RestoreFile, []string{"--list"}, nil, f, listing)
	})
	if e != nil {
		return Readback{}, e
	}
	if !strings.Contains(listing.String(), " TABLE public installation_state ") || !strings.Contains(listing.String(), " TABLE public schema_migrations ") {
		return Readback{}, errors.New("POSTGRES_BACKUP_ARCHIVE_CATALOG_MISSING")
	}
	if e = trustedTool(ctx, tools.RestoreFile, tools.RestoreSHA256); e != nil {
		return Readback{}, e
	}
	if ctx.Err() != nil {
		return Readback{}, errors.New("POSTGRES_BACKUP_INTERRUPTED")
	}
	readback := Readback{"projectone.postgres.independent-readback.v1", r.BackupID, m.RequestSHA256, expected, m.Archive.SHA256, digest(listing.Bytes()), time.Now().UTC().Format(time.RFC3339Nano), false}
	if complete {
		var prior Readback
		raw, e := readPrivateDocument(filepath.Join(r.BackupRoot, readbackName), r.Policy.MaximumManifestBytes, &prior)
		if e != nil {
			return Readback{}, bad
		}
		var c Completion
		_, e = readPrivateDocument(filepath.Join(r.BackupRoot, completeName), r.Policy.MaximumManifestBytes, &c)
		if e != nil || c.Format != "projectone.postgres.backup-complete.v1" || c.BackupID != r.BackupID || c.ManifestSHA256 != expected || c.ReadbackSHA256 != digest(raw) || prior.Format != readback.Format || prior.BackupID != readback.BackupID || prior.RequestSHA256 != readback.RequestSHA256 || prior.ManifestSHA256 != expected || prior.ArchiveSHA256 != readback.ArchiveSHA256 || prior.ArchiveDirectorySHA256 != readback.ArchiveDirectorySHA256 || prior.CurrentLockHeld {
			return Readback{}, bad
		}
		if _, e = time.Parse(time.RFC3339Nano, c.CompletedAt); e != nil {
			return Readback{}, bad
		}
		if _, e = time.Parse(time.RFC3339Nano, prior.ObservedAt); e != nil {
			return Readback{}, bad
		}
	}
	return readback, nil
}

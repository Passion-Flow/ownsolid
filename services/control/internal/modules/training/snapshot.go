package training

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func snapshotAllowed(files []SnapshotFile, includeManifest bool) (map[string]bool, map[string]bool) {
	allowed := map[string]bool{}
	directories := map[string]bool{".": true}
	if includeManifest {
		allowed[snapshotFile] = true
	}
	for _, file := range files {
		allowed[file.Path] = true
		for parent := path.Dir(file.Path); parent != "."; parent = path.Dir(parent) {
			directories[parent] = true
		}
	}
	return allowed, directories
}

// ExportFiles requires the caller's exclusive installation lock and stopped owning runtime.
// It exports accepted immutable files only, never unfinished upload/scratch stages or execution authority.
func (s *Service) ExportFiles(ctx context.Context, destination string) error {
	if s.Store == nil {
		return fail("TRAINING_STORAGE_REQUIRED")
	}
	if relatedSnapshotPaths(s.Store.path, destination) {
		return fail("TRAINING_STORAGE_PATH_INVALID")
	}
	bounded, stop := context.WithTimeout(ctx, time.Duration(s.policy().SnapshotSeconds)*time.Second)
	defer stop()
	if e := s.VerifyStored(bounded); e != nil {
		return e
	}
	references, e := s.snapshotReferences(bounded)
	if e != nil {
		return e
	}
	expected, e := snapshotFileSet(references)
	if e != nil {
		return e
	}
	var total int64
	for name, file := range expected {
		if file.SizeBytes < 0 {
			maximum := int64(wire.MaximumCheckpointMetadataBytes)
			if name == namespaceFile {
				maximum = maximumNamespaceBytes
			}
			file.SHA256, file.SizeBytes, e = snapshotDigest(bounded, s.Store.path, SnapshotFile{Path: name, SizeBytes: maximum})
			if e != nil {
				return e
			}
			expected[name] = file
		}
		total += file.SizeBytes
		if total > s.policy().SnapshotMaximumBytes {
			return fail("TRAINING_SNAPSHOT_LIMIT_REACHED")
		}
	}
	snapshot := FileSnapshot{SchemaVersion: 1, Namespace: s.Store.namespace, References: references, Files: sortedSnapshotFiles(expected)}
	raw, e := json.Marshal(snapshot)
	if e != nil || int64(len(raw)) > s.policy().SnapshotManifestBytes {
		return fail("TRAINING_SNAPSHOT_LIMIT_REACHED")
	}
	if e = snapshotSpace(filepath.Dir(destination), total+int64(len(raw)), s.policy().MinimumFreeBytes); e != nil {
		return e
	}
	if e = newSnapshotDirectory(destination); e != nil {
		return e
	}
	for _, file := range snapshot.Files {
		if e = copySnapshotFile(bounded, s.Store.path, destination, file); e != nil {
			return e
		}
	}
	latest, e := s.snapshotReferences(bounded)
	if e != nil {
		return e
	}
	if !referencesEqual(references, latest) {
		return fail("TRAINING_SNAPSHOT_REFERENCE_CHANGED")
	}
	allowed, dirs := snapshotAllowed(snapshot.Files, false)
	if e = walkSnapshot(bounded, destination, allowed, dirs, len(allowed)+len(dirs), true); e != nil {
		return e
	}
	return writeSnapshotFile(destination, snapshotFile, raw)
}
func (s *Service) readSnapshot(ctx context.Context, source string) (FileSnapshot, error) {
	var snapshot FileSnapshot
	if _, e := ownedPath(source, true, 0); e != nil {
		return snapshot, fail("TRAINING_SNAPSHOT_INVALID")
	}
	if _, e := ownedPath(filepath.Join(source, snapshotFile), false, s.policy().SnapshotManifestBytes); e != nil {
		return snapshot, fail("TRAINING_SNAPSHOT_INVALID")
	}
	raw, e := os.ReadFile(filepath.Join(source, snapshotFile))
	if e != nil || wire.DecodeStrict(raw, &snapshot) != nil || snapshot.SchemaVersion != 1 || snapshot.Namespace.SchemaVersion != 1 || snapshot.Namespace.Kind != "training" || !datawire.ValidID(snapshot.Namespace.StoreID) || snapshot.Namespace.TenantID != snapshot.References.TenantID {
		return snapshot, fail("TRAINING_SNAPSHOT_INVALID")
	}
	expected, e := snapshotFileSet(snapshot.References)
	if e != nil {
		return snapshot, e
	}
	if len(snapshot.Files) != len(expected) || len(snapshot.Files) > s.policy().SnapshotMaximumFiles {
		return snapshot, fail("TRAINING_SNAPSHOT_INVALID")
	}
	previous := ""
	var total int64
	for _, file := range snapshot.Files {
		required, ok := expected[file.Path]
		if !ok || file.Path <= previous || file.SizeBytes < 0 || file.SizeBytes > wire.MaximumCheckpointBytes || required.SizeBytes >= 0 && required.SizeBytes != file.SizeBytes || required.SHA256 != "" && required.SHA256 != file.SHA256 {
			return snapshot, fail("TRAINING_SNAPSHOT_INVALID")
		}
		previous = file.Path
		delete(expected, file.Path)
		if file.Path == namespaceFile && file.SizeBytes > maximumNamespaceBytes || path.Base(file.Path) == publicationIndex && file.SizeBytes > wire.MaximumCheckpointMetadataBytes {
			return snapshot, fail("TRAINING_SNAPSHOT_INVALID")
		}
		total += file.SizeBytes
		if total > s.policy().SnapshotMaximumBytes {
			return snapshot, fail("TRAINING_SNAPSHOT_LIMIT_REACHED")
		}
		sha, n, e := snapshotDigest(ctx, source, file)
		if e != nil || sha != file.SHA256 || n != file.SizeBytes {
			return snapshot, fail("TRAINING_SNAPSHOT_INVALID")
		}
	}
	allowed, dirs := snapshotAllowed(snapshot.Files, true)
	if e = walkSnapshot(ctx, source, allowed, dirs, len(allowed)+len(dirs), false); e != nil {
		return snapshot, e
	}
	marker, e := os.ReadFile(filepath.Join(source, namespaceFile))
	var namespace storeNamespace
	if e != nil || wire.DecodeStrict(marker, &namespace) != nil || namespace != snapshot.Namespace {
		return snapshot, fail("TRAINING_SNAPSHOT_INVALID")
	}
	root, e := os.OpenRoot(source)
	if e != nil {
		return snapshot, fail("TRAINING_SNAPSHOT_INVALID")
	}
	defer root.Close()
	view := &Store{root: root, path: source, namespace: namespace, maximumBytes: wire.MaximumCheckpointBytes, maximumFiles: wire.MaximumCheckpointFiles}
	for _, descriptor := range snapshot.References.Outputs {
		if e = view.verifyPublished(ctx, descriptor); e != nil {
			return snapshot, e
		}
	}
	return snapshot, nil
}
func (s *Service) ImportFiles(ctx context.Context, source, destination string) error {
	if s.Store != nil {
		return fail("TRAINING_STORE_IN_USE")
	}
	if relatedSnapshotPaths(source, destination) {
		return fail("TRAINING_STORAGE_PATH_INVALID")
	}
	bounded, stop := context.WithTimeout(ctx, time.Duration(s.policy().SnapshotSeconds)*time.Second)
	defer stop()
	snapshot, e := s.readSnapshot(bounded, source)
	if e != nil {
		return e
	}
	actual, e := s.snapshotReferences(bounded)
	if e != nil {
		return e
	}
	if !referencesEqual(snapshot.References, actual) {
		return fail("TRAINING_SNAPSHOT_REFERENCE_CHANGED")
	}
	if _, e = ownedPath(destination, true, 0); e != nil {
		return e
	}
	dir, e := os.Open(destination)
	if e != nil {
		return fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	entries, e := dir.ReadDir(1)
	dir.Close()
	if len(entries) > 0 {
		return fail("TRAINING_SNAPSHOT_DESTINATION_EXISTS")
	}
	if e != nil && e != io.EOF {
		return fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	var total int64
	for _, file := range snapshot.Files {
		total += file.SizeBytes
	}
	if e = snapshotSpace(destination, total, s.policy().MinimumFreeBytes); e != nil {
		return e
	}
	for _, name := range []string{"uploads", "checkpoints", "artifacts"} {
		if os.Mkdir(filepath.Join(destination, name), 0700) != nil {
			return fail("TRAINING_SNAPSHOT_DESTINATION_EXISTS")
		}
	}
	for _, file := range snapshot.Files {
		if e = copySnapshotFile(bounded, source, destination, file); e != nil {
			return e
		}
	}
	if e = writeSnapshotFile(destination, ownerLock, []byte{}); e != nil {
		return e
	}
	allowed, dirs := snapshotAllowed(snapshot.Files, false)
	allowed[ownerLock] = true
	for _, name := range []string{"uploads", "checkpoints", "artifacts"} {
		dirs[name] = true
	}
	if e = walkSnapshot(bounded, destination, allowed, dirs, len(allowed)+len(dirs), true); e != nil {
		return e
	}
	store, e := OpenStore(destination, snapshot.Namespace.TenantID, s.policy().MinimumFreeBytes, wire.MaximumCheckpointBytes, wire.MaximumCheckpointFiles)
	if e != nil {
		return e
	}
	defer store.Close()
	verifier := &Service{DB: s.DB, Identity: s.Identity, Store: store}
	if e = verifier.VerifyStored(bounded); e != nil {
		return e
	}
	latest, e := s.snapshotReferences(bounded)
	if e != nil {
		return e
	}
	if !referencesEqual(latest, snapshot.References) {
		return fail("TRAINING_SNAPSHOT_REFERENCE_CHANGED")
	}
	return syncSnapshotDirectory(destination)
}

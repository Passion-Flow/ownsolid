package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/snapshotio"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	lifecyclewire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

const MaximumSnapshotMetadataBytes = 32 * 1024 * 1024

type SnapshotModel struct {
	Kind           string                        `json:"kind,omitempty"`
	DerivedCatalog *lifecyclewire.DerivedCatalog `json:"derived_catalog,omitempty"`
	ID             string                        `json:"id"`
	WorkspaceID    string                        `json:"workspace_id"`
	State          string                        `json:"state"`
	CatalogDigest  string                        `json:"catalog_digest"`
}
type FileSnapshot struct {
	SchemaVersion int             `json:"schema_version"`
	TenantID      string          `json:"tenant_id"`
	Models        []SnapshotModel `json:"models"`
}

// inventory describes immutable version references, not a database backup or credential export.
func (s *Service) inventory(ctx context.Context) (FileSnapshot, error) {
	return s.fileInventory(ctx, false)
}

func verifyObjectsParent(path string) error {
	info, e := os.Lstat(filepath.Join(path, "objects"))
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return fail("MODEL_PATH_INVALID")
	}
	return nil
}

func newSnapshotDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fail("MODEL_PATH_INVALID")
	}
	parent, e := openStore(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer parent.Close()
	if e = parent.Mkdir(filepath.Base(path), 0700); e != nil {
		return fail("MODEL_SNAPSHOT_DESTINATION_EXISTS")
	}
	return syncDirectory(parent, ".")
}
func copyModel(ctx context.Context, source, destination string) error {
	if e := modelartifact.VerifyDirectory(ctx, source); e != nil {
		return e
	}
	if e := os.Mkdir(destination, 0700); e != nil {
		return fail("MODEL_SNAPSHOT_DESTINATION_EXISTS")
	}
	sourceRoot, e := os.OpenRoot(source)
	if e != nil {
		return fail("MODEL_IO_FAILED")
	}
	defer sourceRoot.Close()
	for _, descriptor := range wire.ApprovedModel().Files {
		if ctx.Err() != nil {
			return fail("MODEL_CANCELLED")
		}
		input, e := sourceRoot.Open(descriptor.Path)
		if e != nil {
			return fail("MODEL_IO_FAILED")
		}
		output, e := os.OpenFile(filepath.Join(destination, descriptor.Path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			input.Close()
			return fail("MODEL_IO_FAILED")
		}
		hash := sha256.New()
		count, copyError := snapshotio.Copy(output, hash, contextReader{ctx, io.LimitReader(input, descriptor.SizeBytes+1)})
		input.Close()
		if copyError == nil {
			copyError = output.Chmod(0444)
		}
		if copyError == nil {
			copyError = output.Sync()
		}
		closed := output.Close()
		if copyError != nil || closed != nil || count != descriptor.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != descriptor.SHA256 {
			return fail("MODEL_SNAPSHOT_INVALID")
		}
	}
	root, e := os.OpenRoot(destination)
	if e != nil {
		return fail("MODEL_IO_FAILED")
	}
	defer root.Close()
	return syncDirectory(root, ".")
}

// ExportFiles requires the existing installation process lock and a stopped control process.
// Pair this immutable-file snapshot with a database-aware export while control remains stopped.
func (s *Service) ExportFiles(ctx context.Context, destination string) error {
	inventory, e := s.fileInventory(ctx, true)
	if e != nil {
		return e
	}

	source, e := openStore(s.RootDirectory)
	if e != nil {
		return e
	}
	source.Close()
	if hasBaseSnapshotModels(inventory) {
		if e = verifyObjectsParent(s.RootDirectory); e != nil {
			return e
		}
	}
	if destination == s.RootDirectory || stringsRelated(destination, s.RootDirectory) {
		return fail("MODEL_PATH_INVALID")
	}
	if e = newSnapshotDirectory(destination); e != nil {
		return e
	}
	backup, e := openStore(destination)
	if e != nil {
		return e
	}
	defer backup.Close()
	if e = backup.Mkdir("objects", 0700); e != nil {
		return fail("MODEL_IO_FAILED")
	}
	for _, model := range inventory.Models {
		if e = copySnapshotModel(ctx, s.RootDirectory, destination, model); e != nil {
			return e
		}
	}
	file, e := backup.OpenFile("model-files.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return fail("MODEL_IO_FAILED")
	}
	e = json.NewEncoder(file).Encode(inventory)
	if e == nil {
		e = file.Sync()
	}
	closed := file.Close()
	if e != nil || closed != nil {
		return fail("MODEL_IO_FAILED")
	}
	if e = syncDirectory(backup, "objects"); e != nil {
		return e
	}
	return syncDirectory(backup, ".")
}
func stringsRelated(a, b string) bool {
	relative, e := filepath.Rel(a, b)
	if e == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return true
	}
	relative, e = filepath.Rel(b, a)
	return e == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

// ImportFiles accepts only a newly initialized empty private store and matching restored DB metadata.
// Failures retain the new partial scope for review; no existing file is overwritten or pruned.
func (s *Service) ImportFiles(ctx context.Context, snapshot string) error {
	expected, e := s.inventory(ctx)
	if e != nil {
		return e
	}
	source, e := openStore(snapshot)
	if e != nil {
		return e
	}
	defer source.Close()
	info, e := source.Lstat("model-files.json")
	if e != nil || !info.Mode().IsRegular() || info.Size() > MaximumSnapshotMetadataBytes || info.Mode().Perm()&0077 != 0 {
		return fail("MODEL_SNAPSHOT_INVALID")
	}
	file, e := source.Open("model-files.json")
	if e != nil {
		return fail("MODEL_SNAPSHOT_INVALID")
	}
	raw, e := io.ReadAll(io.LimitReader(file, MaximumSnapshotMetadataBytes+1))
	file.Close()
	if e != nil || len(raw) > MaximumSnapshotMetadataBytes {
		return fail("MODEL_SNAPSHOT_INVALID")
	}
	var inventory FileSnapshot
	if lifecyclewire.DecodeStrict(raw, &inventory) != nil || !reflect.DeepEqual(inventory, expected) {
		return fail("MODEL_SNAPSHOT_DATABASE_MISMATCH")
	}
	if stringsRelated(snapshot, s.RootDirectory) {
		return fail("MODEL_PATH_INVALID")
	}
	root, e := openStore(s.RootDirectory)
	if e != nil {
		return e
	}
	defer root.Close()
	directory, e := root.Open(".")
	if e != nil {
		return fail("MODEL_IO_FAILED")
	}
	entries, readError := directory.ReadDir(1)
	directory.Close()
	if len(entries) != 0 {
		return fail("MODEL_STORE_NOT_EMPTY")
	}
	if readError != nil && readError != io.EOF {
		return fail("MODEL_IO_FAILED")
	}
	if hasBaseSnapshotModels(inventory) {
		if e = verifyObjectsParent(snapshot); e != nil {
			return e
		}
	}
	// Validate every source before creating target files; an incomplete backup is never accepted as ready.
	for _, model := range inventory.Models {
		if e = verifySnapshotModel(ctx, snapshot, model); e != nil {
			return e
		}
	}
	if e = root.Mkdir("objects", 0700); e != nil {
		return fail("MODEL_IO_FAILED")
	}
	for _, model := range inventory.Models {
		if e = copySnapshotModel(ctx, snapshot, s.RootDirectory, model); e != nil {
			return e
		}
	}
	if e = syncDirectory(root, "objects"); e != nil {
		return e
	}
	return syncDirectory(root, ".")
}

// VerifyStoredVersions gates startup after restore; file hashes are also checked during each transfer/cache load.
func (s *Service) VerifyStoredVersions(ctx context.Context) error {
	inventory, e := s.inventory(ctx)
	if e != nil {
		return e
	}
	ready := false
	for _, model := range inventory.Models {
		if model.State == "ready" {
			ready = true
		}
	}
	if s.RootDirectory == "" && !ready {
		return nil
	}
	root, e := openStore(s.RootDirectory)
	if e != nil {
		return e
	}
	root.Close()
	if ready && hasBaseReadySnapshotModels(inventory) {
		if e = verifyObjectsParent(s.RootDirectory); e != nil {
			return e
		}
	}
	for _, model := range inventory.Models {
		if model.State == "ready" {
			if e = verifySnapshotModel(ctx, s.RootDirectory, model); e != nil {
				return e
			}
		}
	}
	return nil
}

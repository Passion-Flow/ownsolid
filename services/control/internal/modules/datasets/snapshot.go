package datasets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/snapshotio"
)

func relatedDirectories(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}
func requireSnapshotSpace(directory string, required, minimum int64) error {
	file, e := os.Open(directory)
	if e != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer file.Close()
	var stat syscall.Statfs_t
	if syscall.Fstatfs(int(file.Fd()), &stat) != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	available := uint64(stat.Bavail) * uint64(stat.Bsize)
	if required < 0 || minimum < 0 || uint64(required) > available || uint64(minimum) > available-uint64(required) {
		return failure("DATASET_STORAGE_FULL")
	}
	return nil
}
func newSnapshotDirectory(destination string) error {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return failure("DATASET_STORAGE_PATH_INVALID")
	}
	parent := filepath.Dir(destination)
	if _, e := privatePath(parent, "", true, 0); e != nil {
		return failure("DATASET_STORAGE_PATH_INVALID")
	}
	if os.Mkdir(destination, 0700) != nil {
		return failure("DATASET_SNAPSHOT_DESTINATION_EXISTS")
	}
	directory, e := os.Open(parent)
	if e != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer directory.Close()
	if directory.Sync() != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	return nil
}
func snapshotParents(root string, name string) error {
	current := root
	for _, part := range strings.Split(path.Dir(name), "/") {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if e := os.Mkdir(current, 0700); e != nil && !os.IsExist(e) {
			return failure("DATASET_STORAGE_UNAVAILABLE")
		}
		if _, e := privatePath(current, root, true, 0); e != nil {
			return failure("DATASET_STORAGE_PATH_INVALID")
		}
	}
	return nil
}
func copySnapshotFile(ctx context.Context, source, destination string, file SnapshotFile) error {
	inputPath := filepath.Join(source, filepath.FromSlash(file.Path))
	if _, e := privatePath(inputPath, source, false, file.SizeBytes); e != nil {
		return failure("DATASET_SNAPSHOT_INVALID")
	}
	if e := snapshotParents(destination, file.Path); e != nil {
		return e
	}
	input, e := os.Open(inputPath)
	if e != nil {
		return failure("DATASET_SNAPSHOT_INVALID")
	}
	defer input.Close()
	output, e := os.OpenFile(filepath.Join(destination, filepath.FromSlash(file.Path)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return failure("DATASET_SNAPSHOT_DESTINATION_EXISTS")
	}
	hash := sha256.New()
	n, copyError := snapshotio.Copy(output, hash, boundedContextReader{ctx, io.LimitReader(input, file.SizeBytes+1)})
	if copyError == nil {
		copyError = output.Sync()
	}
	closeError := output.Close()
	if copyError != nil || closeError != nil || n != file.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
		return failure("DATASET_SNAPSHOT_INVALID")
	}
	return nil
}
func syncTree(root string) error {
	directories := []string{}
	e := filepath.WalkDir(root, func(path string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return failure("DATASET_SNAPSHOT_INVALID")
		}
		if entry.IsDir() {
			directories = append(directories, path)
		}
		return nil
	})
	if e != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	for i := len(directories) - 1; i >= 0; i-- {
		dir, e := os.Open(directories[i])
		if e != nil {
			return failure("DATASET_STORAGE_UNAVAILABLE")
		}
		e = dir.Sync()
		dir.Close()
		if e != nil {
			return failure("DATASET_STORAGE_UNAVAILABLE")
		}
	}
	return nil
}

// ExportFiles requires the operator's installation lock and stopped owning runtime. Pair it with a database-aware logical export in that same maintenance scope.
func (s *Service) ExportFiles(ctx context.Context, destination string) error {
	if s.Store == nil {
		return failure("DATASET_STORAGE_REQUIRED")
	}
	if relatedDirectories(s.Store.path, destination) {
		return failure("DATASET_STORAGE_PATH_INVALID")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(s.policy().SnapshotSeconds)*time.Second)
	defer cancel()
	if e := s.VerifyStored(bounded); e != nil {
		return e
	}
	references, e := s.snapshotReferences(bounded)
	if e != nil {
		return e
	}
	expected, e := referenceFiles(references)
	if e != nil {
		return e
	}
	markerPath := filepath.Join(s.Store.path, markerFile)
	hash, size, e := contextFileDigest(bounded, markerPath, 4096)
	if e != nil {
		return e
	}
	expected[markerFile] = SnapshotFile{markerFile, hash, size}
	if len(expected) > s.policy().SnapshotMaximumFiles {
		return failure("DATASET_SNAPSHOT_LIMIT_REACHED")
	}
	var total int64
	for name, file := range expected {
		if file.SizeBytes < 0 {
			maximum := wire.MaximumResultBytesCeiling
			if path.Base(name) == "publication.json" {
				maximum = 4096
			}
			hash, size, e := contextFileDigest(bounded, filepath.Join(s.Store.path, filepath.FromSlash(name)), maximum)
			if e != nil || file.SHA256 != "" && file.SHA256 != hash {
				return failure("DATASET_SNAPSHOT_INVALID")
			}
			file.SHA256 = hash
			file.SizeBytes = size
			expected[name] = file
		}
		total += file.SizeBytes
		if total > s.policy().SnapshotMaximumBytes {
			return failure("DATASET_SNAPSHOT_LIMIT_REACHED")
		}
	}
	snapshot := FileSnapshot{snapshotSchemaVersion(references), s.Store.namespace, references, sortedFiles(expected)}
	raw, e := json.Marshal(snapshot)
	if e != nil || int64(len(raw)+1) > s.policy().SnapshotManifestBytes {
		return failure("DATASET_SNAPSHOT_LIMIT_REACHED")
	}
	if e = requireSnapshotSpace(filepath.Dir(destination), total+int64(len(raw))+1, s.policy().MinimumFreeBytes); e != nil {
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
		return failure("DATASET_SNAPSHOT_REFERENCE_CHANGED")
	}
	if e = writeNew(filepath.Join(destination, snapshotName), append(raw, '\n')); e != nil {
		return e
	}
	return syncTree(destination)
}

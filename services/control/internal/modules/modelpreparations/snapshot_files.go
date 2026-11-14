package modelpreparations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/snapshotio"
)

func relatedSnapshotPaths(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}
func snapshotSpace(directory string, required, minimum int64) error {
	var stats syscall.Statfs_t
	if required < 0 || minimum < 0 || syscall.Statfs(directory, &stats) != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	free := uint64(stats.Bavail) * uint64(stats.Bsize)
	if uint64(required) > free || uint64(minimum) > free-uint64(required) {
		return fail("MODEL_PREPARATION_STORAGE_FULL")
	}
	return nil
}
func syncSnapshotDirectory(directory string) error {
	file, e := os.Open(directory)
	if e != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	defer file.Close()
	if file.Sync() != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	return nil
}
func newSnapshotDirectory(directory string) error {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return fail("MODEL_PREPARATION_STORAGE_PATH_INVALID")
	}
	if _, e := ownedPath(filepath.Dir(directory), true, 0); e != nil {
		return e
	}
	if os.Mkdir(directory, 0700) != nil {
		return fail("MODEL_PREPARATION_SNAPSHOT_DESTINATION_EXISTS")
	}
	return syncSnapshotDirectory(filepath.Dir(directory))
}
func snapshotParents(root, relative string) error {
	current := root
	for _, part := range strings.Split(path.Dir(relative), "/") {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if e := os.Mkdir(current, 0700); e != nil && !os.IsExist(e) {
			return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
		}
		if _, e := ownedPath(current, true, 0); e != nil {
			return e
		}
	}
	return nil
}
func snapshotDigest(ctx context.Context, root string, file SnapshotFile) (string, int64, error) {
	if filepath.IsAbs(file.Path) || filepath.ToSlash(filepath.Clean(file.Path)) != file.Path || file.Path == ".." || strings.HasPrefix(file.Path, "../") {
		return "", 0, fail("MODEL_PREPARATION_SNAPSHOT_INVALID")
	}
	absolute := filepath.Join(root, filepath.FromSlash(file.Path))
	info, e := ownedPath(absolute, false, file.SizeBytes)
	if e != nil {
		return "", 0, e
	}
	input, e := os.OpenFile(absolute, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return "", 0, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	defer input.Close()
	opened, e := input.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return "", 0, fail("MODEL_PREPARATION_SNAPSHOT_INVALID")
	}
	return fileDigest(ctx, input, file.SizeBytes)
}
func copySnapshotFile(ctx context.Context, source, destination string, file SnapshotFile) error {
	inputPath := filepath.Join(source, filepath.FromSlash(file.Path))
	info, e := ownedPath(inputPath, false, file.SizeBytes)
	if e != nil {
		return e
	}
	input, e := os.OpenFile(inputPath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	defer input.Close()
	opened, e := input.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return fail("MODEL_PREPARATION_SNAPSHOT_INVALID")
	}
	if e = snapshotParents(destination, file.Path); e != nil {
		return e
	}
	output, e := os.OpenFile(filepath.Join(destination, filepath.FromSlash(file.Path)), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return fail("MODEL_PREPARATION_SNAPSHOT_DESTINATION_EXISTS")
	}
	hash := sha256.New()
	n, e := snapshotio.Copy(output, hash, contextReader{ctx: ctx, reader: io.LimitReader(input, file.SizeBytes+1)})
	if e == nil {
		e = output.Sync()
	}
	closeError := output.Close()
	if e != nil || closeError != nil || n != file.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
		return fail("MODEL_PREPARATION_SNAPSHOT_INVALID")
	}
	return syncSnapshotDirectory(filepath.Dir(output.Name()))
}
func writeSnapshotFile(directory, name string, raw []byte) error {
	file, e := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return fail("MODEL_PREPARATION_SNAPSHOT_DESTINATION_EXISTS")
	}
	_, e = file.Write(raw)
	if e == nil {
		e = file.Sync()
	}
	closeError := file.Close()
	if e != nil || closeError != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	return syncSnapshotDirectory(directory)
}

// walkSnapshot bounds directory enumeration before accepting an untrusted snapshot tree.
func walkSnapshot(ctx context.Context, root string, allowedFiles, allowedDirectories map[string]bool, maximum int, sync bool) error {
	count := 0
	var walk func(string, string) error
	walk = func(absolute, relative string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !allowedDirectories[relative] {
			return fail("MODEL_PREPARATION_SNAPSHOT_INVALID")
		}
		info, e := ownedPath(absolute, true, 0)
		if e != nil {
			return e
		}
		dir, e := os.OpenFile(absolute, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if e != nil {
			return fail("MODEL_PREPARATION_SNAPSHOT_INVALID")
		}
		defer dir.Close()
		opened, e := dir.Stat()
		if e != nil || !os.SameFile(info, opened) {
			return fail("MODEL_PREPARATION_SNAPSHOT_INVALID")
		}
		for {
			entries, e := dir.ReadDir(64)
			if e != nil && e != io.EOF {
				return fail("MODEL_PREPARATION_SNAPSHOT_INVALID")
			}
			for _, entry := range entries {
				count++
				if count > maximum {
					return fail("MODEL_PREPARATION_SNAPSHOT_LIMIT_REACHED")
				}
				child := path.Join(relative, entry.Name())
				if entry.IsDir() {
					if e = walk(filepath.Join(absolute, entry.Name()), child); e != nil {
						return e
					}
				} else if !entry.Type().IsRegular() || !allowedFiles[child] {
					return fail("MODEL_PREPARATION_SNAPSHOT_INVALID")
				}
			}
			if e == io.EOF {
				break
			}
		}
		if sync && dir.Sync() != nil {
			return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
		}
		return nil
	}
	return walk(root, ".")
}

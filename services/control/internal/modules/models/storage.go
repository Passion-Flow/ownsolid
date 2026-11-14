package models

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const ChunkBytes int64 = 8 * 1024 * 1024

// MaximumBundleBytes is the schema ceiling; admission uses ModelsPolicy.
const MaximumBundleBytes int64 = config.ModelImportSchemaMaximumBundleBytes

// A fixed protocol/schema bound, not a deployment concurrency setting.
const MaximumImportChunkCount = int((MaximumBundleBytes + ChunkBytes - 1) / ChunkBytes)

func fail(code string) error { return errors.New(code) }
func validID(value string) bool {
	parsed, e := uuid.Parse(value)
	return e == nil && parsed.String() == value
}
func openStore(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fail("MODEL_STORAGE_UNAVAILABLE")
	}
	resolved, resolveError := filepath.EvalSymlinks(path)
	if resolveError != nil || resolved != path {
		return nil, fail("MODEL_STORAGE_UNAVAILABLE")
	}
	info, e := os.Lstat(path)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, fail("MODEL_STORAGE_UNAVAILABLE")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return nil, fail("MODEL_STORAGE_UNAVAILABLE")
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return nil, fail("MODEL_STORAGE_UNAVAILABLE")
	}
	return root, nil
}
func directory(root *os.Root, path string) error {
	info, e := root.Lstat(path)
	if os.IsNotExist(e) {
		if e = root.Mkdir(path, 0700); e != nil {
			return fail("MODEL_IO_FAILED")
		}
		info, e = root.Lstat(path)
	}
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return fail("MODEL_PATH_INVALID")
	}
	return nil
}
func syncDirectory(root *os.Root, path string) error {
	file, e := root.Open(path)
	if e != nil {
		return fail("MODEL_IO_FAILED")
	}
	defer file.Close()
	if file.Sync() != nil {
		return fail("MODEL_IO_FAILED")
	}
	return nil
}
func chunkPath(id string, offset int64) string {
	return "imports/" + id + "/" + fmt.Sprintf("chunk-%012d", offset)
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.source.Read(p)
}
func digestFile(file *os.File, size int64) (string, error) {
	hash := sha256.New()
	n, e := io.Copy(hash, io.LimitReader(file, size+1))
	if e != nil || n != size {
		return "", fail("MODEL_CHUNK_INVALID")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func storeChunk(ctx context.Context, root *os.Root, id string, offset, size int64, expected string, source io.Reader) error {
	if !validID(id) || offset < 0 || offset%ChunkBytes != 0 || size < 1 || size > ChunkBytes || len(expected) != 64 {
		return fail("MODEL_CHUNK_INVALID")
	}
	if raw, e := hex.DecodeString(expected); e != nil || len(raw) != 32 {
		return fail("MODEL_CHUNK_INVALID")
	}
	if e := directory(root, "imports"); e != nil {
		return e
	}
	if e := directory(root, "imports/"+id); e != nil {
		return e
	}
	var random [16]byte
	if _, e := rand.Read(random[:]); e != nil {
		return fail("MODEL_IO_FAILED")
	}
	temporary := "imports/" + id + "/.upload-" + hex.EncodeToString(random[:])
	file, e := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return fail("MODEL_IO_FAILED")
	}
	defer root.Remove(temporary)
	hash := sha256.New()
	n, copyError := io.Copy(io.MultiWriter(file, hash), contextReader{ctx, io.LimitReader(source, size+1)})
	if copyError == nil {
		copyError = file.Sync()
	}
	closeError := file.Close()
	if copyError != nil || closeError != nil || n != size || hex.EncodeToString(hash.Sum(nil)) != expected {
		return fail("MODEL_CHUNK_INVALID")
	}
	final := chunkPath(id, offset)
	if e = root.Link(temporary, final); e != nil {
		info, statError := root.Lstat(final)
		if statError != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != size {
			return fail("MODEL_CHUNK_CONFLICT")
		}
		existing, openError := root.Open(final)
		if openError != nil {
			return fail("MODEL_CHUNK_CONFLICT")
		}
		actual, digestError := digestFile(existing, size)
		existing.Close()
		if digestError != nil || actual != expected {
			return fail("MODEL_CHUNK_CONFLICT")
		}
	}
	return syncDirectory(root, "imports/"+id)
}

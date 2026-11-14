package inference

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func rootOwned(info os.FileInfo) bool {
	owner, ok := info.Sys().(*syscall.Stat_t)
	return ok && owner.Uid == 0
}
func secureRoot(path string, private bool) error {
	if !protectedPath.MatchString(path) || filepath.Clean(path) != path {
		return fail("INFERENCE_STATE_INVALID")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, e := os.Lstat(current)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || !rootOwned(info) {
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		if current == path && private && info.Mode().Perm()&0077 != 0 {
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		if current == "/" {
			break
		}
	}
	return nil
}
func syncDir(path string) error {
	file, e := os.Open(path)
	if e != nil {
		return fail("INFERENCE_IO_FAILED")
	}
	defer file.Close()
	if file.Sync() != nil {
		return fail("INFERENCE_IO_FAILED")
	}
	return nil
}
func writeNew(path string, body []byte, mode os.FileMode) error {
	file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, mode)
	if e != nil {
		return fail("INFERENCE_IO_FAILED")
	}
	_, e = file.Write(body)
	if e == nil {
		e = file.Sync()
	}
	closeError := file.Close()
	if e != nil || closeError != nil {
		return fail("INFERENCE_IO_FAILED")
	}
	return syncDir(filepath.Dir(path))
}
func replaceJSON(path string, value any) error {
	if info, e := os.Lstat(path); e == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !rootOwned(info) {
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
	} else if !os.IsNotExist(e) {
		return fail("INFERENCE_IO_FAILED")
	}
	body, e := json.Marshal(value)
	if e != nil || len(body) > 65536 {
		return fail("INFERENCE_STATE_INVALID")
	}
	file, e := os.CreateTemp(filepath.Dir(path), ".inference-state-")
	if e != nil {
		return fail("INFERENCE_IO_FAILED")
	}
	name := file.Name()
	defer os.Remove(name)
	_, e = file.Write(body)
	if e == nil {
		e = file.Sync()
	}
	closeError := file.Close()
	if e != nil || closeError != nil {
		return fail("INFERENCE_IO_FAILED")
	}
	if os.Rename(name, path) != nil {
		return fail("INFERENCE_IO_FAILED")
	}
	return syncDir(filepath.Dir(path))
}
func commandDigest(command wire.InferenceCommand) string {
	body, _ := json.Marshal(command)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func protectedJSON(path string, value any) error {
	body, e := state.ProtectedFile(path)
	if e != nil {
		return fail("INFERENCE_STATE_INVALID")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		return fail("INFERENCE_STATE_INVALID")
	}
	return nil
}
func randomSuffix() (string, error) {
	var value [16]byte
	if _, e := rand.Read(value[:]); e != nil {
		return "", fail("INFERENCE_IO_FAILED")
	}
	return hex.EncodeToString(value[:]), nil
}
func availableBytes(path string) (uint64, error) {
	var info syscall.Statfs_t
	if syscall.Statfs(path, &info) != nil {
		return 0, fail("INFERENCE_IO_FAILED")
	}
	return uint64(info.Bavail) * uint64(info.Bsize), nil
}

type cacheOwnership struct {
	SchemaVersion int    `json:"schema_version"`
	CatalogDigest string `json:"catalog_digest"`
	Repository    string `json:"repository"`
	Revision      string `json:"revision"`
}

func expectedCacheOwnership() cacheOwnership {
	model := wire.ApprovedModel()
	return cacheOwnership{1, wire.ModelCatalogDigest(), model.Repository, model.Revision}
}
func verifyCache(ctx context.Context, root string) error {
	path := filepath.Join(root, "models", wire.ModelCatalogDigest())
	marker := path + ".owner.json"
	var owner cacheOwnership
	if e := protectedJSON(marker, &owner); e != nil || owner != expectedCacheOwnership() {
		return fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	if e := secureRoot(path, false); e != nil {
		return e
	}
	if e := modelartifact.VerifyDirectory(ctx, path); e != nil {
		return fail("INFERENCE_MODEL_INVALID")
	}
	return nil
}

// writeCacheMarker is reached only after the private journal records the exact new cache publication intent.
func writeCacheMarker(root string) error {
	path := filepath.Join(root, "models", wire.ModelCatalogDigest()+".owner.json")
	body, _ := json.Marshal(expectedCacheOwnership())
	if _, e := os.Lstat(path); e == nil {
		var owner cacheOwnership
		if protectedJSON(path, &owner) != nil || owner != expectedCacheOwnership() {
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		return nil
	} else if !os.IsNotExist(e) {
		return fail("INFERENCE_IO_FAILED")
	}
	return writeNew(path, body, 0600)
}

package inference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// The catalog digest binds its complete scope, provenance and independently accepted model bytes.
// Tensor/base comparison belongs to control acceptance; this consumer rehashes every immutable file.
type derivedCacheOwnership struct {
	SchemaVersion       int    `json:"schema_version"`
	ModelID             string `json:"model_id"`
	CatalogDigest       string `json:"catalog_digest"`
	ServingRecipeSHA256 string `json:"serving_recipe_sha256"`
}

func expectedDerivedCacheOwnership(c wire.InferenceCommand) derivedCacheOwnership {
	return derivedCacheOwnership{2, c.ModelID, c.CatalogDigest, c.Derived.ServingRecipeSHA256}
}

func verifyCommandDirectory(ctx context.Context, c wire.InferenceCommand, path string) error {
	if c.Derived == nil {
		return modelartifact.VerifyDirectory(ctx, path)
	}
	if c.Validate(c.Operation()) != nil || secureModelDirectory(path) != nil {
		return fail("INFERENCE_MODEL_INVALID")
	}
	entries, e := os.ReadDir(path)
	if e != nil || len(entries) != len(c.ModelFiles()) {
		return fail("INFERENCE_MODEL_INVALID")
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return fail("INFERENCE_CANCELLED")
		}
		expected, ok := c.ModelFile(entry.Name())
		if !ok {
			return fail("INFERENCE_MODEL_INVALID")
		}
		name := filepath.Join(path, entry.Name())
		before, e := os.Lstat(name)
		if e != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0133 != 0 || !rootOwned(before) || before.Size() != expected.SizeBytes {
			return fail("INFERENCE_MODEL_INVALID")
		}
		owner, ok := before.Sys().(*syscall.Stat_t)
		if !ok || owner.Nlink != 1 {
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		file, e := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if e != nil {
			return fail("INFERENCE_MODEL_INVALID")
		}
		opened, e := file.Stat()
		if e != nil || !os.SameFile(before, opened) {
			file.Close()
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		sum := sha256.New()
		reader := &cancelledCacheReader{ctx: ctx, source: io.LimitReader(file, expected.SizeBytes+1)}
		n, copyError := io.Copy(sum, reader)
		after, statError := file.Stat()
		final, pathError := os.Lstat(name)
		file.Close()
		if ctx.Err() != nil {
			return fail("INFERENCE_CANCELLED")
		}
		if copyError != nil || statError != nil || pathError != nil || n != expected.SizeBytes || hex.EncodeToString(sum.Sum(nil)) != expected.SHA256 || !os.SameFile(before, after) || !os.SameFile(before, final) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return fail("INFERENCE_MODEL_INVALID")
		}
	}
	return nil
}

type cancelledCacheReader struct {
	ctx    context.Context
	source io.Reader
}

func (r *cancelledCacheReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.source.Read(p)
}

func (m *Manager) verifyCommandCache(ctx context.Context, c wire.InferenceCommand) error {
	if c.Derived == nil {
		return verifyCache(ctx, m.config.RootDirectory)
	}
	var owner derivedCacheOwnership
	if protectedJSON(m.commandModelPath(c)+".owner.json", &owner) != nil || owner != expectedDerivedCacheOwnership(c) {
		return fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	return verifyCommandDirectory(ctx, c, m.commandModelPath(c))
}
func (m *Manager) writeCommandCacheMarker(c wire.InferenceCommand) error {
	if c.Derived == nil {
		return writeCacheMarker(m.config.RootDirectory)
	}
	path := m.commandModelPath(c) + ".owner.json"
	if _, e := os.Lstat(path); e == nil {
		var owner derivedCacheOwnership
		if protectedJSON(path, &owner) != nil || owner != expectedDerivedCacheOwnership(c) {
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		return nil
	} else if !os.IsNotExist(e) {
		return fail("INFERENCE_IO_FAILED")
	}
	body, _ := json.Marshal(expectedDerivedCacheOwnership(c))
	return writeNew(path, body, 0600)
}

// Journal stages have a fixed dot-prefixed basename; validate it separately from public mount paths.
func secureModelDirectory(path string) error {
	if !strings.HasPrefix(filepath.Base(path), ".staging-") {
		return secureRoot(path, false)
	}
	if !validStagePath(filepath.Dir(filepath.Dir(path)), path) || secureRoot(filepath.Dir(path), true) != nil {
		return fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	info, e := os.Lstat(path)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || !rootOwned(info) {
		return fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	return nil
}

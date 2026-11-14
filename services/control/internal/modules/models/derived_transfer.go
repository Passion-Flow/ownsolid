package models

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/jackc/pgx/v5"
)

// OpenDerivedTransfer requires the caller's current node/task/lease and Workspace
// transaction. Its owner rechecks creator authority after IO. The agent must check
// every range hash and the whole command-bound file before caching or execution.
func (s *Service) OpenDerivedTransfer(ctx context.Context, tx pgx.Tx, tenant, workspace string, c wire.InferenceCommand, input wire.ModelFileRequest) (httpserver.Download, error) {
	if tx == nil {
		return httpserver.Download{}, fail("STORAGE_UNAVAILABLE")
	}
	return s.OpenDerivedTransferWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, c, input)
}
func (s *Service) OpenDerivedTransferWithin(ctx context.Context, q transaction.Executor, tenant, workspace string, c wire.InferenceCommand, input wire.ModelFileRequest) (httpserver.Download, error) {
	var out httpserver.Download
	if c.Derived == nil || c.Derived.Catalog.Scope.TenantID != tenant || c.Derived.Catalog.Scope.WorkspaceID != workspace {
		return out, fail("RESOURCE_NOT_FOUND")
	}
	if e := input.ValidateForCommand(c); e != nil {
		return out, e
	}
	catalog, e := s.DecodeDerivedVersionWithin(ctx, q, tenant, workspace, c.ModelID)
	if e != nil {
		return out, e
	}
	digest, e := catalog.Digest()
	if e != nil || digest != c.CatalogDigest {
		return out, fail("MODEL_DERIVED_CATALOG_INVALID")
	}
	root, e := openStore(s.RootDirectory)
	if e != nil {
		return out, e
	}
	defer root.Close()
	if e = verifyDerivedMetadata(root, catalog); e != nil {
		return out, e
	}
	asset, found := c.ModelFile(input.File)
	if !found {
		return out, fail("VALIDATION_FAILED")
	}
	file, e := derivedRegular(root, filepath.Join(derivedDirectory(catalog), asset.Path), asset.SizeBytes, 0444)
	if e != nil {
		return out, e
	}
	keep := false
	defer func() {
		if !keep {
			file.Close()
		}
	}()
	before, e := file.Stat()
	if e != nil || !transferFileMatches(before, asset.SizeBytes) {
		return out, derivedFailure()
	}
	sum := sha256.New()
	n, e := io.Copy(sum, contextReader{ctx, io.NewSectionReader(file, input.Offset, input.Length)})
	after, statError := file.Stat()
	if e != nil {
		return out, e
	}
	if statError != nil || n != input.Length || !transferFileMatches(after, asset.SizeBytes) || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		return out, fail("MODEL_FILE_CHANGED")
	}
	digest, e = modelTransferDigest(input, asset, sum.Sum(nil))
	if e != nil {
		return out, e
	}
	keep = true
	return httpserver.Download{File: file, Offset: input.Offset, Size: input.Length, SHA256: digest, ContentType: "application/octet-stream"}, nil
}

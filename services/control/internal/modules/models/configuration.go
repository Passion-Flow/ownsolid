package models

import (
	"context"
	"io"

	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ConfigurationInTransaction exposes only the frozen hash-verified base config through current owning-module scope.
func (s *Service) ConfigurationInTransaction(ctx context.Context, tx pgx.Tx, tenant, workspace, id string) ([]byte, error) {
	return s.ConfigurationWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, id)
}
func (s *Service) ConfigurationWithin(ctx context.Context, q transaction.Executor, tenant, workspace, id string) ([]byte, error) {
	asset, ok := wire.ModelFileForName("config.json")
	if !ok {
		return nil, fail("MODEL_FILE_INVALID")
	}
	download, e := s.OpenTransferWithin(ctx, q, tenant, workspace, id, wire.ModelFileRequest{File: asset.Path, Offset: 0, Length: asset.SizeBytes})
	if e != nil {
		return nil, e
	}
	defer download.File.Close()
	if download.SHA256 != asset.SHA256 || download.Size != asset.SizeBytes {
		return nil, fail("MODEL_FILE_INVALID")
	}
	raw, e := io.ReadAll(io.NewSectionReader(download.File, 0, asset.SizeBytes+1))
	if e != nil || int64(len(raw)) != asset.SizeBytes {
		return nil, fail("MODEL_IO_FAILED")
	}
	return raw, nil
}

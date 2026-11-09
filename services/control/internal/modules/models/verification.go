package models

import (
	"context"
	"os"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
)

// OpenVerificationAsset requires the owning caller's current authorized scope transaction.
// Machine callers validate node/task/lease first; OpenReviewAsset separately rechecks Admin authority.
// It pins one approved ordinary file for independent verification outside business locks.
// Large-file callers must subsequently verify the complete approved size/hash, not only this first range.
func (s *Service) OpenVerificationAsset(ctx context.Context, tx pgx.Tx, tenant, workspace, model, name, lease string) (*os.File, datawire.Asset, error) {
	if tx == nil {
		return nil, datawire.Asset{}, fail("STORAGE_UNAVAILABLE")
	}
	return s.OpenVerificationAssetWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, model, name, lease)
}
func (s *Service) OpenVerificationAssetWithin(ctx context.Context, q transaction.Executor, tenant, workspace, model, name, lease string) (*os.File, datawire.Asset, error) {
	var asset datawire.Asset
	approved, ok := wire.ModelFileForName(name)
	if !ok {
		return nil, asset, fail("MODEL_FILE_INVALID")
	}
	asset = datawire.Asset{Path: approved.Path, SizeBytes: approved.SizeBytes, SHA256: approved.SHA256}
	length := min(wire.ModelTransferChunkBytes, asset.SizeBytes)
	out, e := s.OpenTransferWithin(ctx, q, tenant, workspace, model, wire.ModelFileRequest{LeaseToken: lease, File: name, Offset: 0, Length: length})
	if e != nil {
		return nil, asset, e
	}
	return out.File, asset, nil
}

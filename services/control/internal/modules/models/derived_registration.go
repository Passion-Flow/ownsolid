package models

import (
	"context"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// RegisterDerivedInTransaction belongs to the caller's current Admin decision transaction.
// The deferred database graph constraint requires its matching review and approved parent at commit.
func (s *Service) RegisterDerivedInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, w workspaces.Workspace, prepared PreparedDerivedFiles, reviewID, name string) (string, error) {
	if tx == nil {
		return "", fail("STORAGE_UNAVAILABLE")
	}
	return s.RegisterDerivedWithin(ctx, transaction.PostgreSQLTransaction(tx), a, w, prepared, reviewID, name)
}

// OpenReviewAsset pins an approved base asset inside current Admin/Workspace authority.
// Large-file callers independently hash every byte outside their transaction.
func (s *Service) OpenReviewAsset(ctx context.Context, tx pgx.Tx, a identity.Auth, w workspaces.Workspace, model, name string) (*os.File, datawire.Asset, error) {
	if tx == nil {
		return nil, datawire.Asset{}, fail("STORAGE_UNAVAILABLE")
	}
	return s.OpenReviewAssetWithin(ctx, transaction.PostgreSQLTransaction(tx), a, w, model, name)
}

// DecodeDerivedVersionInTransaction validates the exact immutable model-owning lineage.
func (s *Service) DecodeDerivedVersionInTransaction(ctx context.Context, tx pgx.Tx, tenant, workspace, model string) (wire.DerivedCatalog, error) {
	return s.readDerivedVersion(ctx, tx, tenant, workspace, model, true)
}

// RetainedDerivedVersionInTransaction permits cleanup of a retired immutable model without requiring its files.
func (s *Service) RetainedDerivedVersionInTransaction(ctx context.Context, tx pgx.Tx, tenant, workspace, model string) (wire.DerivedCatalog, error) {
	return s.readDerivedVersion(ctx, tx, tenant, workspace, model, false)
}
func (s *Service) readDerivedVersion(ctx context.Context, tx pgx.Tx, tenant, workspace, model string, ready bool) (wire.DerivedCatalog, error) {
	if tx == nil {
		return wire.DerivedCatalog{}, fail("STORAGE_UNAVAILABLE")
	}
	return s.derivedVersionWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, model, ready)
}

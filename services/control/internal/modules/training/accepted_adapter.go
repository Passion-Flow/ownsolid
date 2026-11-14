package training

import (
	"context"
	"encoding/json"
	"os"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
)

// AcceptedAdapter is an internal immutable source snapshot, never an external authorization token.
type AcceptedAdapter struct {
	Job        Job
	Command    wire.Command
	Descriptor UploadDescriptor
}

// IdentitySHA256 binds immutable preparation inputs, excluding mutable presentation and queue metadata.
func (a AcceptedAdapter) IdentitySHA256() string {
	raw, _ := json.Marshal(struct {
		Command    wire.Command
		Descriptor UploadDescriptor
	}{a.Command, a.Descriptor})
	return rawDigest(raw)
}

// AcceptedAdapterInTransaction requires the caller's current identity and Workspace authority locks.
func (s *Service) AcceptedAdapterInTransaction(ctx context.Context, tx pgx.Tx, tenant, workspace, job string) (AcceptedAdapter, error) {
	return s.AcceptedAdapterWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, job)
}

// AcceptedAdapterSnapshot owns the selected-store authority transaction and
// exposes no source when that transaction fails or its commit is uncertain.
func (s *Service) AcceptedAdapterSnapshot(ctx context.Context, a identity.Auth, workspace, job string) (AcceptedAdapter, error) {
	var value AcceptedAdapter
	e := s.trainingActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var e error
		value, e = s.AcceptedAdapterWithin(ctx, tx, w.TenantID, w.ID, job)
		return e
	})
	if e != nil {
		return AcceptedAdapter{}, e
	}
	return value, nil
}

// VerifyAcceptedAdapter hashes retained published receipt/files outside all business transactions.
func (s *Service) VerifyAcceptedAdapter(ctx context.Context, source AcceptedAdapter) error {
	files, e := s.publishedFiles(source.Descriptor.Scope.TenantID)
	if e != nil {
		return e
	}
	if source.Descriptor.Kind != "artifact" || len(source.Descriptor.Files) != 3 {
		return fail("TRAINING_DATA_INVALID")
	}
	return files.VerifyPublished(ctx, source.Descriptor)
}

// OpenAcceptedAdapterAsset follows a currently authorized source snapshot and permits only safe adapter files.
func (s *Service) OpenAcceptedAdapterAsset(ctx context.Context, source AcceptedAdapter, asset string) (*os.File, datawire.Asset, error) {
	if !wire.AdapterAssetAllowed(asset) {
		return nil, datawire.Asset{}, fail("TRAINING_NOT_FOUND")
	}
	files, e := s.publishedFiles(source.Descriptor.Scope.TenantID)
	if e != nil {
		return nil, datawire.Asset{}, e
	}
	if source.Descriptor.Kind != "artifact" || len(source.Descriptor.Files) != 3 {
		return nil, datawire.Asset{}, fail("TRAINING_DATA_INVALID")
	}
	return files.OpenPublishedAsset(ctx, source.Descriptor, asset)
}

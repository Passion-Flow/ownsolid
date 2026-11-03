package datasets

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type PublicConfiguration struct {
	StorageAvailable      bool        `json:"storage_available"`
	Limits                wire.Limits `json:"limits"`
	RowSchema             string      `json:"row_schema"`
	ProcessorVersion      string      `json:"processor_version"`
	SplitAlgorithm        string      `json:"split_algorithm"`
	ValidationBasisPoints int         `json:"validation_basis_points"`
	CanImport             bool        `json:"can_import"`
	CanPublish            bool        `json:"can_publish"`
}

func (s *Service) Configuration(ctx context.Context, a identity.Auth, workspace string) (PublicConfiguration, error) {
	var out PublicConfiguration
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		out = PublicConfiguration{StorageAvailable: s.Store != nil && s.Processor != nil, Limits: s.policy().Limits, RowSchema: wire.RowSchema, ProcessorVersion: wire.ProcessorVersion, SplitAlgorithm: wire.SplitAlgorithm, ValidationBasisPoints: s.policy().ValidationBasisPoints, CanImport: canWrite(a, w), CanPublish: isOwner(a, w)}
		return nil
	})
	return committed(out, e)
}

// ResolveDatasetWorkspace rechecks the current Admin before exposing routing scope.
func (s *Service) ResolveDatasetWorkspace(ctx context.Context, a identity.Auth, id string) (string, error) {
	if ctx == nil || !isAdmin(a) || !wire.ValidID(id) {
		return "", failure("DATASET_NOT_FOUND")
	}
	repo, e := s.repository()
	if e != nil {
		return "", e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	var workspace string
	e = repo.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		e := q.QueryRow(ctx, datasetSQL(q, "SELECT d.workspace_id::text FROM datasets d JOIN installation_state i ON i.tenant_id=d.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND d.id=$1", "SELECT d.workspace_id FROM datasets d JOIN installation_state i ON i.tenant_id=d.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND d.id=?", []any{id}, []any{id}, transaction.SQL(transaction.Dameng, dmAdminResolveWorkspace, id))).Scan(&workspace)
		if errors.Is(e, pgx.ErrNoRows) {
			return failure("DATASET_NOT_FOUND")
		}
		return e
	})
	return committed(workspace, e)
}

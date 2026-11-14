package modelpreparations

import (
	"context"
	"encoding/hex"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func (s *Service) HasReservations(ctx context.Context, tx pgx.Tx, w workspaces.Workspace) (bool, error) {
	return s.HasReservationsWithin(ctx, transaction.PostgreSQLTransaction(tx), w)
}

type trainingAdmission struct {
	snapshot          sourceSnapshot
	owner             *Service
	tenant, workspace string
}

func (s *Service) PrepareTrainingAdmission(ctx context.Context, a identity.Auth, input training.CreateRequest) (training.AdmissionCheck, error) {
	if s == nil || ctx == nil {
		return nil, fail("MODEL_HOLDOUT_UNAVAILABLE")
	}
	if !datawire.ValidID(input.WorkspaceID) || !datawire.ValidID(input.DatasetID) || !datawire.ValidID(input.DatasetVersionID) {
		return nil, fail("VALIDATION_FAILED")
	}
	var tenant, workspace string
	if e := s.preparationActorTransaction(ctx, a, input.WorkspaceID, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		tenant, workspace = w.TenantID, w.ID
		return nil
	}); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().ScanSeconds))
	defer cancel()
	release, e := s.acquireScan(ctx)
	if e != nil {
		return nil, e
	}
	defer release()
	if s.Datasets == nil {
		return nil, fail("MODEL_HOLDOUT_UNAVAILABLE")
	}
	snapshot, e := s.scanSource(ctx, a, input.WorkspaceID, input.DatasetID, input.DatasetVersionID, false, false, &analysisBudget{})
	if e != nil {
		return nil, sourceError(e)
	}
	return &trainingAdmission{snapshot: snapshot, owner: s, tenant: tenant, workspace: workspace}, nil
}
func rawHashes(values map[string]bool) ([][]byte, error) {
	hashes := make([][]byte, 0, len(values))
	for _, value := range sortedHashes(values) {
		if !datawire.ValidDigest(value) {
			return nil, fail("MODEL_HOLDOUT_DATA_INVALID")
		}
		raw, e := hex.DecodeString(value)
		if e != nil {
			return nil, fail("MODEL_HOLDOUT_DATA_INVALID")
		}
		hashes = append(hashes, raw)
	}
	return hashes, nil
}
func (check *trainingAdmission) Validate(ctx context.Context, tx pgx.Tx, w workspaces.Workspace, input datasets.TrainingInputs) error {
	return check.ValidateWithin(ctx, transaction.PostgreSQLTransaction(tx), w, input)
}

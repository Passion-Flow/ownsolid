package datasets

import (
	"context"
	"os"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
)

// TrainingInputs is a retained immutable publication; no draft contents are exposed.
type TrainingInputs struct {
	Version    Version
	Train      wire.Asset
	Validation wire.Asset
}

// TrainingInputsInTransaction requires the caller's current user/Workspace authority locks.
// New selection refuses retirement. Retained recovery may reference the same frozen version.
func (s *Service) TrainingInputsInTransaction(ctx context.Context, tx pgx.Tx, tenant, workspace, dataset, version string, retained bool) (TrainingInputs, error) {
	out, e := s.TrainingInputSnapshotWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, dataset, version, retained)
	if e != nil {
		return TrainingInputs{}, e
	}
	if e = s.VerifyTrainingInputSnapshot(ctx, out); e != nil {
		return TrainingInputs{}, e
	}
	return out, nil
}

// OpenTrainingAsset requires a previously authorized, scoped immutable input reference.
func (s *Service) OpenTrainingAsset(input TrainingInputs, asset string) (*os.File, wire.Asset, error) {
	return s.OpenTrainingAssetContext(context.Background(), input, asset)
}

// OpenTrainingAssetContext retains cancellation for a previously authorized snapshot.
func (s *Service) OpenTrainingAssetContext(ctx context.Context, input TrainingInputs, asset string) (*os.File, wire.Asset, error) {
	var selected wire.Asset
	switch asset {
	case "train.jsonl":
		selected = input.Train
	case "validation.jsonl":
		selected = input.Validation
	default:
		return nil, wire.Asset{}, failure("DATASET_FILE_INVALID")
	}
	if s == nil || ctx == nil || !wire.ValidID(input.Version.ID) || !wire.ValidID(input.Version.AttemptID) || selected.Path != asset {
		return nil, wire.Asset{}, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	files, e := s.publishedFileAccess("")
	if e != nil {
		return nil, wire.Asset{}, e
	}
	expected, e := trainingInputsFromVersion(input.Version)
	if e != nil {
		return nil, wire.Asset{}, e
	}
	if input.Train != expected.Train || input.Validation != expected.Validation {
		return nil, wire.Asset{}, failure("DATASET_DATA_INVALID")
	}
	f, e := files.OpenVersionAssetContext(ctx, input.Version.ID, input.Version.AttemptID, asset, selected.SizeBytes, selected.SHA256)
	if e != nil {
		return nil, wire.Asset{}, e
	}
	return f, selected, nil
}

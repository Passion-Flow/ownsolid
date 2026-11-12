package datasets

import (
	"context"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// TrainingInputSnapshotWithin resolves metadata only under the caller's current
// actor/Workspace fences. The owner must verify files before creating execution.
func (s *Service) TrainingInputSnapshotWithin(ctx context.Context, q transaction.Executor, tenant, workspace, dataset, version string, retained bool) (TrainingInputs, error) {
	v, e := s.PublishedSourceWithin(ctx, q, tenant, workspace, dataset, version, retained)
	if e != nil {
		return TrainingInputs{}, e
	}
	return trainingInputsFromVersion(v)
}
func trainingInputsFromVersion(v Version) (TrainingInputs, error) {
	for _, id := range []string{v.ID, v.DatasetID, v.WorkspaceID, v.AttemptID} {
		if !wire.ValidID(id) {
			return TrainingInputs{}, failure("DATASET_DATA_INVALID")
		}
	}
	out := TrainingInputs{Version: v}
	train, validation := 0, 0
	for _, asset := range v.Manifest.Files {
		switch asset.Path {
		case "train.jsonl":
			out.Train = asset
			train++
		case "validation.jsonl":
			out.Validation = asset
			validation++
		}
	}
	if train != 1 || validation != 1 {
		return TrainingInputs{}, failure("DATASET_DATA_INVALID")
	}
	for _, asset := range []wire.Asset{out.Train, out.Validation} {
		if asset.SizeBytes < 1 {
			return TrainingInputs{}, failure("TRAINING_DATASET_EMPTY_PARTITION")
		}
		if asset.SizeBytes > wire.MaximumResultBytesCeiling || !wire.ValidDigest(asset.SHA256) {
			return TrainingInputs{}, failure("DATASET_DATA_INVALID")
		}
	}
	return out, nil
}

// VerifyTrainingInputSnapshot performs no SQL or writes. Legacy transaction
// callers keep their checks; new owners call this after releasing business locks.
func (s *Service) VerifyTrainingInputSnapshot(ctx context.Context, input TrainingInputs) error {
	if s == nil || ctx == nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if s.Identity == nil {
		return failure("DATASET_IDENTITY_REQUIRED")
	}
	files, e := s.publishedFileAccess("")
	if e != nil {
		return e
	}
	expected, e := trainingInputsFromVersion(input.Version)
	if e != nil {
		return e
	}
	if expected.Train != input.Train || expected.Validation != input.Validation {
		return failure("DATASET_DATA_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().SnapshotSeconds))
	defer cancel()
	for _, asset := range []wire.Asset{input.Train, input.Validation} {
		f, e := files.OpenVersionAssetContext(ctx, input.Version.ID, input.Version.AttemptID, asset.Path, asset.SizeBytes, asset.SHA256)
		if e != nil {
			if ctx.Err() != nil {
				return publishedSourceError(ctx.Err())
			}
			return e
		}
		if e = f.Close(); e != nil {
			return failure("DATASET_STORAGE_UNAVAILABLE")
		}
	}
	return nil
}

package modelpreparations

import (
	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations/snapshot"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Retained completed sources borrow the current review owner's authority fences.
// They open no files and have no independent commit authority.
func (s *Service) retainedPreparationSourcesWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, trainingJob, holdout string) (preparationInputs, datasets.Version, error) {
	var zero preparationInputs
	var version datasets.Version
	if s == nil || s.Training == nil || s.Models == nil || s.Datasets == nil {
		return zero, version, fail("MODEL_PREPARATION_UNAVAILABLE")
	}
	training, e := s.Training.AcceptedAdapterWithin(ctx, q, w.TenantID, w.ID, trainingJob)
	if e != nil {
		return zero, version, e
	}
	if e = s.Models.ReadyWithin(ctx, q, w.TenantID, w.ID, training.Job.ModelID); e != nil {
		return zero, version, e
	}
	h, e := scanHoldout(preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationDM("SELECT "+nativeHoldoutFields+` FROM "MODEL_HOLDOUTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=? FOR UPDATE`), "SELECT "+holdoutFields+" FROM model_holdouts WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR SHARE", "SELECT "+mysqlHoldoutFields+" FROM model_holdouts WHERE tenant_id=? AND workspace_id=? AND id=? FOR SHARE", w.TenantID, w.ID, holdout)))
	if e != nil {
		return zero, version, e
	}
	version, e = s.Datasets.PublishedSourceWithin(ctx, q, w.TenantID, w.ID, h.DatasetID, h.DatasetVersionID, true)
	if e != nil {
		return zero, datasets.Version{}, e
	}
	if version.ManifestSHA256 != h.ManifestSHA256 || version.EffectiveSourceSHA256 != h.EffectiveSourceSHA256 || version.RowMappingSHA256 != h.RowMappingSHA256 {
		return zero, datasets.Version{}, fail("MODEL_PREPARATION_INPUT_CHANGED")
	}
	h.CreatedAt = h.CreatedAt.UTC()
	out := preparationInputs{SchemaVersion: 1, TrainingCommand: training.Command, Adapter: snapshot.Adapter(training.Descriptor), TrainingIdentitySHA256: training.IdentitySHA256(), Heldout: h, DatasetAttemptID: version.AttemptID}
	if e = out.validate(); e != nil {
		return zero, datasets.Version{}, e
	}
	return out, version, nil
}

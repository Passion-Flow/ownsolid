package modelpreparations

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func (s *Service) replayPreparation(ctx context.Context, tx pgx.Tx, a identity.Auth, w workspaces.Workspace, key string, wanted []byte) (Preparation, bool, error) {
	return s.replayPreparationWithin(ctx, transaction.PostgreSQLTransaction(tx), a, w, key, wanted)
}

func (s *Service) preparationSources(ctx context.Context, tx pgx.Tx, w workspaces.Workspace, input CreateRequest) (preparationInputs, datasets.Version, error) {
	return s.preparationSourcesWithin(ctx, transaction.PostgreSQLTransaction(tx), w, input)
}

// Retained completed inputs remain reviewable independently of live node execution authority.
func (s *Service) retainedPreparationSources(ctx context.Context, tx pgx.Tx, w workspaces.Workspace, trainingJob, holdout string) (preparationInputs, datasets.Version, error) {
	return s.retainedPreparationSourcesWithin(ctx, transaction.PostgreSQLTransaction(tx), w, trainingJob, holdout)
}

func (s *Service) verifyPreparationSources(ctx context.Context, input preparationInputs, version datasets.Version, recipe wire.ExecutionRecipe) error {
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().ScanSeconds))
	defer cancel()
	release, e := s.acquireScan(ctx)
	if e != nil {
		return e
	}
	defer release()
	var bytes int64 = input.Heldout.SourceBytes
	for _, asset := range input.Adapter.Files {
		bytes += asset.SizeBytes
	}
	if bytes > s.policy().ScanMaximumBytes || input.Heldout.RowCount > recipe.DataLimits.MaximumRows || input.inventory().Validate(recipe.DataLimits) != nil {
		return fail("MODEL_PREPARATION_INPUT_LIMIT_REACHED")
	}
	if e = s.Training.VerifyAcceptedAdapter(ctx, trainingSource(input)); e != nil {
		return e
	}
	file, size, e := s.Datasets.OpenPublishedSource(ctx, version, min(s.policy().ScanMaximumBytes, datawire.MaximumBytesCeiling))
	if e != nil {
		return sourceError(e)
	}
	closeError := file.Close()
	if size != input.Heldout.SourceBytes || closeError != nil {
		return fail("MODEL_PREPARATION_INPUT_CHANGED")
	}
	return ctx.Err()
}

func (s *Service) Create(ctx context.Context, a identity.Auth, input CreateRequest, key, request string) (Preparation, error) {
	var out Preparation
	if ctx == nil {
		return out, fail("ACCESS_DENIED")
	}
	for _, id := range []string{input.WorkspaceID, input.TrainingJobID, input.HeldoutID, input.NodeID, key} {
		if !datawire.ValidID(id) {
			return out, fail("VALIDATION_FAILED")
		}
	}
	if !strings.HasPrefix(input.GPUUUID, "GPU-") || !datawire.ValidID(strings.TrimPrefix(input.GPUUUID, "GPU-")) || input.Configuration.Validate() != nil || input.Configuration.Device != trainingwire.ProductionProfile || !text(input.Reason, 500) {
		return out, fail("VALIDATION_FAILED")
	}
	if _, e := s.reviewRepository(); e != nil {
		return out, preparationRepositoryError(e)
	}
	wanted := fingerprint(a, input)
	replayed := false
	e := s.preparationActorTransaction(ctx, a, input.WorkspaceID, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, replayed, e = s.replayPreparationWithin(ctx, q, a, w, key, wanted)
		return e
	})
	if e != nil {
		return Preparation{}, e
	}
	if replayed {
		return out, nil
	}
	recipe, e := s.executionRecipe()
	if e != nil {
		return out, e
	}
	if recipe.Resources.CPUSeconds > recipe.Resources.WallSeconds*input.Configuration.Threads {
		return out, fail("MODEL_PREPARATION_RECIPE_INVALID")
	}
	var sources preparationInputs
	var version datasets.Version
	e = s.preparationActorTransaction(ctx, a, input.WorkspaceID, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, replayed, e = s.replayPreparationWithin(ctx, q, a, w, key, wanted)
		if e != nil || replayed {
			return e
		}
		sources, version, e = s.preparationSourcesWithin(ctx, q, w, input)
		return e
	})
	if e != nil {
		return Preparation{}, e
	}
	if replayed {
		return out, nil
	}
	if e = s.verifyPreparationSources(ctx, sources, version, recipe); e != nil {
		return out, sourceError(e)
	}
	e = s.preparationActorTransaction(ctx, a, input.WorkspaceID, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, replayed, e = s.replayPreparationWithin(ctx, q, a, w, key, wanted)
		if e != nil || replayed {
			return e
		}
		currentRecipe, e := s.executionRecipe()
		if e != nil {
			return e
		}
		previous, _ := json.Marshal(recipe)
		current, _ := json.Marshal(currentRecipe)
		if !same(previous, current) {
			return fail("MODEL_PREPARATION_RECIPE_UNAVAILABLE")
		}
		now, _, e := s.preparationSourcesWithin(ctx, q, w, input)
		if e != nil {
			return e
		}
		if now.digest() != sources.digest() {
			return fail("MODEL_PREPARATION_INPUT_CHANGED")
		}
		var total, active int
		if e = preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationDM(`SELECT COUNT(*),COUNT(CASE WHEN "STATE" IN ('queued','preparing','running','cancelling','recovery_required') THEN 1 END) FROM "MODEL_PREPARATIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=?`), `SELECT count(*),count(*) FILTER(WHERE state IN ('queued','preparing','running','cancelling','recovery_required')) FROM model_preparations WHERE tenant_id=$1 AND workspace_id=$2`, `SELECT COUNT(*),COUNT(CASE WHEN state IN ('queued','preparing','running','cancelling','recovery_required') THEN 1 END) FROM model_preparations WHERE tenant_id=? AND workspace_id=?`, w.TenantID, w.ID)).Scan(&total, &active); e != nil {
			return e
		}
		if total >= s.policy().MaxPreparationsPerWorkspace || active >= s.policy().MaxActivePerWorkspace {
			return fail("MODEL_PREPARATION_LIMIT_REACHED")
		}
		var membership *string
		if !admin(a) {
			authority, e := s.Workspaces.MembershipAuthorityWithin(ctx, q, w.TenantID, w.ID, a.Me.User.ID)
			if e != nil {
				return e
			}
			membership = &authority
		}
		id := uuid.NewString()
		frozen, _ := json.Marshal(sources)
		configuration, _ := json.Marshal(input.Configuration)
		execution, _ := json.Marshal(recipe)
		configurationSHA, _ := input.Configuration.Digest()
		n, e := q.Exec(ctx, preparationSelectedSQL(q, preparationDM(`INSERT INTO "MODEL_PREPARATIONS"("ID","TENANT_ID","WORKSPACE_ID","NODE_ID","CURRENT_NODE_ID","MODEL_ID","TRAINING_JOB_ID","TRAINING_ATTEMPT_ID","TRAINING_ARTIFACT_ID","HELDOUT_ID","GPU_UUID","INPUT_SNAPSHOT","INPUT_SHA256","CONFIGURATION","CONFIGURATION_SHA256","EXECUTION_RECIPE","CREATED_BY","REQUESTED_BY","CREATION_AUDIENCE","REQUESTED_AUDIENCE","REQUESTED_EPOCH","MEMBERSHIP_AUTHORITY","REASON","CREATION_KEY","CREATION_FINGERPRINT","STATE") VALUES(?,?,?,?,?,?,?,?,?,?,?,CAST(? AS CLOB),?,CAST(? AS CLOB),?,CAST(? AS CLOB),?,?,?,?,?,?,?,?,?,'queued')`, 11, 13, 15), `INSERT INTO model_preparations(id,tenant_id,workspace_id,node_id,current_node_id,model_id,training_job_id,training_attempt_id,training_artifact_id,heldout_id,gpu_uuid,input_snapshot,input_sha256,configuration,configuration_sha256,execution_recipe,created_by,requested_by,creation_audience,requested_audience,requested_epoch,membership_authority,reason,creation_key,creation_fingerprint,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,$14::jsonb,$15,$16::jsonb,$17,$18,$19,$20,$21,$22,$23,$24,$25,'queued')`, `INSERT INTO model_preparations(id,tenant_id,workspace_id,node_id,current_node_id,model_id,training_job_id,training_attempt_id,training_artifact_id,heldout_id,gpu_uuid,input_snapshot,input_sha256,configuration,configuration_sha256,execution_recipe,created_by,requested_by,creation_audience,requested_audience,requested_epoch,membership_authority,reason,creation_key,creation_fingerprint,state) VALUES(?,?,?,?,?,?,?,?,?,?,?,CAST(? AS JSON),?,CAST(? AS JSON),?,CAST(? AS JSON),?,?,?,?,?,?,?,?,?,'queued')`, id, w.TenantID, w.ID, input.NodeID, input.NodeID, sources.TrainingCommand.ModelID, input.TrainingJobID, sources.TrainingCommand.AttemptID, sources.Adapter.UploadID, input.HeldoutID, input.GPUUUID, frozen, sources.digest(), configuration, configurationSHA, execution, a.Me.User.ID, a.Me.User.ID, a.Me.Audience, a.Me.Audience, a.Epoch, membership, input.Reason, key, wanted))
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("STORAGE_UNAVAILABLE")
		}
		if e = preparationAuditWithin(ctx, q, a, w, "model.preparation_created", id, input.Reason, request); e != nil {
			return e
		}
		out, e = lockedPreparationWithin(ctx, q, w, id)
		return e
	})
	if e != nil {
		return Preparation{}, e
	}
	return out, nil
}

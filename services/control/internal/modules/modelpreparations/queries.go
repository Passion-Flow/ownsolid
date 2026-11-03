package modelpreparations

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

const preparationFields = `id::text,tenant_id::text,workspace_id::text,node_id::text,current_node_id::text,model_id::text,training_job_id::text,training_attempt_id::text,training_artifact_id::text,heldout_id::text,gpu_uuid,input_snapshot,input_sha256,configuration,configuration_sha256,execution_recipe,created_by::text,reason,state,revision,wait_code,error_code,cancel_requested_at,created_at,updated_at,finished_at,attempt_number,current_attempt_id::text,pending_recovery_id::text,review_id::text,derived_model_id::text`

func scanPreparation(row pgx.Row) (Preparation, error) {
	row = preparationIDRow(row, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 16, 27, 28, 29, 30)
	var p Preparation
	var inputs, configuration, recipe []byte
	e := row.Scan(&p.ID, &p.tenant, &p.WorkspaceID, &p.NodeID, &p.CurrentNodeID, &p.ModelID, &p.TrainingJobID, &p.TrainingAttemptID, &p.TrainingArtifactID, &p.HeldoutID, &p.GPUUUID, &inputs, &p.InputSHA256, &configuration, &p.ConfigurationSHA256, &recipe, &p.CreatedBy, &p.Reason, &p.State, &p.Revision, &p.WaitCode, &p.ErrorCode, &p.CancelRequestedAt, &p.CreatedAt, &p.UpdatedAt, &p.FinishedAt, &p.AttemptNumber, &p.CurrentAttemptID, &p.PendingRecoveryID, &p.ReviewID, &p.DerivedModelID)
	if errors.Is(e, pgx.ErrNoRows) {
		return p, fail("MODEL_PREPARATION_NOT_FOUND")
	}
	if e != nil {
		return p, e
	}
	if wire.DecodeStrict(inputs, &p.inputs) != nil || p.inputs.validate() != nil || p.inputs.digest() != p.InputSHA256 || wire.DecodeStrict(configuration, &p.Configuration) != nil || p.Configuration.Validate() != nil || p.Configuration.Device != trainingwire.ProductionProfile || wire.DecodeStrict(recipe, &p.Recipe) != nil || p.Recipe.Validate() != nil || !validPreparationState(p.State) {
		return Preparation{}, fail("MODEL_PREPARATION_DATA_INVALID")
	}
	digest, e := p.Configuration.Digest()
	c := p.inputs.TrainingCommand
	if e != nil || digest != p.ConfigurationSHA256 || c.Scope.TenantID != p.tenant || c.Scope.WorkspaceID != p.WorkspaceID || c.Scope.JobID != p.TrainingJobID || c.AttemptID != p.TrainingAttemptID || c.ModelID != p.ModelID || p.inputs.Adapter.UploadID != p.TrainingArtifactID || p.inputs.Heldout.ID != p.HeldoutID || p.inputs.inventory().Validate(p.Recipe.DataLimits) != nil || p.Recipe.Resources.CPUSeconds > p.Recipe.Resources.WallSeconds*p.Configuration.Threads {
		return Preparation{}, fail("MODEL_PREPARATION_DATA_INVALID")
	}
	p.TrainingResultSHA256 = p.inputs.Adapter.ManifestSHA256
	p.HeldoutManifestSHA256 = p.inputs.Heldout.ManifestSHA256
	return p, nil
}
func lockedPreparation(ctx context.Context, tx pgx.Tx, w workspaces.Workspace, id string) (Preparation, error) {
	if !datawire.ValidID(id) {
		return Preparation{}, fail("MODEL_PREPARATION_NOT_FOUND")
	}
	return scanPreparation(tx.QueryRow(ctx, "SELECT "+preparationFields+" FROM model_preparations WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", w.TenantID, w.ID, id))
}
func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, id string) (Preparation, error) {
	var p Preparation
	e := s.preparationActorTransaction(ctx, a, workspace, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		var e error
		p, e = lockedPreparationWithin(ctx, q, w, id)
		return e
	})
	if e != nil {
		return Preparation{}, e
	}
	return p, nil
}
func (s *Service) List(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[Preparation], error) {
	empty := Page[Preparation]{Items: []Preparation{}}
	if ctx == nil || a.Me.Audience != "portal" && !admin(a) {
		return empty, fail("ACCESS_DENIED")
	}
	if _, e := s.reviewRepository(); e != nil {
		return empty, preparationRepositoryError(e)
	}
	query := url.Values{}
	for key, value := range values {
		query[key] = append([]string{}, value...)
	}
	state := ""
	if v, ok := query["state"]; ok {
		if len(v) != 1 || !validPreparationState(v[0]) {
			return empty, fail("VALIDATION_FAILED")
		}
		state = v[0]
		query.Del("state")
	}
	if _, ok := query["limit"]; !ok {
		query.Set("limit", strconv.Itoa(s.Identity.Config.Policy().UI.PageSize))
	}
	limit, cursor, scope, e := pagination.Parse(query, "model:preparations:"+a.Me.Audience+":"+a.Me.User.ID+":"+workspace+":"+state)
	if e != nil {
		return empty, e
	}
	page := Page[Preparation]{Items: []Preparation{}}
	var cursorTime, cursorID any
	if cursor.ID != "" {
		cursorTime, cursorID = cursor.Time.UTC(), cursor.ID
	}
	e = s.preparationActorTransaction(ctx, a, workspace, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if e := preparationSelectedCursor(q, cursor); e != nil {
			return e
		}
		rows, e := preparationQuery(ctx, q, preparationSelectedSQL(q, preparationNativePage(state, cursor.ID != ""), "SELECT "+preparationFields+" FROM model_preparations WHERE tenant_id=$1 AND workspace_id=$2 AND ($3='' OR state=$4) AND ($5::timestamptz IS NULL OR (created_at,id)<($6::timestamptz,$7::uuid)) ORDER BY created_at DESC,id DESC LIMIT $8", "SELECT "+mysqlPreparationFields+" FROM model_preparations WHERE tenant_id=? AND workspace_id=? AND (?='' OR state=?) AND (? IS NULL OR (created_at,id)<(?,?)) ORDER BY created_at DESC,id DESC LIMIT ?", w.TenantID, w.ID, state, state, cursorTime, cursorTime, cursorID, limit+1))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			p, e := scanPreparation(rows)
			if e != nil {
				return e
			}
			preparationTimestampsUTC(&p)
			page.Items = append(page.Items, p)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(page.Items) > limit {
			page.Items = page.Items[:limit]
			last := page.Items[len(page.Items)-1]
			page.NextCursor = pagination.Encode(last.CreatedAt, last.ID, scope)
		}
		return nil
	})
	if e != nil {
		return empty, e
	}
	return page, nil
}

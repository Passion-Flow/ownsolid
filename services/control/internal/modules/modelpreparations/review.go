package modelpreparations

import (
	"bytes"
	"context"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type ReviewRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	ArtifactID       string `json:"artifact_id"`
	ReportSHA256     string `json:"report_sha256"`
	Decision         string `json:"decision"`
	Name             string `json:"name"`
	Reason           string `json:"reason"`
}
type ModelReview struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspace_id"`
	PreparationID    string    `json:"preparation_id"`
	AttemptID        string    `json:"attempt_id"`
	ArtifactID       string    `json:"artifact_id"`
	ReportSHA256     string    `json:"report_sha256"`
	ExpectedRevision int64     `json:"expected_revision"`
	Decision         string    `json:"decision"`
	ModelID          *string   `json:"model_id"`
	Name             string    `json:"name"`
	ReviewedBy       string    `json:"reviewed_by"`
	Reason           string    `json:"reason"`
	CreatedAt        time.Time `json:"created_at"`
}

const reviewFields = `id::text,workspace_id::text,preparation_id::text,attempt_id::text,artifact_id::text,report_sha256,expected_revision,decision,model_id::text,model_name,reviewed_by::text,reason,created_at`

func scanReview(row transaction.Row) (ModelReview, error) {
	row = preparationIDRow(row, 0, 1, 2, 3, 4, 8, 10)
	var out ModelReview
	e := row.Scan(&out.ID, &out.WorkspaceID, &out.PreparationID, &out.AttemptID, &out.ArtifactID, &out.ReportSHA256, &out.ExpectedRevision, &out.Decision, &out.ModelID, &out.Name, &out.ReviewedBy, &out.Reason, &out.CreatedAt)
	return out, e
}

// Review binds a current explicit human decision to the independently verified stopped artifact.
func (s *Service) Review(ctx context.Context, a identity.Auth, workspace, id string, input ReviewRequest, key, request string) (ModelReview, error) {
	var out ModelReview
	if ctx == nil || !admin(a) {
		return out, fail("ACCESS_DENIED")
	}
	if !datawire.ValidID(id) || !datawire.ValidID(key) || !datawire.ValidID(input.ArtifactID) || !datawire.ValidDigest(input.ReportSHA256) || input.ExpectedRevision < 1 || input.ExpectedRevision >= 9007199254740991 || !text(input.Reason, 500) || (input.Decision != "approve" && input.Decision != "reject") {
		return out, fail("VALIDATION_FAILED")
	}
	if input.Decision == "approve" {
		name, e := security.Name(input.Name)
		if e != nil {
			return out, e
		}
		input.Name = name
	} else if input.Name != "" {
		return out, fail("VALIDATION_FAILED")
	}
	if _, e := s.reviewRepository(); e != nil {
		return out, preparationRepositoryError(e)
	}
	intent := fingerprint(a, struct {
		WorkspaceID, PreparationID string
		Input                      ReviewRequest
	}{workspace, id, input})
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().OutputSeconds))
	defer cancel()
	var snapshot reviewSnapshot
	replayed := false
	e := s.reviewTransaction(ctx, a, workspace, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		var e error
		out, replayed, e = s.replayReviewWithin(ctx, q, a, w, key, intent)
		if e != nil || replayed {
			return e
		}
		snapshot, e = s.reviewSnapshotWithin(ctx, q, a, w, id, input, input.Decision == "approve")
		return e
	})
	defer snapshot.Close()
	if e != nil {
		return ModelReview{}, e
	}
	if replayed {
		return out, nil
	}
	var prepared models.PreparedDerivedFiles
	if input.Decision == "approve" {
		expected, e := s.verifyReviewSnapshot(ctx, snapshot)
		if e != nil {
			return out, e
		}
		snapshot.expected = expected
		if s.Store == nil || s.Models == nil {
			return out, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
		}
		release, e := s.acquireScan(ctx)
		if e != nil {
			return out, e
		}
		defer release()
		files, e := s.Store.OpenPublishedFiles(ctx, snapshot.descriptor)
		if e != nil {
			return out, e
		}
		defer func() {
			for _, file := range files {
				file.Close()
			}
		}()
		info, e := snapshot.expected.BaseWeights.Stat()
		if e != nil {
			return out, fail("MODEL_PREPARATION_INPUT_CHANGED")
		}
		var base datawire.Asset
		for _, asset := range wire.ApprovedBaseFiles() {
			if asset.Path == "model.safetensors" {
				base = asset
			}
		}
		for _, asset := range snapshot.descriptor.Files {
			if asset.Path != "model.safetensors" {
				continue
			}
			verified, e := wire.VerifyMergedWeights(ctx, snapshot.expected.BaseWeights, info.Size(), base, files[asset.Path], asset.SizeBytes, true)
			if e != nil {
				return out, e
			}
			if verified.SHA256 != asset.SHA256 || verified.SizeBytes != asset.SizeBytes || !reflect.DeepEqual(verified.ChangedTargets, snapshot.expected.Report.ChangedTargets) {
				return out, fail("MODEL_PREPARATION_REPORT_INVALID")
			}
		}
		prepared, e = s.Models.PrepareDerivedFiles(ctx, reviewCatalog(snapshot), files, s.policy().MinimumFreeBytes)
		if e != nil {
			return out, e
		}
	}
	e = s.reviewTransaction(ctx, a, workspace, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		var e error
		var replay bool
		out, replay, e = s.replayReviewWithin(ctx, q, a, w, key, intent)
		if e != nil {
			return e
		}
		if replay {
			return nil
		}
		current, e := s.reviewSnapshotWithin(ctx, q, a, w, id, input, false)
		if e != nil {
			return e
		}
		if current.stopSHA != snapshot.stopSHA || current.expected.Preparation.InputSHA256 != snapshot.expected.Preparation.InputSHA256 || !bytes.Equal(current.raw, snapshot.raw) || !reflect.DeepEqual(current.descriptor, snapshot.descriptor) {
			return fail("MODEL_PREPARATION_REVIEW_BINDING_INVALID")
		}
		if input.Decision == "approve" {
			sources, _, e := s.retainedPreparationSourcesWithin(ctx, q, w, current.expected.Preparation.TrainingJobID, current.expected.Preparation.HeldoutID)
			if e != nil {
				return e
			}
			if sources.digest() != snapshot.expected.Preparation.InputSHA256 {
				return fail("MODEL_PREPARATION_INPUT_CHANGED")
			}
		}
		reviewID := uuid.NewString()
		var model *string
		if input.Decision == "approve" {
			model = &current.descriptor.UploadID
			staged, e := s.Models.StageDerivedWithin(ctx, q, a, w, prepared, reviewID, input.Name)
			if e != nil {
				return e
			}
			if staged != *model {
				return fail("MODEL_PREPARATION_REVIEW_BINDING_INVALID")
			}
		}
		if _, e = q.Exec(ctx, preparationSelectedSQL(q, preparationDM(`INSERT INTO "MODEL_PREPARATION_REVIEWS"("ID","TENANT_ID","WORKSPACE_ID","PREPARATION_ID","ATTEMPT_ID","ARTIFACT_ID","REPORT_SHA256","EXPECTED_REVISION","DECISION","MODEL_ID","MODEL_NAME","REVIEWED_BY","AUDIENCE","REASON","IDEMPOTENCY_KEY","FINGERPRINT") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'admin',?,?,?)`), `INSERT INTO model_preparation_reviews(id,tenant_id,workspace_id,preparation_id,attempt_id,artifact_id,report_sha256,expected_revision,decision,model_id,model_name,reviewed_by,audience,reason,idempotency_key,fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'admin',$13,$14,$15)`, `INSERT INTO model_preparation_reviews(id,tenant_id,workspace_id,preparation_id,attempt_id,artifact_id,report_sha256,expected_revision,decision,model_id,model_name,reviewed_by,audience,reason,idempotency_key,fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'admin',?,?,?)`, reviewID, w.TenantID, w.ID, id, current.descriptor.AttemptID, input.ArtifactID, input.ReportSHA256, input.ExpectedRevision, input.Decision, model, input.Name, a.Me.User.ID, input.Reason, key, intent)); e != nil {
			return e
		}
		state, action := "rejected", "model.preparation_review_rejected"
		if model != nil {
			registered, e := s.Models.RegisterDerivedWithin(ctx, q, a, w, prepared, reviewID, input.Name)
			if e != nil {
				return e
			}
			if registered != *model {
				return fail("MODEL_PREPARATION_REVIEW_BINDING_INVALID")
			}
			state, action = "approved", "model.preparation_review_approved"
		}
		n, e := q.Exec(ctx, preparationSelectedSQL(q, preparationDM(`UPDATE "MODEL_PREPARATIONS" SET "STATE"=?,"REVIEW_ID"=?,"DERIVED_MODEL_ID"=?,"REVISION"="REVISION"+1,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=? AND "STATE"='awaiting_review' AND "REVISION"=?`), "UPDATE model_preparations SET state=$1,review_id=$2,derived_model_id=$3,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$4 AND workspace_id=$5 AND id=$6 AND state='awaiting_review' AND revision=$7", "UPDATE model_preparations SET state=?,review_id=?,derived_model_id=?,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=? AND state='awaiting_review' AND revision=?", state, reviewID, model, w.TenantID, w.ID, id, input.ExpectedRevision))
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("MODEL_PREPARATION_REVISION_CONFLICT")
		}
		if model != nil {
			finalized, e := s.Models.FinalizeDerivedWithin(ctx, q, a, w, prepared, reviewID, input.Name)
			if e != nil {
				return e
			}
			if finalized != *model {
				return fail("MODEL_PREPARATION_REVIEW_BINDING_INVALID")
			}
		}
		if e = preparationAuditWithin(ctx, q, a, w, action, id, input.Reason, request); e != nil {
			return e
		}
		out, e = scanReview(preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationDM("SELECT "+nativeReviewFields+` FROM "MODEL_PREPARATION_REVIEWS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`), "SELECT "+reviewFields+" FROM model_preparation_reviews WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "SELECT "+mysqlReviewFields+" FROM model_preparation_reviews WHERE tenant_id=? AND workspace_id=? AND id=?", w.TenantID, w.ID, reviewID)))
		return e
	})
	if e != nil {
		return ModelReview{}, e
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return out, nil
}

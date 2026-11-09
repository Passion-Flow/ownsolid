package modelpreparations

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

func trainingSource(input preparationInputs) training.AcceptedAdapter {
	return training.AcceptedAdapter{Command: input.TrainingCommand, Descriptor: training.UploadDescriptor(input.Adapter)}
}

func (s *Service) Cancel(ctx context.Context, a identity.Auth, workspace, id string, input ActionRequest, request string) (Preparation, error) {
	var out Preparation
	if input.ExpectedRevision < 1 || !text(input.Reason, 500) {
		return out, fail("VALIDATION_FAILED")
	}
	e := s.preparationActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		p, e := lockedPreparationWithin(ctx, tx, w, id)
		if e != nil {
			return e
		}
		if !owner(a, w) && (p.CreatedBy != a.Me.User.ID || w.Role == nil || *w.Role != "member") {
			return fail("ACCESS_DENIED")
		}
		if p.Revision != input.ExpectedRevision {
			return fail("MODEL_PREPARATION_REVISION_CONFLICT")
		}
		switch p.State {
		case "quality_failed", "approved", "rejected", "cancelled", "failed", "cancelling":
			out = p
			return nil
		case "recovery_required":
			return fail("MODEL_PREPARATION_PHYSICAL_STOP_REQUIRED")
		}
		state := "cancelling"
		if p.State == "queued" || p.State == "awaiting_review" {
			state = "cancelled"
		}
		if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATIONS" SET "STATE"=?,"CANCEL_REQUESTED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"CANCEL_REQUESTED_BY"=?,"REVISION"="REVISION"+1,"WAIT_CODE"=NULL,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),"FINISHED_AT"=CASE WHEN ?='cancelled' THEN SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) ELSE NULL END WHERE "ID"=?`), `UPDATE model_preparations SET state=$2::varchar,cancel_requested_at=clock_timestamp(),cancel_requested_by=$3,revision=revision+1,wait_code=NULL,updated_at=clock_timestamp(),finished_at=CASE WHEN $2::varchar='cancelled' THEN clock_timestamp() ELSE NULL END WHERE id=$1`, `UPDATE model_preparations SET state=?,cancel_requested_at=UTC_TIMESTAMP(6),cancel_requested_by=?,revision=revision+1,wait_code=NULL,updated_at=UTC_TIMESTAMP(6),finished_at=CASE WHEN ?='cancelled' THEN UTC_TIMESTAMP(6) ELSE NULL END WHERE id=?`, []any{p.ID, state, a.Me.User.ID}, []any{state, a.Me.User.ID, state, p.ID})); e != nil {
			return e
		}
		if e = preparationAuditWithin(ctx, tx, a, w, "model.preparation_cancel_requested", p.ID, input.Reason, request); e != nil {
			return e
		}
		out, e = lockedPreparationWithin(ctx, tx, w, id)
		return e
	})
	if e != nil {
		return Preparation{}, e
	}
	return out, e
}

package training

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

func terminal(state string) bool {
	return state == "succeeded" || state == "failed" || state == "cancelled"
}

func (s *Service) Cancel(ctx context.Context, a identity.Auth, workspace, id string, input ActionRequest, request string) (Job, error) {
	var out Job
	if input.ExpectedRevision < 1 || !validReason(input.Reason) {
		return out, fail("VALIDATION_FAILED")
	}
	e := s.trainingActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		j, e := lockedJobWithin(ctx, tx, w, id)
		if e != nil {
			return e
		}
		if !owner(a, w) && j.CreatedBy != a.Me.User.ID {
			return fail("ACCESS_DENIED")
		}
		if j.Revision != input.ExpectedRevision {
			return fail("TRAINING_REVISION_CONFLICT")
		}
		if terminal(j.State) || j.State == "cancelling" {
			out = j
			return nil
		}
		if j.State == "recovery_required" {
			return fail("TRAINING_PHYSICAL_STOP_REQUIRED")
		}
		state := "cancelling"
		if j.State == "queued" {
			state = "cancelled"
		}
		args := []any{j.ID, state}
		if tx.Dialect() == transaction.MySQL {
			args = []any{state, state, w.TenantID, w.ID, j.ID}
		}
		n, e := tx.Exec(ctx, acceptedAdapterSQL(tx, `UPDATE training_jobs SET state=$2::varchar,cancel_requested_at=clock_timestamp(),wait_code=NULL,pending_recovery_id=NULL,revision=revision+1,finished_at=CASE WHEN $2::varchar='cancelled' THEN clock_timestamp() ELSE NULL END,updated_at=clock_timestamp() WHERE tenant_id=$3 AND workspace_id=$4 AND id=$1`, `UPDATE training_jobs SET state=?,cancel_requested_at=UTC_TIMESTAMP(6),wait_code=NULL,pending_recovery_id=NULL,revision=revision+1,finished_at=CASE WHEN ?='cancelled' THEN UTC_TIMESTAMP(6) ELSE NULL END,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`, cancellationArguments(tx, args, w)...))
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("STORAGE_UNAVAILABLE")
		}
		n, e = tx.Exec(ctx, acceptedAdapterSQL(tx, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,'training.cancel_requested','success','training_job',$6,$7,$8)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,?,'training.cancel_requested','success','training_job',?,?,?)`, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, id, input.Reason, request))
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("STORAGE_UNAVAILABLE")
		}
		out, e = lockedJobWithin(ctx, tx, w, id)
		return e
	})
	if e != nil {
		return Job{}, e
	}
	return out, nil
}

func cancellationArguments(q transaction.Executor, args []any, w workspaces.Workspace) []any {
	if q.Dialect() == transaction.PostgreSQL {
		return append(args, w.TenantID, w.ID)
	}
	return args
}

// Referenced preserves base artifacts for every retained training provenance reference.
func (s *Service) Referenced(ctx context.Context, q transaction.Executor, tenant, workspace, model string) (bool, error) {
	if s == nil || ctx == nil || q == nil {
		return false, fail("STORAGE_UNAVAILABLE")
	}
	statement := "SELECT EXISTS(SELECT 1 FROM training_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND model_id=$3)"
	if q.Dialect() == transaction.MySQL {
		statement = "SELECT EXISTS(SELECT 1 FROM training_jobs WHERE tenant_id=? AND workspace_id=? AND model_id=?)"
	}
	var found bool
	e := q.QueryRow(ctx, transaction.SQL(q.Dialect(), statement, tenant, workspace, model)).Scan(&found)
	return found, e
}

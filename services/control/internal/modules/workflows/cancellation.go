package workflows

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// Cancellation is durable and idempotent. The owning process also receives an
// immediate signal; another live process observes the same fence on heartbeat.
func (s *Service) CancelRun(ctx context.Context, a identity.Auth, workspace, workflow, id, reason, request string) (Run, error) {
	var out Run
	if !wire.ValidID(id) {
		return out, failure("WORKFLOW_RUN_NOT_FOUND")
	}
	if !validReason(reason) {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if w.Role == nil {
			return failure("ACCESS_DENIED")
		}
		if _, e := lockedWorkflow(ctx, tx, w, workflow); e != nil {
			return e
		}
		// Workflow lock serializes conversation mutations. Read its ID before taking
		// conversation -> run locks, preserving the executor's global lock order.
		var conversation *string
		if e := tx.QueryRow(ctx, workflowStatement(tx, workflowRunConversation, w.TenantID, w.ID, workflow, a.Me.User.ID, id)).Scan(&conversation); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return failure("WORKFLOW_RUN_NOT_FOUND")
			}
			return failure("STORAGE_UNAVAILABLE")
		}
		if conversation != nil {
			if _, e := lockedConversation(ctx, tx, a, w, workflow, *conversation); e != nil {
				return e
			}
		}
		r, e := scanRun(tx.QueryRow(ctx, workflowStatement(tx, workflowRunLock, w.TenantID, w.ID, workflow, a.Me.User.ID, id)))
		if e != nil {
			return e
		}
		if (r.State == PendingState || r.State == RunningState) && r.CancellationRequestedAt == nil {
			if e = workflowExecOne(ctx, tx, workflowStatement(tx, workflowCancelOne, id)); e != nil {
				return e
			}
			if e = s.audit(ctx, tx, a, w, "workflow.run_cancel_requested", "workflow_run", id, reason, request); e != nil {
				return e
			}
			r, e = scanRun(tx.QueryRow(ctx, workflowStatement(tx, workflowRunByID, id)))
			if e != nil {
				return e
			}
		}
		out, e = s.hydrateRun(ctx, tx, a, w, r)
		return e
	})
	if e != nil {
		return Run{}, e
	}
	if out.State == PendingState || out.State == RunningState {
		s.lifecycleMu.Lock()
		if cancel := s.running[id]; cancel != nil {
			cancel(failure("WORKFLOW_RUN_CANCELLED"))
		}
		s.lifecycleMu.Unlock()
	}
	return out, nil
}

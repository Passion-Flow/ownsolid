package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

// finishSuccess verifies the exact original question/graph/evidence in the same
// transaction as the private answer, usage, end-node result and success audit.
func (s *Service) finishSuccess(ctx context.Context, execution *runExecution, completed *knowledge.CompletedAnswer) error {
	if completed == nil {
		return failure("WORKFLOW_RESULT_INVALID")
	}
	answer := completed.Answer()
	if len(answer.Answer) > s.policy().AnswerBytes {
		return failure("WORKFLOW_RESULT_LIMIT_EXCEEDED")
	}
	return s.withExecutionWithin(ctx, execution, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, workflow workflowRecord, run runRecord) error {
		expected := knowledge.CompletionBinding{WorkspaceID: w.ID, BaseID: execution.graph.Retrieval.BaseID, AliasID: execution.graph.Answer.AliasID, Question: run.Question}
		if e := s.Knowledge.RecheckCompletedAnswerWithin(ctx, tx, execution.actor, expected, completed); e != nil {
			return e
		}
		evidence, e := json.Marshal(completed.Evidence())
		if e != nil {
			return failure("WORKFLOW_RESULT_INVALID")
		}
		alias, e := json.Marshal(modelSnapshot(completed.Alias()))
		if e != nil {
			return failure("WORKFLOW_RESULT_INVALID")
		}
		usage, e := json.Marshal(answer.Usage)
		if e != nil {
			return failure("WORKFLOW_RESULT_INVALID")
		}
		result, e := json.Marshal(TraceResult{AnswerBytes: len(answer.Answer), PromptTokens: answer.Usage.PromptTokens, CompletionTokens: answer.Usage.CompletionTokens, TotalTokens: answer.Usage.TotalTokens})
		if e != nil {
			return failure("WORKFLOW_RESULT_INVALID")
		}
		end, e := tx.Exec(ctx, workflowStatement(tx, workflowTraceEnd, execution.id, result))
		if e != nil {
			return e
		}
		if end != 1 {
			return failure("WORKFLOW_RUN_STALE")
		}
		if e = workflowExecOne(ctx, tx, workflowStatement(tx, workflowRunAnswerSuccess, execution.id, answer.Answer, evidence, alias, usage)); e != nil {
			return e
		}
		return s.audit(ctx, tx, execution.actor, w, "workflow.run_succeeded", "workflow_run", execution.id, "Actual current-evidence model result committed", execution.request)
	})
}
func runFailure(ctx context.Context, e error) (string, string) {
	if cause := context.Cause(ctx); cause != nil {
		e = cause
	}
	// A parent process signal can cancel the lifecycle before Stop installs its
	// named cause. Such cancellation is interruption, not a database failure.
	if errors.Is(e, context.Canceled) {
		return InterruptedState, "WORKFLOW_RUN_INTERRUPTED"
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return FailedState, "WORKFLOW_RUN_TIMEOUT"
	}
	code := httpserver.PublicErrorCode(e)
	switch code {
	case "WORKFLOW_RUN_CANCELLED":
		return CancelledState, code
	case "WORKFLOW_RUN_INTERRUPTED":
		return InterruptedState, code
	default:
		return FailedState, code
	}
}

// finishFailure permits only reduction of this exact already admitted run. It
// deliberately does not require a still-valid session and cannot create success,
// renew a lease, retry a model or release independent gateway capacity.
func (s *Service) finishFailure(ctx context.Context, execution *runExecution, state, code string) error {
	if state != FailedState && state != CancelledState && state != InterruptedState {
		return failure("VALIDATION_FAILED")
	}
	code = httpserver.PublicErrorCode(failure(code))
	return s.maintenanceWithin(ctx, func(ctx context.Context, tx transaction.Executor) error {
		var lockedActor string

		if e := tx.QueryRow(ctx, workflowStatement(tx, workflowLockActor, execution.actor.Me.User.ID)).Scan(&lockedActor); e != nil {
			return e
		}
		var id string
		var e error
		if e = tx.QueryRow(ctx, workflowStatement(tx, workflowLockWorkspace, execution.tenant, execution.workspace)).Scan(&id); e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, workflowStatement(tx, workflowLockIdentity, execution.tenant, execution.workspace, execution.workflow)).Scan(&id); e != nil {
			return e
		}
		if execution.conversation != nil {
			if e = tx.QueryRow(ctx, workflowStatement(tx, workflowLockOwnedConversation, execution.tenant, execution.workspace, execution.workflow, *execution.conversation, execution.actor.Me.User.ID)).Scan(&id); e != nil {
				return e
			}
		}
		r, e := scanRun(tx.QueryRow(ctx, workflowStatement(tx, workflowRunLock, execution.tenant, execution.workspace, execution.workflow, execution.actor.Me.User.ID, execution.id)))
		if e != nil && e.Error() == "WORKFLOW_RUN_NOT_FOUND" {
			return nil
		}
		if e != nil {
			return e
		}
		if r.State != PendingState && r.State != RunningState {
			return nil
		}
		if r.GraphSHA256 != execution.graph.SHA256 {
			return failure("WORKFLOW_RUN_STALE")
		}
		if r.State == RunningState && (r.processOwner == nil || r.leaseID == nil || *r.processOwner != execution.process || *r.leaseID != execution.lease) {
			return failure("WORKFLOW_RUN_STALE")
		}
		if _, e = tx.Exec(ctx, workflowStatement(tx, workflowTraceFailure, execution.id, state, code)); e != nil {
			return e
		}
		if e = workflowExecOne(ctx, tx, workflowStatement(tx, workflowRunFailure, execution.id, state, code)); e != nil {
			return e
		}
		w := workspaces.Workspace{ID: execution.workspace, TenantID: execution.tenant}
		if e = s.audit(ctx, tx, execution.actor, w, "workflow.run_"+state, "workflow_run", execution.id, "Owned workflow terminated without returning a result", execution.request); e != nil {
			return e
		}
		return nil
	})
}

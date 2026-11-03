package workflows

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"sync/atomic"
)

type runExecution struct {
	actor                                                                           identity.Auth
	tenant, workspace, workflow, id, process, lease, membership, authority, request string
	conversation                                                                    *string
	draftRevision                                                                   *int64
	graph                                                                           ParsedGraph
	prepared                                                                        atomic.Pointer[knowledge.PreparedAnswer]
}

func (s *Service) withExecutionWithin(ctx context.Context, execution *runExecution, pending bool, fn func(context.Context, transaction.Executor, workspaces.Workspace, workflowRecord, runRecord) error) error {
	return s.transaction(ctx, execution.actor, execution.workspace, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		return s.executionAuthorityWithin(ctx, q, w, execution, pending, fn)
	})
}
func (s *Service) executionAuthorityWithin(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, execution *runExecution, pending bool, fn func(context.Context, transaction.Executor, workspaces.Workspace, workflowRecord, runRecord) error) error {

	if w.Role == nil || w.TenantID != execution.tenant {
		return failure("ACCESS_DENIED")
	}
	if execution.draftRevision != nil && !editable(execution.actor, w) {
		return failure("ACCESS_DENIED")
	}
	membership, e := s.Workspaces.MembershipAuthorityWithin(ctx, tx, w.TenantID, w.ID, execution.actor.Me.User.ID)
	if e != nil {
		return e
	}
	if membership != execution.membership {
		return failure("WORKFLOW_AUTHORITY_CHANGED")
	}
	workflow, e := lockedWorkflow(ctx, tx, w, execution.workflow)
	if e != nil {
		return e
	}
	if !workflow.Enabled {
		return failure("WORKFLOW_DISABLED")
	}
	if workflow.authority != execution.authority {
		return failure("WORKFLOW_AUTHORITY_CHANGED")
	}
	if execution.draftRevision != nil && workflow.Revision != *execution.draftRevision {
		return failure("WORKFLOW_DRAFT_CHANGED")
	}
	if execution.conversation != nil {
		if _, e = lockedConversation(ctx, tx, execution.actor, w, execution.workflow, *execution.conversation); e != nil {
			return e
		}
	}
	run, e := scanRun(tx.QueryRow(ctx, workflowStatement(tx, workflowRunLock, w.TenantID, w.ID, execution.workflow, execution.actor.Me.User.ID, execution.id)))
	if e != nil {
		return e
	}
	expected := RunningState
	if pending {
		expected = PendingState
	}
	if run.State != expected || run.GraphSHA256 != execution.graph.SHA256 || run.audience != execution.actor.Me.Audience || run.authority != execution.authority || run.membership != execution.membership {
		return failure("WORKFLOW_RUN_STALE")
	}
	if run.CancellationRequestedAt != nil {
		return failure("WORKFLOW_RUN_CANCELLED")
	}
	if !pending && (run.processOwner == nil || run.leaseID == nil || *run.processOwner != execution.process || *run.leaseID != execution.lease) {
		return failure("WORKFLOW_RUN_STALE")
	}
	var timely bool
	if e = tx.QueryRow(ctx, workflowStatement(tx, workflowRunTimely, execution.id, pending)).Scan(&timely); e != nil {
		return e
	}
	if !timely {
		return failure("WORKFLOW_RUN_TIMEOUT")
	}
	return fn(ctx, tx, w, workflow, run)
}
func (s *Service) claimExecution(ctx context.Context, execution *runExecution) error {
	return s.withExecutionWithin(ctx, execution, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, workflow workflowRecord, run runRecord) error {
		if _, e := s.references(ctx, tx, execution.actor, w, execution.graph); e != nil {
			return e
		}
		if e := workflowExecOne(ctx, tx, workflowStatement(tx, workflowRunClaim, execution.id, execution.process, execution.lease, s.policy().LeaseSeconds)); e != nil {
			return e
		}
		return s.audit(ctx, tx, execution.actor, w, "workflow.run_started", "workflow_run", execution.id, "Current caller and pinned graph claimed for execution", execution.request)
	})
}
func (s *Service) renewExecution(ctx context.Context, execution *runExecution) error {
	return s.withExecutionWithin(ctx, execution, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, workflow workflowRecord, run runRecord) error {
		if prepared := execution.prepared.Load(); prepared != nil {
			if e := s.Knowledge.RecheckPreparedAnswerWithin(ctx, tx, execution.actor, prepared); e != nil {
				return e
			}
		} else if _, e := s.references(ctx, tx, execution.actor, w, execution.graph); e != nil {
			return e
		}
		e := workflowExecOne(ctx, tx, workflowStatement(tx, workflowRunRenew, execution.id, s.policy().LeaseSeconds))
		return e
	})
}
func (s *Service) nodeTransition(ctx context.Context, execution *runExecution, kind, previous, next string, result TraceResult) error {
	return s.withExecutionWithin(ctx, execution, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, workflow workflowRecord, run runRecord) error {
		metadata, e := json.Marshal(result)
		if e != nil {
			return failure("WORKFLOW_RESULT_INVALID")
		}
		changed, e := tx.Exec(ctx, workflowStatement(tx, workflowTraceTransition, execution.id, kind, previous, next, metadata))
		if e != nil {
			return e
		}
		if changed != 1 {
			return failure("WORKFLOW_RUN_STALE")
		}
		action := "workflow.node_started"
		if next == SucceededState {
			action = "workflow.node_succeeded"
		}
		return s.audit(ctx, tx, execution.actor, w, action, "workflow_run", execution.id, "Bounded pinned workflow node state transition", uuid.NewString())
	})
}

package workflows

import (
	"context"
	"encoding/json"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

func (s *Service) executeToolNode(ctx context.Context, execution *runExecution, step func(func(context.Context) error) error) (*plugins.CompletedModelList, error) {
	version := execution.graph.Tool.PluginVersionID
	if s.Plugins == nil {
		return nil, failure("PLUGIN_UNAVAILABLE")
	}
	if e := step(func(operation context.Context) error {
		return s.nodeTransition(operation, execution, ToolNode, NotStartedState, RunningState, TraceResult{ToolVersionID: version})
	}); e != nil {
		return nil, e
	}
	var completed *plugins.CompletedModelList
	e := step(func(operation context.Context) error {
		return s.withExecutionWithin(operation, execution, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, _ workflowRecord, r runRecord) error {
			var e error
			completed, e = s.Plugins.ExecuteModelListWithin(ctx, tx, execution.actor, w.ID, version, r.ID, r.Question)
			return e
		})
	})
	if e != nil {
		return nil, e
	}
	count := len(completed.Result().Items)
	e = step(func(operation context.Context) error {
		return s.nodeTransition(operation, execution, ToolNode, RunningState, SucceededState, TraceResult{ToolVersionID: version, ModelCount: &count})
	})
	return completed, e
}

// finishToolSuccess stores a real SQL result without manufacturing an answer,
// citations, gateway request or usage. Exact grant/caller authority is fenced
// atomically with the terminal trace, private result and audit event.
func (s *Service) finishToolSuccess(ctx context.Context, execution *runExecution, completed *plugins.CompletedModelList) error {
	if s.Plugins == nil || completed == nil {
		return failure("PLUGIN_RESULT_INVALID")
	}
	result := completed.Result()
	raw, e := json.Marshal(result)
	if e != nil {
		return failure("PLUGIN_RESULT_INVALID")
	}
	return s.withExecutionWithin(ctx, execution, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, _ workflowRecord, r runRecord) error {
		if r.ResultKind != "model_list" {
			return failure("WORKFLOW_RESULT_INVALID")
		}
		if e = s.Plugins.RecheckCompletedModelListWithin(ctx, tx, execution.actor, w.ID, execution.graph.Tool.PluginVersionID, r.ID, r.Question, completed); e != nil {
			return e
		}
		count := len(result.Items)
		metadata, _ := json.Marshal(TraceResult{ToolVersionID: execution.graph.Tool.PluginVersionID, ModelCount: &count})
		changed, e := tx.Exec(ctx, workflowStatement(tx, workflowTraceEnd, r.ID, metadata))
		if e != nil {
			return e
		}
		if changed != 1 {
			return failure("WORKFLOW_RUN_STALE")
		}
		if e = workflowExecOne(ctx, tx, workflowStatement(tx, workflowRunToolSuccess, r.ID, raw, completed.StoredProof())); e != nil {
			return e
		}
		return s.audit(ctx, tx, execution.actor, w, "workflow.run_succeeded", "workflow_run", r.ID, "Actual current-caller signed model directory committed", execution.request)
	})
}

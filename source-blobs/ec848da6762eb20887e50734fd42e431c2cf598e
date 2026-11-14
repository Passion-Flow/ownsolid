package workflows

import (
	"context"
	"log/slog"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"time"
)

// execute owns one bounded admitted run. Each IO step closes its own database
// transactions before calling Python or Rust. Every terminal write is fenced.
func (s *Service) execute(ctx context.Context, terminate context.CancelCauseFunc, execution *runExecution, question string) {
	step := func(fn func(context.Context) error) error {
		operation, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
		defer cancel()
		return fn(operation)
	}
	heartbeat, stopHeartbeat := context.WithCancel(ctx)
	joined := make(chan struct{})
	// Claim before starting renewal: a pending run has no lease to renew.
	e := step(func(operation context.Context) error { return s.claimExecution(operation, execution) })
	claimed := e == nil
	go func() {
		if !claimed {
			close(joined)
			return
		}
		defer close(joined)
		ticker := time.NewTicker(config.Seconds(s.policy().HeartbeatSeconds))
		defer ticker.Stop()
		for {
			select {
			case <-heartbeat.Done():
				return
			case <-ticker.C:
				operation, cancel := context.WithTimeout(heartbeat, config.Seconds(s.policy().CommitSeconds))
				e := s.renewExecution(operation, execution)
				cancel()
				if e != nil {
					if heartbeat.Err() == nil {
						terminate(e)
					}
					return
				}
			}
		}
	}()
	finishHeartbeat := func() { stopHeartbeat(); <-joined }
	if e == nil {
		e = step(func(operation context.Context) error {
			return s.nodeTransition(operation, execution, StartNode, NotStartedState, RunningState, TraceResult{QuestionBytes: len(question)})
		})
	}
	if e == nil {
		e = step(func(operation context.Context) error {
			return s.nodeTransition(operation, execution, StartNode, RunningState, SucceededState, TraceResult{QuestionBytes: len(question)})
		})
	}
	var completed *knowledge.CompletedAnswer
	var toolCompleted *plugins.CompletedModelList
	if execution.graph.IsTool() {
		if e == nil {
			toolCompleted, e = s.executeToolNode(ctx, execution, step)
		}
	} else {
		if e == nil {
			e = step(func(operation context.Context) error {
				return s.nodeTransition(operation, execution, RetrievalNode, NotStartedState, RunningState, TraceResult{})
			})
		}
		var prepared *knowledge.PreparedAnswer
		if e == nil {
			operation, cancel := context.WithTimeoutCause(ctx, config.Seconds(s.policy().RetrievalSeconds), failure("WORKFLOW_RUN_TIMEOUT"))
			prepared, e = s.Knowledge.PrepareAnswer(operation, execution.actor, execution.workspace, execution.graph.Retrieval.BaseID, knowledge.AnswerRequest{Question: question, AliasID: execution.graph.Answer.AliasID, TopK: execution.graph.Retrieval.TopK, MinimumScore: execution.graph.Retrieval.MinimumScore}, execution.request)
			if e != nil && operation.Err() != nil {
				e = context.Cause(operation)
			}
			cancel()
			if e == nil {
				execution.prepared.Store(prepared)
			}
		}
		if e == nil {
			evidence := prepared.Evidence()
			e = step(func(operation context.Context) error {
				return s.nodeTransition(operation, execution, RetrievalNode, RunningState, SucceededState, TraceResult{SourceCount: len(evidence.Sources), GenerationID: evidence.GenerationID})
			})
		}
		if e == nil {
			e = step(func(operation context.Context) error {
				return s.nodeTransition(operation, execution, AnswerNode, NotStartedState, RunningState, TraceResult{AliasID: execution.graph.Answer.AliasID})
			})
		}
		if e == nil {
			operation, cancel := context.WithTimeoutCause(ctx, config.Seconds(s.policy().AnswerSeconds), failure("WORKFLOW_RUN_TIMEOUT"))
			completed, e = s.Knowledge.CompletePreparedAnswer(operation, execution.actor, prepared, execution.graph.Answer.Instructions, execution.request)
			if e != nil && operation.Err() != nil {
				e = context.Cause(operation)
			}
			cancel()
		}
		if e == nil {
			answer := completed.Answer()
			e = step(func(operation context.Context) error {
				return s.nodeTransition(operation, execution, AnswerNode, RunningState, SucceededState, TraceResult{AliasID: answer.AliasID, AnswerBytes: len(answer.Answer), PromptTokens: answer.Usage.PromptTokens, CompletionTokens: answer.Usage.CompletionTokens, TotalTokens: answer.Usage.TotalTokens})
			})
		}
	}
	// Join renewal before terminal write so a normal completed row cannot be
	// mistaken for lost authority by a racing final heartbeat.
	finishHeartbeat()
	if e == nil {
		e = step(func(operation context.Context) error {
			return s.nodeTransition(operation, execution, EndNode, NotStartedState, RunningState, TraceResult{})
		})
	}
	if e == nil {
		e = step(func(operation context.Context) error {
			if execution.graph.IsTool() {
				return s.finishToolSuccess(operation, execution, toolCompleted)
			}
			return s.finishSuccess(operation, execution, completed)
		})
	}
	if e == nil {
		return
	}
	if uncertainWorkflowCommit(e) || uncertainWorkflowCommit(context.Cause(ctx)) {
		// Durable lease expiry can reconcile an abandoned run. Do not execute another
		// tool/model or overwrite a possibly committed terminal result here.
		slog.Warn("workflow_commit_requires_reconciliation", "run_id", execution.id, "request_id", execution.request)
		return
	}
	state, code := runFailure(ctx, e)
	slog.Warn("workflow_run_terminated", "state", state, "code", code, "run_id", execution.id, "request_id", execution.request)
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	if s.finishFailure(cleanup, execution, state, code) != nil {
		s.lifecycleMu.Lock()
		s.maintenanceError = "WORKFLOW_MAINTENANCE_FAILED"
		s.publishConfigurationLocked()
		s.lifecycleMu.Unlock()
	}
}

func uncertainWorkflowCommit(e error) bool {
	kind, ok := transaction.CommitOutcome(e)
	return ok && kind == transaction.CommitUnknown
}

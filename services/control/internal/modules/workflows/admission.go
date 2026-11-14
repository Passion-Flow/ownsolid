package workflows

import (
	"context"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validQuestion(question string, maximum int) bool {
	return utf8.ValidString(question) && strings.TrimSpace(question) != "" && len(question) <= maximum && !strings.ContainsFunc(question, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}
func (s *Service) Debug(ctx context.Context, a identity.Auth, workspace, workflow string, input DebugRequest, key, request string) (Run, error) {
	if input.ExpectedRevision < 1 || input.ExpectedRevision > MaximumRevision {
		return Run{}, failure("VALIDATION_FAILED")
	}
	return s.admit(ctx, a, workspace, workflow, nil, &input.ExpectedRevision, RunRequest{Question: input.Question, Reason: input.Reason}, key, request)
}
func (s *Service) RunConversation(ctx context.Context, a identity.Auth, workspace, workflow, conversation string, input RunRequest, key, request string) (Run, error) {
	return s.admit(ctx, a, workspace, workflow, &conversation, nil, input, key, request)
}

// admit serializes process admission with shutdown. Database locks additionally
// bound every live process in a Workspace and every conversation's active turn.
// An idempotent replay consumes no execution slot and never restarts a model.
func (s *Service) admit(ctx context.Context, a identity.Auth, workspace, workflow string, conversation *string, revision *int64, input RunRequest, key, request string) (Run, error) {
	var out Run
	if !s.Identity.Config.Workflows.Enabled {
		return out, failure("WORKFLOW_UNAVAILABLE")
	}
	if !validReason(input.Reason) || !validQuestion(input.Question, s.questionBytes()) {
		return out, failure("VALIDATION_FAILED")
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if !s.started || s.closing || s.lifecycleContext.Err() != nil || s.maintenanceError != "" {
		return out, failure("WORKFLOW_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	var execution *runExecution
	reserved := false
	launched := false
	defer func() {
		if reserved && !launched {
			<-s.active
		}
	}()
	purpose := "debug:" + workflow
	if conversation != nil {
		purpose = "run:" + *conversation
	}
	intentInput := struct {
		Workflow     string
		Conversation *string
		Revision     *int64
		Input        RunRequest
	}{workflow, conversation, revision, input}
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if w.Role == nil {
			return failure("ACCESS_DENIED")
		}
		previous, digest, e := s.intent(ctx, tx, a, w, purpose, key, intentInput)
		if e != nil {
			return e
		}
		b, e := lockedWorkflow(ctx, tx, w, workflow)
		if e != nil {
			return e
		}
		if previous != "" {
			r, e := scanRun(tx.QueryRow(ctx, workflowStatement(tx, workflowRunVisible, w.TenantID, w.ID, workflow, a.Me.User.ID, previous)))
			if e != nil {
				return e
			}
			out, e = s.hydrateRun(ctx, tx, a, w, r)
			return e
		}
		if !b.Enabled {
			return failure("WORKFLOW_DISABLED")
		}
		var graph ParsedGraph
		var version *string
		var turn *int
		if conversation == nil {
			if !editable(a, w) {
				return failure("ACCESS_DENIED")
			}
			if revision == nil || b.Revision != *revision {
				return failure("WORKFLOW_DRAFT_CHANGED")
			}
			graph, e = s.DecodePublishedGraph(b.graph, b.digest)
		} else {
			c, err := lockedConversation(ctx, tx, a, w, workflow, *conversation)
			if err != nil {
				return err
			}
			if c.TurnCount >= s.policy().MaximumTurns {
				return failure("WORKFLOW_LIMIT_EXCEEDED")
			}
			var active bool
			if err = tx.QueryRow(ctx, workflowStatement(tx, workflowConversationBusy, c.ID)).Scan(&active); err != nil {
				return err
			}
			if active {
				return failure("WORKFLOW_CONVERSATION_BUSY")
			}
			v, err := scanVersion(tx.QueryRow(ctx, workflowStatement(tx, workflowVersionScoped, w.TenantID, w.ID, workflow, c.VersionID)))
			if err != nil {
				return err
			}
			graph, e = s.DecodePublishedGraph(v.Graph, v.GraphSHA256)
			version = &c.VersionID
			next := c.TurnCount + 1
			turn = &next
		}
		if e != nil {
			return e
		}
		if _, e = s.references(ctx, tx, a, w, graph); e != nil {
			return e
		}
		membership, e := s.Workspaces.MembershipAuthorityWithin(ctx, tx, w.TenantID, w.ID, a.Me.User.ID)
		if e != nil {
			return e
		}
		var count int
		if e = tx.QueryRow(ctx, workflowStatement(tx, workflowLiveCount, w.TenantID, w.ID)).Scan(&count); e != nil {
			return e
		}
		if count >= s.policy().WorkspaceConcurrency {
			return failure("WORKFLOW_CAPACITY_EXCEEDED")
		}
		if e = tx.QueryRow(ctx, workflowStatement(tx, workflowRunCount, w.TenantID, w.ID, a.Me.User.ID)).Scan(&count); e != nil {
			return e
		}
		if count >= s.policy().MaximumRuns {
			return failure("WORKFLOW_LIMIT_EXCEEDED")
		}
		select {
		case s.active <- struct{}{}:
			reserved = true
		default:
			return failure("WORKFLOW_CAPACITY_EXCEEDED")
		}
		id := uuid.NewString()
		if e = workflowExecOne(ctx, tx, workflowStatement(tx, workflowRunCreate, id, w.TenantID, w.ID, workflow, a.Me.User.ID, conversation, turn, version, revision, b.authority, membership, a.Me.Audience, graph.CanonicalJSON, graph.SHA256, input.Question, s.policy().RunSeconds, graph.ResultKind())); e != nil {
			return e
		}
		for _, node := range graph.Graph.Nodes {
			if e = workflowExecOne(ctx, tx, workflowStatement(tx, workflowTraceCreate, w.TenantID, w.ID, workflow, id, node.ID, node.Kind)); e != nil {
				return e
			}
		}
		if conversation != nil {
			if e = workflowExecOne(ctx, tx, workflowStatement(tx, workflowConversationTurn, *conversation, *turn)); e != nil {
				return e
			}
		}
		if e = saveIntent(ctx, tx, a, w, purpose, key, id, digest); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "workflow.run_accepted", "workflow_run", id, input.Reason, request); e != nil {
			return e
		}
		r, e := scanRun(tx.QueryRow(ctx, workflowStatement(tx, workflowRunByID, id)))
		if e != nil {
			return e
		}
		out, e = s.hydrateRun(ctx, tx, a, w, r)
		if e != nil {
			return e
		}
		actor := a
		actor.TokenHash = append([]byte(nil), a.TokenHash...)
		execution = &runExecution{actor: actor, tenant: w.TenantID, workspace: w.ID, workflow: workflow, id: id, process: s.processOwner, lease: uuid.NewString(), membership: membership, authority: b.authority, request: request, conversation: conversation, draftRevision: revision, graph: graph}
		return nil
	})
	if e != nil {
		return Run{}, e
	}
	if execution != nil {
		job, cancel := context.WithDeadlineCause(s.lifecycleContext, out.DeadlineAt, failure("WORKFLOW_RUN_TIMEOUT"))
		job, terminate := context.WithCancelCause(job)
		s.running[execution.id] = terminate
		s.workers.Add(1)
		launched = true
		go func() {
			defer s.workers.Done()
			defer cancel()
			defer terminate(nil)
			defer func() { s.lifecycleMu.Lock(); delete(s.running, execution.id); s.lifecycleMu.Unlock(); <-s.active }()
			s.execute(job, terminate, execution, input.Question)
		}()
	}
	return out, nil
}

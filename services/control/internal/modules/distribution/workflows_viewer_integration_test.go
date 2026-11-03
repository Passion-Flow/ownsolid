//go:build knowledge_gateway_integration

package distribution_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
)

func verifyWorkflowViewerAuthority(t *testing.T, ctx context.Context, c scenario, s *workflows.Service, actor identity.Auth, publishedWorkflow, version string, graph workflows.Graph, question string, barrier *atomic.Pointer[completionBarrier]) {
	t.Helper()
	w := c.p.Me.Workspace.ID
	draft, e := s.Create(ctx, actor, workflows.CreateRequest{WorkspaceID: w, Name: "Current editor authority fence", Reason: "Actual user-owned saved draft"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	draft, e = s.Update(ctx, actor, w, draft.ID, workflows.UpdateRequest{Name: draft.Name, Graph: graph, Enabled: true, ExpectedRevision: draft.Revision, Reason: "Actual configured editor draft"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	held := &completionBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	barrier.Store(held)
	defer func() {
		select {
		case <-held.release:
		default:
			close(held.release)
		}
		barrier.CompareAndSwap(held, nil)
	}()
	run, e := s.Debug(ctx, actor, w, draft.ID, workflows.DebugRequest{Question: question, ExpectedRevision: draft.Revision, Reason: "Actual debug before permission downgrade"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-held.entered:
	case <-ctx.Done():
		t.Fatal("editor downgrade model transport barrier")
	}
	var revision int64
	if e = s.DB.QueryRow(ctx, "SELECT version FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", w, actor.Me.User.ID).Scan(&revision); e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.Workspaces.ChangeMember(ctx, c.a, w, actor.Me.User.ID, "viewer", revision, "Actual in-flight editor downgrade", uuid.NewString(), false); e != nil {
		t.Fatal(e)
	}
	close(held.release)
	barrier.CompareAndSwap(held, nil)
	wait := func(workflow, id string) workflows.Run {
		t.Helper()
		operation, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			r, e := s.GetRun(operation, actor, w, workflow, id)
			if e != nil {
				t.Fatal(e)
			}
			if r.State != "pending" && r.State != "running" {
				return r
			}
			select {
			case <-operation.Done():
				t.Fatal("viewer actual run deadline")
			case <-ticker.C:
			}
		}
	}
	run = wait(draft.ID, run.ID)
	if run.State != "failed" || workflowTestCode(run.ErrorCode) != "ACCESS_DENIED" || run.Answer != nil || run.ResultAvailable {
		t.Fatal("actual debug survived editor downgrade", run.State, workflowTestCode(run.ErrorCode))
	}
	configuration, e := s.Configuration(ctx, actor, w)
	if e != nil || configuration.CanEdit || !configuration.CanExecute {
		t.Fatal("viewer ordinary execution rights changed", e)
	}
	conversation, e := s.CreateConversation(ctx, actor, w, publishedWorkflow, workflows.ConversationRequest{VersionID: version, Reason: "Actual current viewer published access"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("viewer publication access", e)
	}
	run, e = s.RunConversation(ctx, actor, w, publishedWorkflow, conversation.ID, workflows.RunRequest{Question: question, Reason: "Actual viewer ordinary model authority"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("viewer actual admission", e)
	}
	run = wait(publishedWorkflow, run.ID)
	if run.State != "succeeded" || !run.ResultAvailable || run.Answer == nil || !strings.Contains(*run.Answer, "Lisbon") || !strings.Contains(*run.Answer, "[S1]") || run.Usage == nil || run.Usage.CompletionTokens < 1 {
		t.Fatal("current viewer published execution failed", run.State, workflowTestCode(run.ErrorCode))
	}
	if _, e = s.GetRun(ctx, c.p, w, publishedWorkflow, run.ID); e == nil || e.Error() != "WORKFLOW_RUN_NOT_FOUND" {
		t.Fatal("editor read viewer private answer", e)
	}
}

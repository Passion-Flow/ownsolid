//go:build repository_integration

package workflows

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func workflowActualProcessor(t *testing.T, g workflowRuntimeStory) {
	t.Helper()
	scratch := t.TempDir()
	if e := os.Chmod(scratch, 0700); e != nil {
		t.Fatal(e)
	}
	repository := os.Getenv("PROJECT_ONE_REPOSITORY_ROOT")
	if repository == "" || !filepath.IsAbs(repository) || os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON") == "" || os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY") == "" {
		t.Fatal("source-bound actual offline encoder inputs required; no fake readiness")
	}
	g.owner.Config.Knowledge = config.KnowledgeConfiguration{Enabled: true, PythonExecutable: os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON"), WorkerFile: filepath.Join(repository, "runtimes/knowledge/embed.py"), ModelDirectory: os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY"), ScratchRoot: scratch}
	if e := g.s.Knowledge.OpenConfiguredProcessor(g.ctx); e != nil {
		t.Fatal("actual CPU encoder bridge/readiness", e)
	}
	t.Cleanup(g.s.Knowledge.Processor.Close)
}
func workflowRuntimeStart(t *testing.T, s *Service, ctx context.Context) {
	t.Helper()
	if e := s.Start(ctx); e != nil {
		t.Fatal("actual owned workflow lifecycle", e)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := s.Stop(cleanup); e != nil {
			t.Error("actual workflow join", e)
		}
	})
}
func workflowRuntimeWait(t *testing.T, g workflowRuntimeStory, workflow, id string) Run {
	t.Helper()
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		result, e := g.s.GetRun(g.ctx, g.a, g.f.Workspace, workflow, id)
		if e != nil {
			t.Fatal(e)
		}
		if result.State != PendingState && result.State != RunningState {
			return result
		}
		select {
		case <-timer.C:
			t.Fatal("actual owned tool interpreter did not terminate")
		case <-g.ctx.Done():
			t.Fatal("actual owned interpreter context", context.Cause(g.ctx))
		case <-tick.C:
		}
	}
}

// Real offline encoder readiness, actual signed tool interpreter and real paired
// SQL lifecycle. There is no retrieval/model-answer/gateway qualification here;
// the tool produces the actual current scoped SQL directory without fake usage.
func TestActualPairedWorkflowInterpreterRuntime(t *testing.T) {
	t.Run("actual_processor_admission_replay_capacity_unknown_and_join", func(t *testing.T) {
		g := workflowRuntimeSetup(t)
		workflowActualProcessor(t, g)
		policy := g.owner.Config.Policy()
		policy.Workflows.Concurrency = 1
		policy.Workflows.WorkspaceConcurrency = 1
		g.owner.Config.Runtime = &policy
		v, graph := g.tool(t, "Real bounded interpreter")
		_, conversation := g.publish(t, v)
		pending := g.execution(t, v, graph, &conversation)
		workflowRuntimeStart(t, g.s, g.ctx)
		if e := g.s.VerifyStored(g.ctx); e != nil {
			t.Fatal(e)
		}
		input := RunRequest{Question: "Actual signed directory request \u4e16\u754c", Reason: "Actual durable interpreter admission"}
		_, e := g.s.RunConversation(g.ctx, g.a, g.f.Workspace, v.ID, conversation.ID, input, uuid.NewString(), uuid.NewString())
		workflowRuntimeWant(t, e, "WORKFLOW_CONVERSATION_BUSY")
		another, e := g.s.CreateConversation(g.ctx, g.a, g.f.Workspace, v.ID, ConversationRequest{VersionID: conversation.VersionID, Reason: "Actual capacity fence"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		_, e = g.s.RunConversation(g.ctx, g.a, g.f.Workspace, v.ID, another.ID, input, uuid.NewString(), uuid.NewString())
		workflowRuntimeWant(t, e, "WORKFLOW_CAPACITY_EXCEEDED")
		if e = g.s.finishFailure(g.ctx, pending, CancelledState, "WORKFLOW_RUN_CANCELLED"); e != nil {
			t.Fatal(e)
		}
		invoked := g.auditCount(t, "plugin.invoked")
		key := uuid.NewString()
		admitted, e := g.s.RunConversation(g.ctx, g.a, g.f.Workspace, v.ID, conversation.ID, input, key, uuid.NewString())
		if e != nil || admitted.ID == "" {
			t.Fatal("actual durable admission", e)
		}
		result := workflowRuntimeWait(t, g, v.ID, admitted.ID)
		if result.State != SucceededState || !result.ResultAvailable || result.ToolResult == nil || result.Answer != nil || result.Usage != nil || len(result.Nodes) != 3 {
			t.Fatal("actual tool interpreter failed", result.State, result.ErrorCode)
		}
		for _, node := range result.Nodes {
			if node.State != SucceededState || node.StartedAt == nil || node.FinishedAt == nil {
				t.Fatal("actual node transition missing", node.Kind)
			}
		}
		replay, e := g.s.RunConversation(g.ctx, g.a, g.f.Workspace, v.ID, conversation.ID, input, key, uuid.NewString())
		if e != nil || replay.ID != result.ID || g.auditCount(t, "plugin.invoked") != invoked+1 {
			t.Fatal("exact replay reinvoked tool", e)
		}

		// The only fault is a lost COMMIT acknowledgement after the real store
		// completed. Build a distinct real service identity; never copy live mutexes.
		owner := &identity.Service{Storage: workflowLostAcknowledgement{g.f.Store}, Config: g.owner.Config, HMACKey: append([]byte{}, g.owner.HMACKey...)}
		ws := &workspaces.Service{Identity: owner}
		model := &models.Service{Identity: owner, Workspaces: ws}
		d := &distribution.Service{Identity: owner, Workspaces: ws}
		p := &plugins.Service{Identity: owner, Workspaces: ws, Models: model}
		k := &knowledge.Service{Identity: owner, Workspaces: ws, Distribution: d, Processor: g.s.Knowledge.Processor}
		uncertain := &Service{Identity: owner, Workspaces: ws, Knowledge: k, Plugins: p, Distribution: d}
		if e = p.Start(g.ctx); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if e := p.Stop(cleanup); e != nil {
				t.Error(e)
			}
		})
		workflowRuntimeStart(t, uncertain, g.ctx)
		unknownKey := uuid.NewString()
		out, e := uncertain.RunConversation(g.ctx, g.a, g.f.Workspace, v.ID, another.ID, input, unknownKey, uuid.NewString())
		workflowRuntimeWant(t, e, string(transaction.CommitUnknown))
		if out.ID != "" {
			t.Fatal("unknown admission projected accepted run")
		}
		uncertain.lifecycleMu.Lock()
		launched := len(uncertain.running)
		slots := len(uncertain.active)
		uncertain.lifecycleMu.Unlock()
		if launched != 0 || slots != 0 || g.auditCount(t, "plugin.invoked") != invoked+1 {
			t.Fatal("unknown admission launched worker or retained local slot")
		}
		recovered, e := g.s.RunConversation(g.ctx, g.a, g.f.Workspace, v.ID, another.ID, input, unknownKey, uuid.NewString())
		if e != nil || recovered.ID == "" || recovered.State != PendingState || g.auditCount(t, "plugin.invoked") != invoked+1 {
			t.Fatal("exact pending intent recovery adopted unknown work", e)
		}
		reduction := &runExecution{actor: g.a, tenant: g.f.Tenant, workspace: g.f.Workspace, workflow: v.ID, id: recovered.ID, conversation: &another.ID, graph: graph, request: uuid.NewString()}
		if e = g.s.finishFailure(g.ctx, reduction, InterruptedState, "WORKFLOW_RUN_INTERRUPTED"); e != nil {
			t.Fatal("explicit retained pending reduction", e)
		}
		if e = uncertain.Stop(g.ctx); e != nil {
			t.Fatal(e)
		}
		if e = g.s.Stop(g.ctx); e != nil {
			t.Fatal(e)
		}
		if e = g.s.VerifyStored(g.ctx); e == nil {
			t.Fatal("joined stopped lifecycle reported ready")
		}
	})
}

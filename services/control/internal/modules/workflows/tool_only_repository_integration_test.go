//go:build repository_integration

package workflows

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
)

// Each leaf needs a fresh selected PostgreSQL/MySQL fixture. These tests use
// actual signed packages, SQL and the workflow interpreter, with no encoder,
// gateway, knowledge service or distribution service. SQL state fixtures below
// test cancellation/recovery/history boundaries, not historical model execution.
func workflowToolOnlySetup(t *testing.T) workflowRuntimeStory {
	t.Helper()
	g := workflowRuntimeSetup(t)
	g.s.Knowledge = nil
	g.s.Distribution = nil
	if g.owner.Config.Knowledge.Enabled {
		t.Fatal("fixture unexpectedly enabled knowledge")
	}
	return g
}

func TestActualPairedToolOnlyWorkflow(t *testing.T) {
	t.Run("plugin_lifecycle_and_repository_owner_required", func(t *testing.T) {
		g := workflowToolOnlySetup(t)
		if e := g.s.Plugins.Stop(g.ctx); e != nil {
			t.Fatal(e)
		}
		workflowRuntimeWant(t, g.s.Start(g.ctx), "PLUGIN_UNAVAILABLE")
		if g.s.configurationAvailable() || g.s.started {
			t.Fatal("stopped plugin admitted workflow lifecycle")
		}
		wrong := &Service{Identity: g.owner, Workspaces: g.s.Workspaces, Plugins: &plugins.Service{Identity: &identity.Service{}}}
		workflowRuntimeWant(t, wrong.Start(g.ctx), "WORKFLOW_STORAGE_MISMATCH")
		if e := g.s.Plugins.Start(g.ctx); e != nil {
			t.Fatal(e)
		}
		workflowRuntimeStart(t, g.s, g.ctx)
		if e := g.s.VerifyStored(g.ctx); e != nil || g.auditCount(t, "plugin.invoked") != 0 {
			t.Fatal("joined tool dependency did not recover without invocation", e)
		}
	})
	t.Run("default_draft_publish_execute_replay_revoke_restart", func(t *testing.T) {
		g := workflowToolOnlySetup(t)
		// Deployment configuration is immutable while any lifecycle owner runs.
		if e := g.s.Plugins.Stop(g.ctx); e != nil {
			t.Fatal(e)
		}
		policy := g.owner.Config.Policy()
		policy.Workflows.QuestionBytes = 128
		policy.Knowledge.QueryBytes = 32
		g.owner.Config.Runtime = &policy
		if e := g.s.Plugins.Start(g.ctx); e != nil {
			t.Fatal(e)
		}
		workflowRuntimeStart(t, g.s, g.ctx)
		configuration, e := g.s.Configuration(g.ctx, g.a, g.f.Workspace)
		if e != nil || !configuration.CanExecute || !slices.Equal(configuration.NodeKinds, []string{StartNode, ToolNode, EndNode}) || configuration.Limits.QuestionBytes != 128 {
			t.Fatal("actual tool-only configuration", e)
		}
		v := g.create(t, "Tool only from default draft")
		var graph Graph
		if json.Unmarshal(v.DraftGraph, &graph) != nil || graph.SchemaVersion != ToolGraphSchemaVersion || len(graph.Nodes) != 3 {
			t.Fatal("new default draft is not the tool family")
		}
		validation, e := g.s.Validate(g.ctx, g.a, g.f.Workspace, v.ID, RevisionReason{ExpectedRevision: v.Revision, Reason: "Unselected tool version"})
		if e != nil || validation.Valid {
			t.Fatal("unselected plugin was executable", e)
		}
		for i := range graph.Nodes {
			if graph.Nodes[i].Kind == ToolNode {
				graph.Nodes[i].Configuration, _ = json.Marshal(ToolConfiguration{PluginVersionID: g.version.ID})
			}
		}
		v, e = g.s.Update(g.ctx, g.a, g.f.Workspace, v.ID, UpdateRequest{Name: v.Name, Graph: graph, Enabled: true, ExpectedRevision: v.Revision, Reason: "Select exact installed grant"}, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		publication, conversation := g.publish(t, v)
		input := RunRequest{Question: strings.Repeat("q", 80), Reason: "Actual tool-only execution"}
		key := uuid.NewString()
		accepted := g.auditCount(t, "workflow.run_accepted")
		invoked := g.auditCount(t, "plugin.invoked")
		run, e := g.s.RunConversation(g.ctx, g.a, g.f.Workspace, v.ID, conversation.ID, input, key, uuid.NewString())
		if e != nil {
			t.Fatal("tool question above disabled knowledge limit", e)
		}
		result := workflowRuntimeWait(t, g, v.ID, run.ID)
		if result.State != SucceededState || !result.ResultAvailable || result.ToolResult == nil || result.ToolResult.Items == nil || result.Answer != nil || result.Usage != nil || len(result.Nodes) != 3 || result.VersionID == nil || *result.VersionID != publication.ID {
			t.Fatal("actual scoped SQL tool did not commit a private structured result")
		}
		for _, node := range result.Nodes {
			if node.State != SucceededState || node.StartedAt == nil || node.FinishedAt == nil {
				t.Fatal("actual interpreter trace missing")
			}
		}
		replay, e := g.s.RunConversation(g.ctx, g.a, g.f.Workspace, v.ID, conversation.ID, input, key, uuid.NewString())
		if e != nil || replay.ID != run.ID || g.auditCount(t, "plugin.invoked") != invoked+1 || g.auditCount(t, "workflow.run_accepted") != accepted+1 {
			t.Fatal("exact durable replay reinvoked a tool", e)
		}
		changed := input
		changed.Question = "Conflicting payload"
		_, e = g.s.RunConversation(g.ctx, g.a, g.f.Workspace, v.ID, conversation.ID, changed, key, uuid.NewString())
		workflowRuntimeWant(t, e, "IDEMPOTENCY_CONFLICT")
		_, e = g.s.GetRun(g.ctx, g.viewer(t), g.f.Workspace, v.ID, run.ID)
		workflowRuntimeWant(t, e, "WORKFLOW_RUN_NOT_FOUND")
		admin := g.auth(t, g.f.User, "admin")
		revoked, e := g.s.Plugins.UpdateGrant(g.ctx, admin, g.f.Workspace, g.grant.ID, plugins.GrantUpdate{Enabled: false, ExpectedRevision: g.grant.Revision, Reason: "Revoke current tool authority"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		hidden, e := g.s.GetRun(g.ctx, g.a, g.f.Workspace, v.ID, run.ID)
		if e != nil || hidden.ResultAvailable || hidden.ToolResult != nil || hidden.ResultUnavailableCode == nil || *hidden.ResultUnavailableCode != "PLUGIN_GRANT_REVOKED" {
			t.Fatal("revoked grant disclosed old tool result", e)
		}
		_, e = g.s.RunConversation(g.ctx, g.a, g.f.Workspace, v.ID, conversation.ID, input, uuid.NewString(), uuid.NewString())
		workflowRuntimeWant(t, e, "PLUGIN_GRANT_REVOKED")
		if g.auditCount(t, "workflow.run_accepted") != accepted+1 {
			t.Fatal("revoked run was durably admitted")
		}
		if _, e = g.s.Plugins.UpdateGrant(g.ctx, admin, g.f.Workspace, revoked.ID, plugins.GrantUpdate{Enabled: true, ExpectedRevision: revoked.Revision, Reason: "Explicit reviewed regrant"}, uuid.NewString(), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if e = g.s.Stop(g.ctx); e != nil {
			t.Fatal(e)
		}
		if g.s.configurationAvailable() {
			t.Fatal("joined lifecycle still advertises admission")
		}
		workflowRuntimeStart(t, g.s, g.ctx)
		if e = g.s.VerifyStored(g.ctx); e != nil || g.auditCount(t, "plugin.invoked") != invoked+1 {
			t.Fatal("restart reinvoked retained work", e)
		}
		next, e := g.s.RunConversation(g.ctx, g.a, g.f.Workspace, v.ID, conversation.ID, input, uuid.NewString(), uuid.NewString())
		if e != nil || next.ID == run.ID {
			t.Fatal("explicit new run after reviewed regrant", e)
		}
		if result = workflowRuntimeWait(t, g, v.ID, next.ID); result.State != SucceededState || g.auditCount(t, "plugin.invoked") != invoked+2 {
			t.Fatal("new tool run did not complete")
		}
	})
	t.Run("terminal_audit_failure_rolls_back_result", func(t *testing.T) {
		g := workflowToolOnlySetup(t)
		v, _ := g.tool(t, "Tool terminal audit failure")
		_, c := g.publish(t, v)
		workflowRuntimeStart(t, g.s, g.ctx)
		remove := g.auditFailure(t, "workflow.run_succeeded")
		input := RunRequest{Question: "Current SQL directory", Reason: "Required terminal audit"}
		key := uuid.NewString()
		run, e := g.s.RunConversation(g.ctx, g.a, g.f.Workspace, v.ID, c.ID, input, key, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		result := workflowRuntimeWait(t, g, v.ID, run.ID)
		if result.State != FailedState || result.ResultAvailable || result.ToolResult != nil || g.auditCount(t, "workflow.run_succeeded") != 0 || g.auditCount(t, "plugin.invoked") != 1 {
			t.Fatal("terminal audit failure committed success or retried invocation")
		}
		remove()
		replay, e := g.s.RunConversation(g.ctx, g.a, g.f.Workspace, v.ID, c.ID, input, key, uuid.NewString())
		if e != nil || replay.ID != run.ID || replay.State != FailedState || g.auditCount(t, "plugin.invoked") != 1 {
			t.Fatal("audit repair automatically repeated failed tool work", e)
		}
	})
	t.Run("durable_cancel_expired_recovery_and_live_owner", func(t *testing.T) {
		g := workflowToolOnlySetup(t)
		v, graph := g.tool(t, "Tool-only recovery controls")
		expired := g.execution(t, v, graph, nil)
		live := g.execution(t, v, graph, nil)
		for _, run := range []*runExecution{expired, live} {
			if e := g.s.claimExecution(g.ctx, run); e != nil {
				t.Fatal(e)
			}
		}
		workflowRuntimeExec(t, g, `UPDATE workflow_runs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, `UPDATE workflow_runs SET lease_expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?`, expired.id)
		workflowRuntimeStart(t, g.s, g.ctx)
		for _, test := range []struct{ id, state string }{{expired.id, InterruptedState}, {live.id, RunningState}} {
			result, e := g.s.GetRun(g.ctx, g.a, g.f.Workspace, v.ID, test.id)
			if e != nil || result.State != test.state {
				t.Fatal("startup recovery changed a live owner or left an expired lease", e)
			}
		}
		pending := g.execution(t, v, graph, nil)
		for range 2 {
			result, e := g.s.CancelRun(g.ctx, g.a, g.f.Workspace, v.ID, pending.id, "Durable cancellation before claim", uuid.NewString())
			if e != nil || result.CancellationRequestedAt == nil {
				t.Fatal("durable cancellation missing", e)
			}
		}
		job, terminate := context.WithCancelCause(g.ctx)
		g.s.execute(job, terminate, pending, "Explicit SQL ownership envelope")
		terminate(nil)
		result, e := g.s.GetRun(g.ctx, g.a, g.f.Workspace, v.ID, pending.id)
		if e != nil || result.State != CancelledState || g.auditCount(t, "workflow.run_cancel_requested") != 1 || g.auditCount(t, "plugin.invoked") != 0 {
			t.Fatal("cancelled pending run invoked a tool or was not reduced", e)
		}
		if e = g.s.finishFailure(g.ctx, live, InterruptedState, "WORKFLOW_RUN_INTERRUPTED"); e != nil {
			t.Fatal("explicit fixture owner cleanup", e)
		}
	})
	t.Run("saved_answer_graph_is_preserved_and_withheld", func(t *testing.T) {
		g := workflowToolOnlySetup(t)
		workflowRuntimeStart(t, g.s, g.ctx)
		v := g.create(t, "Retained answer graph")
		previous := &Service{Identity: &identity.Service{}}
		previous.Identity.Config.Knowledge.Enabled = true
		graph, e := previous.initialGraph()
		if e != nil {
			t.Fatal(e)
		}
		for i := range graph.Graph.Nodes {
			switch graph.Graph.Nodes[i].Kind {
			case RetrievalNode:
				graph.Graph.Nodes[i].Configuration, _ = json.Marshal(RetrievalConfiguration{BaseID: uuid.NewString(), TopK: 1, MinimumScore: 0})
			case AnswerNode:
				graph.Graph.Nodes[i].Configuration, _ = json.Marshal(AnswerConfiguration{AliasID: uuid.NewString(), Instructions: "Retained answer instructions"})
			}
		}
		v, e = g.s.Update(g.ctx, g.a, g.f.Workspace, v.ID, UpdateRequest{Name: v.Name, Graph: graph.Graph, Enabled: true, ExpectedRevision: v.Revision, Reason: "Retain historical graph family"}, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		graph, e = g.s.DecodePublishedGraph(v.DraftGraph, v.DraftSHA256)
		if e != nil || graph.IsTool() {
			t.Fatal("saved answer graph was coerced to tool graph", e)
		}
		_, e = g.s.Publish(g.ctx, g.a, g.f.Workspace, v.ID, RevisionReason{ExpectedRevision: v.Revision, Reason: "Disabled family publication"}, uuid.NewString(), uuid.NewString())
		workflowRuntimeWant(t, e, "KNOWLEDGE_UNAVAILABLE")
		_, e = g.s.Debug(g.ctx, g.a, g.f.Workspace, v.ID, DebugRequest{ExpectedRevision: v.Revision, Question: "Disabled retrieval", Reason: "No fallback execution"}, uuid.NewString(), uuid.NewString())
		workflowRuntimeWant(t, e, "KNOWLEDGE_UNAVAILABLE")
		// Explicit historical SQL fixture, not a generated/qualified model answer.
		historical := g.execution(t, v, graph, nil)
		if _, e = g.f.Store.Exec(g.ctx, workflowStatement(g.f.Store, workflowRunAnswerSuccess, historical.id, "Synthetic private answer", []byte(`{}`), []byte(`{}`), []byte(`{}`))); e != nil {
			t.Fatal(e)
		}
		hidden, e := g.s.GetRun(g.ctx, g.a, g.f.Workspace, v.ID, historical.id)
		if e != nil || hidden.State != SucceededState || hidden.Answer != nil || hidden.Usage != nil || hidden.ResultAvailable || hidden.ResultUnavailableCode == nil || *hidden.ResultUnavailableCode != "KNOWLEDGE_UNAVAILABLE" {
			t.Fatal("disabled family disclosed history or dereferenced an absent service", e)
		}
	})
}

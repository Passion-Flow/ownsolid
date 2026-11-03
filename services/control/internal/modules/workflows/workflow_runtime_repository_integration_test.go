//go:build repository_integration

package workflows

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
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
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

type workflowRuntimeStory struct {
	f       repositoryfixture.Fixture
	ctx     context.Context
	owner   *identity.Service
	s       *Service
	a       identity.Auth
	version plugins.Version
	grant   plugins.Grant
}

func workflowRuntimeExec(t *testing.T, g workflowRuntimeStory, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(pg, my, args...)); e != nil {
		t.Fatal("actual workflow fixture SQL", workflowRepositoryError(e))
	}
}

// Real isolated databases and the current canonical migration catalog. The
// repository fixture owns migration; replaying the promoted candidate DDL here
// would recreate existing tables. Signed capsules and tool
// results use the real plugin implementation. Explicit SQL pending/run ownership
// envelopes below do not claim durable admission, model execution or encoder IO.
func workflowRuntimeSetup(t *testing.T) workflowRuntimeStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	macKey := make([]byte, 32)
	if _, e := rand.Read(macKey); e != nil {
		t.Fatal(e)
	}
	owner := &identity.Service{Storage: f.Store, HMACKey: macKey}
	if profile := os.Getenv("PROJECT_ONE_TEST_APPLICATION_PROFILE"); profile == "plugins" || profile == "portal" || profile == "localization" {
		owner.Config.Server.ApplicationProfile = &profile
	}
	owner.Config.Workflows.Enabled = true
	public, private, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal(e)
	}
	key, e := wire.KeyID(public)
	if e != nil {
		t.Fatal(e)
	}
	owner.Config.Plugins = config.PluginConfiguration{Enabled: true, TrustedKeys: []config.PluginTrustKey{{KeyID: key, PublicKey: base64.StdEncoding.EncodeToString(public)}}}
	ws := &workspaces.Service{Identity: owner}
	model := &models.Service{Identity: owner, Workspaces: ws}
	d := &distribution.Service{Identity: owner, Workspaces: ws}
	k := &knowledge.Service{Identity: owner, Workspaces: ws, Distribution: d}
	p := &plugins.Service{Identity: owner, Workspaces: ws, Models: model}
	s := &Service{Identity: owner, Workspaces: ws, Knowledge: k, Distribution: d, Plugins: p}
	g := workflowRuntimeStory{f: f, ctx: ctx, owner: owner, s: s}
	workflowRuntimeExec(t, g, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES($1,$2,$3,'owner',$4)`, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES(?,?,?,'owner',?)`, f.User, f.Tenant, f.Workspace, uuid.NewString())
	g.a = g.auth(t, f.User, "portal")
	admin := g.auth(t, f.User, "admin")
	if e = p.Start(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := p.Stop(cleanup); e != nil {
			t.Error(e)
		}
	})
	raw, e := wire.Sign(wire.Manifest{PluginID: wire.PluginID, Version: "1.0.0", ABI: wire.ABI, Executor: wire.Executor, Capabilities: []string{wire.Capability}, License: wire.License}, private, owner.Config.Policy().Plugins.PackageLimits())
	if e != nil {
		t.Fatal(e)
	}
	g.version, e = p.Import(ctx, admin, plugins.ImportRequest{Capsule: base64.StdEncoding.EncodeToString(raw), SHA256: wire.Digest(raw), Reason: "Actual signed isolated workflow fixture"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.version, e = p.Install(ctx, admin, g.version.ID, plugins.RevisionReason{ExpectedRevision: g.version.Revision, Reason: "Actual signed fixture installation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.grant, e = p.Grant(ctx, admin, f.Workspace, plugins.GrantRequest{PluginVersionID: g.version.ID, Reason: "Actual current Workspace tool grant"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return g
}
func (g workflowRuntimeStory) auth(t *testing.T, user, audience string) identity.Auth {
	t.Helper()
	var session identity.IssuedSession
	e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		session, e = g.owner.IssueSessionWithin(ctx, q, user, 1, audience, "", uuid.NewString(), nil)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	a, e := g.owner.Authenticate(g.ctx, session.Token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func (g workflowRuntimeStory) viewer(t *testing.T) identity.Auth {
	t.Helper()
	id := uuid.NewString()
	email := id + "@example.invalid"
	workflowRuntimeExec(t, g, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Actual private viewer','en','active',false)`, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Actual private viewer','en','active',false)`, id, email, email)
	// PG uses each provided argument too, so both vendor statements bind the exact
	// same tuple rather than silently dropping one test argument.
	workflowRuntimeExec(t, g, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES($1,$2,$3,'viewer',$4)`, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES(?,?,?,'viewer',?)`, id, g.f.Tenant, g.f.Workspace, uuid.NewString())
	return g.auth(t, id, "portal")
}

func (g workflowRuntimeStory) create(t *testing.T, name string) Workflow {
	t.Helper()
	v, e := g.s.Create(g.ctx, g.a, CreateRequest{WorkspaceID: g.f.Workspace, Name: name, Reason: "Actual workflow runtime fixture"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func (g workflowRuntimeStory) tool(t *testing.T, name string) (Workflow, ParsedGraph) {
	t.Helper()
	v := g.create(t, name)
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	raw, _ := json.Marshal(ToolConfiguration{PluginVersionID: g.version.ID})
	graph := Graph{SchemaVersion: ToolGraphSchemaVersion, Nodes: []Node{{ID: ids[0], Kind: StartNode, Configuration: json.RawMessage(`{}`)}, {ID: ids[1], Kind: ToolNode, Configuration: raw}, {ID: ids[2], Kind: EndNode, Configuration: json.RawMessage(`{}`)}}, Edges: []Edge{{ID: uuid.NewString(), Source: ids[0], Target: ids[1]}, {ID: uuid.NewString(), Source: ids[1], Target: ids[2]}}}
	var e error
	v, e = g.s.Update(g.ctx, g.a, g.f.Workspace, v.ID, UpdateRequest{Name: v.Name, Graph: graph, Enabled: true, ExpectedRevision: v.Revision, Reason: "Actual signed tool graph"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	p, e := g.s.DecodePublishedGraph(v.DraftGraph, v.DraftSHA256)
	if e != nil {
		t.Fatal(e)
	}
	return v, p
}
func (g workflowRuntimeStory) publish(t *testing.T, v Workflow) (Version, Conversation) {
	t.Helper()
	key := uuid.NewString()
	input := RevisionReason{ExpectedRevision: v.Revision, Reason: "Actual immutable publication"}
	before := g.auditCount(t, "workflow.published")
	publication, e := g.s.Publish(g.ctx, g.a, g.f.Workspace, v.ID, input, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	replay, e := g.s.Publish(g.ctx, g.a, g.f.Workspace, v.ID, input, key, uuid.NewString())
	if e != nil || replay.ID != publication.ID || g.auditCount(t, "workflow.published") != before+1 {
		t.Fatal("exact immutable publication replay", e)
	}
	conversation, e := g.s.CreateConversation(g.ctx, g.a, g.f.Workspace, v.ID, ConversationRequest{VersionID: publication.ID, Reason: "Actual pinned private conversation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return publication, conversation
}
func (g workflowRuntimeStory) execution(t *testing.T, v Workflow, graph ParsedGraph, conversation *Conversation) *runExecution {
	t.Helper()
	run := &runExecution{actor: g.a, tenant: g.f.Tenant, workspace: g.f.Workspace, workflow: v.ID, id: uuid.NewString(), process: uuid.NewString(), lease: uuid.NewString(), request: uuid.NewString(), graph: graph}
	e := g.s.transaction(g.ctx, g.a, g.f.Workspace, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		b, e := lockedWorkflow(ctx, q, w, v.ID)
		if e != nil {
			return e
		}
		run.authority = b.authority
		run.membership, e = g.s.Workspaces.MembershipAuthorityWithin(ctx, q, w.TenantID, w.ID, g.f.User)
		if e != nil {
			return e
		}
		var version, conv *string
		var turn *int
		revision := &b.Revision
		run.draftRevision = revision
		if conversation != nil {
			conv = &conversation.ID
			version = &conversation.VersionID
			n := conversation.TurnCount + 1
			turn = &n
			revision = nil
			run.conversation = conv
			run.draftRevision = nil
		}
		if _, e = q.Exec(ctx, workflowStatement(q, workflowRunCreate, run.id, w.TenantID, w.ID, v.ID, g.f.User, conv, turn, version, revision, b.authority, run.membership, g.a.Me.Audience, graph.CanonicalJSON, graph.SHA256, "Explicit SQL ownership envelope", g.s.policy().RunSeconds, graph.ResultKind())); e != nil {
			return e
		}
		for _, node := range graph.Graph.Nodes {
			if _, e = q.Exec(ctx, workflowStatement(q, workflowTraceCreate, w.TenantID, w.ID, v.ID, run.id, node.ID, node.Kind)); e != nil {
				return e
			}
		}
		if conversation != nil {
			_, e = q.Exec(ctx, workflowStatement(q, workflowConversationTurn, conversation.ID, *turn))
		}
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return run
}
func workflowRuntimeWant(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("workflow runtime expected %s got %v", code, e)
	}
}
func (g workflowRuntimeStory) auditCount(t *testing.T, action string) int {
	t.Helper()
	var count int
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT count(*) FROM audit_events WHERE action=$1`, `SELECT count(*) FROM audit_events WHERE action=?`, action)).Scan(&count); e != nil {
		t.Fatal(e)
	}
	return count
}
func (g workflowRuntimeStory) auditFailure(t *testing.T, action string) func() {
	t.Helper()
	if action != "workflow.created" && action != "workflow.node_succeeded" && action != "workflow.run_succeeded" && action != "workflow.run_interrupted" && action != "workflow.retention_completed" {
		t.Fatal("owned audit action required")
	}
	if g.f.Store.Dialect() == transaction.PostgreSQL {
		workflowRuntimeExec(t, g, `CREATE FUNCTION workflow_runtime_audit_refusal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='`+action+`' THEN RAISE EXCEPTION 'EXPLICIT_WORKFLOW_AUDIT_REFUSAL';END IF;RETURN NEW;END;$$`, "")
		workflowRuntimeExec(t, g, `CREATE TRIGGER workflow_runtime_audit_refusal BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION workflow_runtime_audit_refusal()`, "")
	} else {
		workflowRuntimeExec(t, g, "", `CREATE TRIGGER workflow_runtime_audit_refusal BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action='`+action+`' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='EXPLICIT_WORKFLOW_AUDIT_REFUSAL';END IF;END`)
	}
	removed := false
	remove := func() {
		if removed {
			return
		}
		removed = true
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		drop := `DROP TRIGGER workflow_runtime_audit_refusal ON audit_events`
		if g.f.Store.Dialect() == transaction.MySQL {
			drop = `DROP TRIGGER workflow_runtime_audit_refusal`
		}
		if _, e := g.f.Store.Exec(ctx, transaction.SQL(g.f.Store.Dialect(), drop)); e != nil {
			t.Error(e)
		}
		if g.f.Store.Dialect() == transaction.PostgreSQL {
			if _, e := g.f.Store.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `DROP FUNCTION workflow_runtime_audit_refusal()`)); e != nil {
				t.Error(e)
			}
		}
	}
	t.Cleanup(remove)
	return remove
}
func (g workflowRuntimeStory) prepareTool(t *testing.T, run *runExecution) *plugins.CompletedModelList {
	t.Helper()
	if e := g.s.claimExecution(g.ctx, run); e != nil {
		t.Fatal(e)
	}
	for _, next := range []struct{ kind, old, state string }{{StartNode, NotStartedState, RunningState}, {StartNode, RunningState, SucceededState}, {ToolNode, NotStartedState, RunningState}} {
		if e := g.s.nodeTransition(g.ctx, run, next.kind, next.old, next.state, TraceResult{}); e != nil {
			t.Fatal(e)
		}
	}
	var completed *plugins.CompletedModelList
	e := g.s.withExecutionWithin(g.ctx, run, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, _ workflowRecord, r runRecord) error {
		var e error
		completed, e = g.s.Plugins.ExecuteModelListWithin(ctx, q, g.a, w.ID, g.version.ID, r.ID, r.Question)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	count := len(completed.Result().Items)
	if e = g.s.nodeTransition(g.ctx, run, ToolNode, RunningState, SucceededState, TraceResult{ToolVersionID: g.version.ID, ModelCount: &count}); e != nil {
		t.Fatal(e)
	}
	if e = g.s.nodeTransition(g.ctx, run, EndNode, NotStartedState, RunningState, TraceResult{}); e != nil {
		t.Fatal(e)
	}
	return completed
}
func (g workflowRuntimeStory) completeTool(t *testing.T, run *runExecution) {
	t.Helper()
	completed := g.prepareTool(t, run)
	if e := g.s.finishToolSuccess(g.ctx, run, completed); e != nil {
		t.Fatal(e)
	}
}

type workflowLostAcknowledgement struct{ transaction.Store }

func (s workflowLostAcknowledgement) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	if e := s.Store.Within(ctx, ro, fn); e != nil {
		return e
	}
	return &transaction.Error{Kind: transaction.CommitUnknown}
}
func (g workflowRuntimeStory) unknown() *Service {
	owner := &identity.Service{Storage: workflowLostAcknowledgement{g.f.Store}, Config: g.owner.Config, HMACKey: append([]byte{}, g.owner.HMACKey...)}
	return &Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}}
}

// The harness selects one leaf per new real database. Do not run all MySQL
// leaves against the same configured database or reuse a failed DDL fixture.
func TestActualPairedWorkflowRuntime(t *testing.T) {
	t.Run("draft_idempotency_name_false_revision_and_cursor", func(t *testing.T) {
		g := workflowRuntimeSetup(t)
		input := CreateRequest{WorkspaceID: g.f.Workspace, Name: " Caf\u00e9 workflow ", Reason: "Actual private draft"}
		key := uuid.NewString()
		v, e := g.s.Create(g.ctx, g.a, input, key, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		again, e := g.s.Create(g.ctx, g.a, input, key, uuid.NewString())
		if e != nil || again.ID != v.ID {
			t.Fatal("exact durable draft replay", e)
		}
		changed := input
		changed.Name = "Other"
		out, e := g.s.Create(g.ctx, g.a, changed, key, uuid.NewString())
		workflowRuntimeWant(t, e, "IDEMPOTENCY_CONFLICT")
		if out.ID != "" {
			t.Fatal("conflict projection")
		}
		changed.Name = "Café workflow"
		_, e = g.s.Create(g.ctx, g.a, changed, uuid.NewString(), uuid.NewString())
		workflowRuntimeWant(t, e, "WORKFLOW_NAME_EXISTS")
		var graph Graph
		if json.Unmarshal(v.DraftGraph, &graph) != nil {
			t.Fatal("real stored draft")
		}
		v, e = g.s.Update(g.ctx, g.a, g.f.Workspace, v.ID, UpdateRequest{Name: v.Name, Graph: graph, Enabled: false, ExpectedRevision: v.Revision, Reason: "Explicit false enabled"}, uuid.NewString())
		if e != nil || v.Enabled {
			t.Fatal("false value lost", e)
		}
		_, e = g.s.Update(g.ctx, g.a, g.f.Workspace, v.ID, UpdateRequest{Name: v.Name, Graph: graph, Enabled: true, ExpectedRevision: v.Revision - 1, Reason: "Stale revision"}, uuid.NewString())
		workflowRuntimeWant(t, e, "WORKFLOW_DRAFT_CHANGED")
		g.create(t, "Second actual draft")
		page, e := g.s.List(g.ctx, g.a, g.f.Workspace, url.Values{"limit": {"1"}})
		if e != nil || len(page.Items) != 1 || page.NextCursor == nil {
			t.Fatal("actual keyset first page", e)
		}
		page, e = g.s.List(g.ctx, g.a, g.f.Workspace, url.Values{"limit": {"1"}, "cursor": {*page.NextCursor}})
		if e != nil || len(page.Items) != 1 {
			t.Fatal("actual keyset continuation", e)
		}
	})
	t.Run("viewer_current_scope_and_immutable_publication_conversation", func(t *testing.T) {
		g := workflowRuntimeSetup(t)
		v, _ := g.tool(t, "Current signed publication")
		publication, conversation := g.publish(t, v)
		versions, e := g.s.Versions(g.ctx, g.a, g.f.Workspace, v.ID, url.Values{})
		if e != nil || len(versions.Items) != 1 || versions.Items[0].ID != publication.ID {
			t.Fatal("actual immutable version read", e)
		}
		workflowRuntimeExec(t, g, `UPDATE workspace_memberships SET role='viewer' WHERE user_id=$1 AND workspace_id=$2`, `UPDATE workspace_memberships SET role='viewer' WHERE user_id=? AND workspace_id=?`, g.f.User, g.f.Workspace)
		visible, e := g.s.Get(g.ctx, g.a, g.f.Workspace, v.ID)
		if e != nil || visible.CanEdit || len(visible.DraftGraph) != 0 {
			t.Fatal("current viewer graph privacy", e)
		}
		if _, e = g.s.Create(g.ctx, g.a, CreateRequest{WorkspaceID: g.f.Workspace, Name: "Denied", Reason: "Viewer mutation refusal"}, uuid.NewString(), uuid.NewString()); e == nil {
			t.Fatal("viewer wrote draft")
		}
		c, e := g.s.Conversation(g.ctx, g.a, g.f.Workspace, v.ID, conversation.ID)
		if e != nil || c.VersionID != publication.ID {
			t.Fatal("viewer own version pin", e)
		}
		_, e = g.s.Conversation(g.ctx, g.a, uuid.NewString(), v.ID, conversation.ID)
		if e == nil {
			t.Fatal("foreign Workspace read admitted")
		}
	})
	t.Run("actual_signed_tool_steps_private_history_and_notice", func(t *testing.T) {
		g := workflowRuntimeSetup(t)
		v, graph := g.tool(t, "Actual signed execution")
		run := g.execution(t, v, graph, nil)
		g.completeTool(t, run)
		result, e := g.s.GetRun(g.ctx, g.a, g.f.Workspace, v.ID, run.id)
		if e != nil || result.State != SucceededState || !result.ResultAvailable || result.ToolResult == nil || result.ToolResult.Items == nil || result.Answer != nil || result.Usage != nil || len(result.Nodes) != 3 {
			t.Fatal("actual signed private tool result", e)
		}
		other := g.viewer(t)
		private, e := g.s.GetRun(g.ctx, other, g.f.Workspace, v.ID, run.id)
		workflowRuntimeWant(t, e, "WORKFLOW_RUN_NOT_FOUND")
		if private.ID != "" {
			t.Fatal("private run disclosed to another current viewer")
		}
		var notices int
		if e = g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT count(*) FROM terminal_notifications WHERE run_id=$1 AND recipient_id=$2`, `SELECT count(*) FROM terminal_notifications WHERE run_id=? AND recipient_id=?`, run.id, g.f.User)).Scan(&notices); e != nil || notices != 1 {
			t.Fatal("actual atomic terminal notice", e)
		}
		_, e = g.s.Plugins.UpdateGrant(g.ctx, g.auth(t, g.f.User, "admin"), g.f.Workspace, g.grant.ID, plugins.GrantUpdate{Enabled: false, ExpectedRevision: g.grant.Revision, Reason: "Current grant revocation"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		result, e = g.s.GetRun(g.ctx, g.a, g.f.Workspace, v.ID, run.id)
		if e != nil || result.ResultAvailable || result.ToolResult != nil || result.ResultUnavailableCode == nil {
			t.Fatal("revoked grant disclosed historical result", e)
		}
	})
	t.Run("worker_lease_membership_lifetime_and_reduction", func(t *testing.T) {
		g := workflowRuntimeSetup(t)
		v, graph := g.tool(t, "Current worker authority")
		run := g.execution(t, v, graph, nil)
		if e := g.s.claimExecution(g.ctx, run); e != nil {
			t.Fatal(e)
		}
		if e := g.s.renewExecution(g.ctx, run); e != nil {
			t.Fatal(e)
		}
		if e := g.s.nodeTransition(g.ctx, run, StartNode, NotStartedState, RunningState, TraceResult{}); e != nil {
			t.Fatal(e)
		}
		before := g.auditCount(t, "workflow.node_succeeded")
		remove := g.auditFailure(t, "workflow.node_succeeded")
		if e := g.s.nodeTransition(g.ctx, run, StartNode, RunningState, SucceededState, TraceResult{}); e == nil {
			t.Fatal("failed node audit committed")
		}
		if g.auditCount(t, "workflow.node_succeeded") != before {
			t.Fatal("failed node audit count changed")
		}
		remove()
		stale := &runExecution{actor: run.actor, tenant: run.tenant, workspace: run.workspace, workflow: run.workflow, id: run.id, process: run.process, lease: uuid.NewString(), membership: run.membership, authority: run.authority, request: run.request, conversation: run.conversation, draftRevision: run.draftRevision, graph: run.graph}
		workflowRuntimeWant(t, g.s.renewExecution(g.ctx, stale), "WORKFLOW_RUN_STALE")
		workflowRuntimeExec(t, g, `DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, `DELETE FROM workspace_memberships WHERE user_id=? AND workspace_id=?`, g.f.User, g.f.Workspace)
		workflowRuntimeExec(t, g, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES($1,$2,$3,'owner',$4)`, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES(?,?,?,'owner',?)`, g.f.User, g.f.Tenant, g.f.Workspace, uuid.NewString())
		workflowRuntimeWant(t, g.s.renewExecution(g.ctx, run), "WORKFLOW_AUTHORITY_CHANGED")
		if e := g.s.finishFailure(g.ctx, run, InterruptedState, "WORKFLOW_RUN_INTERRUPTED"); e != nil {
			t.Fatal("safe existing-run reduction after membership replacement", e)
		}
	})
	t.Run("cancel_tombstone_retention_and_audit_rollback", func(t *testing.T) {
		g := workflowRuntimeSetup(t)
		v, graph := g.tool(t, "Owned cleanup")
		_, conversation := g.publish(t, v)
		run := g.execution(t, v, graph, &conversation)
		cancelled, e := g.s.CancelRun(g.ctx, g.a, g.f.Workspace, v.ID, run.id, "Explicit current owner cancellation", uuid.NewString())
		if e != nil || cancelled.CancellationRequestedAt == nil {
			t.Fatal("durable cancel fence", e)
		}
		if e = g.s.DeleteConversation(g.ctx, g.a, g.f.Workspace, v.ID, conversation.ID, RevisionReason{ExpectedRevision: conversation.Revision, Reason: "Private tombstone"}, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if e = g.s.finishFailure(g.ctx, run, CancelledState, "WORKFLOW_RUN_CANCELLED"); e != nil {
			t.Fatal(e)
		}
		before := g.auditCount(t, "workflow.retention_completed")
		remove := g.auditFailure(t, "workflow.retention_completed")
		out, e := g.s.Cleanup(g.ctx)
		if e == nil || out.Runs != 0 || out.Conversations != 0 {
			t.Fatal("retention audit refusal returned counters")
		}
		if g.auditCount(t, "workflow.retention_completed") != before {
			t.Fatal("audit rollback changed count")
		}
		remove()
		out, e = g.s.Cleanup(g.ctx)
		if e != nil || out.Runs != 1 || out.Conversations != 1 {
			t.Fatal("exact terminal/tombstone cleanup", e)
		}
	})
	t.Run("expired_only_interrupt_restore_and_live_owner", func(t *testing.T) {
		g := workflowRuntimeSetup(t)
		v, graph := g.tool(t, "Expired reduction")
		expired := g.execution(t, v, graph, nil)
		live := g.execution(t, v, graph, nil)
		for _, run := range []*runExecution{expired, live} {
			if e := g.s.claimExecution(g.ctx, run); e != nil {
				t.Fatal(e)
			}
		}
		workflowRuntimeExec(t, g, `UPDATE workflow_runs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, `UPDATE workflow_runs SET lease_expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?`, expired.id)
		before := g.auditCount(t, "workflow.run_interrupted")
		remove := g.auditFailure(t, "workflow.run_interrupted")
		if e := g.s.RecoverExpired(g.ctx); e == nil {
			t.Fatal("interruption audit failure committed")
		}
		if g.auditCount(t, "workflow.run_interrupted") != before {
			t.Fatal("failed recovery changed audit")
		}
		remove()
		if e := g.s.RecoverExpired(g.ctx); e != nil {
			t.Fatal(e)
		}
		result, e := g.s.GetRun(g.ctx, g.a, g.f.Workspace, v.ID, live.id)
		if e != nil || result.State != RunningState {
			t.Fatal("live lease adopted/interrupted", e)
		}
		if e = g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error { return InvalidateAuthoritiesWithin(ctx, q) }); e != nil {
			t.Fatal(e)
		}
		result, e = g.s.GetRun(g.ctx, g.a, g.f.Workspace, v.ID, live.id)
		if e != nil || result.State != InterruptedState {
			t.Fatal("restore resumed old worker", e)
		}
	})
	t.Run("unknown_commit_zero_projection_and_exact_durable_replay", func(t *testing.T) {
		g := workflowRuntimeSetup(t)
		bad := g.unknown()
		input := CreateRequest{WorkspaceID: g.f.Workspace, Name: "Lost actual acknowledgement", Reason: "Explicit completed-tx transport fault"}
		key := uuid.NewString()
		out, e := bad.Create(g.ctx, g.a, input, key, uuid.NewString())
		workflowRuntimeWant(t, e, string(transaction.CommitUnknown))
		if out.ID != "" {
			t.Fatal("unknown create exposed result")
		}
		actual, e := g.s.Create(g.ctx, g.a, input, key, uuid.NewString())
		if e != nil || actual.ID == "" {
			t.Fatal("exact durable intent recovery", e)
		}
		out, e = bad.Get(g.ctx, g.a, g.f.Workspace, actual.ID)
		workflowRuntimeWant(t, e, string(transaction.CommitUnknown))
		if out.ID != "" {
			t.Fatal("unknown read projection")
		}
		page, e := bad.List(g.ctx, g.a, g.f.Workspace, url.Values{})
		workflowRuntimeWant(t, e, string(transaction.CommitUnknown))
		if len(page.Items) != 0 || page.NextCursor != nil {
			t.Fatal("unknown page projection")
		}
	})
	t.Run("create_audit_and_late_history_failure_return_no_partial_result", func(t *testing.T) {
		g := workflowRuntimeSetup(t)
		before := g.auditCount(t, "workflow.created")
		remove := g.auditFailure(t, "workflow.created")
		out, e := g.s.Create(g.ctx, g.a, CreateRequest{WorkspaceID: g.f.Workspace, Name: "Actual rollback", Reason: "Real audit refusal"}, uuid.NewString(), uuid.NewString())
		if e == nil || out.ID != "" || g.auditCount(t, "workflow.created") != before {
			t.Fatal("failed audit published draft")
		}
		remove()
		v, graph := g.tool(t, "Late corrupt result")
		bad := g.execution(t, v, graph, nil)
		if e = g.s.finishFailure(g.ctx, bad, FailedState, "STORAGE_UNAVAILABLE"); e != nil {
			t.Fatal(e)
		}
		good := g.execution(t, v, graph, nil)
		if e = g.s.finishFailure(g.ctx, good, FailedState, "STORAGE_UNAVAILABLE"); e != nil {
			t.Fatal(e)
		}
		workflowRuntimeExec(t, g, `UPDATE workflow_node_traces SET result_metadata=jsonb_build_object('unknown_private_field','explicit corruption fixture') WHERE run_id=$1 AND kind='start'`, `UPDATE workflow_node_traces SET result_metadata=JSON_OBJECT('unknown_private_field','explicit corruption fixture') WHERE run_id=? AND kind='start'`, bad.id)
		page, e := g.s.RunPage(g.ctx, g.a, g.f.Workspace, v.ID, nil, url.Values{})
		workflowRuntimeWant(t, e, "WORKFLOW_RESULT_INVALID")
		if len(page.Items) != 0 || page.NextCursor != nil {
			t.Fatal("late corrupt row disclosed earlier result")
		}
	})
	t.Run("success_audit_rollback_and_independent_failed_notice", func(t *testing.T) {
		g := workflowRuntimeSetup(t)
		v, graph := g.tool(t, "Atomic terminal result")
		run := g.execution(t, v, graph, nil)
		completed := g.prepareTool(t, run)
		before := g.auditCount(t, "workflow.run_succeeded")
		remove := g.auditFailure(t, "workflow.run_succeeded")
		if e := g.s.finishToolSuccess(g.ctx, run, completed); e == nil {
			t.Fatal("failed success audit published result")
		}
		if g.auditCount(t, "workflow.run_succeeded") != before {
			t.Fatal("success audit leaked")
		}
		var count int
		if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT count(*) FROM terminal_notifications WHERE run_id=$1`, `SELECT count(*) FROM terminal_notifications WHERE run_id=?`, run.id)).Scan(&count); e != nil || count != 0 {
			t.Fatal("rolled success notice persisted", e)
		}
		remove()
		if e := g.s.finishFailure(g.ctx, run, FailedState, "STORAGE_UNAVAILABLE"); e != nil {
			t.Fatal(e)
		}
		result, e := g.s.GetRun(g.ctx, g.a, g.f.Workspace, v.ID, run.id)
		if e != nil || result.State != FailedState || result.ResultAvailable || result.ToolResult != nil {
			t.Fatal("independent failure claimed output", e)
		}
		if e = g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT count(*) FROM terminal_notifications WHERE run_id=$1 AND outcome='failed'`, `SELECT count(*) FROM terminal_notifications WHERE run_id=? AND outcome='failed'`, run.id)).Scan(&count); e != nil || count != 1 {
			t.Fatal("independent failure notice", e)
		}
	})
	t.Run("disable_fences_running_execution_and_keeps_owned_history", func(t *testing.T) {
		g := workflowRuntimeSetup(t)
		v, graph := g.tool(t, "Disable running authority")
		run := g.execution(t, v, graph, nil)
		if e := g.s.claimExecution(g.ctx, run); e != nil {
			t.Fatal(e)
		}
		disabled, e := g.s.Update(g.ctx, g.a, g.f.Workspace, v.ID, UpdateRequest{Name: v.Name, Graph: graph.Graph, Enabled: false, ExpectedRevision: v.Revision, Reason: "Explicit current disable"}, uuid.NewString())
		if e != nil || disabled.Enabled {
			t.Fatal(e)
		}
		workflowRuntimeWant(t, g.s.renewExecution(g.ctx, run), "WORKFLOW_DISABLED")
		if e = g.s.finishFailure(g.ctx, run, CancelledState, "WORKFLOW_RUN_CANCELLED"); e != nil {
			t.Fatal(e)
		}
		result, e := g.s.GetRun(g.ctx, g.a, g.f.Workspace, v.ID, run.id)
		if e != nil || result.State != CancelledState || result.CancellationRequestedAt == nil {
			t.Fatal("disabled workflow lost owned terminal history", e)
		}
	})

}

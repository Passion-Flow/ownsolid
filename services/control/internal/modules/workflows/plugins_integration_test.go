//go:build plugin_database_integration && knowledge_encoder_integration

package workflows_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

func toolGraph(version string) workflows.Graph {
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	raw, _ := json.Marshal(workflows.ToolConfiguration{PluginVersionID: version})
	return workflows.Graph{SchemaVersion: 2, Nodes: []workflows.Node{{ID: ids[0], Kind: "start", Configuration: json.RawMessage(`{}`)}, {ID: ids[1], Kind: "tool", Position: workflows.Position{X: 220}, Configuration: raw}, {ID: ids[2], Kind: "end", Position: workflows.Position{X: 440}, Configuration: json.RawMessage(`{}`)}}, Edges: []workflows.Edge{{ID: uuid.NewString(), Source: ids[0], Target: ids[1]}, {ID: uuid.NewString(), Source: ids[1], Target: ids[2]}}}
}
func toolFixture(t *testing.T) (*identityhttp.Handler, identity.Auth, identity.Auth, string, ed25519.PrivateKey) {
	t.Helper()
	fixture.PreflightOwnedPostgres(t)
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	portal := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	public, private, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := wire.KeyID(public)
	account.Config.Plugins = config.PluginConfiguration{Enabled: true, TrustedKeys: []config.PluginTrustKey{{KeyID: id, PublicKey: base64.StdEncoding.EncodeToString(public)}}}
	account.Config.Workflows.Enabled = true
	scratch := t.TempDir()
	if os.Chmod(scratch, 0700) != nil {
		t.Fatal("private scratch")
	}
	account.Config.Knowledge = config.KnowledgeConfiguration{Enabled: true, PythonExecutable: os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON"), WorkerFile: filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "runtimes/knowledge/embed.py"), ModelDirectory: os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY"), ScratchRoot: scratch}
	policy := account.Config.Policy()
	policy.HTTP.RequestSeconds = 90
	policy.HTTP.StartupSeconds = 60
	policy.HTTP.ShutdownSeconds = 30
	account.Config.Runtime = &policy
	h := identityhttp.New(account)
	ctx := context.Background()
	if e = h.Knowledge.Service.OpenConfiguredProcessor(ctx); e != nil {
		t.Fatal("actual encoder prerequisite", e)
	}
	t.Cleanup(h.Knowledge.Service.Processor.Close)
	if e = h.Plugins.Service.Start(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := h.Plugins.Service.Stop(ctx); e != nil {
			t.Error(e)
		}
	})
	if e = h.Workflows.Service.Start(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := h.Workflows.Service.Stop(ctx); e != nil {
			t.Error(e)
		}
	})
	return h, admin, portal, password, private
}
func installTool(t *testing.T, s *plugins.Service, a identity.Auth, private ed25519.PrivateKey, release string) (plugins.Version, plugins.Grant) {
	t.Helper()
	raw, e := wire.Sign(wire.Manifest{PluginID: wire.PluginID, Version: release, ABI: wire.ABI, Executor: wire.Executor, Capabilities: []string{wire.Capability}, License: wire.License}, private, s.Identity.Config.Policy().Plugins.PackageLimits())
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	version, e := s.Import(ctx, a, plugins.ImportRequest{Capsule: base64.StdEncoding.EncodeToString(raw), SHA256: wire.Digest(raw), Reason: "Actual synthetic tool release"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	version, e = s.Install(ctx, a, version.ID, plugins.RevisionReason{ExpectedRevision: version.Revision, Reason: "Explicit synthetic install"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	grant, e := s.Grant(ctx, a, a.Me.Workspace.ID, plugins.GrantRequest{PluginVersionID: version.ID, Reason: "Explicit synthetic Workspace grant"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return version, grant
}
func waitTool(t *testing.T, s *workflows.Service, a identity.Auth, workflow, id string) workflows.Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r, e := s.GetRun(context.Background(), a, a.Me.Workspace.ID, workflow, id)
		if e != nil {
			t.Fatal("actual tool run read", e)
		}
		if r.State != "pending" && r.State != "running" {
			return r
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("actual tool run did not reach a terminal state")
	return workflows.Run{}
}
func expectToolSuccess(t *testing.T, r workflows.Run) {
	t.Helper()
	if r.State != "succeeded" || r.ResultKind != "model_list" || !r.ResultAvailable || r.ToolResult == nil || r.ToolResult.Items == nil || r.Answer != nil || r.Usage != nil || len(r.Sources) != 0 || len(r.Nodes) != 3 {
		t.Fatalf("actual three-node result: state=%s kind=%s available=%v error=%v", r.State, r.ResultKind, r.ResultAvailable, r.ErrorCode)
	}
	for _, node := range r.Nodes {
		if node.State != "succeeded" || node.StartedAt == nil || node.FinishedAt == nil {
			t.Fatal("missing durable tool trace", node.Kind, node.State)
		}
	}
	if r.Nodes[0].Kind != "start" || r.Nodes[1].Kind != "tool" || r.Nodes[2].Kind != "end" || r.Nodes[1].Result.ModelCount == nil || *r.Nodes[1].Result.ModelCount != len(r.ToolResult.Items) {
		t.Fatal("wrong ordered real tool traces")
	}
}
func TestM14ActualToolWorkflowPublicationReplayUpgradeReferencesAndCommonRestore(t *testing.T) {
	h, admin, portal, password, private := toolFixture(t)
	s := h.Workflows.Service
	p := h.Plugins.Service
	ctx := context.Background()
	workspace := portal.Me.Workspace.ID
	version, grant := installTool(t, p, admin, private, "1.0.0")
	draft, e := s.Create(ctx, portal, workflows.CreateRequest{WorkspaceID: workspace, Name: "Caf\u00e9 signed model directory", Reason: "Actual tool graph"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	draft, e = s.Update(ctx, portal, workspace, draft.ID, workflows.UpdateRequest{Name: draft.Name, Graph: toolGraph(version.ID), Enabled: true, ExpectedRevision: draft.Revision, Reason: "Explicit schema2 template"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	validation, e := s.Validate(ctx, portal, workspace, draft.ID, workflows.RevisionReason{ExpectedRevision: draft.Revision, Reason: "Current resources"})
	if e != nil || !validation.Valid {
		t.Fatal("actual tool validation", e)
	}
	publication, e := s.Publish(ctx, portal, workspace, draft.ID, workflows.RevisionReason{ExpectedRevision: draft.Revision, Reason: "Actual immutable tool publication"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	draft, e = s.Get(ctx, portal, workspace, draft.ID)
	if e != nil {
		t.Fatal(e)
	}
	input := workflows.DebugRequest{Question: "List ready models · \u5de5\u4f5c\u533a", ExpectedRevision: draft.Revision, Reason: "Actual read-only tool debug"}
	intent := uuid.NewString()
	admitted, e := s.Debug(ctx, portal, workspace, draft.ID, input, intent, uuid.NewString())
	if e != nil {
		t.Fatal("actual durable tool admission", e)
	}
	result := waitTool(t, s, portal, draft.ID, admitted.ID)
	expectToolSuccess(t, result)
	if len(result.ToolResult.Items) != 0 {
		t.Fatal("empty real directory fabricated models")
	}
	replay, e := s.Debug(ctx, portal, workspace, draft.ID, input, intent, uuid.NewString())
	if e != nil || replay.ID != result.ID {
		t.Fatal("real terminal replay", e)
	}
	var invocations int
	if e = p.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='plugin.invoked' AND request_id=$1", result.ID).Scan(&invocations); e != nil || invocations != 1 {
		t.Fatal("replay reinvoked tool", e, invocations)
	}
	conversation, e := s.CreateConversation(ctx, portal, workspace, draft.ID, workflows.ConversationRequest{VersionID: publication.ID, Reason: "Actual pinned tool conversation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	run, e := s.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, workflows.RunRequest{Question: "Read this version", Reason: "Actual conversation tool"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	run = waitTool(t, s, portal, draft.ID, run.ID)
	expectToolSuccess(t, run)
	grant, e = p.UpdateGrant(ctx, admin, workspace, grant.ID, plugins.GrantUpdate{Enabled: false, ExpectedRevision: grant.Revision, Reason: "Actual revocation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	withheld, e := s.GetRun(ctx, portal, workspace, draft.ID, run.ID)
	if e != nil || withheld.State != "succeeded" || withheld.ResultAvailable || withheld.ToolResult != nil || withheld.ResultUnavailableCode == nil || *withheld.ResultUnavailableCode != "PLUGIN_GRANT_REVOKED" {
		t.Fatal("revoked history leaked", e)
	}
	grant, e = p.UpdateGrant(ctx, admin, workspace, grant.ID, plugins.GrantUpdate{Enabled: true, ExpectedRevision: grant.Revision, Reason: "Explicit reviewed re-enable"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	visible, e := s.GetRun(ctx, portal, workspace, draft.ID, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	expectToolSuccess(t, visible)
	priorReferences, e := p.References(ctx, admin, version.ID)
	if e != nil || priorReferences.Count != 3 {
		t.Fatal("real initial tool reference set", e)
	}
	limitedAccount := *h.Service
	limitedPolicy := limitedAccount.Config.Policy()
	limitedPolicy.Plugins.MaximumReferences = 1
	limitedAccount.Config.Runtime = &limitedPolicy
	limited := identityhttp.New(&limitedAccount).Plugins.Service
	if _, e = limited.References(ctx, admin, version.ID); e == nil || e.Error() != "PLUGIN_REFERENCE_LIMIT_EXCEEDED" {
		t.Fatal("reference preview silently truncated", e)
	}
	newer, newGrant := installTool(t, p, admin, private, "1.1.0")
	draft, e = s.Update(ctx, portal, workspace, draft.ID, workflows.UpdateRequest{Name: draft.Name, Graph: toolGraph(newer.ID), Enabled: true, ExpectedRevision: draft.Revision, Reason: "Explicit version upgrade"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	next, e := s.Publish(ctx, portal, workspace, draft.ID, workflows.RevisionReason{ExpectedRevision: draft.Revision, Reason: "Explicit new publication"}, uuid.NewString(), uuid.NewString())
	if e != nil || next.ID == publication.ID {
		t.Fatal("new publication", e)
	}
	pinned, e := s.Conversation(ctx, portal, workspace, draft.ID, conversation.ID)
	if e != nil || pinned.VersionID != publication.ID {
		t.Fatal("upgrade retargeted old conversation", e)
	}
	oldRun, e := s.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, workflows.RunRequest{Question: "Old pinned tool", Reason: "Upgrade leaves old grant explicit"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	oldRun = waitTool(t, s, portal, draft.ID, oldRun.ID)
	expectToolSuccess(t, oldRun)
	_, e = p.Uninstall(ctx, admin, version.ID, plugins.UninstallRequest{ExpectedRevision: version.Revision, ReferencesSHA256: priorReferences.SHA256, ConfirmReferencedUninstall: true, Reason: "Actual stale preview must not authorize changed references"}, uuid.NewString(), uuid.NewString())
	if e == nil || e.Error() != "PLUGIN_REFERENCES_CHANGED" {
		t.Fatal("changed reference set accepted stale preview", e)
	}
	refs, e := p.References(ctx, admin, version.ID)
	if e != nil || refs.Count != 2 {
		t.Fatal("actual old publication and conversation references", e, refs.Count)
	}
	_, e = p.Uninstall(ctx, admin, version.ID, plugins.UninstallRequest{ExpectedRevision: version.Revision, ReferencesSHA256: refs.SHA256, Reason: "No implicit reference confirmation"}, uuid.NewString(), uuid.NewString())
	if e == nil || e.Error() != "PLUGIN_REFERENCED" {
		t.Fatal(e)
	}
	_, e = p.Uninstall(ctx, admin, version.ID, plugins.UninstallRequest{ExpectedRevision: version.Revision, ReferencesSHA256: refs.SHA256, ConfirmReferencedUninstall: true, Reason: "Actual confirmed reference-aware uninstall"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	withheld, e = s.GetRun(ctx, portal, workspace, draft.ID, oldRun.ID)
	if e != nil || withheld.ResultAvailable || withheld.ToolResult != nil || withheld.ResultUnavailableCode == nil || *withheld.ResultUnavailableCode != "PLUGIN_NOT_INSTALLED" {
		t.Fatal("uninstalled result leaked", e)
	}
	current, e := s.CreateConversation(ctx, portal, workspace, draft.ID, workflows.ConversationRequest{VersionID: next.ID, Reason: "New explicitly granted version"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	currentRun, e := s.RunConversation(ctx, portal, workspace, draft.ID, current.ID, workflows.RunRequest{Question: "New version still works", Reason: "Independent exact version grant"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	currentRun = waitTool(t, s, portal, draft.ID, currentRun.ID)
	expectToolSuccess(t, currentRun)
	if e = recovery.InvalidateRestored(ctx, h.Service, "Actual isolated common restore invalidation"); e != nil {
		t.Fatal("common restore", e)
	}
	var enabled bool
	var revision int64
	if e = p.DB.QueryRow(ctx, "SELECT enabled,revision FROM plugin_grants WHERE id=$1", newGrant.ID).Scan(&enabled, &revision); e != nil || enabled || revision != newGrant.Revision+1 {
		t.Fatal("restore retained execution grant", e)
	}
	tx, e := p.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = h.Service.Recheck(ctx, tx, portal); e == nil {
		t.Fatal("restored old session accepted")
	}
	tx.Rollback(ctx)
	fresh := fixture.Login(t, h.Service, "admin@example.invalid", password, "portal")
	withheld, e = s.GetRun(ctx, fresh, workspace, draft.ID, currentRun.ID)
	if e != nil || withheld.State != "succeeded" || withheld.ResultAvailable || withheld.ToolResult != nil {
		t.Fatal("restore exposed result without grant review", e)
	}
}

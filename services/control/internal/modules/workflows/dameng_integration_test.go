//go:build cgo && odbc_manager && dameng_integration

package workflows

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

// This suite requires an already migrated, exclusive empty disposable native
// schema. It creates no server, principal, schema, compatibility layer or driver.
// The operator retains the schema on failure and owns exact-schema destruction.
func localeNativeStore(t *testing.T) transaction.Store {
	t.Helper()
	path := os.Getenv("PROJECT_ONE_LOCALE_DAMENG_CONFIGURATION")
	schema := os.Getenv("PROJECT_ONE_LOCALE_DAMENG_DISPOSABLE_SCHEMA")
	c, e := dameng.LoadConnectionConfiguration(path, "")
	if e != nil || !regexp.MustCompile(`^M20_LOCALE_[A-F0-9]{32}_TEST$`).MatchString(schema) || c.Schema != schema || c.Policy.MaximumColumns < 32 {
		t.Fatal("LOCALE_NATIVE_EXCLUSIVE_CONFIG_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	kernel, _, e := dameng.Open(ctx, c)
	if kernel != nil {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, e := kernel.Close(ctx); e != nil {
				t.Error("LOCALE_NATIVE_CLOSE_REQUIRES_OPERATOR_RECONCILIATION")
			}
		})
	}
	if e != nil {
		t.Fatal(dameng.SafeError(e))
	}
	store, e := transaction.FromDameng(kernel)
	if e != nil {
		t.Fatal(e)
	}
	var users, tenants, workspaces, workflows, versions, bootstrapped int
	e = store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM tenants),(SELECT count(*) FROM workspaces),(SELECT count(*) FROM workflows),(SELECT count(*) FROM plugin_versions),(SELECT count(*) FROM installation_state WHERE bootstrap_complete=1) FROM DUAL`)).Scan(&users, &tenants, &workspaces, &workflows, &versions, &bootstrapped)
	if e != nil || users+tenants+workspaces+workflows+versions+bootstrapped != 0 {
		t.Fatal("LOCALE_NATIVE_EMPTY_MIGRATED_SCHEMA_REQUIRED", workflowRepositoryError(e))
	}
	return store
}

type localeNativeLostAck struct {
	transaction.Store
	armed atomic.Bool
}

func (s *localeNativeLostAck) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	e := s.Store.Within(ctx, readOnly, fn)
	if e == nil && s.armed.CompareAndSwap(true, false) {
		return errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitUnknown})
	}
	return e
}

func TestActualDamengWorkflowPlugin(t *testing.T) {
	store := localeNativeStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	tenant, workspace, actor := uuid.NewString(), uuid.NewString(), uuid.NewString()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(workflowRepositoryError(e))
		}
	}
	must(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		statements := []transaction.Statement{
			transaction.SQL(transaction.Dameng, "INSERT INTO tenants(id,name,status) VALUES(?,?,'active')", tenant, "Synthetic native tenant"),
			transaction.SQL(transaction.Dameng, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,?,'active')", workspace, tenant, "Synthetic native Workspace"),
			transaction.SQL(transaction.Dameng, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,?,'en','active',1)", actor, "locale-native@example.invalid", "locale-native@example.invalid", "Synthetic native actor"),
			transaction.SQL(transaction.Dameng, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES(?,?,?,'owner',?)", actor, tenant, workspace, uuid.NewString()),
			transaction.SQL(transaction.Dameng, "UPDATE installation_state SET bootstrap_complete=1,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE id=1", tenant, workspace, actor),
		}
		for _, statement := range statements {
			if e := workflowExecOne(ctx, q, statement); e != nil {
				return e
			}
		}
		return nil
	}))
	key := make([]byte, 32)
	_, e := rand.Read(key)
	must(e)
	lost := &localeNativeLostAck{Store: store}
	owner := &identity.Service{Storage: lost, HMACKey: key}
	policy := owner.Config.Policy()
	policy.Workflows.MaintenanceSeconds = 3600
	policy.Plugins.MaintenanceSeconds = 3600
	owner.Config.Runtime = &policy
	owner.Config.Workflows.Enabled = true
	public, private, e := ed25519.GenerateKey(nil)
	must(e)
	keyID, e := wire.KeyID(public)
	must(e)
	owner.Config.Plugins = config.PluginConfiguration{Enabled: true, TrustedKeys: []config.PluginTrustKey{{KeyID: keyID, PublicKey: base64.StdEncoding.EncodeToString(public)}}}
	ws := &workspaces.Service{Identity: owner}
	model := &models.Service{Identity: owner, Workspaces: ws}
	plugin := &plugins.Service{Identity: owner, Workspaces: ws, Models: model}
	workflow := &Service{Identity: owner, Workspaces: ws, Plugins: plugin}
	auth := func(user, audience string) identity.Auth {
		var issued identity.IssuedSession
		must(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			issued, e = owner.IssueSessionWithin(ctx, q, user, 1, audience, "", uuid.NewString(), nil)
			return e
		}))
		a, e := owner.Authenticate(ctx, issued.Token, audience)
		must(e)
		return a
	}
	admin, portal := auth(actor, "admin"), auth(actor, "portal")
	must(plugin.Start(ctx))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := plugin.Stop(cleanup); e != nil {
			t.Error(e)
		}
	})
	must(workflow.Start(ctx))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := workflow.Stop(cleanup); e != nil {
			t.Error(e)
		}
	})
	capsule, e := wire.Sign(wire.Manifest{PluginID: wire.PluginID, Version: "1.0.0", ABI: wire.ABI, Executor: wire.Executor, Capabilities: []string{wire.Capability}, License: wire.License}, private, owner.Config.Policy().Plugins.PackageLimits())
	must(e)
	input := plugins.ImportRequest{Capsule: base64.StdEncoding.EncodeToString(capsule), SHA256: wire.Digest(capsule), Reason: "Synthetic native import"}
	importKey := uuid.NewString()
	version, e := plugin.Import(ctx, admin, input, importKey, uuid.NewString())
	must(e)
	replay, e := plugin.Import(ctx, admin, input, importKey, uuid.NewString())
	must(e)
	if replay.ID != version.ID {
		t.Fatal("native import replay duplicated version")
	}
	version, e = plugin.Install(ctx, admin, version.ID, plugins.RevisionReason{ExpectedRevision: version.Revision, Reason: "Synthetic native install"}, uuid.NewString(), uuid.NewString())
	must(e)
	grant, e := plugin.Grant(ctx, admin, workspace, plugins.GrantRequest{PluginVersionID: version.ID, Reason: "Synthetic native grant"}, uuid.NewString(), uuid.NewString())
	must(e)
	// The actual native no-row result must retain the public grant-not-found
	// contract and leave neither a mutation intent nor a success audit behind.
	missingGrant, missingKey, missingRequest := uuid.NewString(), uuid.NewString(), uuid.NewString()
	missing, e := plugin.UpdateGrant(ctx, admin, workspace, missingGrant, plugins.GrantUpdate{Enabled: false, ExpectedRevision: 1, Reason: "Native missing grant rejection"}, missingKey, missingRequest)
	if e == nil || e.Error() != "PLUGIN_GRANT_NOT_FOUND" || missing.ID != "" {
		t.Fatal("native missing grant did not preserve not-found contract", e)
	}
	var missingIntents, missingAudits int
	must(store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT count(*) FROM plugin_mutation_intents WHERE tenant_id=? AND actor_id=? AND idempotency_key=?", tenant, actor, missingKey)).Scan(&missingIntents))
	must(store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT count(*) FROM audit_events WHERE tenant_id=? AND request_id=?", tenant, missingRequest)).Scan(&missingAudits))
	var grantEnabled bool
	var grantRevision int64
	must(store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT enabled,revision FROM plugin_grants WHERE tenant_id=? AND workspace_id=? AND id=?", tenant, workspace, grant.ID)).Scan(&grantEnabled, &grantRevision))
	if missingIntents != 0 || missingAudits != 0 || !grantEnabled || grantRevision != grant.Revision {
		t.Fatal("native missing grant rejection changed durable grant state")
	}
	draft, e := workflow.Create(ctx, portal, CreateRequest{WorkspaceID: workspace, Name: "Native tool path", Reason: "Synthetic native workflow"}, uuid.NewString(), uuid.NewString())
	must(e)
	var graph Graph
	must(json.Unmarshal(draft.DraftGraph, &graph))
	if graph.SchemaVersion != ToolGraphSchemaVersion {
		t.Fatal("native tool profile fell back")
	}
	for i := range graph.Nodes {
		if graph.Nodes[i].Kind == ToolNode {
			graph.Nodes[i].Configuration, _ = json.Marshal(ToolConfiguration{PluginVersionID: version.ID})
		}
	}
	draft, e = workflow.Update(ctx, portal, workspace, draft.ID, UpdateRequest{Name: draft.Name, Graph: graph, Enabled: true, ExpectedRevision: draft.Revision, Reason: "Native exact grant"}, uuid.NewString())
	must(e)
	publicationKey := uuid.NewString()
	publicationInput := RevisionReason{ExpectedRevision: draft.Revision, Reason: "Native publication"}
	publication, e := workflow.Publish(ctx, portal, workspace, draft.ID, publicationInput, publicationKey, uuid.NewString())
	must(e)
	publicationReplay, e := workflow.Publish(ctx, portal, workspace, draft.ID, publicationInput, publicationKey, uuid.NewString())
	must(e)
	if publicationReplay.ID != publication.ID {
		t.Fatal("native publication replay duplicated version")
	}
	conversation, e := workflow.CreateConversation(ctx, portal, workspace, draft.ID, ConversationRequest{VersionID: publication.ID, Reason: "Native private conversation"}, uuid.NewString(), uuid.NewString())
	must(e)
	runKey := uuid.NewString()
	request := RunRequest{Question: "List current models", Reason: "Actual native tool execution"}
	run, e := workflow.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, request, runKey, uuid.NewString())
	must(e)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		run, e = workflow.GetRun(ctx, portal, workspace, draft.ID, run.ID)
		must(e)
		if run.State != PendingState && run.State != RunningState {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native execution deadline")
		case <-ticker.C:
		}
	}
	if run.State != SucceededState || !run.ResultAvailable || run.ToolResult == nil || run.Answer != nil || run.Usage != nil || len(run.Nodes) != ToolNodeCount {
		t.Fatal("native tool result or traces failed")
	}
	replayRun, e := workflow.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, request, runKey, uuid.NewString())
	must(e)
	if replayRun.ID != run.ID {
		t.Fatal("native run replay duplicated")
	}
	for _, node := range run.Nodes {
		if node.State != SucceededState || node.StartedAt == nil || node.FinishedAt == nil {
			t.Fatal("native trace transition missing")
		}
	}
	var invoked, notices int
	must(store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT count(*) FROM audit_events WHERE action='plugin.invoked' AND tenant_id=?", tenant)).Scan(&invoked))
	must(store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT count(*) FROM terminal_notifications WHERE run_id=?", run.ID)).Scan(&notices))
	if invoked != 1 || notices != 1 {
		t.Fatal("native replay duplicated audit or terminal notice")
	}

	// A second authenticated Workspace member cannot read another caller's run.
	viewerID := uuid.NewString()
	viewerEmail := viewerID + "@example.invalid"
	must(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := workflowExecOne(ctx, q, transaction.SQL(transaction.Dameng, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Synthetic viewer','en','active',0)", viewerID, viewerEmail, viewerEmail)); e != nil {
			return e
		}
		return workflowExecOne(ctx, q, transaction.SQL(transaction.Dameng, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES(?,?,?,'viewer',?)", viewerID, tenant, workspace, uuid.NewString()))
	}))
	viewer := auth(viewerID, "portal")
	if _, e = workflow.GetRun(ctx, viewer, workspace, draft.ID, run.ID); e == nil || e.Error() != "WORKFLOW_RUN_NOT_FOUND" {
		t.Fatal("native private run crossed caller scope", e)
	}
	_, e = workflow.Create(ctx, portal, CreateRequest{WorkspaceID: workspace, Name: "Native seek second row", Reason: "Native pagination fixture"}, uuid.NewString(), uuid.NewString())
	must(e)
	page, e := workflow.List(ctx, portal, workspace, url.Values{"limit": {"1"}})
	must(e)
	if len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal("native page missing cursor")
	}
	next, e := workflow.List(ctx, portal, workspace, url.Values{"limit": {"1"}, "cursor": {*page.NextCursor}})
	must(e)
	if len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal("native cursor repeated/skipped row")
	}
	refs, e := plugin.References(ctx, admin, version.ID)
	must(e)
	if refs.Count < 3 {
		t.Fatal("native CLOB references incomplete")
	}
	_, e = plugin.List(ctx, admin, url.Values{"limit": {"1"}})
	must(e)
	_, e = plugin.Grants(ctx, portal, workspace, url.Values{"limit": {"1"}})
	must(e)
	_, e = workflow.List(ctx, portal, workspace, url.Values{"limit": {"1"}})
	must(e)
	_, e = workflow.Versions(ctx, portal, workspace, draft.ID, url.Values{"limit": {"1"}})
	must(e)
	_, e = workflow.Conversations(ctx, portal, workspace, draft.ID, url.Values{"limit": {"1"}})
	must(e)
	_, e = workflow.RunPage(ctx, portal, workspace, draft.ID, &conversation.ID, url.Values{"limit": {"1"}})
	must(e)
	// Actual COMMIT succeeds but the injected acknowledgement is unknown. Restore
	// the acknowledgement boundary and reconcile the key in a new real transaction.
	lost.armed.Store(true)
	uncertainKey := uuid.NewString()
	_, e = workflow.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, request, uncertainKey, uuid.NewString())
	if kind, ok := transaction.CommitOutcome(e); !ok || kind != transaction.CommitUnknown {
		t.Fatal("native unknown admission was hidden")
	}
	pending, e := workflow.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, request, uncertainKey, uuid.NewString())
	must(e)
	if pending.State != PendingState {
		t.Fatal("unknown native admission automatically dispatched")
	}
	_, e = workflow.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, request, uuid.NewString(), uuid.NewString())
	if e == nil || e.Error() != "WORKFLOW_CONVERSATION_BUSY" {
		t.Fatal("native one-active-turn fence", e)
	}

	// The native unique active-conversation index remains a second fence. The
	// failing statement ends this transaction immediately; readback is separate.
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var authority, membership string
		if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT execution_authority FROM workflows WHERE id=?", draft.ID)).Scan(&authority); e != nil {
			return e
		}
		if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT authority_id FROM workspace_memberships WHERE user_id=? AND workspace_id=?", actor, workspace)).Scan(&membership); e != nil {
			return e
		}
		return workflowExecOne(ctx, q, workflowStatement(q, workflowRunCreate, uuid.NewString(), tenant, workspace, draft.ID, actor, conversation.ID, 3, publication.ID, nil, authority, membership, "portal", []byte(publication.Graph), publication.GraphSHA256, "Duplicate active turn", 180, "model_list"))
	})
	var conflict *transaction.Error
	if !errors.As(e, &conflict) || conflict.Kind != transaction.UniqueConflict {
		t.Fatal("native active-turn database fence", workflowRepositoryError(e))
	}
	pending, e = workflow.CancelRun(ctx, portal, workspace, draft.ID, pending.ID, "Cancel reconciled pending run", uuid.NewString())
	must(e)
	if pending.CancellationRequestedAt == nil {
		t.Fatal("native cancellation was not durable")
	}
	// Explicit expired lease fixture preserves the immutable admission deadline.
	must(workflowExecOne(ctx, store, workflowStatement(store, workflowRunClaim, pending.ID, uuid.NewString(), uuid.NewString(), -1)))
	must(workflow.RecoverExpired(ctx))
	pending, e = workflow.GetRun(ctx, portal, workspace, draft.ID, pending.ID)
	must(e)
	if pending.State != InterruptedState {
		t.Fatal("native recovery resumed or retained expired execution")
	}
	must(store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT count(*) FROM audit_events WHERE action='plugin.invoked' AND tenant_id=?", tenant)).Scan(&invoked))
	if invoked != 1 {
		t.Fatal("unknown admission reinvoked tool")
	}
	grant, e = plugin.UpdateGrant(ctx, admin, workspace, grant.ID, plugins.GrantUpdate{Enabled: false, ExpectedRevision: grant.Revision, Reason: "Native revoke"}, uuid.NewString(), uuid.NewString())
	must(e)
	run, e = workflow.GetRun(ctx, portal, workspace, draft.ID, run.ID)
	must(e)
	if run.ResultAvailable {
		t.Fatal("revoked grant exposed historic result")
	}
	// No knowledge service is configured: schema1 authority must fail explicitly.
	must(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		_, e := workflow.references(ctx, q, portal, workspaces.Workspace{ID: workspace, TenantID: tenant}, ParsedGraph{Graph: Graph{SchemaVersion: GraphSchemaVersion}})
		if e == nil || e.Error() != "KNOWLEDGE_UNAVAILABLE" {
			return failure("WORKFLOW_SCHEMA1_FALLBACK")
		}
		return nil
	}))
	refs, e = plugin.References(ctx, admin, version.ID)
	must(e)
	_, e = plugin.Uninstall(ctx, admin, version.ID, plugins.UninstallRequest{ExpectedRevision: version.Revision, ReferencesSHA256: refs.SHA256, ConfirmReferencedUninstall: true, Reason: "Native referenced uninstall"}, uuid.NewString(), uuid.NewString())
	must(e)
	_, e = workflow.Cleanup(ctx)
	must(e)
	_, e = plugin.Cleanup(ctx)
	must(e)
	// Offline restore callbacks run only after both process lifecycles have joined.
	must(workflow.Stop(ctx))
	must(plugin.Stop(ctx))
	must(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := InvalidateAuthoritiesWithin(ctx, q); e != nil {
			return e
		}
		return plugins.InvalidateAuthoritiesWithin(ctx, q)
	}))
}

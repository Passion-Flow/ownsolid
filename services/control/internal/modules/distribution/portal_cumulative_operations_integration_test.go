//go:build plugin_database_integration && knowledge_gateway_integration

package distribution_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

func TestM15CumulativeNativeToolCrashRestartLogicalRestoreAndGrantRecovery(t *testing.T) {
	for _, toolOnly := range []bool{false, true} {
		name := map[bool]string{false: "mixed", true: "tool-only"}[toolOnly]
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), name)
			if e := os.Mkdir(root, 0700); e != nil {
				t.Fatal(e)
			}
			t.Setenv("PROJECT_ONE_EVIDENCE_ROOT", root)
			runM15CumulativeNativeTool(t, toolOnly)
		})
	}
}
func runM15CumulativeNativeTool(t *testing.T, toolOnly bool) {
	fixture.PreflightOwnedPostgres(t)
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	admin := fixture.Login(t, accounts, "admin@example.invalid", password, "admin")
	portal := fixture.Login(t, accounts, "admin@example.invalid", password, "portal")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	public, private, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal(e)
	}
	keyID, _ := wire.KeyID(public)
	accounts.Config.Plugins = config.PluginConfiguration{Enabled: true, TrustedKeys: []config.PluginTrustKey{{KeyID: keyID, PublicKey: base64.StdEncoding.EncodeToString(public)}}}
	accounts.Config.Workflows.Enabled = true
	scratch := t.TempDir()
	if e = os.Chmod(scratch, 0700); e != nil {
		t.Fatal(e)
	}

	// Test-only process observation around the actual approved Python executable.
	// It forwards every byte/argument and supplies no result or model substitute.
	encoderCount := filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "actual-encoder-execs.count")
	if e = os.WriteFile(encoderCount, nil, 0600); e != nil {
		t.Fatal(e)
	}
	wrapper := filepath.Join(t.TempDir(), "observe-actual-python")
	actualPython := os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	if e = os.WriteFile(wrapper, []byte("#!/bin/sh\nprintf x >> "+quote(encoderCount)+"\nexec "+quote(actualPython)+" \"$@\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	encoderExecutions := func() int {
		raw, e := os.ReadFile(encoderCount)
		if e != nil {
			t.Fatal(e)
		}
		return len(raw)
	}
	accounts.Config.Knowledge = config.KnowledgeConfiguration{Enabled: true, PythonExecutable: wrapper, WorkerFile: filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "runtimes/knowledge/embed.py"), ModelDirectory: os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY"), ScratchRoot: scratch}
	if toolOnly {
		accounts.Config.Knowledge = config.KnowledgeConfiguration{}
	}
	accounts.Config.Models.ArtifactRoot = t.TempDir()
	if e = models.InitializeStore(accounts.Config.Models.ArtifactRoot, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}

	if !accounts.Config.PortalOnly() {
		t.Fatal("current cumulative portal profile required")
	}
	accounts.Config.Datasets.ArtifactRoot = t.TempDir()
	accounts.Config.Datasets.PythonExecutable = os.Getenv("PROJECT_ONE_WORKFLOW_DATASET_PYTHON")
	accounts.Config.Datasets.ProcessorFile = filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "runtimes/data-processing/process.py")
	accounts.Config.Training.ArtifactRoot = t.TempDir()
	accounts.Config.ModelPreparation.ArtifactRoot = t.TempDir()
	for _, store := range []struct {
		path       string
		initialize func(string, string, int, int) error
	}{{accounts.Config.Datasets.ArtifactRoot, datasets.InitializeStore}, {accounts.Config.Training.ArtifactRoot, training.InitializeStore}, {accounts.Config.ModelPreparation.ArtifactRoot, modelpreparations.InitializeStore}} {
		if e = store.initialize(store.path, portal.Me.Workspace.TenantID, os.Geteuid(), os.Getegid()); e != nil {
			t.Fatal("current four-store init", e)
		}
	}
	policy := accounts.Config.Policy()
	policy.HTTP.RequestSeconds = 90
	policy.HTTP.StartupSeconds = 60
	policy.HTTP.ShutdownSeconds = 30
	policy.Workflows.RetrievalSeconds, policy.Workflows.AnswerSeconds = 10, 10
	policy.Workflows.CommitSeconds, policy.Workflows.RunSeconds = 2, 22
	policy.Workflows.LeaseSeconds, policy.Workflows.HeartbeatSeconds = 24, 1
	policy.Workflows.MaintenanceSeconds = 1
	accounts.Config.Runtime = &policy
	h := identityhttp.New(accounts)
	if e = h.Knowledge.Service.OpenConfiguredProcessor(ctx); e != nil {
		t.Fatal(e)
	}
	if h.Knowledge.Service.Processor != nil {
		defer h.Knowledge.Service.Processor.Close()
	}
	if e = h.Plugins.Service.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if e := h.Plugins.Service.Stop(cleanup); e != nil {
			t.Error(e)
		}
	}()
	s := h.Workflows.Service
	if e = s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if e := s.Stop(cleanup); e != nil {
			t.Error(e)
		}
	}()
	raw, e := wire.Sign(wire.Manifest{PluginID: wire.PluginID, Version: "1.0.0", ABI: wire.ABI, Executor: wire.Executor, Capabilities: []string{wire.Capability}, License: wire.License}, private, policy.Plugins.PackageLimits())
	if e != nil {
		t.Fatal(e)
	}
	version, e := h.Plugins.Service.Import(ctx, admin, plugins.ImportRequest{Capsule: base64.StdEncoding.EncodeToString(raw), SHA256: wire.Digest(raw), Reason: "Actual synthetic native plugin"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	version, e = h.Plugins.Service.Install(ctx, admin, version.ID, plugins.RevisionReason{ExpectedRevision: version.Revision, Reason: "Actual native install"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	workspace := portal.Me.Workspace.ID
	scope := uuid.NewString()
	currentWorkspace, e := h.Workspaces.Service.Get(ctx, admin, workspace, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.Workspaces.Service.Update(ctx, admin, workspace, "Plugin qualification "+scope, "active", currentWorkspace.Version, "Name only the new isolated target-client fixture", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	grant, e := h.Plugins.Service.Grant(ctx, admin, workspace, plugins.GrantRequest{PluginVersionID: version.ID, Reason: "Actual native grant"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	draft, e := s.Create(ctx, portal, workflows.CreateRequest{WorkspaceID: workspace, Name: "Native signed tool recovery", Reason: "Actual synthetic native workflow"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	tool, _ := json.Marshal(workflows.ToolConfiguration{PluginVersionID: version.ID})
	graph := workflows.Graph{SchemaVersion: 2, Nodes: []workflows.Node{{ID: ids[0], Kind: "start", Configuration: json.RawMessage(`{}`)}, {ID: ids[1], Kind: "tool", Position: workflows.Position{X: 220}, Configuration: tool}, {ID: ids[2], Kind: "end", Position: workflows.Position{X: 440}, Configuration: json.RawMessage(`{}`)}}, Edges: []workflows.Edge{{ID: uuid.NewString(), Source: ids[0], Target: ids[1]}, {ID: uuid.NewString(), Source: ids[1], Target: ids[2]}}}
	draft, e = s.Update(ctx, portal, workspace, draft.ID, workflows.UpdateRequest{Name: draft.Name, Graph: graph, Enabled: true, ExpectedRevision: draft.Revision, Reason: "Actual native saved tool graph"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	publication, e := s.Publish(ctx, portal, workspace, draft.ID, workflows.RevisionReason{ExpectedRevision: draft.Revision, Reason: "Actual native immutable version"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	draft, e = s.Get(ctx, portal, workspace, draft.ID)
	if e != nil {
		t.Fatal(e)
	}
	conversation, e := s.CreateConversation(ctx, portal, workspace, draft.ID, workflows.ConversationRequest{VersionID: publication.ID, Reason: "Actual native pinned conversation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	request := workflows.RunRequest{Question: "List currently accessible model references", Reason: "Actual native persisted tool result"}
	beforeEncoder := encoderExecutions()
	var beforeGateway int
	if e = accounts.DB.QueryRow(ctx, "SELECT count(*) FROM gateway_requests").Scan(&beforeGateway); e != nil {
		t.Fatal(e)
	}
	successful, e := s.RunConversation(ctx, portal, workspace, draft.ID, conversation.ID, request, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		successful, e = s.GetRun(ctx, portal, workspace, draft.ID, successful.ID)
		if e != nil {
			t.Fatal(e)
		}
		if successful.State != "pending" && successful.State != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial actual tool result deadline")
		}
		time.Sleep(25 * time.Millisecond)
	}
	if successful.State != "succeeded" || successful.ToolResult == nil || !successful.ResultAvailable {
		t.Fatal("initial actual tool result", successful.State)
	}
	afterEncoder := encoderExecutions()
	var afterGateway int
	if e = accounts.DB.QueryRow(ctx, "SELECT count(*) FROM gateway_requests").Scan(&afterGateway); e != nil || afterGateway != beforeGateway || afterEncoder != beforeEncoder {
		t.Fatal("SQL tool touched encoder/gateway", e)
	}
	if toolOnly && beforeEncoder != 0 {
		t.Fatal("tool-only started encoder")
	}
	observed, _ := json.MarshalIndent(map[string]any{"profile": accounts.Config.Server.Profile(), "tool_only": toolOnly, "actual_encoder_executions_before": beforeEncoder, "actual_encoder_executions_after": afterEncoder, "actual_gateway_rows_before": beforeGateway, "actual_gateway_rows_after": afterGateway, "structured_actual_sql_result": true, "synthetic_observation_wrapper": true, "actual_python": actualPython}, "", "  ")
	if e = os.WriteFile(filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "actual-tool-dependency-observation.json"), observed, 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	if e = h.Plugins.Service.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	if h.Knowledge.Service.Processor != nil {
		h.Knowledge.Service.Processor.Close()
	}
	owned := fixture.OwnedPostgres(t, accounts.DB)
	p := nativeWorkflow(t, ctx, accounts, accounts.DB.Config().ConnConfig.Database, "plugin-source")
	p.start()
	p.call("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, "", 200)
	path := "/api/v1/workspaces/" + workspace + "/workflows/" + draft.ID
	readRun := func(process *nativeWorkflowProcess, id string) workflows.Run {
		var r workflows.Run
		if json.Unmarshal(process.call("GET", path+"/runs/"+id, nil, "", 200), &r) != nil {
			t.Fatal("native tool durable read")
		}
		return r
	}
	if r := readRun(p, successful.ID); r.State != "succeeded" || !r.ResultAvailable || r.ToolResult == nil || len(r.ToolResult.Items) != 0 {
		t.Fatal("native startup lost actual empty tool result")
	}
	verifyPluginTargetClient(t, ctx, p, scope, workspace, version.ID, password)
	currentWorkspace, e = h.Workspaces.Service.Get(ctx, admin, workspace, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.Workspaces.Service.Update(ctx, admin, workspace, "Portal qualification "+scope, "active", currentWorkspace.Version, "Name only the new isolated Portal target-client fixture", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	verifyPortalTargetClient(t, ctx, p, scope, workspace, draft.ID, password)
	portalState := capturePortalNativeState(t, p, successful.ID)

	// A test-only advisory gate in the real invocation audit transaction provides
	// a deterministic observed crash point; no application result is synthesized.
	const gate int64 = 140017
	hold := func() (workflows.Run, string, pgx.Tx) {
		t.Helper()
		lock, e := accounts.DB.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = lock.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", gate); e != nil {
			t.Fatal(e)
		}
		if _, e = accounts.DB.Exec(ctx, `CREATE FUNCTION plugin_native_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='plugin.invoked' THEN PERFORM pg_advisory_xact_lock(140017); END IF; RETURN NEW; END $$; CREATE TRIGGER plugin_native_gate BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION plugin_native_gate()`); e != nil {
			t.Fatal(e)
		}
		key := uuid.NewString()
		var r workflows.Run
		input := workflows.DebugRequest{Question: request.Question, ExpectedRevision: draft.Revision, Reason: "Actual native held invocation"}
		if json.Unmarshal(p.call("POST", path+"/debug-runs", input, key, 202), &r) != nil {
			t.Fatal("native held admission")
		}
		limit := time.Now().Add(10 * time.Second)
		for {
			var observed bool
			e = accounts.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND query LIKE '%INSERT INTO audit_events%')`).Scan(&observed)
			if e != nil {
				t.Fatal(e)
			}
			if observed {
				break
			}
			if time.Now().After(limit) {
				t.Fatal("real tool invocation advisory barrier not observed")
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Cleanup(func() { lock.Rollback(context.Background()) })
		return r, key, lock
	}
	release := func(lock pgx.Tx) {
		t.Helper()
		lock.Rollback(ctx)
		if _, e := accounts.DB.Exec(ctx, "DROP TRIGGER plugin_native_gate ON audit_events;DROP FUNCTION plugin_native_gate()"); e != nil {
			t.Fatal(e)
		}
	}
	crashed, key, lock := hold()
	p.crash()
	release(lock)
	archive := owned.DumpSchema(t, ctx)
	if e = os.WriteFile(filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "plugin-backup.dump"), archive, 0600); e != nil {
		t.Fatal(e)
	}
	var before, after int
	if e = accounts.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='plugin.invoked'").Scan(&before); e != nil {
		t.Fatal(e)
	}
	p.start()
	deadline = time.Now().Add(35 * time.Second)
	for {
		r := readRun(p, crashed.ID)
		if r.State == "interrupted" {
			if r.ResultAvailable || r.ToolResult != nil || r.ErrorCode == nil || *r.ErrorCode != "WORKFLOW_RUN_INTERRUPTED" || len(r.Nodes) != 3 || r.Nodes[1].State != "interrupted" || r.Nodes[2].State != "not_started" {
				t.Fatal("native crash fabricated tool completion")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real native lease expiry deadline")
		}
		time.Sleep(100 * time.Millisecond)
	}
	var noticeCount int
	if e = accounts.DB.QueryRow(ctx, "SELECT count(*) FROM terminal_notifications WHERE run_id=$1 AND outcome='interrupted'", crashed.ID).Scan(&noticeCount); e != nil || noticeCount != 1 {
		t.Fatal("crash interruption notice cardinality", e, noticeCount)
	}
	verifyPortalNativeState(t, p, portalState)
	var replay workflows.Run
	if json.Unmarshal(p.call("POST", path+"/debug-runs", workflows.DebugRequest{Question: request.Question, ExpectedRevision: draft.Revision, Reason: "Actual native held invocation"}, key, 202), &replay) != nil || replay.ID != crashed.ID || replay.State != "interrupted" {
		t.Fatal("native replay changed interrupted identity")
	}
	if e = accounts.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='plugin.invoked'").Scan(&after); e != nil || before != after {
		t.Fatal("native restart/replay reinvoked tool", e)
	}
	graceful, _, lock := hold()
	p.stop()
	release(lock)
	var state string
	if e = accounts.DB.QueryRow(ctx, "SELECT state FROM workflow_runs WHERE id=$1", graceful.ID).Scan(&state); e != nil || state != "interrupted" {
		t.Fatal("graceful native tool owner did not join", e, state)
	}
	if e = accounts.DB.QueryRow(ctx, "SELECT count(*) FROM terminal_notifications WHERE run_id=$1 AND outcome='interrupted'", graceful.ID).Scan(&noticeCount); e != nil || noticeCount != 1 {
		t.Fatal("graceful joined terminal notice", e, noticeCount)
	}
	restored := owned.RestoreFresh(t, ctx, accounts.DB, archive, accounts.Config.AccountRuntime())
	if e = database.Check(ctx, restored, accounts.Config.Server.Profile()); e != nil {
		t.Fatal(e)
	}
	recoveredAccounts := *accounts
	recoveredAccounts.DB = restored
	mail := *accounts.Mail
	mail.DB = restored
	recoveredAccounts.Mail = &mail
	target := nativeWorkflow(t, ctx, &recoveredAccounts, restored.Config().ConnConfig.Database, "plugin-restored")
	clearFault := fixture.ArmLateWorkflowRestoreFailure(t, restored)
	target.operator(false)
	var enabled bool
	if e = restored.QueryRow(ctx, "SELECT enabled FROM plugin_grants WHERE id=$1", grant.ID).Scan(&enabled); e != nil || !enabled {
		t.Fatal("failed restore changed grant", e)
	}
	if e = restored.QueryRow(ctx, "SELECT state FROM workflow_runs WHERE id=$1", crashed.ID).Scan(&state); e != nil || state != "running" {
		t.Fatal("failed restore partially interrupted tool", e, state)
	}
	check, e := restored.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	e = recoveredAccounts.Recheck(ctx, check, portal)
	check.Rollback(ctx)
	if e != nil {
		t.Fatal("failed restore changed prior session", e)
	}
	if e = restored.QueryRow(ctx, "SELECT count(*) FROM terminal_notifications WHERE run_id=$1", crashed.ID).Scan(&noticeCount); e != nil || noticeCount != 0 {
		t.Fatal("failed restore emitted phantom notice", e, noticeCount)
	}
	clearFault()
	target.operator(true)
	if e = restored.QueryRow(ctx, "SELECT count(*) FROM terminal_notifications WHERE run_id=$1 AND outcome='interrupted'", crashed.ID).Scan(&noticeCount); e != nil || noticeCount != 1 {
		t.Fatal("restored interruption notice cardinality", e, noticeCount)
	}

	target.client.Jar = p.client.Jar
	target.start()
	target.call("GET", "/api/v1/me", nil, "", 401)
	target.call("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, "", 200)
	var futureTables int
	if e = restored.QueryRow(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname=current_schema() AND tablename IN ('terminal_notifications','user_preferences','applications')").Scan(&futureTables); e != nil || futureTables != 2 {
		t.Fatal("restore activated future owners", e)
	}
	r := readRun(target, successful.ID)
	if r.State != "succeeded" || r.ResultAvailable || r.ToolResult != nil {
		t.Fatal("restored tool result visible without grant review")
	}
	if r = readRun(target, crashed.ID); r.State != "interrupted" || r.ToolResult != nil {
		t.Fatal("restore did not interrupt durable tool")
	}
	var pinned workflows.Conversation
	if json.Unmarshal(target.call("GET", path+"/conversations/"+conversation.ID, nil, "", 200), &pinned) != nil || pinned.VersionID != publication.ID {
		t.Fatal("restore changed immutable tool pin")
	}
	recoveredHandler := identityhttp.New(&recoveredAccounts)
	if e = recoveredHandler.Plugins.Service.Start(ctx); e != nil {
		t.Fatal(e)
	}
	freshAdmin := fixture.Login(t, &recoveredAccounts, "admin@example.invalid", password, "admin")
	var revision int64
	if e = restored.QueryRow(ctx, "SELECT revision FROM plugin_grants WHERE id=$1", grant.ID).Scan(&revision); e != nil {
		t.Fatal(e)
	}
	_, e = recoveredHandler.Plugins.Service.UpdateGrant(ctx, freshAdmin, workspace, grant.ID, plugins.GrantUpdate{Enabled: true, ExpectedRevision: revision, Reason: "Explicit reviewed grant after actual logical restore"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = recoveredHandler.Plugins.Service.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	verifyPortalNativeState(t, target, portalState)
	if r = readRun(target, successful.ID); !r.ResultAvailable || r.ToolResult == nil {
		t.Fatal("explicit grant review failed to restore unchanged history")
	}
	target.stop()
	raw, _ = json.MarshalIndent(map[string]any{"status": "passed_actual_native_tool_crash_restart_backup_restore", "real_pg_advisory_barrier": true, "actual_sigkill_and_join": true, "actual_clock_lease_interruption": true, "exact_replay_no_invocation": true, "graceful_shutdown_joined": true, "actual_logical_export_fresh_restore": true, "restore_failure_atomic": true, "old_sessions_denied": true, "grants_disabled_until_explicit_review": true, "immutable_publication_pin": true, "historical_result_restored_without_reinvocation": true, "target_qualification": false, "tool_only": toolOnly, "knowledge_enabled": accounts.Config.Knowledge.Enabled}, "", "  ")
	if e = os.WriteFile(filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "portal-cumulative-native-operations.json"), append(raw, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}

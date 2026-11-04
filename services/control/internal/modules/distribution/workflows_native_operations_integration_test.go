//go:build knowledge_gateway_integration

package distribution_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// This fixture copies the real approved immutable model files for native startup
// validation. Synthetic machine observations remain explicit; no GPU is present.
func workflowNativeModelStore(t *testing.T, ctx context.Context, c scenario) string {
	t.Helper()
	root := t.TempDir()
	if e := models.InitializeStore(root, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal("native workflow model store", e)
	}
	var id string
	if e := c.s.DB.QueryRow(ctx, "SELECT id::text FROM model_versions WHERE state='ready'").Scan(&id); e != nil {
		t.Fatal(e)
	}
	objects := filepath.Join(root, "objects", id)
	if e := os.MkdirAll(objects, 0700); e != nil {
		t.Fatal(e)
	}
	for _, asset := range wire.ApprovedModel().Files {
		source, e := os.Open(filepath.Join(os.Getenv("PROJECT_ONE_CAUSAL_MODEL_DIRECTORY"), asset.Path))
		if e != nil {
			t.Fatal("actual approved native model source", e)
		}
		target, e := os.OpenFile(filepath.Join(objects, asset.Path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
		if e != nil {
			source.Close()
			t.Fatal(e)
		}
		n, copyError := io.Copy(target, io.LimitReader(source, asset.SizeBytes+1))
		source.Close()
		closeError := target.Close()
		if copyError != nil || closeError != nil || n != asset.SizeBytes {
			t.Fatal("bounded native model copy")
		}
	}
	if e := modelartifact.VerifyDirectory(ctx, objects); e != nil {
		t.Fatal("actual native model directory verification", e)
	}
	return root
}

func verifyWorkflowNativeOperations(t *testing.T, ctx context.Context, c scenario, handler *identityhttp.Handler, draft workflows.Workflow, conversation workflows.Conversation, successful workflows.Run, alias distribution.Alias, barrier *atomic.Pointer[completionBarrier], quiesceMachine func()) {
	t.Helper()
	owned := fixture.OwnedPostgres(t, c.s.DB)
	cleanup, cancel := context.WithTimeout(ctx, 30*time.Second)
	e := handler.Workflows.Service.Stop(cleanup)
	cancel()
	if e != nil {
		t.Fatal("stop parent workflow maintenance before native ownership", e)
	}
	accounts := *c.s.Identity
	accounts.Config.Models.ArtifactRoot = workflowNativeModelStore(t, ctx, c)
	accounts.Config.Datasets.ArtifactRoot = t.TempDir()
	accounts.Config.Datasets.PythonExecutable = os.Getenv("PROJECT_ONE_WORKFLOW_DATASET_PYTHON")
	accounts.Config.Datasets.ProcessorFile = filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "runtimes/data-processing/process.py")
	accounts.Config.Training.ArtifactRoot = t.TempDir()
	accounts.Config.ModelPreparation.ArtifactRoot = t.TempDir()
	for _, store := range []struct {
		path       string
		initialize func(string, string, int, int) error
	}{
		{accounts.Config.Datasets.ArtifactRoot, datasets.InitializeStore},
		{accounts.Config.Training.ArtifactRoot, training.InitializeStore},
		{accounts.Config.ModelPreparation.ArtifactRoot, modelpreparations.InitializeStore},
	} {
		if e := store.initialize(store.path, c.p.Me.Workspace.TenantID, os.Geteuid(), os.Getegid()); e != nil {
			t.Fatal("current four-store initialization", e)
		}
	}
	policy := accounts.Config.Policy()
	// An explicitly different supported deployment bounds real lease-clock wait.
	policy.Workflows.RetrievalSeconds, policy.Workflows.AnswerSeconds = 10, 10
	policy.Workflows.CommitSeconds, policy.Workflows.RunSeconds = 2, 22
	policy.Workflows.LeaseSeconds, policy.Workflows.HeartbeatSeconds = 24, 1
	policy.Workflows.MaintenanceSeconds = 1
	accounts.Config.Runtime = &policy
	p := nativeWorkflow(t, ctx, &accounts, accounts.DB.Config().ConnConfig.Database, "workflow-source")
	p.start()
	p.call("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, "", 200)
	w := c.p.Me.Workspace.ID
	path := "/api/v1/workspaces/" + w + "/workflows/" + draft.ID
	var pinned workflows.Conversation
	if json.Unmarshal(p.call("GET", path+"/conversations/"+conversation.ID, nil, "", 200), &pinned) != nil || pinned.VersionID != conversation.VersionID {
		t.Fatal("native process lost persisted conversation publication")
	}
	var history workflows.Run
	if json.Unmarshal(p.call("GET", path+"/runs/"+successful.ID, nil, "", 200), &history) != nil || !history.ResultAvailable || history.Answer == nil {
		t.Fatal("native process lost previously committed answer")
	}
	verifyWorkflowTargetClient(t, ctx, c, p, alias)
	question := "Which city does the project launch in? Answer in one short sentence with a source citation."
	admitHeld := func() (workflows.Run, *completionBarrier, string) {
		t.Helper()
		held := &completionBarrier{entered: make(chan struct{}), release: make(chan struct{})}
		barrier.Store(held)
		t.Cleanup(func() {
			select {
			case <-held.release:
			default:
				close(held.release)
			}
			barrier.CompareAndSwap(held, nil)
		})
		key := uuid.NewString()
		var run workflows.Run
		input := workflows.DebugRequest{Question: question, ExpectedRevision: draft.Revision, Reason: "Actual native lifecycle interruption"}
		if json.Unmarshal(p.call("POST", path+"/debug-runs", input, key, 202), &run) != nil || run.ID == "" {
			t.Fatal("native durable HTTP admission")
		}
		select {
		case <-held.entered:
		case <-ctx.Done():
			t.Fatal("native real model transport barrier")
		}
		return run, held, key
	}
	crashed, held, key := admitHeld()
	var keyID string
	if e = accounts.DB.QueryRow(ctx, "SELECT id::text FROM api_keys WHERE revoked_at IS NULL AND expires_at>clock_timestamp() AND created_by=$1", c.p.Me.User.ID).Scan(&keyID); e != nil {
		t.Fatal("exact crash-owned ordinary key", e)
	}
	p.crash()
	close(held.release)
	barrier.Store(nil)
	// A process kill cannot execute deferred key cleanup. Reduce only the exact
	// newly observed key through the supported operator path; never infer provider
	// termination or release unknown gateway capacity from workflow interruption.
	if _, e = c.s.RevokeKey(ctx, c.a, w, keyID, "Exact isolated key after actual process crash", uuid.NewString()); e != nil {
		t.Fatal("crashed ordinary key explicit revocation", e)
	}
	archive := owned.DumpSchema(t, ctx)
	evidence := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if e = os.WriteFile(filepath.Join(evidence, "workflow-backup.dump"), archive, 0600); e != nil {
		t.Fatal(e)
	}
	var requests int
	if e = accounts.DB.QueryRow(ctx, "SELECT count(*) FROM gateway_requests").Scan(&requests); e != nil {
		t.Fatal(e)
	}
	p.start()
	deadline := time.NewTimer(35 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if json.Unmarshal(p.call("GET", path+"/runs/"+crashed.ID, nil, "", 200), &history) != nil {
			t.Fatal("native restarted durable run read")
		}
		if history.State == "interrupted" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native crash overall deadline")
		case <-deadline.C:
			t.Fatal("native real lease expiration deadline")
		case <-ticker.C:
		}
	}
	if history.Answer != nil || history.ResultAvailable || workflowTestCode(history.ErrorCode) != "WORKFLOW_RUN_INTERRUPTED" || len(history.Nodes) != 4 || history.Nodes[2].State != "interrupted" || history.Nodes[3].State != "not_started" {
		t.Fatal("native crash fabricated success or downstream execution", history.State)
	}
	var replay workflows.Run
	input := workflows.DebugRequest{Question: question, ExpectedRevision: draft.Revision, Reason: "Actual native lifecycle interruption"}
	if json.Unmarshal(p.call("POST", path+"/debug-runs", input, key, 202), &replay) != nil || replay.ID != crashed.ID || replay.State != "interrupted" {
		t.Fatal("native replay automatically repeated interrupted model")
	}
	var after int
	if e = accounts.DB.QueryRow(ctx, "SELECT count(*) FROM gateway_requests").Scan(&after); e != nil || after != requests {
		t.Fatal("native restart/replay repeated gateway invocation", e)
	}
	graceful, held, _ := admitHeld()
	p.stop()
	close(held.release)
	barrier.Store(nil)
	var state string
	if e = accounts.DB.QueryRow(ctx, "SELECT state FROM workflow_runs WHERE id=$1", graceful.ID).Scan(&state); e != nil || state != "interrupted" {
		t.Fatal("native graceful shutdown did not join interrupted run", e, state)
	}
	// No fresh model requests follow this point. Join synthetic observations before
	// the exact owning coordinator restarts PostgreSQL; keep the product's ordinary
	// freshness checks and all persisted source/lease assertions unchanged.
	quiesceMachine()
	owned.RestartNative(t, ctx)
	if e = database.Check(ctx, accounts.DB); e != nil {
		t.Fatal("actual PostgreSQL restart lost current workflow catalog", e)
	}
	restored := owned.RestoreFresh(t, ctx, accounts.DB, archive)
	if e = database.Check(ctx, restored); e != nil {
		t.Fatal("actual restored workflow schema", e)
	}
	restoredAccounts := accounts
	restoredAccounts.DB = restored
	mail := *accounts.Mail
	mail.DB = restored
	restoredAccounts.Mail = &mail
	target := nativeWorkflow(t, ctx, &restoredAccounts, restored.Config().ConnConfig.Database, "workflow-restored")
	clearFault := fixture.ArmLateWorkflowRestoreFailure(t, restored)
	target.operator(false)
	if e = restored.QueryRow(ctx, "SELECT state FROM workflow_runs WHERE id=$1", crashed.ID).Scan(&state); e != nil || state != "running" {
		t.Fatal("failed common restore mutated durable workflow", e, state)
	}
	check, e := restored.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	e = restoredAccounts.Recheck(ctx, check, c.p)
	check.Rollback(ctx)
	if e != nil {
		t.Fatal("failed common restore changed prior session authority", e)
	}
	clearFault()
	target.operator(true)
	target.client.Jar = p.client.Jar
	target.start()
	target.call("GET", "/api/v1/me", nil, "", 401)
	target.call("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, "", 200)
	if json.Unmarshal(target.call("GET", path+"/runs/"+crashed.ID, nil, "", 200), &history) != nil || history.State != "interrupted" || history.Answer != nil {
		t.Fatal("common restore did not interrupt snapshot run")
	}
	if json.Unmarshal(target.call("GET", path+"/conversations/"+conversation.ID, nil, "", 200), &pinned) != nil || pinned.VersionID != conversation.VersionID {
		t.Fatal("logical restore lost immutable publication pin")
	}
	// Restore invalidates execution secrets and observations. It does not revoke
	// the unchanged alias or erase authorized historical source evidence.
	if json.Unmarshal(target.call("GET", path+"/runs/"+successful.ID, nil, "", 200), &history) != nil || !history.ResultAvailable || history.Answer == nil || *history.Answer != *successful.Answer {
		t.Fatal("restore lost current-authorized exact historical answer")
	}
	restoredHandler := identityhttp.New(&restoredAccounts)
	freshAdmin := fixture.Login(t, &restoredAccounts, "admin@example.invalid", c.password, "admin")
	if _, e = restoredHandler.Distribution.Service.DisableAlias(ctx, freshAdmin, w, alias.ID, alias.Revision, "Revoke exact restored synthetic alias", uuid.NewString()); e != nil {
		t.Fatal("actual restored alias revocation", e)
	}
	if json.Unmarshal(target.call("GET", path+"/runs/"+successful.ID, nil, "", 200), &history) != nil || history.ResultAvailable || history.Answer != nil || history.ResultUnavailableCode == nil {
		t.Fatal("current revoked restored alias exposed private body")
	}
	target.stop()
	digest := sha256.Sum256(archive)
	receipt, _ := json.MarshalIndent(map[string]any{"status": "passed_actual_native_workflow_crash_restart_shutdown_and_logical_restore", "crashed_run": crashed.ID, "graceful_run": graceful.ID, "actual_process_kill_and_join": true, "real_clock_expired_lease": true, "no_automatic_model_reinvocation": true, "exact_crash_key_operator_revoked": true, "unknown_provider_capacity_not_released": true, "custom_format_backup_sha256": hex.EncodeToString(digest[:]), "backup_bytes": len(archive), "actual_fresh_database_restore": true, "common_invalidation_rollback": true, "old_session_denied": true, "immutable_version_pin_preserved": true, "exact_authorized_history_preserved": true, "actual_alias_revocation_withholds_history": true, "target_qualification": false}, "", "  ")
	if e = os.WriteFile(filepath.Join(evidence, "workflow-native-operations.json"), append(receipt, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}

func verifyWorkflowTargetClient(t *testing.T, ctx context.Context, c scenario, p *nativeWorkflowProcess, alias distribution.Alias) {
	t.Helper()
	scope := uuid.NewString()
	workspace, e := c.s.Workspaces.Get(ctx, c.a, c.p.Me.Workspace.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	originalName := workspace.Name
	workspace, e = c.s.Workspaces.Update(ctx, c.a, workspace.ID, "Workflow qualification "+scope, "active", workspace.Version, "Exact owned synthetic qualifier scope", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		current, e := c.s.Workspaces.Get(ctx, c.a, workspace.ID, true)
		if e == nil {
			_, e = c.s.Workspaces.Update(ctx, c.a, workspace.ID, originalName, "active", current.Version, "Restore exact synthetic fixture name", uuid.NewString())
		}
		if e != nil {
			t.Error("owned qualifier Workspace name cleanup", e)
		}
	}()
	parent := t.TempDir()
	if os.Chmod(parent, 0700) != nil {
		t.Fatal("private qualifier scope")
	}
	parent, e = filepath.EvalSymlinks(parent)
	if e != nil {
		t.Fatal(e)
	}
	credentials := filepath.Join(parent, "credentials.json")
	secret, _ := json.Marshal(map[string]string{"email": "admin@example.invalid", "password": c.password})
	if os.WriteFile(credentials, secret, 0600) != nil {
		t.Fatal("private qualifier credentials")
	}
	configuration := filepath.Join(parent, "journey.json")
	raw, _ := json.Marshal(map[string]any{"schema_version": 1, "kind": "projectone-isolated-workflow-journey-v1", "scope_id": scope, "mount_root": os.Getenv("PROJECT_ONE_EXTERNAL_ROOT"), "workspace_id": workspace.ID, "portal_origin": p.origin, "ca_file": p.caFile, "credentials_file": credentials, "alias_id": alias.ID})
	if os.WriteFile(configuration, raw, 0600) != nil {
		t.Fatal("private qualifier configuration")
	}
	output := filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), "workflow-target-client")
	command := exec.CommandContext(ctx, "python3", "-B", filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "tests/validation/workflows/journey.py"), "--local-protocol-check", "--config", configuration, "--output-directory", output)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if e = command.Run(); e != nil {
		t.Fatal("actual workflow target client local protocol journey", e)
	}
	var receipt struct{ Status, Mode, Cleanup string }
	raw, e = os.ReadFile(filepath.Join(output, "manifest.json"))
	if e != nil || json.Unmarshal(raw, &receipt) != nil || receipt.Status != "passed_local_protocol_only" || receipt.Mode != "local_protocol_only" || receipt.Cleanup == "" {
		t.Fatal("workflow qualifier fabricated target qualification")
	}
}

package http_test

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

type cliRow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	State      string `json:"state"`
	Version    int64  `json:"version"`
	Revision   int64  `json:"revision"`
	Desired    string `json:"desired_state"`
	Observed   string `json:"observed_state"`
	LastTaskID string `json:"last_task_id"`
}

func TestLaneDAdminTLS(t *testing.T) {
	ctx := t.Context()
	binary := os.Getenv("LANE_D_CLI_BINARY")
	if binary == "" {
		t.Fatal("set LANE_D_CLI_BINARY to the compiled CLI")
	}
	service := fixture.New(t)
	var handler *transport.Handler
	var loseResponse atomic.Bool
	var committed atomic.Bool
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/admin/deployments") && loseResponse.CompareAndSwap(true, false) {
			reply := httptest.NewRecorder()
			handler.ServeHTTP(reply, r)
			committed.Store(reply.Code == http.StatusAccepted)
			// Simulate the proxy losing the reply after dispatching the real request.
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	service.Config.Server.AdminOrigin = "https://" + server.Listener.Addr().String()
	password := fixture.Bootstrap(t, service)
	auth := fixture.Login(t, service, "admin@example.invalid", password, "admin")
	handler = transport.New(service)
	if err := handler.LicenseInitializationError(); err != nil {
		t.Fatal(err)
	}
	if err := handler.Licensing.Service.VerifyStored(ctx); err != nil {
		t.Fatal(err)
	}
	server.StartTLS()

	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(dir, "ca.pem")
	passwordPath := filepath.Join(dir, "password")
	config, err := json.Marshal(map[string]string{"ca_file": caPath})
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string][]byte{
		caPath:                            pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}),
		passwordPath:                      []byte(password),
		filepath.Join(dir, "config.json"): config,
	} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OWNSOLID_CONFIG_DIR", dir)
	t.Setenv("OWNSOLID_SERVER", server.URL)
	call := func(args ...string) (string, error) {
		t.Helper()
		output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		if strings.Contains(string(output), password) {
			t.Fatal("CLI printed its password")
		}
		return string(output), err
	}
	ok := func(want string, args ...string) string {
		t.Helper()
		out, err := call(args...)
		if err != nil || want != "" && !strings.Contains(out, want) {
			t.Fatalf("CLI %v, want %q: %v %s", args, want, err, out)
		}
		return out
	}
	refused := func(want string, args ...string) {
		t.Helper()
		out, err := call(args...)
		if err == nil || !strings.Contains(out, want) {
			t.Fatalf("CLI %v, want refusal %q: %v %s", args, want, err, out)
		}
	}
	row := func(args ...string) cliRow {
		t.Helper()
		out := ok("", append(args, "-o", "json")...)
		var value cliRow
		if err := json.Unmarshal([]byte(out), &value); err != nil || value.ID == "" {
			t.Fatalf("invalid CLI resource for %v: %v", args, err)
		}
		return value
	}
	sql := func(statement string, args ...any) {
		t.Helper()
		if _, err := service.DB.Exec(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	count := func(want int, statement string, args ...any) {
		t.Helper()
		var got int
		if err := service.DB.QueryRow(ctx, statement, args...).Scan(&got); err != nil || got != want {
			t.Fatalf("fixture count %d, want %d: %v", got, want, err)
		}
	}

	ok("Examples:", "--help")
	ok("Examples:", "node", "list", "--help")
	ok("signed in", "login", "--email", "admin@example.invalid", "--password-file", passwordPath)
	const nodeName = "Lane D synthetic node"
	tokenFile := filepath.Join(dir, "enrollment")
	issued := ok("", "node", "join-token", "create", nodeName, "--profile", wire.DockerInferenceProfile, "--token-file", tokenFile, "-o", "json")
	var invite cliRow
	if err := json.Unmarshal([]byte(issued), &invite); err != nil || invite.ID == "" {
		t.Fatal("invalid invitation metadata", err)
	}
	secret, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(tokenFile)
	if err != nil || info.Mode().Perm() != 0600 || len(secret) == 0 {
		t.Fatal("enrollment credential mode or contents", err)
	}
	listed := ok(invite.ID, "node", "join-token", "list", "-o", "yaml")
	if strings.Contains(issued+listed, strings.TrimSpace(string(secret))) {
		t.Fatal("CLI leaked an enrollment credential")
	}
	refused("choose an unused --token-file", "node", "join-token", "create", nodeName, "--token-file", tokenFile)
	count(1, "SELECT count(*) FROM node_join_tokens")
	revoked := row("node", "join-token", "create", "unused invitation", "--token-file", filepath.Join(dir, "unused"))
	if result := row("node", "join-token", "revoke", revoked.ID, "--reason", "Fixture revoked"); result.Status != "revoked" {
		t.Fatal("invitation was not revoked")
	}
	handshake := wire.Handshake{
		ProtocolVersion: wire.InferenceVersion, ExecutionProfile: wire.DockerInferenceProfile,
		Capabilities: wire.Capabilities(wire.DockerInferenceProfile),
		Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "lane-d-node", LogicalCPUs: 4,
			MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion},
	}
	nodeService := handler.Nodes.Service
	node, err := nodeService.Enroll(ctx, wire.Enrollment{JoinToken: strings.TrimSpace(string(secret)), Handshake: handshake}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	ok(nodeName, "node", "list")
	current := row("node", "get", nodeName)
	nodeChange := func(command string, version int64, extra ...string) []string {
		return append([]string{"node", command, node.NodeID, "--version", strconv.FormatInt(version, 10), "--reason", "Lane D maintenance"}, extra...)
	}
	disabled := row(nodeChange("disable", current.Version)...)
	if disabled.Status != "disabled" || disabled.Version != current.Version+1 {
		t.Fatalf("disable result %#v", disabled)
	}
	refused("VERSION_CONFLICT", nodeChange("enable", current.Version)...)
	sql("UPDATE users SET is_platform_admin=false WHERE id=$1", auth.Me.User.ID)
	refused("HTTP 401", nodeChange("enable", disabled.Version)...)
	sql("UPDATE users SET is_platform_admin=true WHERE id=$1", auth.Me.User.ID)
	sql("UPDATE workspaces SET status='disabled' WHERE id=$1", auth.Me.Workspace.ID)
	refused("ACCESS_DENIED", nodeChange("enable", disabled.Version)...)
	sql("UPDATE workspaces SET status='active' WHERE id=$1", auth.Me.Workspace.ID)
	enabled := row(nodeChange("enable", disabled.Version)...)
	if enabled.Status != "active" || enabled.Version != disabled.Version+1 {
		t.Fatalf("enable result %#v", enabled)
	}
	renamed := row(nodeChange("rename", enabled.Version, "Renamed synthetic node")...)
	if renamed.Name != "Renamed synthetic node" {
		t.Fatalf("rename result %#v", renamed)
	}
	count(3, "SELECT count(*) FROM audit_events WHERE action='node.updated' AND target_id=$1", node.NodeID)

	model, gpu := uuid.NewString(), "GPU-"+uuid.NewString()
	manifest, err := json.Marshal(wire.ApprovedModel())
	if err != nil {
		t.Fatal(err)
	}
	sql("INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,'ready',$8)",
		model, auth.Me.Workspace.TenantID, auth.Me.Workspace.ID, "Synthetic approved metadata", wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, auth.Me.User.ID)
	handshake.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{{UUID: gpu, Name: "Synthetic GPU", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}}, ObservedAt: time.Now().UTC()}
	if _, err := nodeService.Heartbeat(ctx, node.NodeID, node.Credential, handshake); err != nil {
		t.Fatal(err)
	}
	ok(gpu, "node", "inference", node.NodeID, "-o", "json")
	probe := row("node", "probe-inference", node.NodeID, "--idempotency-key", uuid.NewString(), "--reason", "Refresh GPU inventory")
	lease := claimTask(t, nodeService, node, probe.ID)
	probeResult := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Inference: handshake.Inference}
	if state, err := nodeService.Complete(ctx, node.NodeID, node.Credential, probe.ID, probeResult, uuid.NewString()); err != nil || state != "succeeded" {
		t.Fatalf("settle GPU probe: %s %v", state, err)
	}
	ok(model, "model", "list", "-o", "yaml")
	ok("Synthetic approved metadata", "model", "get", model, "-o", "yaml")
	ok(gpu, "deployment", "placement", model)
	createKey := uuid.NewString()
	create := []string{"deployment", "create", "--node", node.NodeID, "--model", model, "--gpu", gpu, "--idempotency-key", createKey, "--reason", "Lane D placement"}
	refused("outside the selected workspace", append(append([]string{}, create...), "--workspace", uuid.NewString())...)
	loseResponse.Store(true)
	refused("retry with the same --idempotency-key "+createKey, create...)
	if !committed.Load() {
		t.Fatal("real deployment was not accepted before the response was lost")
	}
	deployment := row(create...)
	if deployment.Desired != "running" || deployment.LastTaskID == "" {
		t.Fatalf("create result %#v", deployment)
	}
	if replay := row(create...); replay.ID != deployment.ID || replay.LastTaskID != deployment.LastTaskID {
		t.Fatal("create retry issued another deployment or task")
	}
	count(1, "SELECT count(*) FROM deployments")
	count(1, "SELECT count(*) FROM node_tasks WHERE idempotency_key=$1", createKey)
	completeInference(t, nodeService, node, deployment.LastTaskID, "running")
	if read := row("deployment", "get", deployment.ID); read.Observed != "running" {
		t.Fatalf("observed deployment %#v", read)
	}
	ok(deployment.ID, "deployment", "list")
	action := func(command string, revision int64, key string, confirm bool) []string {
		args := []string{"deployment", command, deployment.ID, "--revision", strconv.FormatInt(revision, 10), "--idempotency-key", key, "--reason", "Lane D service action"}
		if confirm {
			args = append(args, "--confirm")
		}
		return args
	}
	stopKey := uuid.NewString()
	refused("pass --confirm", action("stop", deployment.Revision, stopKey, false)...)
	stop := action("stop", deployment.Revision, stopKey, true)
	loseResponse.Store(true)
	refused("retry with the same --idempotency-key "+stopKey, stop...)
	if !committed.Load() {
		t.Fatal("real stop was not accepted before the response was lost")
	}
	stopped := row(stop...)
	if stopped.Desired != "stopped" || stopped.Revision != deployment.Revision+1 {
		t.Fatalf("stop result %#v", stopped)
	}
	count(1, "SELECT count(*) FROM node_tasks WHERE idempotency_key=$1", stopKey)
	completeInference(t, nodeService, node, stopped.LastTaskID, "stopped")
	refused("VERSION_CONFLICT", action("stop", deployment.Revision, uuid.NewString(), true)...)
	if _, err := nodeService.Heartbeat(ctx, node.NodeID, node.Credential, handshake); err != nil {
		t.Fatal(err)
	}
	started := row(action("start", stopped.Revision, uuid.NewString(), false)...)
	completeInference(t, nodeService, node, started.LastTaskID, "running")
	observed := row(action("observe", started.Revision, uuid.NewString(), false)...)
	completeInference(t, nodeService, node, observed.LastTaskID, "running")
	verified := row(action("verify", observed.Revision, uuid.NewString(), false)...)
	completeInference(t, nodeService, node, verified.LastTaskID, "running")
	removed := row(action("remove", verified.Revision, uuid.NewString(), true)...)
	completeInference(t, nodeService, node, removed.LastTaskID, "not_installed")
	if read := row("deployment", "get", deployment.ID); read.Desired != "removed" || read.Observed != "not_installed" {
		t.Fatalf("removed deployment %#v", read)
	}
	refused("pass --confirm", "model", "retire", model, "--reason", "Model superseded")
	if retired := row("model", "retire", model, "--reason", "Model superseded", "--confirm"); retired.State != "deleted" {
		t.Fatalf("retired model %#v", retired)
	}

	task := row("node", "collect-facts", node.NodeID, "--idempotency-key", uuid.NewString())
	ok(task.ID, "node", "tasks", node.NodeID)
	ok(task.ID, "node", "task", task.ID, "-o", "yaml")
	factsLease := claimTask(t, nodeService, node, task.ID)
	now := time.Now().UTC()
	report := wire.TaskProgress{LeaseToken: factsLease.LeaseToken, Entries: []wire.ProgressEntry{
		{Sequence: 1, Kind: wire.ProgressKindLog, At: now, Level: wire.ProgressLevelInfo, Message: "Read host identity"},
		{Sequence: 2, Kind: wire.ProgressKindLog, At: now, Level: wire.ProgressLevelInfo, Message: "Read host memory"},
	}}
	if accepted, err := nodeService.RecordTaskProgress(ctx, node.NodeID, node.Credential, task.ID, report); err != nil || accepted.Accepted != 2 {
		t.Fatalf("record actual task progress: %v %v", accepted, err)
	}
	first := ok("\"cursor\": \"1.1\"", "node", "task-log", task.ID, "--limit", "1", "-o", "json")
	second := ok("Read host memory", "node", "task-log", task.ID, "--after", "1.1")
	if !strings.Contains(first, "Read host identity") || strings.Contains(first, "Read host memory") || strings.Contains(second, "Read host identity") {
		t.Fatal("task log pagination returned the wrong lines")
	}
	if outcome, err := nodeService.Complete(ctx, node.NodeID, node.Credential, task.ID, wire.Result{LeaseToken: factsLease.LeaseToken, Outcome: "success", Facts: &handshake.Facts}, uuid.NewString()); err != nil || outcome != "succeeded" {
		t.Fatalf("settle fact collection: %s %v", outcome, err)
	}
	refused("pass --confirm", nodeChange("remove", renamed.Version)...)
	if result := row(nodeChange("remove", renamed.Version, "--confirm")...); result.Status != "removed" {
		t.Fatalf("node removal result %#v", result)
	}
	count(1, "SELECT count(*) FROM managed_nodes WHERE id=$1 AND status='removed' AND credential_hash IS NULL", node.NodeID)
	ok("signed out", "logout")
	refused("no saved Admin session", "node", "list")
	count(1, "SELECT count(*) FROM audit_events WHERE action='auth.logout'")
}

func claimTask(t *testing.T, service *nodes.Service, node wire.Enrolled, task string) wire.Lease {
	t.Helper()
	ctx := t.Context()
	claim, err := service.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if err != nil || claim.Lease == nil || claim.Lease.TaskID != task {
		t.Fatalf("claim synthetic task %s: %v", task, err)
	}
	lease := claim.Lease
	if _, err := service.Acknowledge(ctx, node.NodeID, node.Credential, task, wire.TaskAcknowledgementRequest{Sequence: lease.Sequence, LeaseToken: lease.LeaseToken}, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	return *lease
}

func completeInference(t *testing.T, service *nodes.Service, node wire.Enrolled, task, state string) {
	t.Helper()
	ctx := t.Context()
	lease := claimTask(t, service, node, task)
	command := lease.Inference
	if command == nil {
		t.Fatal("claimed task has no inference command")
	}
	observation := &wire.DeploymentObservation{DeploymentID: command.DeploymentID, ModelID: command.ModelID, CatalogDigest: command.CatalogDigest, GPUUUID: command.GPUUUID, Revision: command.Revision, State: state}
	if state == "running" || state == "stopped" {
		observation.Managed = true
		observation.ContainerID = strings.Repeat("a", 64)
		observation.ImageID = wire.ApprovedInferenceRecipe().ImageConfigDigest
	}
	now := time.Now().UTC()
	if state == "running" {
		observation.Ready = true
		observation.Verification = &wire.GenerationVerification{Model: wire.InferenceModelName, PromptSHA256: wire.InferencePromptDigest(), ResponseSHA256: strings.Repeat("b", 64), InputTokens: 10, OutputTokens: 1, ObservedAt: now}
	}
	result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Inference: &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, Deployment: observation, ObservedAt: now}}
	if outcome, err := service.Complete(ctx, node.NodeID, node.Credential, task, result, uuid.NewString()); err != nil || outcome != "succeeded" {
		t.Fatalf("complete synthetic inference task: %s %v", outcome, err)
	}
}

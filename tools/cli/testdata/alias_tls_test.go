package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/google/uuid"
)

func TestLaneDAliasTLS(t *testing.T) {
	var lose, committed atomic.Bool
	c := newWorkflowCLI(t, nil, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/aliases") && lose.CompareAndSwap(true, false) {
				reply := httptest.NewRecorder()
				next.ServeHTTP(reply, r)
				committed.Store(reply.Code == http.StatusOK)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	ctx := t.Context()
	node := enrollWorkflowNode(c, "Alias publication fixture", wire.DockerInferenceProfile)
	model, gpu := uuid.NewString(), "GPU-"+uuid.NewString()
	manifest, err := json.Marshal(wire.ApprovedModel())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.service.DB.Exec(ctx, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,'ready',$8)",
		model, c.auth.Me.Workspace.TenantID, c.auth.Me.Workspace.ID, "Synthetic publication model", wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, c.auth.Me.User.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	handshake := wire.Handshake{
		ProtocolVersion: wire.InferenceVersion, ExecutionProfile: wire.DockerInferenceProfile,
		Capabilities: wire.Capabilities(wire.DockerInferenceProfile),
		Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "lane-d-node", LogicalCPUs: 4,
			MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: now, AgentVersion: wire.AgentVersion},
		Inference: &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{{UUID: gpu, Name: "Synthetic GPU", DriverVersion: "580.1",
			MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: now}}, ObservedAt: now},
	}
	if _, err := c.handler.Nodes.Service.Heartbeat(ctx, node.NodeID, node.Credential, handshake); err != nil {
		t.Fatal(err)
	}
	var deploy cliRow
	result := c.ok(`"desired_state": "running"`, "deployment", "create", "--model", model, "--node", node.NodeID, "--gpu", gpu,
		"--idempotency-key", uuid.NewString(), "--reason", "Synthetic publication target", "-o", "json")
	if err := json.Unmarshal([]byte(result), &deploy); err != nil || deploy.ID == "" || deploy.LastTaskID == "" {
		t.Fatal("invalid deployment intent", err)
	}
	completeInference(t, c.handler.Nodes.Service, node, deploy.LastTaskID, "running")
	gateway, err := c.handler.Distribution.Service.Provision(ctx, c.auth, distribution.ProvisionRequest{
		WorkspaceID: c.auth.Me.Workspace.ID, NodeID: node.NodeID, Name: "Provisioned fixture gateway",
		ClientOrigin: "https://gateway.example.invalid", Reason: "Synthetic gateway identity for Admin publication",
	}, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	c.ok(gateway.Gateway.ID, "deployment", "alias", "gateways")

	publish := func(name, key string, revision int64) []string {
		return []string{"deployment", "alias", "publish", deploy.ID, "--gateway", gateway.Gateway.ID, "--name", name,
			"--revision", strconv.FormatInt(revision, 10), "--idempotency-key", key, "--reason", "Publish approved fixture", "-o", "json"}
	}
	c.refused("MODEL_ALIAS_UNAVAILABLE", publish("stale-alias", uuid.NewString(), deploy.Revision+1)...)
	foreign := append(publish("foreign-alias", uuid.NewString(), deploy.Revision), "--workspace", uuid.NewString())
	c.refused("RESOURCE_NOT_FOUND", foreign...)
	c.count(0, "SELECT count(*) FROM model_aliases")
	key := uuid.NewString()
	args := publish("lane-d-model", key, deploy.Revision)
	lose.Store(true)
	c.refused("retry with the same --idempotency-key "+key, args...)
	if !committed.Load() {
		t.Fatal("the Admin handler did not accept the alias before losing its reply")
	}
	var alias distribution.Alias
	if err := json.Unmarshal([]byte(c.ok(`"available": true`, args...)), &alias); err != nil || alias.ID == "" {
		t.Fatal("invalid publication result", err)
	}
	c.ok(alias.ID, "deployment", "alias", "list")
	c.ok(alias.Name, "deployment", "alias", "get", alias.ID, "-o", "yaml")
	c.count(1, "SELECT count(*) FROM model_aliases WHERE idempotency_key=$1", key)
	c.count(1, "SELECT count(*) FROM audit_events WHERE action='model.alias_published' AND target_id=$1", alias.ID)

	disable := []string{"deployment", "alias", "disable", alias.ID, "--revision", strconv.FormatInt(alias.Revision, 10), "--reason", "Withdraw fixture", "-o", "json"}
	c.refused("--confirm", disable...)
	c.ok(`"available": false`, append(disable, "--confirm")...)
	c.count(1, "SELECT count(*) FROM audit_events WHERE action='model.alias_disabled' AND target_id=$1", alias.ID)
	c.count(1, "SELECT count(*) FROM model_aliases WHERE id=$1 AND disabled_at IS NOT NULL", alias.ID)
}

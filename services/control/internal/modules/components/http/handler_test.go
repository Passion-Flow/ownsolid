package http_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"net/http"
	"os"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"reflect"
	"testing"
	"time"
)

func TestM04HTTPActionsObservationsAndFencedActualArtifact(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	s.Config.Components.NodeExporterArchiveFile = filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/components/node-exporter/1.12.1/linux-amd64/node_exporter-1.12.1.linux-amd64.tar.gz")
	handler := transport.New(s)
	for _, name := range []string{"Models", "Deployments", "Distribution", "Training", "ModelPreparations", "Datasets", "Knowledge", "Workflows", "Plugins", "SSO", "ImageRegistry", "Monitoring", "TaskDeliveries", "Releases"} {
		if !reflect.ValueOf(handler).Elem().FieldByName(name).IsNil() {
			t.Fatal("later owner constructed", name)
		}
	}
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/nodes/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	portal := httpfixture.New(t, server, "portal", schemas)
	portal.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	var workspace string
	if e := s.DB.QueryRow(context.Background(), "SELECT default_workspace_id::text FROM installation_state WHERE id=1").Scan(&workspace); e != nil {
		t.Fatal(e)
	}
	join := admin.Write("POST", "/api/v1/admin/node-join-tokens", map[string]any{"workspace_id": workspace, "node_name": "Synthetic component HTTP node", "execution_profile": wire.SystemdComponentsProfile}, 201, "MintedTokenResponse")
	h := wire.Handshake{ProtocolVersion: wire.ManagementVersion, ExecutionProfile: wire.SystemdComponentsProfile, Capabilities: wire.Capabilities(wire.SystemdComponentsProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-component-http", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	machine := httpfixture.New(t, server, "admin", schemas)
	enrolled := machine.Call("POST", "/agent/v1/enroll", wire.Enrollment{JoinToken: join["token"].(string), Handshake: h}, 201, "EnrolledResponse")
	id := enrolled["node_id"].(string)
	credential := enrolled["credential"].(string)
	machine.Headers = map[string]string{"Authorization": "Bearer " + credential}
	base := "/agent/v1/sites/default/cells/default/nodes/" + id
	path := "/api/v1/admin/nodes/" + id + "/components/node-exporter"
	admin.Call("GET", path, nil, 200, "ComponentStateResponse")
	admin.Call("GET", "/api/v1/alias/nodes/"+id+"/components/node-exporter", nil, 404, "Problem")
	portal.Call("GET", "/api/v1/workspaces/"+workspace+"/nodes/"+id+"/components/node-exporter", nil, 200, "ComponentStateResponse")
	portal.Call("GET", "/api/v1/workspaces/"+uuid.NewString()+"/nodes/"+id+"/components/node-exporter", nil, 404, "Problem")
	portal.Write("POST", "/api/v1/workspaces/"+workspace+"/nodes/"+id+"/components/node-exporter/actions", map[string]any{"action": "install", "reason": "Denied"}, 404, "Problem")

	// Positive component/Workspace/node controls above, then exact closed descendants.
	for _, tail := range []string{"components/node-exporter/logs", "components/node-exporter/actions/extra", "components/other", "inference", "model-files", "task-deliveries", "training", "model-preparations"} {
		admin.Call("GET", "/api/v1/admin/nodes/"+id+"/"+tail, nil, 404, "Problem")
		portal.Call("GET", "/api/v1/workspaces/"+workspace+"/nodes/"+id+"/"+tail, nil, 404, "Problem")
		admin.Call("GET", "/api/v1/admin/workspaces/"+workspace+"/nodes/"+id+"/"+tail, nil, 404, "Problem")
	}
	for _, tail := range []string{"models", "datasets", "deployments", "training", "knowledge-bases", "workflows", "plugins", "middleware", "aws", "private-clusters"} {
		portal.Call("GET", "/api/v1/workspaces/"+workspace+"/"+tail, nil, 404, "Problem")
		admin.Call("GET", "/api/v1/admin/workspaces/"+workspace+"/"+tail, nil, 404, "Problem")
	}
	for _, tail := range []string{"model-files", "training-input-files", "model-preparation-input-files", "logs"} {
		machine.Call("POST", base+"/tasks/"+uuid.NewString()+"/"+tail, map[string]any{}, 404, "Problem")
	}
	for _, profile := range []string{wire.SystemdMaintenanceProfile, wire.DockerInferenceProfile, wire.DockerAIProfile, wire.DockerModelLifecycleProfile} {
		admin.Write("POST", "/api/v1/admin/node-join-tokens", map[string]any{"workspace_id": workspace, "node_name": "Refused inactive profile", "execution_profile": profile}, 403, "Problem")
	}
	for _, action := range []string{"configure", "restart", "upgrade", "rollback", "logs"} {
		admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
		admin.Write("POST", path+"/actions", map[string]any{"action": action, "reason": "Refused M05"}, 400, "Problem")
	}
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	task := admin.Write("POST", path+"/actions", map[string]any{"action": "install", "reason": "Synthetic HTTP artifact validation"}, 202, "TaskResponse")
	lease := machine.Call("POST", base+"/tasks/claims", map[string]any{}, 200, "ClaimResponse")["lease"].(map[string]any)
	taskBase := base + "/tasks/" + task["id"].(string)
	machine.Call("POST", taskBase+"/artifact", wire.LeaseRenewal{LeaseToken: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}, 409, "Problem")
	machine.Call("POST", taskBase+"/renewals", wire.LeaseRenewal{LeaseToken: lease["lease_token"].(string)}, 200, "RenewedLeaseResponse")
	body, _ := json.Marshal(wire.LeaseRenewal{LeaseToken: lease["lease_token"].(string)})
	request, _ := http.NewRequest("POST", httpfixture.AdminOrigin+taskBase+"/artifact", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+credential)
	response, e := server.Client().Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	hash := sha256.New()
	count, e := io.Copy(hash, io.LimitReader(response.Body, wire.NodeExporterArchiveBytes+1))
	if e != nil || response.StatusCode != 200 || count != wire.NodeExporterArchiveBytes || response.ContentLength != count || hex.EncodeToString(hash.Sum(nil)) != wire.NodeExporterSHA256 {
		t.Fatal("approved artifact transfer failed", e)
	}

	machine.Call("POST", "/agent/v1/sites/default/cells/default/nodes/"+uuid.NewString()+"/tasks/"+task["id"].(string)+"/artifact", wire.LeaseRenewal{LeaseToken: lease["lease_token"].(string)}, 401, "Problem")
	o := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "unknown", ObservedAt: time.Now().UTC(), ErrorCode: "COMPONENT_SYSTEMD_UNAVAILABLE"}
	machine.Call("POST", taskBase+"/results", wire.Result{LeaseToken: lease["lease_token"].(string), Outcome: "failure", ErrorCode: o.ErrorCode, Component: &o}, 200, "CompletedResponse")
	machine.Call("POST", taskBase+"/artifact", wire.LeaseRenewal{LeaseToken: lease["lease_token"].(string)}, 409, "Problem")
	h.Component = &o
	machine.Call("POST", base+"/heartbeat", h, 200, "DispositionResponse")
	view := admin.Call("GET", path, nil, 200, "ComponentStateResponse")
	if view["observation"].(map[string]any)["error_code"] != o.ErrorCode {
		t.Fatal("failure not visible through real API")
	}
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	admin.Write("POST", path+"/actions", map[string]any{"action": "shell", "reason": "Denied", "command": "untrusted"}, 400, "Problem")
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	next := admin.Write("POST", path+"/actions", map[string]any{"action": "install", "reason": "Synthetic expired transfer"}, 202, "TaskResponse")
	nextLease := machine.Call("POST", base+"/tasks/claims", map[string]any{}, 200, "ClaimResponse")["lease"].(map[string]any)
	nextBase := base + "/tasks/" + next["id"].(string)
	if _, e = s.DB.Exec(t.Context(), "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", next["id"].(string)); e != nil {
		t.Fatal(e)
	}
	machine.Call("POST", nextBase+"/artifact", wire.LeaseRenewal{LeaseToken: nextLease["lease_token"].(string)}, 409, "Problem")
	machine.Call("POST", nextBase+"/renewals", wire.LeaseRenewal{LeaseToken: nextLease["lease_token"].(string)}, 409, "Problem")
	current := admin.Call("GET", "/api/v1/admin/nodes/"+id, nil, 200, "NodeResponse")
	current = admin.Write("PATCH", "/api/v1/admin/nodes/"+id, map[string]any{"name": current["name"], "status": "disabled", "version": current["version"], "reason": "Synthetic transfer cancellation"}, 200, "NodeResponse")
	machine.Call("POST", nextBase+"/artifact", wire.LeaseRenewal{LeaseToken: nextLease["lease_token"].(string)}, 403, "Problem")
	admin.Write("PATCH", "/api/v1/admin/nodes/"+id, map[string]any{"name": current["name"], "status": "removed", "version": current["version"], "reason": "Synthetic credential revocation"}, 200, "NodeResponse")
	machine.Call("POST", nextBase+"/artifact", wire.LeaseRenewal{LeaseToken: nextLease["lease_token"].(string)}, 401, "Problem")

}

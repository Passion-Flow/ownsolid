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
	"testing"
	"time"
)

func TestM05StrictHTTPMaintenanceAndVersionBoundArtifacts(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	s.Config.Components.NodeExporterCatalogDirectory = filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/components/node-exporter")
	server := httpfixture.NewTLSServer(t, transport.New(s))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/components/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	portal := httpfixture.New(t, server, "portal", schemas)
	portal.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	var workspace string
	if e := s.DB.QueryRow(context.Background(), "SELECT default_workspace_id::text FROM installation_state WHERE id=1").Scan(&workspace); e != nil {
		t.Fatal(e)
	}
	join := admin.Write("POST", "/api/v1/admin/node-join-tokens", map[string]any{"workspace_id": workspace, "node_name": "Synthetic maintenance HTTP", "execution_profile": wire.SystemdMaintenanceProfile}, 201, "MintedTokenResponse")
	h := wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: wire.Capabilities(wire.SystemdMaintenanceProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-maintenance-http", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	machine := httpfixture.New(t, server, "admin", schemas)
	node := machine.Call("POST", "/agent/v1/enroll", wire.Enrollment{JoinToken: join["token"].(string), Handshake: h}, 201, "EnrolledResponse")
	id := node["node_id"].(string)
	credential := node["credential"].(string)
	machine.Headers = map[string]string{"Authorization": "Bearer " + credential}
	base := "/agent/v1/nodes/" + id
	path := "/api/v1/admin/nodes/" + id + "/components/node-exporter"
	configuration := wire.DefaultComponentConfig()
	// Machine observations below are explicit wire-contract fixtures, not real service evidence.
	observation := func(version string) wire.ComponentObservation {
		return wire.ComponentObservation{Component: wire.NodeExporter, Version: version, State: "running", Managed: true, Config: &configuration, ObservedAt: time.Now().UTC(), Metrics: &wire.ComponentMetrics{CPUSecondsTotal: 100, CPUIdleSecondsTotal: 80, MemoryTotalBytes: 1024, MemoryAvailableBytes: 512, FilesystemTotalBytes: 4096, FilesystemAvailableBytes: 2048}}
	}
	requestTask := func(input map[string]any) (map[string]any, map[string]any) {
		admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
		task := admin.Write("POST", path+"/actions", input, 202, "TaskResponse")
		lease := machine.Call("POST", base+"/tasks/claims", map[string]any{}, 200, "ClaimResponse")["lease"].(map[string]any)
		return task, lease
	}
	complete := func(task, lease map[string]any, o wire.ComponentObservation, logs *wire.ComponentLogs) {
		result := wire.Result{LeaseToken: lease["lease_token"].(string), Outcome: "success", Component: &o, Logs: logs}
		if o.ErrorCode != "" {
			result.Outcome = "failure"
			result.ErrorCode = o.ErrorCode
		}
		machine.Call("POST", base+"/tasks/"+task["id"].(string)+"/results", result, 200, "CompletedResponse")
	}
	download := func(task, lease map[string]any, version string) {
		recipe, ok := wire.RecipeForVersion(version)
		if !ok {
			t.Fatal("recipe missing")
		}
		body, _ := json.Marshal(wire.LeaseRenewal{LeaseToken: lease["lease_token"].(string)})
		request, _ := http.NewRequest("POST", httpfixture.AdminOrigin+base+"/tasks/"+task["id"].(string)+"/artifact", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+credential)
		response, e := server.Client().Do(request)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		hash := sha256.New()
		count, e := io.Copy(hash, io.LimitReader(response.Body, recipe.ArchiveBytes+1))
		if e != nil || response.StatusCode != 200 || count != recipe.ArchiveBytes || hex.EncodeToString(hash.Sum(nil)) != recipe.ArchiveSHA256 || response.Header.Get("X-Artifact-SHA256") != recipe.ArchiveSHA256 {
			t.Fatal("fenced task served another approved version", e)
		}
	}
	task, lease := requestTask(map[string]any{"action": "install", "version": "1.12.0", "reason": "Synthetic older baseline", "config": configuration})
	download(task, lease, "1.12.0")
	complete(task, lease, observation("1.12.0"), nil)
	configuration = wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	task, lease = requestTask(map[string]any{"action": "configure", "reason": "Synthetic bounded configuration", "config": configuration})
	o := observation("1.12.0")
	o.PreviousVersion = "1.12.0"
	complete(task, lease, o, nil)
	task, lease = requestTask(map[string]any{"action": "logs", "reason": "Synthetic scoped journal"})
	now := time.Now().UTC()
	logs := wire.ComponentLogs{Source: "systemd_journal", Available: true, ObservedAt: now, Since: now.Add(-time.Minute), Entries: []wire.ComponentLogEntry{{Timestamp: now, Priority: 6, Message: "SYNTHETIC_HTTP_LOG <script>text</script>"}}}
	complete(task, lease, o, &logs)
	admin.Call("GET", path+"/logs", nil, 200, "LogSnapshotResponse")
	portal.Call("GET", "/api/v1/workspaces/"+workspace+"/nodes/"+id+"/components/node-exporter/logs", nil, 404, "Problem")
	task, lease = requestTask(map[string]any{"action": "upgrade", "version": "1.12.1", "reason": "Synthetic failed upgrade"})
	download(task, lease, "1.12.1")
	o.ErrorCode = "COMPONENT_UPDATE_ROLLED_BACK"
	o.MaintenanceError = o.ErrorCode
	o.MaintenanceCause = "COMPONENT_SERVICE_FAILED"
	complete(task, lease, o, nil)
	view := admin.Call("GET", path, nil, 200, "ComponentStateResponse")
	if view["version"] != "1.12.0" || view["desired_version"] != "1.12.1" {
		t.Fatal("rollback was hidden as upgrade success")
	}
	task, lease = requestTask(map[string]any{"action": "upgrade", "version": "1.12.1", "reason": "Synthetic repaired upgrade"})
	o = observation("1.12.1")
	o.PreviousVersion = "1.12.0"
	complete(task, lease, o, nil)
	task, lease = requestTask(map[string]any{"action": "rollback", "version": "1.12.0", "reason": "Synthetic explicit rollback"})
	o = observation("1.12.0")
	o.PreviousVersion = "1.12.1"
	complete(task, lease, o, nil)
	view = admin.Call("GET", path, nil, 200, "ComponentStateResponse")
	if view["desired_version"] != "1.12.0" {
		t.Fatal("rollback intent did not reconcile")
	}
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	admin.Write("POST", path+"/actions", map[string]any{"action": "logs", "reason": "Denied arbitrary unit", "unit": "unowned.service"}, 400, "Problem")
	admin.Write("POST", path+"/actions", map[string]any{"action": "configure", "reason": "Denied config", "config": map[string]any{"log_level": "info", "max_scrape_requests": 0}}, 400, "Problem")
}

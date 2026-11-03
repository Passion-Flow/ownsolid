package http_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// Agent facts/results here are explicit synthetic protocol fixtures. The TLS,
// License signature/state, sessions, CSRF, commands, leases, SQL and audits are real.
func TestM19ExpiredLicenseComponentRecoveryAndAcceptedAgentWork(t *testing.T) {
	owner := fixture.New(t)
	password := fixture.Bootstrap(t, owner)
	ctx := context.Background()
	owner.Config.Components.NodeExporterArchiveFile = filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/components/node-exporter", wire.NodeExporterVersion, "linux-amd64", "node_exporter-"+wire.NodeExporterVersion+".linux-amd64.tar.gz")
	handler := transport.New(owner)
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/nodes/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	machine := httpfixture.New(t, server, "admin", schemas)
	for _, client := range []*httpfixture.Client{admin, portal} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	}
	var workspace string
	if e := owner.DB.QueryRow(ctx, "SELECT default_workspace_id::text FROM installation_state WHERE id=1").Scan(&workspace); e != nil {
		t.Fatal(e)
	}
	join := admin.Write("POST", "/api/v1/admin/node-join-tokens", map[string]any{"workspace_id": workspace, "node_name": "Synthetic expired recovery node", "execution_profile": wire.SystemdComponentsProfile}, 201, "MintedTokenResponse")
	facts := wire.Handshake{ProtocolVersion: wire.ManagementVersion, ExecutionProfile: wire.SystemdComponentsProfile, Capabilities: wire.Capabilities(wire.SystemdComponentsProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-expired-component", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	enrolled := machine.Call("POST", "/agent/v1/enroll", wire.Enrollment{JoinToken: join["token"].(string), Handshake: facts}, 201, "EnrolledResponse")
	id := enrolled["node_id"].(string)
	machine.Headers = map[string]string{"Authorization": "Bearer " + enrolled["credential"].(string)}
	base := "/agent/v1/nodes/" + id
	path := "/api/v1/admin/nodes/" + id + "/components/node-exporter"
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	accepted := admin.Write("POST", path+"/actions", map[string]any{"action": "install", "reason": "Synthetic previously accepted component task"}, 202, "TaskResponse")
	trust, e := owner.Config.LicenseTrust()
	if e != nil {
		t.Fatal(e)
	}
	expired, e := licensing.New(owner, trust, owner.Config.LicensePolicy(), func() time.Time { return time.Now().UTC().Add(26 * time.Hour) })
	if e != nil {
		t.Fatal(e)
	}
	owner.BusinessAdmission = expired
	lease := machine.Call("POST", base+"/tasks/claims", map[string]any{}, 200, "ClaimResponse")["lease"].(map[string]any)
	if lease["task_id"] != accepted["id"] {
		t.Fatal("previously accepted task did not continue")
	}
	task := base + "/tasks/" + accepted["id"].(string)
	machine.Call("POST", task+"/renewals", wire.LeaseRenewal{LeaseToken: lease["lease_token"].(string)}, 200, "RenewedLeaseResponse")
	observation := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "unknown", ObservedAt: time.Now().UTC(), ErrorCode: "COMPONENT_SYSTEMD_UNAVAILABLE"}
	machine.Call("POST", task+"/results", wire.Result{LeaseToken: lease["lease_token"].(string), Outcome: "failure", ErrorCode: observation.ErrorCode, Component: &observation}, 200, "CompletedResponse")
	for _, action := range []string{"install", "start", "configure"} {
		admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
		problem := admin.Write("POST", path+"/actions", map[string]any{"action": action, "reason": "Synthetic denied new work"}, 403, "Problem")
		if problem["code"] != "LICENSE_NEW_BUSINESS_UNAVAILABLE" {
			t.Fatal("new work denial changed")
		}
	}
	var before int
	if e = owner.DB.QueryRow(ctx, "SELECT count(*) FROM node_tasks").Scan(&before); e != nil || before != 1 {
		t.Fatal("denied task persisted", e, before)
	}
	for _, action := range []string{"stop", "remove"} {
		admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
		next := admin.Write("POST", path+"/actions", map[string]any{"action": action, "reason": "Synthetic expired safe recovery"}, 202, "TaskResponse")
		lease = machine.Call("POST", base+"/tasks/claims", map[string]any{}, 200, "ClaimResponse")["lease"].(map[string]any)
		if lease["task_id"] != next["id"] {
			t.Fatal("wrong recovery task lease")
		}
		observation = wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "stopped", Managed: true, ObservedAt: time.Now().UTC()}
		if action == "remove" {
			observation.State = "not_installed"
			observation.Managed = false
		}
		machine.Call("POST", base+"/tasks/"+next["id"].(string)+"/results", wire.Result{LeaseToken: lease["lease_token"].(string), Outcome: "success", Component: &observation}, 200, "CompletedResponse")
	}
	final := admin.Call("GET", path, nil, 200, "ComponentStateResponse")
	if final["desired_state"] != "not_installed" {
		t.Fatal("recovery state not persisted")
	}
	portal.Write("POST", "/api/v1/workspaces/"+workspace+"/nodes/"+id+"/components/node-exporter/actions", map[string]any{"action": "stop", "reason": "Forbidden ordinary recovery mutation"}, 403, "Problem")
	admin.CSRF = ""
	admin.Call("POST", path+"/actions", map[string]any{"action": "stop", "reason": "No CSRF"}, 403, "Problem")
	admin.Write("POST", path+"/actions", map[string]any{"action": "stop", "reason": "Strict recovery input", "command": "unapproved"}, 400, "Problem")
	var count int
	if e = owner.DB.QueryRow(ctx, "SELECT count(*) FROM node_tasks WHERE state IN ('succeeded','failed')").Scan(&count); e != nil || count != 3 {
		t.Fatal("recovery command/result integrity", e, count)
	}
}

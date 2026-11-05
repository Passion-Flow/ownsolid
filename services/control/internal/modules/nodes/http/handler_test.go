package http_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestM03HTTPContractAndHumanMachineSeparation(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	server := httpfixture.NewTLSServer(t, transport.New(s))
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
	tokens := "/api/v1/admin/node-join-tokens"
	token := admin.Write("POST", tokens, map[string]any{"workspace_id": workspace, "node_name": "HTTP node"}, 201, "MintedTokenResponse")
	list := admin.Call("GET", tokens, nil, 200, "JoinTokenListResponse")
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), token["token"].(string)) {
		t.Fatal("join token leaked in list")
	}
	f := wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-http-node", LogicalCPUs: 2, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 2048, DiskAvailableBytes: 1024, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
	h := wire.Handshake{ProtocolVersion: wire.Version, Capabilities: []string{wire.CollectFacts}, Facts: f}
	machine := httpfixture.New(t, server, "admin", schemas)
	enrolled := machine.Call("POST", "/agent/v1/enroll", wire.Enrollment{JoinToken: token["token"].(string), Handshake: h}, 201, "EnrolledResponse")
	id := enrolled["node_id"].(string)
	credential := enrolled["credential"].(string)
	machine.Headers = map[string]string{"Authorization": "Bearer " + credential}
	admin.Call("GET", "/api/v1/admin/nodes", nil, 200, "NodeListResponse")
	node := admin.Call("GET", "/api/v1/admin/nodes/"+id, nil, 200, "NodeResponse")
	base := "/agent/v1/sites/default/cells/default/nodes/" + id
	machine.Call("POST", base+"/heartbeat", h, 200, "DispositionResponse")
	admin.Call("POST", base+"/heartbeat", h, 401, "Problem")
	portal.Call("POST", base+"/heartbeat", h, 404, "Problem")
	machine.Call("GET", "/api/v1/admin/nodes", nil, 401, "Problem")
	wrong := httpfixture.New(t, server, "admin", schemas)
	wrong.Headers = map[string]string{"Authorization": "Bearer " + credential}
	wrong.Call("POST", "/agent/v1/sites/default/cells/default/nodes/"+uuid.NewString()+"/heartbeat", h, 401, "Problem")
	portalPath := "/api/v1/workspaces/" + workspace + "/nodes"
	portal.Call("GET", portalPath, nil, 200, "NodeListResponse")
	portal.Call("GET", portalPath+"/"+id, nil, 200, "NodeResponse")
	portal.Call("GET", portalPath+"/"+id+"/tasks", nil, 200, "TaskListResponse")
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	task := admin.Write("POST", "/api/v1/admin/nodes/"+id+"/tasks", map[string]any{"operation": wire.CollectFacts}, 202, "TaskResponse")
	claim := machine.Call("POST", base+"/tasks/claims", map[string]any{}, 200, "ClaimResponse")
	lease := claim["lease"].(map[string]any)
	machine.Call("POST", base+"/tasks/"+task["id"].(string)+"/results", wire.Result{LeaseToken: lease["lease_token"].(string), Outcome: "success", Facts: &f}, 200, "CompletedResponse")
	machine.Call("POST", base+"/tasks/claims", map[string]any{}, 200, "ClaimResponse")
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	failedTask := admin.Write("POST", "/api/v1/admin/nodes/"+id+"/tasks", map[string]any{"operation": wire.CollectFacts}, 202, "TaskResponse")
	failedLease := machine.Call("POST", base+"/tasks/claims", map[string]any{}, 200, "ClaimResponse")["lease"].(map[string]any)
	machine.Call("POST", base+"/tasks/"+failedTask["id"].(string)+"/results", wire.Result{LeaseToken: failedLease["lease_token"].(string), Outcome: "failure", ErrorCode: "DISCOVERY_FAILED"}, 200, "CompletedResponse")
	admin.Call("GET", "/api/v1/admin/nodes/"+id+"/tasks", nil, 200, "TaskListResponse")
	admin.Write("PATCH", "/api/v1/admin/nodes/"+id, map[string]any{"name": "HTTP renamed", "status": "disabled", "version": node["version"], "reason": "Synthetic disable"}, 200, "NodeResponse")
	machine.Call("POST", base+"/heartbeat", h, 200, "DispositionResponse")
	machine.Call("POST", base+"/tasks/claims", map[string]any{}, 403, "Problem")
	// Agent pacing belongs to platform administrators; a change needs the current version
	// and a reason, and may not mark healthy nodes offline.
	pacingPath := "/api/v1/admin/agent-pacing"
	pacing := admin.Call("GET", pacingPath, nil, 200, "AgentPacingResponse")
	machine.Call("GET", pacingPath, nil, 401, "Problem")
	portal.Call("GET", pacingPath, nil, 404, "Problem")
	portal.Write("PUT", pacingPath, map[string]any{"heartbeat_seconds": 15, "jitter_percent": 10, "reconnect_base_seconds": 2, "reconnect_max_seconds": 120, "version": pacing["version"], "reason": "Portal attempt"}, 404, "Problem")
	admin.Write("PUT", pacingPath, map[string]any{"heartbeat_seconds": 120, "jitter_percent": 10, "reconnect_base_seconds": 2, "reconnect_max_seconds": 120, "version": pacing["version"], "reason": "Too slow"}, 409, "Problem")
	admin.Write("PUT", pacingPath, map[string]any{"heartbeat_seconds": 1, "jitter_percent": 10, "reconnect_base_seconds": 2, "reconnect_max_seconds": 120, "version": pacing["version"], "reason": "Too fast"}, 400, "Problem")
	changed := admin.Write("PUT", pacingPath, map[string]any{"heartbeat_seconds": 15, "jitter_percent": 10, "reconnect_base_seconds": 2, "reconnect_max_seconds": 120, "version": pacing["version"], "reason": "Reducing heartbeat load"}, 200, "AgentPacingResponse")
	if changed["heartbeat_seconds"].(float64) != 15 || changed["version"].(float64) != pacing["version"].(float64)+1 {
		t.Fatal("pacing update", changed)
	}
	admin.Write("PUT", pacingPath, map[string]any{"heartbeat_seconds": 16, "jitter_percent": 10, "reconnect_base_seconds": 2, "reconnect_max_seconds": 120, "version": pacing["version"], "reason": "Stale version"}, 409, "Problem")
	admin.Write("PUT", pacingPath, map[string]any{"heartbeat_seconds": 15, "unknown": true, "version": changed["version"], "reason": "Unknown field"}, 400, "Problem")
	if told := machine.Call("POST", base+"/heartbeat", h, 200, "DispositionResponse"); told["pacing"].(map[string]any)["heartbeat_seconds"].(float64) != 15 {
		t.Fatal("the next heartbeat reply did not carry the change", told)
	}
	h.ProtocolVersion = "999"
	machine.Call("POST", base+"/heartbeat", h, 400, "Problem")
	h.ProtocolVersion = wire.Version
	machine.Call("POST", base+"/heartbeat?tenant_id="+uuid.NewString(), h, 400, "Problem")
	machine.Call("POST", base+"/heartbeat", []byte(`{"protocol_version":"1.0","protocol_version":"2.0"}`), 400, "Problem")
	revoke := admin.Write("POST", tokens, map[string]any{"workspace_id": workspace, "node_name": "Revoke node"}, 201, "MintedTokenResponse")
	admin.Write("POST", tokens+"/"+revoke["id"].(string)+"/revocations", map[string]any{"reason": "Synthetic revoke"}, 200, "JoinTokenResponse")
	machine.Call("POST", "/agent/v1/enroll", wire.Enrollment{JoinToken: revoke["token"].(string), Handshake: h}, 400, "Problem")
}

func TestM03UntrustedCredentialCannotPoisonNodeRateBucket(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	ctx := context.Background()
	admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	handler := transport.New(s)
	join, e := handler.Nodes.Service.Mint(ctx, admin, admin.Me.Workspace.ID, "Isolated rate node", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	h := wire.Handshake{ProtocolVersion: wire.Version, Capabilities: []string{wire.CollectFacts}, Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-rate-node", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 1024, DiskAvailableBytes: 512, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	node, e := handler.Nodes.Service.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/nodes/openapi.json")
	attacker := httpfixture.New(t, server, "admin", schemas)
	attacker.Headers = map[string]string{"Authorization": "Bearer " + security.Token()}
	path := "/agent/v1/sites/default/cells/default/nodes/" + node.NodeID + "/heartbeat"
	// The attacker spends the allowance of its own credential, not the node's (ADR 0018).
	for i := 0; i < s.Config.Policy().AgentAdmission.Calls.Heartbeat.Burst; i++ {
		attacker.Call("POST", path, h, 401, "Problem")
	}
	attacker.Call("POST", path, h, 429, "Problem")
	legitimate := httpfixture.New(t, server, "admin", schemas)
	legitimate.Headers = map[string]string{"Authorization": "Bearer " + node.Credential}
	legitimate.Call("POST", path, h, 200, "DispositionResponse")
}

func TestM03HTTPStorageAndAuditRefuseWithoutSuccess(t *testing.T) {
	testMinimalHTTPStorageAndAudit(t, false)
}
func TestM04HTTPStorageAndAuditRefuseWithoutSuccess(t *testing.T) {
	testMinimalHTTPStorageAndAudit(t, true)
}
func TestM05HTTPStorageAndAuditRefuseWithoutSuccess(t *testing.T) {
	testMinimalHTTPStorageAndAudit(t, true, true)
}
func testMinimalHTTPStorageAndAudit(t *testing.T, components bool, maintenance ...bool) {
	var s *identity.Service
	if len(maintenance) > 0 && maintenance[0] {
		s = fixture.New(t)
	} else if components {
		s = fixture.New(t)
	} else {
		s = fixture.New(t)
	}
	password := fixture.Bootstrap(t, s)
	handler := transport.New(s)
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/nodes/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	auth := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	ctx := t.Context()
	token, e := handler.Nodes.Service.Mint(ctx, auth, auth.Me.Workspace.ID, "Synthetic storage node", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	handshake := wire.Handshake{ProtocolVersion: wire.Version, Capabilities: []string{wire.CollectFacts}, Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-fault", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 2048, DiskAvailableBytes: 1024, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	enrolled, e := handler.Nodes.Service.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: handshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	task, e := handler.Nodes.Service.CreateTask(ctx, auth, enrolled.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	machine := httpfixture.New(t, server, "admin", schemas)
	machine.Headers = map[string]string{"Authorization": "Bearer " + enrolled.Credential}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION reject_m03_http_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic audit fault'; END$$; CREATE TRIGGER reject_m03_http_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_m03_http_audit()`); e != nil {
		t.Fatal(e)
	}
	admin.Write("POST", "/api/v1/admin/node-join-tokens", map[string]any{"workspace_id": auth.Me.Workspace.ID, "node_name": "Must not persist"}, 503, "Problem")
	machine.Call("POST", "/agent/v1/sites/default/cells/default/nodes/"+enrolled.NodeID+"/tasks/claims", map[string]any{}, 503, "Problem")
	var state string
	var attempt int
	if e = s.DB.QueryRow(ctx, "SELECT state,attempt FROM node_tasks WHERE id=$1", task.ID).Scan(&state, &attempt); e != nil || state != "pending" || attempt != 0 {
		t.Fatal("failed HTTP claim persisted", e)
	}
	var count int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM node_join_tokens").Scan(&count); e != nil || count != 1 {
		t.Fatal("failed HTTP mint persisted", e)
	}
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER reject_m03_http_audit ON audit_events"); e != nil {
		t.Fatal(e)
	}
	machine.Call("POST", "/agent/v1/sites/default/cells/default/nodes/"+enrolled.NodeID+"/tasks/claims", map[string]any{}, 200, "ClaimResponse")
	if _, e = s.DB.Exec(ctx, "ALTER TABLE managed_nodes RENAME TO unavailable_m03_nodes"); e != nil {
		t.Fatal(e)
	}
	renamed := true
	t.Cleanup(func() {
		if renamed {
			if _, e := s.DB.Exec(context.Background(), "ALTER TABLE unavailable_m03_nodes RENAME TO managed_nodes"); e != nil {
				t.Error(e)
			}
		}
	})
	admin.Call("GET", "/api/v1/admin/nodes", nil, 503, "Problem")
	machine.Call("POST", "/agent/v1/sites/default/cells/default/nodes/"+enrolled.NodeID+"/heartbeat", handshake, 503, "Problem")
	if _, e = s.DB.Exec(ctx, "ALTER TABLE unavailable_m03_nodes RENAME TO managed_nodes"); e != nil {
		t.Fatal(e)
	}
	renamed = false
	admin.Call("GET", "/api/v1/admin/nodes", nil, 200, "NodeListResponse")
	t.Log("real HTTP audit rollback and unavailable SQL return structured503, no false success; explicit recovery works")
}

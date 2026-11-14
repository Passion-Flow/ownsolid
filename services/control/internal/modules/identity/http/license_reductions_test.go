package http_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// Actual TLS/auth/License/SQL/CSRF/token/session/audit behavior. Agent facts are
// explicitly synthetic; no machine or customer environment is enrolled here.
func TestM19ExpiredLicenseIdentityReductionsHTTP(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second).Add(2 * time.Second)
	clock := &licenseClock{value: base}
	owner, _, admin, portal := licenseClients(t, clock)
	ctx := context.Background()
	nodeSchemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/nodes/openapi.json")
	for name, schema := range nodeSchemas {
		if name == "Problem" || admin.schemas[name] == nil {
			admin.schemas[name] = schema
			portal.schemas[name] = schema
		}
	}
	claims := licenseClaims(t, owner, base)
	admin.mutate("POST", "/api/v1/admin/license", licenseInput(t, owner, claims, ""), 200, "")
	me := admin.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	workspace := me["workspace"].(map[string]any)["id"].(string)
	primary := me["user"].(map[string]any)
	email := "synthetic-disabled-license@example.invalid"
	invitation := admin.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": email, "locale": "en"}, 201, "InvitationResponse")
	password := fixture.Password()
	if e := owner.Register(ctx, fixture.InvitationToken(t, owner, invitation["id"].(string)), "Synthetic reduction account", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	member := newClient(t, admin.s, "portal", admin.schemas)
	login := member.mutate("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": password}, 200, "LoginResponse")
	user := login["user"].(map[string]any)
	mint := func(name string) map[string]any {
		return admin.mutate("POST", "/api/v1/admin/node-join-tokens", map[string]any{"workspace_id": workspace, "node_name": name, "execution_profile": agentwire.ReadOnlyProfile}, 201, "MintedTokenResponse")
	}
	revoked, accepted := mint("Synthetic revoked node intent"), mint("Synthetic accepted node intent")
	clock.set(base.Add(121 * time.Second))
	denied := admin.mutate("POST", "/api/v1/admin/node-join-tokens", map[string]any{"workspace_id": workspace, "node_name": "Denied new node intent", "execution_profile": agentwire.ReadOnlyProfile}, 403, "Problem")
	if denied["code"] != "LICENSE_NEW_BUSINESS_UNAVAILABLE" {
		t.Fatal("new enrollment intent bypassed License")
	}
	revokePath := "/api/v1/admin/node-join-tokens/" + revoked["id"].(string) + "/revocations"
	admin.csrf = ""
	admin.request("POST", revokePath, map[string]any{"reason": "No CSRF"}, 403, "Problem", nil)
	admin.mutate("POST", revokePath, map[string]any{"reason": "Strict token reduction", "workspace_id": uuid.NewString()}, 400, "Problem")
	reduction := admin.mutate("POST", revokePath, map[string]any{"reason": "Synthetic expired join token revocation"}, 200, "JoinTokenResponse")
	if reduction["status"] != "revoked" {
		t.Fatal("revocation not durable")
	}
	repeated := admin.mutate("POST", revokePath, map[string]any{"reason": "Original token terminal conflict remains"}, 409, "Problem")
	if repeated["code"] != "NODE_JOIN_STATE_CONFLICT" {
		t.Fatal("terminal join semantics changed")
	}
	machine := httpfixture.New(t, admin.s, "admin", nodeSchemas)
	facts := agentwire.Handshake{ProtocolVersion: agentwire.Version, ExecutionProfile: agentwire.ReadOnlyProfile, Capabilities: agentwire.Capabilities(agentwire.ReadOnlyProfile), Facts: agentwire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-license-identity", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: agentwire.AgentVersion}}
	unusable := machine.Call("POST", "/agent/v1/enroll", agentwire.Enrollment{JoinToken: revoked["token"].(string), Handshake: facts}, 400, "Problem")
	if unusable["code"] != "TOKEN_UNUSABLE" {
		t.Fatal("revoked join token accepted")
	}
	machine.Call("POST", "/agent/v1/enroll", agentwire.Enrollment{JoinToken: accepted["token"].(string), Handshake: facts}, 201, "EnrolledResponse")
	statusPath := "/api/v1/admin/users/" + user["id"].(string) + "/status"
	admin.mutate("PATCH", statusPath, map[string]any{"status": "disabled", "version": user["version"].(float64) + 1, "reason": "Original current version remains required"}, 409, "Problem")
	admin.csrf = ""
	admin.request("PATCH", statusPath, map[string]any{"status": "disabled", "version": user["version"], "reason": "No CSRF"}, 403, "Problem", nil)
	disabled := admin.mutate("PATCH", statusPath, map[string]any{"status": "disabled", "version": user["version"], "reason": "Synthetic expired existing user disable"}, 200, "UserResponse")
	if disabled["status"] != "disabled" {
		t.Fatal("user reduction not persisted")
	}
	member.request("GET", "/api/v1/me", nil, 401, "Problem", nil)
	enable := admin.mutate("PATCH", statusPath, map[string]any{"status": "active", "version": disabled["version"], "reason": "New account authority requires valid License"}, 403, "Problem")
	if enable["code"] != "LICENSE_NEW_BUSINESS_UNAVAILABLE" {
		t.Fatal("disabled account re-enabled after expiry")
	}
	admin.mutate("PATCH", "/api/v1/admin/users/"+primary["id"].(string)+"/status", map[string]any{"status": "disabled", "version": primary["version"], "reason": "Primary administrator remains protected"}, 409, "Problem")
	var tokens, nodes, audits int
	if e := owner.DB.QueryRow(ctx, "SELECT count(*) FROM node_join_tokens").Scan(&tokens); e != nil || tokens != 2 {
		t.Fatal("denied token minted authority", e, tokens)
	}
	if e := owner.DB.QueryRow(ctx, "SELECT count(*) FROM managed_nodes").Scan(&nodes); e != nil || nodes != 1 {
		t.Fatal("revoked token created node or accepted work lost", e, nodes)
	}
	for _, action := range []string{"node.join_token_revoked", "account.disabled"} {
		if e := owner.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action=$1", action).Scan(&audits); e != nil || audits != 1 {
			t.Fatal("reduction audit integrity", action, e, audits)
		}
	}
}

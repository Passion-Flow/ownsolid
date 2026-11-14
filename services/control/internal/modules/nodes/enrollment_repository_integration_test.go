//go:build repository_integration

package nodes_test

import (
	"bytes"
	"encoding/json"
	"net/url"
	"reflect"
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func nodeEnrollmentInput(profile string) wire.Handshake {
	return wire.Handshake{ProtocolVersion: wire.ProfileVersion(profile), ExecutionProfile: profile,
		Capabilities: wire.Capabilities(profile), Facts: facts()}
}

func nodeEnrollmentMint(t *testing.T, x nodeReadActual, workspace, name, profile string) nodes.MintedToken {
	t.Helper()
	value, e := x.s.MintForProfile(x.ctx, x.admin, workspace, name, profile, uuid.NewString())
	if e != nil {
		t.Fatal("actual enrollment join preparation", e)
	}
	return value
}

func nodeEnrollmentRefused(t *testing.T, x nodeReadActual, raw string, handshake wire.Handshake, code string) {
	t.Helper()
	before, audits := nodeReadStoredDigest(t, x), x.audits(t)
	value, e := x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: raw, Handshake: handshake}, uuid.NewString())
	nodeReadWant(t, e, code)
	if !reflect.DeepEqual(value, wire.Enrolled{}) || nodeReadStoredDigest(t, x) != before || x.audits(t) != audits {
		t.Fatal("refused actual enrollment exposed credential or changed persistence/audit")
	}
}

func nodeEnrollmentStored(t *testing.T, x nodeReadActual, token nodes.MintedToken, value wire.Enrolled, handshake wire.Handshake, request string) {
	t.Helper()
	if value.NodeID == "" || !security.ValidToken(value.Credential) || value.ProtocolVersion != handshake.ProtocolVersion || value.HeartbeatSeconds != wire.HeartbeatSeconds {
		t.Fatal("actual enrolled credential/protocol projection invalid")
	}
	node, e := x.s.Get(x.ctx, x.admin, "", value.NodeID)
	if e != nil || node.TenantID != x.f.Tenant || node.WorkspaceID != token.WorkspaceID || node.Name != token.NodeName ||
		node.ExecutionProfile != handshake.Profile() || node.ProtocolVersion != handshake.ProtocolVersion || node.Status != "active" ||
		node.Version != 1 || !reflect.DeepEqual(node.Facts, handshake.Facts) || !reflect.DeepEqual(node.Capabilities, handshake.Capabilities) {
		t.Fatal("actual persisted enrollment scope/handshake/profile mismatch", e)
	}
	var hash []byte
	var linked string
	var consumed bool
	if e = x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT credential_hash FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3`,
		`SELECT credential_hash FROM managed_nodes WHERE tenant_id=? AND workspace_id=? AND id=?`, x.f.Tenant, token.WorkspaceID, value.NodeID)).Scan(&hash); e != nil ||
		!bytes.Equal(hash, security.Digest("node-credential:v1:"+value.Credential)) {
		t.Fatal("actual enrollment credential was not purpose hashed", e)
	}
	if e = x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT node_id::text,consumed_at IS NOT NULL FROM node_join_tokens WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3`,
		`SELECT node_id,consumed_at IS NOT NULL FROM node_join_tokens WHERE id=? AND tenant_id=? AND workspace_id=?`, token.ID, x.f.Tenant, token.WorkspaceID)).Scan(&linked, &consumed); e != nil || linked != value.NodeID || !consumed {
		t.Fatal("actual one-use token/node linkage missing", e)
	}
	var count int
	if e = x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT count(*) FROM audit_events WHERE action='node.enrolled' AND actor_user_id IS NULL AND audience='system' AND outcome='success' AND tenant_id=$1 AND workspace_id=$2 AND target_type='node' AND target_id=$3 AND request_id=$4 AND reason IS NULL`,
		`SELECT count(*) FROM audit_events WHERE action='node.enrolled' AND actor_user_id IS NULL AND audience='system' AND outcome='success' AND tenant_id=? AND workspace_id=? AND target_type='node' AND target_id=? AND request_id=? AND reason IS NULL`, x.f.Tenant, token.WorkspaceID, value.NodeID, request)).Scan(&count); e != nil || count != 1 {
		t.Fatal("actual enrollment/system audit not atomic", e)
	}
	page, e := x.s.Tokens(x.ctx, x.admin, url.Values{"limit": {"100"}})
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal([]any{node, page})
	if e != nil || bytes.Contains(raw, []byte(value.Credential)) || bytes.Contains(raw, []byte(token.Token)) ||
		bytes.Contains(raw, []byte("credential_hash")) || bytes.Contains(raw, []byte("token_hash")) {
		t.Fatal("actual node/history projection exposed one-time input/credential/hash")
	}
}

func TestActualNodeEnrollmentLifecycle(t *testing.T) {
	t.Run("all_profiles_one_use_hash_scope_and_audit", func(t *testing.T) {
		x := newNodeReadActual(t)
		store, proxy := nodeReadFaultStore(t, x)
		owner := *x.owner
		owner.Storage, owner.DB = store, nil
		service := &nodes.Service{Identity: &owner, Workspaces: &workspaces.Service{Identity: &owner}}
		profiles := []string{wire.ReadOnlyProfile, wire.SystemdComponentsProfile, wire.SystemdMaintenanceProfile,
			wire.DockerInferenceProfile, wire.DockerAIProfile, wire.DockerModelLifecycleProfile}
		for _, profile := range profiles {
			token := nodeEnrollmentMint(t, x, x.f.Workspace, "Synthetic actual enrolled "+profile, profile)
			request, handshake := uuid.NewString(), nodeEnrollmentInput(profile)
			value, e := service.Enroll(x.ctx, wire.Enrollment{JoinToken: token.Token, Handshake: handshake}, request)
			if e != nil {
				t.Fatal("actual separate normal-forwarding enrollment", profile, e)
			}
			// Original pool is independent of the service's real forwarding connection.
			nodeEnrollmentStored(t, x, token, value, handshake, request)
			nodeEnrollmentRefused(t, x, token.Token, handshake, "TOKEN_UNUSABLE")
		}
		if int(proxy.Commits()) != len(profiles) {
			t.Fatal("enrollment retried a real COMMIT")
		}
	})
	t.Run("token_state_scope_and_current_authority", func(t *testing.T) {
		x := newNodeReadActual(t)
		handshake := nodeEnrollmentInput(wire.ReadOnlyProfile)
		for _, state := range []string{"revoked", "expired", "consumed", "workspace_disabled", "tenant_disabled", "foreign_scope", "unbootstrapped"} {
			token := nodeEnrollmentMint(t, x, x.f.Workspace, "Synthetic enrollment refusal "+state, wire.ReadOnlyProfile)
			code := "TOKEN_UNUSABLE"
			switch state {
			case "revoked":
				if _, e := x.s.RevokeToken(x.ctx, x.admin, token.ID, "Synthetic actual pre-enrollment revoke", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
			case "expired":
				x.exec(t, `UPDATE node_join_tokens SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`,
					`UPDATE node_join_tokens SET expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?`, token.ID)
			case "consumed":
				if _, e := x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: token.Token, Handshake: handshake}, uuid.NewString()); e != nil {
					t.Fatal(e)
				}
			case "workspace_disabled":
				x.exec(t, `UPDATE workspaces SET status='disabled',version=version+1 WHERE id=$1`, `UPDATE workspaces SET status='disabled',version=version+1 WHERE id=?`, x.f.Workspace)
				code = "ACCESS_DENIED"
			case "tenant_disabled":
				x.exec(t, `UPDATE tenants SET status='disabled' WHERE id=$1`, `UPDATE tenants SET status='disabled' WHERE id=?`, x.f.Tenant)
				code = "ACCESS_DENIED"
			case "foreign_scope":
				tenant, workspace := uuid.NewString(), uuid.NewString()
				x.exec(t, `INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign enrollment tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic foreign enrollment tenant','active')`, tenant)
				x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign enrollment Workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic foreign enrollment Workspace','active')`, workspace, tenant)
				token.Token = security.Token()
				x.exec(t, `INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at) VALUES($1,$2,$3,'Synthetic foreign enrollment',$4,$5,clock_timestamp()+interval '1 hour')`, `INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at) VALUES(?,?,?,'Synthetic foreign enrollment',?,?,TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)))`, uuid.NewString(), tenant, workspace, security.Digest("node-join:v1:"+token.Token), x.f.User)
			case "unbootstrapped":
				x.exec(t, `UPDATE installation_state SET bootstrap_complete=false,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=1`, `UPDATE installation_state SET bootstrap_complete=false,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=1`)
			}
			nodeEnrollmentRefused(t, x, token.Token, handshake, code)
			if state == "workspace_disabled" {
				x.exec(t, `UPDATE workspaces SET status='active',version=version+1 WHERE id=$1`, `UPDATE workspaces SET status='active',version=version+1 WHERE id=?`, x.f.Workspace)
			}
			if state == "tenant_disabled" {
				x.exec(t, `UPDATE tenants SET status='active' WHERE id=$1`, `UPDATE tenants SET status='active' WHERE id=?`, x.f.Tenant)
			}
		}
	})
	t.Run("nondefault_capacity_across_workspaces_and_live_names", func(t *testing.T) {
		x := newNodeReadActual(t)
		policy := config.DefaultRuntimePolicy()
		policy.Nodes.MaximumLiveNodes = 10
		x.owner.Config.Runtime = &policy
		workspace := uuid.NewString()
		x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic enrollment capacity Workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic enrollment capacity Workspace','active')`, workspace, x.f.Tenant)
		token := nodeEnrollmentMint(t, x, workspace, "Synthetic nondefault capacity enrollment", wire.ReadOnlyProfile)
		request, handshake := uuid.NewString(), nodeEnrollmentInput(wire.ReadOnlyProfile)
		value, e := x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: token.Token, Handshake: handshake}, request)
		if e != nil {
			t.Fatal(e)
		}
		nodeEnrollmentStored(t, x, token, value, handshake, request)
		pending := nodeEnrollmentMint(t, x, x.f.Workspace, "Synthetic capacity pending enrollment", wire.ReadOnlyProfile)
		nodeEnrollmentRefused(t, x, pending.Token, handshake, "RESOURCE_LIMIT_REACHED")
		x.exec(t, `UPDATE managed_nodes SET status='disabled',version=version+1 WHERE id=$1`, `UPDATE managed_nodes SET status='disabled',version=version+1 WHERE id=?`, value.NodeID)
		nodeEnrollmentRefused(t, x, pending.Token, handshake, "RESOURCE_LIMIT_REACHED")
		// Explicit state fixture establishes which rows count. This is not the
		// still-pending MySQL Admin orphan-removal product recovery operation.
		x.exec(t, `UPDATE managed_nodes SET status='removed',credential_hash=NULL,version=version+1 WHERE id=$1`, `UPDATE managed_nodes SET status='removed',credential_hash=NULL,version=version+1 WHERE id=?`, value.NodeID)
		if _, e = x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: pending.Token, Handshake: handshake}, uuid.NewString()); e != nil {
			t.Fatal("removed fixture still counted against configured capacity", e)
		}
		policy.Nodes.MaximumLiveNodes = 100
		existing, e := x.s.Get(x.ctx, x.admin, "", x.ids[3])
		if e != nil {
			t.Fatal(e)
		}
		collision := nodeEnrollmentMint(t, x, x.f.Workspace, existing.Name, wire.ReadOnlyProfile)
		nodeEnrollmentRefused(t, x, collision.Token, handshake, "NODE_NAME_EXISTS")
		x.exec(t, `UPDATE managed_nodes SET status='removed',credential_hash=NULL,version=version+1 WHERE id=$1`, `UPDATE managed_nodes SET status='removed',credential_hash=NULL,version=version+1 WHERE id=?`, existing.ID)
		if _, e = x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: collision.Token, Handshake: handshake}, uuid.NewString()); e != nil {
			t.Fatal("removed live-name fixture did not release its name", e)
		}
	})
}

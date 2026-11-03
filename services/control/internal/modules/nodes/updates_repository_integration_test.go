//go:build repository_integration

package nodes_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

// These cases exercise the actual paired node/task/audit core. Observers are
// explicitly unattached, so they do not establish full production orphan recovery.
func nodeUpdateCoreEnrolled(t *testing.T, x nodeReadActual, profile, name string) nodes.Node {
	t.Helper()
	pending := nodeEnrollmentMint(t, x, x.f.Workspace, name, profile)
	enrolled, e := x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: pending.Token, Handshake: nodeEnrollmentInput(profile)}, uuid.NewString())
	if e != nil {
		t.Fatal("actual update core enrollment prerequisite", e)
	}
	node, e := x.s.Get(x.ctx, x.admin, "", enrolled.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	return node
}
func nodeUpdateCoreRefused(t *testing.T, x nodeReadActual, actor identity.Auth, node nodes.Node, name, status string, version int64, code string) {
	t.Helper()
	before, audits := nodeReadStoredDigest(t, x), x.audits(t)
	value, e := x.s.Update(x.ctx, actor, node.ID, name, status, version, "Explicit refused mutation", uuid.NewString())
	nodeReadWant(t, e, code)
	if !reflect.DeepEqual(value, nodes.Node{}) || nodeReadStoredDigest(t, x) != before || x.audits(t) != audits {
		t.Fatal("refused actual node update exposed DTO or changed stored authority")
	}
}
func nodeUpdateCoreAudit(t *testing.T, x nodeReadActual, node, request string) {
	t.Helper()
	var count int
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(
		`SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND workspace_id=$2 AND actor_user_id=$3 AND audience='admin' AND action='node.updated' AND outcome='success' AND target_type='node' AND target_id=$4 AND request_id=$5 AND reason='Explicit actual node change'`,
		`SELECT count(*) FROM audit_events WHERE tenant_id=? AND workspace_id=? AND actor_user_id=? AND audience='admin' AND action='node.updated' AND outcome='success' AND target_type='node' AND target_id=? AND request_id=? AND reason='Explicit actual node change'`,
		x.f.Tenant, x.f.Workspace, x.f.User, node, request)).Scan(&count); e != nil || count != 1 {
		t.Fatal("actual atomic node Admin audit mismatch", e)
	}
}

func TestActualNodeUpdateCoreLifecycle(t *testing.T) {
	t.Run("state_version_credential_tasks_and_audit", func(t *testing.T) {
		x := newNodeReadActual(t)
		node := nodeUpdateCoreEnrolled(t, x, wire.ReadOnlyProfile, "Synthetic mutation lifecycle")
		var originalHash []byte
		if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT credential_hash FROM managed_nodes WHERE id=$1", "SELECT credential_hash FROM managed_nodes WHERE id=?", node.ID)).Scan(&originalHash); e != nil {
			t.Fatal(e)
		}
		for _, status := range []string{"active", "disabled", "active", "removed"} {
			task := ""
			if status == "disabled" {
				// A valid persisted running task is explicit synthetic dispatch data;
				// actual MySQL task creation/claim execution is a separate pending port.
				task = uuid.NewString()
				_, e := x.f.Store.Exec(x.ctx, x.f.Statement(
					`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,attempt,lease_token_hash,lease_expires_at,deadline_at) VALUES($1,$2,$3,$4,'collect_facts.v1','running',$5,$6,1,$7,clock_timestamp()+interval '1 minute',clock_timestamp()+interval '2 minutes')`,
					`INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,attempt,lease_token_hash,lease_expires_at,deadline_at) VALUES(?,?,?,?,'collect_facts.v1','running',?,?,1,?,TIMESTAMPADD(MINUTE,1,UTC_TIMESTAMP(6)),TIMESTAMPADD(MINUTE,2,UTC_TIMESTAMP(6)))`,
					task, x.f.Tenant, x.f.Workspace, node.ID, uuid.NewString(), x.f.User, security.Digest("synthetic valid running lease")))
				if e != nil {
					t.Fatal(e)
				}
			}
			request, before, priorVersion := uuid.NewString(), x.audits(t), node.Version
			updated, e := x.s.Update(x.ctx, x.admin, node.ID, "  Updated \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629  ", status, priorVersion, "Explicit actual node change", request)
			if e != nil || updated.ID != node.ID || updated.TenantID != x.f.Tenant || updated.WorkspaceID != x.f.Workspace || updated.Name != "Updated \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || updated.Status != status || updated.Version != priorVersion+1 || !reflect.DeepEqual(updated.Facts, node.Facts) || !reflect.DeepEqual(updated.Capabilities, node.Capabilities) || x.audits(t) != before+1 {
				t.Fatal("actual paired node transition/scope/version/immutable facts mismatch", e)
			}
			var hash []byte
			if e = x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT credential_hash FROM managed_nodes WHERE id=$1", "SELECT credential_hash FROM managed_nodes WHERE id=?", node.ID)).Scan(&hash); e != nil || (status == "removed" && hash != nil) || (status != "removed" && !bytes.Equal(hash, originalHash)) {
				t.Fatal("actual node credential retention/revocation mismatch", e)
			}
			nodeUpdateCoreAudit(t, x, node.ID, request)
			if task != "" {
				var state, code string
				var cleared, completed bool
				if e = x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT state,error_code,lease_token_hash IS NULL AND lease_expires_at IS NULL,completed_at IS NOT NULL FROM node_tasks WHERE id=$1", "SELECT state,error_code,lease_token_hash IS NULL AND lease_expires_at IS NULL,completed_at IS NOT NULL FROM node_tasks WHERE id=?", task)).Scan(&state, &code, &cleared, &completed); e != nil || state != "cancelled" || code != "NODE_UNAVAILABLE" || !cleared || !completed {
					t.Fatal("actual node disable did not atomically cancel stored lease", e)
				}
			}
			raw, _ := json.Marshal(updated)
			if bytes.Contains(raw, []byte("credential")) || bytes.Contains(raw, []byte("token_hash")) {
				t.Fatal("actual node update projection exposed private field")
			}
			node = updated
		}
		nodeUpdateCoreRefused(t, x, x.admin, node, node.Name, "active", node.Version, "NODE_REMOVED")
		fresh := nodeUpdateCoreEnrolled(t, x, wire.ReadOnlyProfile, node.Name)
		if fresh.ID == node.ID {
			t.Fatal("removed live name was not available for a deliberately new identity")
		}
		t.Log("Actual node/task/audit core only; unattached observers do not establish full production orphan recovery")
	})
	t.Run("live_names_and_safe_version", func(t *testing.T) {
		x := newNodeReadActual(t)
		one := nodeUpdateCoreEnrolled(t, x, wire.ReadOnlyProfile, "Synthetic unique first")
		two := nodeUpdateCoreEnrolled(t, x, wire.ReadOnlyProfile, "Synthetic unique second")
		nodeUpdateCoreRefused(t, x, x.admin, one, two.Name, "active", one.Version, "NODE_NAME_EXISTS")
		nodeUpdateCoreRefused(t, x, x.admin, one, one.Name, "disabled", one.Version+1, "VERSION_CONFLICT")
		x.exec(t, "UPDATE managed_nodes SET version=$1 WHERE id=$2", "UPDATE managed_nodes SET version=? WHERE id=?", workspaces.MaximumVersion, one.ID)
		nodeUpdateCoreRefused(t, x, x.admin, one, one.Name, "disabled", workspaces.MaximumVersion, "RESOURCE_LIMIT_REACHED")
	})
	t.Run("all_current_inference_profile_fences", func(t *testing.T) {
		x := newNodeReadActual(t)
		for _, profile := range []string{wire.ReadOnlyProfile, wire.SystemdComponentsProfile, wire.SystemdMaintenanceProfile, wire.DockerInferenceProfile, wire.DockerAIProfile, wire.DockerModelLifecycleProfile} {
			node := nodeUpdateCoreEnrolled(t, x, profile, "Synthetic core fence "+profile)
			updated, e := x.s.Update(x.ctx, x.admin, node.ID, node.Name, "removed", node.Version, "Explicit actual node change", uuid.NewString())
			var fenced bool
			if e != nil || updated.Status != "removed" || x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT inference_fenced_at IS NOT NULL FROM managed_nodes WHERE id=$1", "SELECT inference_fenced_at IS NOT NULL FROM managed_nodes WHERE id=?", node.ID)).Scan(&fenced) != nil || fenced != wire.IsInferenceProfile(profile) {
				t.Fatal("shared inference profile core fence omitted or extended authority", profile, e)
			}
		}
		t.Log("Actual profile fence column only; no fake reservation observer or physical GPU execution")
	})
	t.Run("disabled_workspace_Admin_recovery", func(t *testing.T) {
		x := newNodeReadActual(t)
		node := nodeUpdateCoreEnrolled(t, x, wire.ReadOnlyProfile, "Synthetic disabled Workspace node")
		x.exec(t, "UPDATE workspaces SET status='disabled',version=version+1 WHERE id=$1", "UPDATE workspaces SET status='disabled',version=version+1 WHERE id=?", x.f.Workspace)
		nodeUpdateCoreRefused(t, x, x.admin, node, node.Name, "active", node.Version, "ACCESS_DENIED")
		for _, status := range []string{"disabled", "removed"} {
			var e error
			node, e = x.s.Update(x.ctx, x.admin, node.ID, node.Name, status, node.Version, "Explicit actual node change", uuid.NewString())
			if e != nil || node.Status != status {
				t.Fatal("actual Admin core recovery in disabled Workspace failed", e)
			}
		}
	})
}

func TestActualNodeUpdateCoreAuthority(t *testing.T) {
	for _, kind := range []string{"portal", "revoked_session", "demoted_admin", "disabled_user", "wrong_token", "foreign_node", "inactive_tenant", "unbootstrapped_installation"} {
		t.Run(kind, func(t *testing.T) {
			x := newNodeReadActual(t)
			node := nodeUpdateCoreEnrolled(t, x, wire.ReadOnlyProfile, "Synthetic current authority mutation")
			a, code := x.admin, "AUTH_SESSION_EXPIRED"
			switch kind {
			case "portal":
				a, code = x.portal, "ACCESS_DENIED"
			case "revoked_session":
				x.exec(t, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_update' WHERE id=$1", "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_update' WHERE id=?", a.SessionID)
			case "demoted_admin":
				x.exec(t, "UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", x.f.User)
			case "disabled_user":
				x.exec(t, "UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=?", x.f.User)
			case "wrong_token":
				a.TokenHash = append([]byte(nil), a.TokenHash...)
				a.TokenHash[0] ^= 1
			case "foreign_node":
				tenant, workspace := uuid.NewString(), uuid.NewString()
				node.ID, code = uuid.NewString(), "RESOURCE_NOT_FOUND"
				x.exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign update tenant','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic foreign update tenant','active')", tenant)
				x.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign update Workspace','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic foreign update Workspace','active')", workspace, tenant)
				if _, e := x.f.Store.Exec(x.ctx, x.f.Statement("INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at) VALUES($1,$2,$3,'Synthetic foreign update node','active',$4,'1.0','{}',clock_timestamp())", "INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at) VALUES(?,?,?,'Synthetic foreign update node','active',?,'1.0','{}',UTC_TIMESTAMP(6))", node.ID, tenant, workspace, security.Digest("synthetic foreign credential"))); e != nil {
					t.Fatal(e)
				}
			case "inactive_tenant":
				x.exec(t, "UPDATE tenants SET status='disabled' WHERE id=$1", "UPDATE tenants SET status='disabled' WHERE id=?", x.f.Tenant)
				code = "RESOURCE_NOT_FOUND"
			case "unbootstrapped_installation":
				x.exec(t, "UPDATE installation_state SET bootstrap_complete=false,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=1", "UPDATE installation_state SET bootstrap_complete=false,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=1")
				code = "RESOURCE_NOT_FOUND"
			}
			nodeUpdateCoreRefused(t, x, a, node, node.Name, "disabled", node.Version, code)
		})
	}
}

//go:build repository_integration

package nodes_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

func nodeJoinAuditCount(t *testing.T, x nodeReadActual, id, action, request, workspace string) int {
	t.Helper()
	var count int
	e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT count(*) FROM audit_events WHERE target_id=$1 AND action=$2 AND request_id=$3 AND tenant_id=$4 AND workspace_id=$5 AND actor_user_id=$6 AND audience='admin' AND outcome='success'`, `SELECT count(*) FROM audit_events WHERE target_id=? AND action=? AND request_id=? AND tenant_id=? AND workspace_id=? AND actor_user_id=? AND audience='admin' AND outcome='success'`, id, action, request, x.f.Tenant, workspace, x.f.User)).Scan(&count)
	if e != nil {
		t.Fatal("actual scoped join audit readback", e)
	}
	return count
}
func nodeJoinMint(t *testing.T, x nodeReadActual, workspace, profile string) nodes.MintedToken {
	t.Helper()
	request := uuid.NewString()
	before := time.Now().UTC()
	value, e := x.s.MintForProfile(x.ctx, x.admin, workspace, "  Synthetic join \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629  ", profile, request)
	if e != nil {
		t.Fatal("actual paired join mint", e)
	}
	expected := profile
	if expected == "" {
		expected = wire.ReadOnlyProfile
	}
	if !security.ValidToken(value.Token) || value.ID == "" || value.TenantID != x.f.Tenant || value.WorkspaceID != workspace || value.NodeName != "Synthetic join \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || value.CreatedBy != x.f.User || value.NodeID != nil || value.Status != "pending" || value.ExecutionProfile != expected || value.CreatedAt.Location() != time.UTC || value.ExpiresAt.Location() != time.UTC {
		t.Fatal("actual paired join projection/profile/UTC mismatch")
	}
	ttl := time.Duration(x.owner.Config.Policy().Nodes.JoinTTLSeconds) * time.Second
	if value.ExpiresAt.Before(before.Add(ttl-2*time.Second)) || value.ExpiresAt.After(time.Now().UTC().Add(ttl+2*time.Second)) {
		t.Fatal("configured join TTL not applied")
	}
	var hash []byte
	if e = x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT token_hash FROM node_join_tokens WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3`, `SELECT token_hash FROM node_join_tokens WHERE tenant_id=? AND workspace_id=? AND id=?`, x.f.Tenant, workspace, value.ID)).Scan(&hash); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(hash, security.Digest("node-join:v1:"+value.Token)) || len(hash) != 32 {
		t.Fatal("actual stored token purpose hash mismatch")
	}
	if nodeJoinAuditCount(t, x, value.ID, "node.join_token_created", request, workspace) != 1 {
		t.Fatal("creation and scoped audit not atomic")
	}
	page, e := x.s.Tokens(x.ctx, x.admin, url.Values{"limit": {"100"}})
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(page)
	if e != nil || bytes.Contains(raw, []byte(value.Token)) || bytes.Contains(raw, []byte("token_hash")) {
		t.Fatal("history exposed one-time token/hash")
	}
	return value
}
func nodeJoinRevoked(t *testing.T, x nodeReadActual, value nodes.MintedToken) {
	t.Helper()
	request := uuid.NewString()
	revoked, e := x.s.RevokeToken(x.ctx, x.admin, value.ID, "Synthetic revoke \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", request)
	if e != nil || revoked.ID != value.ID || revoked.Status != "revoked" || revoked.WorkspaceID != value.WorkspaceID || !revoked.ExpiresAt.Equal(value.ExpiresAt) {
		t.Fatal("actual paired revoke projection", e)
	}
	if nodeJoinAuditCount(t, x, value.ID, "node.join_token_revoked", request, value.WorkspaceID) != 1 {
		t.Fatal("revocation and scoped audit not atomic")
	}
}
func TestActualNodeJoinLifecycle(t *testing.T) {
	t.Run("mint_profiles_and_atomic_scoped_audit", func(t *testing.T) {
		x := newNodeReadActual(t)
		workspace := uuid.NewString()
		x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic nondefault join Workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic nondefault join Workspace','active')`, workspace, x.f.Tenant)
		// Profile creation preserves existing identifiers; no enrollment or target
		// execution is simulated by these real token writes.
		for _, profile := range []string{wire.ReadOnlyProfile, wire.SystemdComponentsProfile, wire.SystemdMaintenanceProfile, wire.DockerInferenceProfile, wire.DockerAIProfile, wire.DockerModelLifecycleProfile, ""} {
			nodeJoinMint(t, x, workspace, profile)
		}
	})
	t.Run("pending_revocation_and_terminal_refusals", func(t *testing.T) {
		x := newNodeReadActual(t)
		pending := nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
		nodeJoinRevoked(t, x, pending)
		before := x.audits(t)
		v, e := x.s.RevokeToken(x.ctx, x.admin, pending.ID, "Synthetic repeated revoke", uuid.NewString())
		nodeReadWant(t, e, "NODE_JOIN_STATE_CONFLICT")
		if !reflect.DeepEqual(v, nodes.JoinToken{}) || x.audits(t) != before {
			t.Fatal("terminal revoke exposed DTO or phantom audit")
		}
		for _, state := range []string{"expired", "consumed"} {
			value := nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
			if state == "expired" {
				x.exec(t, `UPDATE node_join_tokens SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, `UPDATE node_join_tokens SET expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?`, value.ID)
			} else {
				x.exec(t, `UPDATE node_join_tokens SET consumed_at=clock_timestamp(),node_id=$1 WHERE id=$2`, `UPDATE node_join_tokens SET consumed_at=UTC_TIMESTAMP(6),node_id=? WHERE id=?`, x.ids[0], value.ID)
			}
			before := x.audits(t)
			v, e := x.s.RevokeToken(x.ctx, x.admin, value.ID, "Synthetic terminal refusal", uuid.NewString())
			nodeReadWant(t, e, "NODE_JOIN_STATE_CONFLICT")
			if !reflect.DeepEqual(v, nodes.JoinToken{}) || x.audits(t) != before {
				t.Fatal("terminal state lost zero result/audit rollback")
			}
		}
		value := nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
		x.exec(t, `UPDATE workspaces SET status='disabled',version=version+1 WHERE id=$1`, `UPDATE workspaces SET status='disabled',version=version+1 WHERE id=?`, x.f.Workspace)
		nodeJoinRevoked(t, x, value)
		minted, e := x.s.Mint(x.ctx, x.admin, x.f.Workspace, "Synthetic disabled Workspace", uuid.NewString())
		nodeReadWant(t, e, "ACCESS_DENIED")
		if !reflect.DeepEqual(minted, nodes.MintedToken{}) {
			t.Fatal("disabled Workspace exposed minted secret")
		}
	})
	t.Run("configured_capacity_and_terminal_rows", func(t *testing.T) {
		x := newNodeReadActual(t)
		policy := config.DefaultRuntimePolicy()
		policy.Nodes.MaximumLiveJoinTokens, policy.Nodes.JoinTTLSeconds = 2, 90
		x.owner.Config.Runtime = &policy
		one := nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
		two := nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
		before := x.audits(t)
		value, e := x.s.Mint(x.ctx, x.admin, x.f.Workspace, "Synthetic over capacity", uuid.NewString())
		nodeReadWant(t, e, "RESOURCE_LIMIT_REACHED")
		if !reflect.DeepEqual(value, nodes.MintedToken{}) || x.audits(t) != before {
			t.Fatal("capacity refusal persisted audit or exposed token")
		}
		nodeJoinRevoked(t, x, one)
		three := nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
		x.exec(t, `UPDATE node_join_tokens SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, `UPDATE node_join_tokens SET expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?`, two.ID)
		_ = nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
		x.exec(t, `UPDATE node_join_tokens SET consumed_at=clock_timestamp(),node_id=$1 WHERE id=$2`, `UPDATE node_join_tokens SET consumed_at=UTC_TIMESTAMP(6),node_id=? WHERE id=?`, x.ids[0], three.ID)
		_ = nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
	})
	t.Run("independent_actual_connection_reopen", func(t *testing.T) {
		x := newNodeReadActual(t)
		value := nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
		store, _ := nodeReadFaultStore(t, x) // Real separate connection with normal forwarding; no fault armed.
		isolated := *x.owner
		isolated.Storage, isolated.DB = store, nil
		service := &nodes.Service{Identity: &isolated, Workspaces: &workspaces.Service{Identity: &isolated}}
		page, e := service.Tokens(x.ctx, x.admin, url.Values{"limit": {"100"}})
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, item := range page.Items {
			found = found || item.ID == value.ID
		}
		if !found {
			t.Fatal("committed mint absent on independent connection")
		}
		revoked, e := service.RevokeToken(x.ctx, x.admin, value.ID, "Synthetic reopened revoke", uuid.NewString())
		if e != nil || revoked.Status != "revoked" {
			t.Fatal("reopened actual revoke", e)
		}
		page, e = x.s.Tokens(x.ctx, x.admin, url.Values{"limit": {"100"}})
		if e != nil {
			t.Fatal(e)
		}
		found = false
		for _, item := range page.Items {
			if item.ID == value.ID {
				found = item.Status == "revoked"
			}
		}
		if !found {
			t.Fatal("independent committed revoke not durable")
		}
		raw, _ := json.Marshal(page)
		if strings.Contains(string(raw), value.Token) {
			t.Fatal("reopened history exposed one-time secret")
		}
	})
}

func nodeJoinRefused(t *testing.T, x nodeReadActual, a identity.Auth, workspace, id, code string) {
	t.Helper()
	before := nodeReadStoredDigest(t, x)
	minted, e := x.s.Mint(x.ctx, a, workspace, "Synthetic refused writer", uuid.NewString())
	nodeReadWant(t, e, code)
	if !reflect.DeepEqual(minted, nodes.MintedToken{}) {
		t.Fatal("refused join writer exposed DTO/secret")
	}
	revoked, e := x.s.RevokeToken(x.ctx, a, id, "Synthetic refused revoke", uuid.NewString())
	nodeReadWant(t, e, code)
	if !reflect.DeepEqual(revoked, nodes.JoinToken{}) || nodeReadStoredDigest(t, x) != before {
		t.Fatal("refused join writer exposed DTO or changed persistence/audit")
	}
}
func TestActualNodeJoinAuthority(t *testing.T) {
	for _, kind := range []string{"portal_roles", "current_admin_without_membership", "revoked_session", "demoted_admin", "disabled_user", "wrong_token", "wrong_user", "foreign_tenant", "inactive_tenant", "unbootstrapped_installation", "connector_disabled", "binding_inactive_with_unrevoked_session"} {
		t.Run(kind, func(t *testing.T) {
			x := newNodeReadActual(t)
			pending := nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
			switch kind {
			case "portal_roles":
				for _, role := range []string{"owner", "member", "viewer"} {
					nodeJoinRefused(t, x, nodeReadPeer(t, x, role), x.f.Workspace, pending.ID, "ACCESS_DENIED")
				}
				forged := x.portal
				forged.Me.Audience = "admin"
				forged.Me.User.IsPlatformAdmin = true
				nodeJoinRefused(t, x, forged, x.f.Workspace, pending.ID, "AUTH_SESSION_EXPIRED")
			case "current_admin_without_membership":
				x.exec(t, `DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, `DELETE FROM workspace_memberships WHERE user_id=? AND workspace_id=?`, x.f.User, x.f.Workspace)
				value := nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
				nodeJoinRevoked(t, x, value)
			case "revoked_session", "demoted_admin", "disabled_user", "wrong_token", "wrong_user":
				a := x.admin
				switch kind {
				case "revoked_session":
					x.exec(t, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_join_writer' WHERE id=$1`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_join_writer' WHERE id=?`, a.SessionID)
				case "demoted_admin":
					x.exec(t, `UPDATE users SET is_platform_admin=false WHERE id=$1`, `UPDATE users SET is_platform_admin=false WHERE id=?`, x.f.User)
				case "disabled_user":
					x.exec(t, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=?`, x.f.User)
				case "wrong_token":
					a.TokenHash = append([]byte{}, a.TokenHash...)
					a.TokenHash[0] ^= 1
				case "wrong_user":
					a.Me.User.ID = nodeReadPeer(t, x, "member").Me.User.ID
				}
				nodeJoinRefused(t, x, a, x.f.Workspace, pending.ID, "AUTH_SESSION_EXPIRED")
			case "foreign_tenant":
				tenant, workspace, id := uuid.NewString(), uuid.NewString(), uuid.NewString()
				hash := sha256.Sum256([]byte(uuid.NewString()))
				x.exec(t, `INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign join tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic foreign join tenant','active')`, tenant)
				x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign join Workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic foreign join Workspace','active')`, workspace, tenant)
				x.exec(t, `INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at) VALUES($1,$2,$3,'Synthetic foreign join token',$4,$5,clock_timestamp()+interval '1 hour')`, `INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at) VALUES(?,?,?,'Synthetic foreign join token',?,?,TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)))`, id, tenant, workspace, hash[:], x.f.User)
				nodeJoinRefused(t, x, x.admin, workspace, id, "RESOURCE_NOT_FOUND")
			case "inactive_tenant":
				x.exec(t, `UPDATE tenants SET status='disabled' WHERE id=$1`, `UPDATE tenants SET status='disabled' WHERE id=?`, x.f.Tenant)
				nodeJoinRefused(t, x, x.admin, x.f.Workspace, pending.ID, "RESOURCE_NOT_FOUND")
			case "unbootstrapped_installation":
				x.exec(t, `UPDATE installation_state SET bootstrap_complete=false,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=1`, `UPDATE installation_state SET bootstrap_complete=false,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=1`)
				nodeJoinRefused(t, x, x.admin, x.f.Workspace, pending.ID, "RESOURCE_NOT_FOUND")
			case "connector_disabled", "binding_inactive_with_unrevoked_session":
				source := nodeReadSourcedActor(t, x)
				sourced := x
				sourced.admin = source.actor
				value := nodeJoinMint(t, sourced, x.f.Workspace, wire.ReadOnlyProfile)
				nodeJoinRevoked(t, sourced, value)
				if kind == "connector_disabled" {
					if _, e := source.provider.UpdateConnector(x.ctx, x.admin, source.connector.ID, sso.UpdateConnector{Revision: 1, Name: source.connector.Name, Enabled: false, Reason: "Synthetic current join source revocation"}, uuid.NewString(), uuid.NewString()); e != nil {
						t.Fatal(e)
					}
				} else {
					x.exec(t, `UPDATE sso_bindings SET active=false,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE sso_bindings SET active=false,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, source.source.BindingID)
					var active bool
					if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT revoked_at IS NULL FROM sessions WHERE id=$1`, `SELECT revoked_at IS NULL FROM sessions WHERE id=?`, source.actor.SessionID)).Scan(&active); e != nil || !active {
						t.Fatal("unrevoked source session prerequisite missing", e)
					}
				}
				nodeJoinRefused(t, x, source.actor, x.f.Workspace, pending.ID, "AUTH_SESSION_EXPIRED")
				recovered := nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
				nodeJoinRevoked(t, x, recovered)
			}
		})
	}
}

func TestActualNodeJoinCommitAcknowledgementLoss(t *testing.T) {
	for _, operation := range []string{"mint", "revoke"} {
		t.Run(operation, func(t *testing.T) {
			for _, fault := range []struct {
				name      string
				mode      commitfault.Mode
				committed bool
			}{
				{"normal_acknowledged_commit", commitfault.Pass, true},
				{"commit_not_forwarded", commitfault.DropBeforeCommit, false},
				{"actual_server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true},
			} {
				t.Run(fault.name, func(t *testing.T) {
					x := newNodeReadActual(t)
					name, request := "Synthetic wire join "+uuid.NewString(), uuid.NewString()
					id := ""
					if operation == "revoke" {
						value, e := x.s.Mint(x.ctx, x.admin, x.f.Workspace, name, uuid.NewString())
						if e != nil {
							t.Fatal("actual revoke prerequisite", e)
						}
						id = value.ID
					}
					before := x.audits(t)
					store, proxy := nodeReadFaultStore(t, x)
					isolated := *x.owner
					isolated.Storage, isolated.DB = store, nil
					service := &nodes.Service{Identity: &isolated, Workspaces: &workspaces.Service{Identity: &isolated}}
					if fault.mode != commitfault.Pass {
						if e := proxy.Arm(fault.mode); e != nil {
							t.Fatal(e)
						}
					}
					var minted nodes.MintedToken
					var revoked nodes.JoinToken
					var e error
					if operation == "mint" {
						minted, e = service.Mint(x.ctx, x.admin, x.f.Workspace, name, request)
					} else {
						revoked, e = service.RevokeToken(x.ctx, x.admin, id, "Synthetic uncertain revoke", request)
					}
					if fault.mode == commitfault.Pass {
						if e != nil {
							t.Fatal("normal actual join COMMIT", e)
						}
						if operation == "mint" && (!security.ValidToken(minted.Token) || minted.ID == "" || minted.Status != "pending") || operation == "revoke" && (revoked.ID != id || revoked.Status != "revoked") {
							t.Fatal("acknowledged join result missing")
						}
					} else {
						nodeReadWant(t, e, string(transaction.CommitUnknown))
						if !reflect.DeepEqual(minted, nodes.MintedToken{}) || !reflect.DeepEqual(revoked, nodes.JoinToken{}) {
							t.Fatal("unknown join COMMIT exposed captured DTO/secret")
						}
						select {
						case observed := <-proxy.Outcomes():
							if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
								t.Fatal("actual join COMMIT outcome not established", observed)
							}
						case <-x.ctx.Done():
							t.Fatal("actual join wire observation missing")
						}
					}
					if proxy.Commits() != 1 {
						t.Fatal("join COMMIT automatically resent", proxy.Commits())
					}
					var count int
					if e = x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT count(*) FROM node_join_tokens WHERE tenant_id=$1 AND workspace_id=$2 AND node_name=$3`, `SELECT count(*) FROM node_join_tokens WHERE tenant_id=? AND workspace_id=? AND node_name=?`, x.f.Tenant, x.f.Workspace, name)).Scan(&count); e != nil {
						t.Fatal(e)
					}
					expectedRows := 1
					if operation == "mint" && !fault.committed {
						expectedRows = 0
					}
					if count != expectedRows {
						t.Fatal("actual join committed/absent readback mismatch")
					}
					if count == 1 {
						var status string
						var hash []byte
						if e = x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT id::text,CASE WHEN revoked_at IS NOT NULL THEN 'revoked' ELSE 'pending' END,token_hash FROM node_join_tokens WHERE tenant_id=$1 AND workspace_id=$2 AND node_name=$3`, `SELECT id,CASE WHEN revoked_at IS NOT NULL THEN 'revoked' ELSE 'pending' END,token_hash FROM node_join_tokens WHERE tenant_id=? AND workspace_id=? AND node_name=?`, x.f.Tenant, x.f.Workspace, name)).Scan(&id, &status, &hash); e != nil {
							t.Fatal(e)
						}
						expectedState := "pending"
						if operation == "revoke" && fault.committed {
							expectedState = "revoked"
						}
						if status != expectedState || len(hash) != 32 {
							t.Fatal("actual join durable state/hash mismatch")
						}
						if operation == "mint" && fault.mode == commitfault.Pass && !bytes.Equal(hash, security.Digest("node-join:v1:"+minted.Token)) {
							t.Fatal("acknowledged secret differs from actual stored hash")
						}
					}
					expectedAudit := 0
					if fault.committed {
						expectedAudit = 1
					}
					if x.audits(t) != before+expectedAudit {
						t.Fatal("actual join mutation/audit did not share COMMIT")
					}
					action := "node.join_token_created"
					if operation == "revoke" {
						action = "node.join_token_revoked"
					}
					if id != "" && nodeJoinAuditCount(t, x, id, action, request, x.f.Workspace) != expectedAudit {
						t.Fatal("uncertain join scoped audit readback mismatch")
					}
					// Recovery starts only after independent authoritative inspection. It uses
					// the original real pool,never retries the uncertain forwarding request.
					page, e := x.s.Tokens(x.ctx, x.admin, url.Values{"limit": {"100"}})
					if e != nil {
						t.Fatal("explicit authoritative join refresh", e)
					}
					if operation == "mint" {
						if fault.committed {
							found := false
							for _, item := range page.Items {
								found = found || item.ID == id
							}
							if !found {
								t.Fatal("committed uncertain mint absent from authoritative history")
							}
							value, e := x.s.RevokeToken(x.ctx, x.admin, id, "Explicit revoke after uncertain mint inspection", uuid.NewString())
							if e != nil || value.Status != "revoked" {
								t.Fatal("explicit uncertain-mint recovery", e)
							}
						} else {
							for _, item := range page.Items {
								if item.NodeName == name {
									t.Fatal("uncommitted uncertain mint survived")
								}
							}
							recovered, e := x.s.Mint(x.ctx, x.admin, x.f.Workspace, "Synthetic deliberate new join "+uuid.NewString(), uuid.NewString())
							if e != nil || !security.ValidToken(recovered.Token) {
								t.Fatal("explicit new mint after authoritative absence", e)
							}
						}
					} else if !fault.committed {
						value, e := x.s.RevokeToken(x.ctx, x.admin, id, "Explicit revoke after pending-state inspection", uuid.NewString())
						if e != nil || value.Status != "revoked" {
							t.Fatal("explicit uncertain-revoke recovery", e)
						}
					}
					if proxy.Commits() != 1 {
						t.Fatal("explicit recovery resent uncertain proxy COMMIT")
					}
				})
			}
		})
	}
}

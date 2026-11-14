//go:build repository_integration

package http_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

const nodeJoinHTTPPath = "/api/v1/admin/node-join-tokens"

func nodeJoinHTTPBody(workspace, profile string) map[string]any {
	return map[string]any{"workspace_id": workspace, "node_name": "  Synthetic HTTPS join \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629  ", "execution_profile": profile}
}
func nodeJoinHTTPMint(t *testing.T, x pairedNodeHTTP, profile string) map[string]any {
	t.Helper()
	v := x.admin.mutate("POST", nodeJoinHTTPPath, nodeJoinHTTPBody(x.f.Workspace, profile), 201, "MintedTokenResponse")
	raw, ok := v["token"].(string)
	if !ok || !security.ValidToken(raw) || v["workspace_id"] != x.f.Workspace || v["tenant_id"] != x.f.Tenant || v["created_by"] != x.f.User || v["node_name"] != "Synthetic HTTPS join \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || v["execution_profile"] != profile || v["status"] != "pending" {
		t.Fatal("actual HTTPS mint projection mismatch")
	}
	var hash []byte
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT token_hash FROM node_join_tokens WHERE id=$1", "SELECT token_hash FROM node_join_tokens WHERE id=?", v["id"])).Scan(&hash); e != nil || !bytes.Equal(hash, security.Digest("node-join:v1:"+raw)) {
		t.Fatal("actual HTTPS token hash mismatch", e)
	}
	return v
}
func nodeJoinHTTPRevoke(x pairedNodeHTTP, token string) map[string]any {
	return x.admin.mutate("POST", nodeJoinHTTPPath+"/"+token+"/revocations", map[string]any{"reason": "Synthetic HTTPS revoke \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629"}, 200, "JoinTokenResponse")
}
func nodeJoinHTTPAudit(t *testing.T, x pairedNodeHTTP, action, request string) int {
	t.Helper()
	var n int
	if request == "" {
		request = "%"
	}
	e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND workspace_id=$2 AND actor_user_id=$3 AND audience='admin' AND outcome='success' AND action=$4 AND request_id::text LIKE $5`, `SELECT count(*) FROM audit_events WHERE tenant_id=? AND workspace_id=? AND actor_user_id=? AND audience='admin' AND outcome='success' AND action=? AND request_id LIKE ?`, x.f.Tenant, x.f.Workspace, x.f.User, action, request)).Scan(&n)
	if e != nil {
		t.Fatal("actual HTTPS scoped join audit", e)
	}
	return n
}
func TestM20PairedNodeJoinHTTPPaths(t *testing.T) {
	x := newPairedNodeHTTP(t)
	for _, profile := range []string{wire.ReadOnlyProfile, wire.SystemdComponentsProfile, wire.SystemdMaintenanceProfile, wire.DockerInferenceProfile, wire.DockerAIProfile, wire.DockerModelLifecycleProfile} {
		v := nodeJoinHTTPMint(t, x, profile)
		page := x.admin.request("GET", nodeJoinHTTPPath+"?limit=100", nil, 200, "JoinTokenListResponse", nil)
		raw, _ := json.Marshal(page)
		if bytes.Contains(raw, []byte(v["token"].(string))) {
			t.Fatal("actual HTTPS history disclosed raw secret")
		}
		nodeHTTPSNoSecrets(t, page)
		if result := nodeJoinHTTPRevoke(x, v["id"].(string)); result["status"] != "revoked" {
			t.Fatal("actual HTTPS revoke did not publish durable state")
		}
		problem := x.admin.mutate("POST", nodeJoinHTTPPath+"/"+v["id"].(string)+"/revocations", map[string]any{"reason": "Synthetic repeated revoke"}, 409, "Problem")
		if problem["code"] != "NODE_JOIN_STATE_CONFLICT" {
			t.Fatal("actual HTTPS terminal conflict mismatch")
		}
		nodeHTTPSNoSecrets(t, problem)
	}
	if nodeJoinHTTPAudit(t, x, "node.join_token_created", "") != 6 || nodeJoinHTTPAudit(t, x, "node.join_token_revoked", "") != 6 {
		t.Fatal("actual HTTPS scoped join audits missing or duplicated")
	}
}

func TestM20PairedNodeJoinHTTPAuthority(t *testing.T) {
	for _, kind := range []string{"entrance_csrf_origin_and_strict_json", "session_revoked", "admin_demoted", "foreign_scope", "disabled_workspace", "license_blocked"} {
		t.Run(kind, func(t *testing.T) {
			x := newPairedNodeHTTP(t)
			pending := nodeJoinHTTPMint(t, x, wire.ReadOnlyProfile)["id"].(string)
			x.admin.refresh()
			before := nodeHTTPStoredDigest(t, x)
			beforeAudits := nodeJoinHTTPAudit(t, x, "node.join_token_created", "") + nodeJoinHTTPAudit(t, x, "node.join_token_revoked", "")
			create, revoke := nodeJoinHTTPBody(x.f.Workspace, wire.ReadOnlyProfile), map[string]any{"reason": "Synthetic refusal"}
			path := nodeJoinHTTPPath + "/" + pending + "/revocations"
			refuse := func(c *client, target string, body any, status int, headers map[string]string) {
				problem := c.request("POST", target, body, status, "Problem", headers)
				nodeHTTPSNoSecrets(t, problem)
				if problem["data"] != nil || nodeHTTPStoredDigest(t, x) != before || nodeJoinHTTPAudit(t, x, "node.join_token_created", "")+nodeJoinHTTPAudit(t, x, "node.join_token_revoked", "") != beforeAudits {
					t.Fatal("refused actual HTTPS join write changed token state")
				}
			}
			switch kind {
			case "entrance_csrf_origin_and_strict_json":
				x.portal.refresh()
				refuse(x.portal, nodeJoinHTTPPath, create, 404, nil)
				refuse(x.portal, path, revoke, 404, nil)
				for _, target := range []string{nodeJoinHTTPPath, path} {
					body := any(create)
					if target == path {
						body = revoke
					}
					refuse(x.admin, target, body, 403, map[string]string{"X-CSRF-Token": security.Token()})
					refuse(x.admin, target, body, 403, map[string]string{"Origin": "https://synthetic-foreign.example.invalid"})
				}
				create["tenant_id"] = uuid.NewString()
				refuse(x.admin, nodeJoinHTTPPath, create, 400, nil)
				revoke["workspace_id"] = uuid.NewString()
				refuse(x.admin, path, revoke, 400, nil)
			case "session_revoked", "admin_demoted":
				if kind == "session_revoked" {
					x.exec(t, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_join_http' WHERE id=$1", "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_join_http' WHERE id=?", x.adminAuth.SessionID)
				} else {
					x.exec(t, "UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", x.f.User)
				}
				refuse(x.admin, nodeJoinHTTPPath, create, 401, nil)
				refuse(x.admin, path, revoke, 401, nil)
			case "foreign_scope":
				tenant, workspace, token := uuid.NewString(), uuid.NewString(), uuid.NewString()
				x.exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign join tenant','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic foreign join tenant','active')", tenant)
				x.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign join Workspace','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic foreign join Workspace','active')", workspace, tenant)
				x.exec(t, "INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at) VALUES($1,$2,$3,'Synthetic foreign token',$4,$5,clock_timestamp()+interval '1 minute')", "INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,expires_at) VALUES(?,?,?,'Synthetic foreign token',?,?,TIMESTAMPADD(MINUTE,1,UTC_TIMESTAMP(6)))", token, tenant, workspace, security.Digest(uuid.NewString()), x.f.User)
				before = nodeHTTPStoredDigest(t, x)
				create["workspace_id"] = workspace
				refuse(x.admin, nodeJoinHTTPPath, create, 404, nil)
				refuse(x.admin, nodeJoinHTTPPath+"/"+token+"/revocations", revoke, 404, nil)
			case "disabled_workspace":
				x.exec(t, "UPDATE workspaces SET status='disabled',version=version+1 WHERE id=$1", "UPDATE workspaces SET status='disabled',version=version+1 WHERE id=?", x.f.Workspace)
				refuse(x.admin, nodeJoinHTTPPath, create, 403, nil)
				if nodeJoinHTTPRevoke(x, pending)["status"] != "revoked" {
					t.Fatal("disabled Workspace recovery revoked no token")
				}
			case "license_blocked":
				// Exclusive synthetic fixture state represents no installed License.
				x.exec(t, "DELETE FROM platform_license_artifacts", "DELETE FROM platform_license_artifacts")
				refuse(x.admin, nodeJoinHTTPPath, create, 403, nil)
				if nodeJoinHTTPRevoke(x, pending)["status"] != "revoked" {
					t.Fatal("unlicensed recovery revoked no token")
				}
			}
		})
	}
}

func TestM20PairedNodeJoinHTTPUnknown(t *testing.T) {
	for _, operation := range []string{"mint", "revoke"} {
		t.Run(operation, func(t *testing.T) {
			for _, fault := range []struct {
				name      string
				mode      commitfault.Mode
				committed bool
			}{
				{"commit_not_forwarded", commitfault.DropBeforeCommit, false},
				{"actual_server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true},
			} {
				t.Run(fault.name, func(t *testing.T) {
					x := newPairedNodeHTTP(t)
					token, path, body, action := "", nodeJoinHTTPPath, nodeJoinHTTPBody(x.f.Workspace, wire.ReadOnlyProfile), "node.join_token_created"
					if operation == "revoke" {
						token = nodeJoinHTTPMint(t, x, wire.ReadOnlyProfile)["id"].(string)
						path, body, action = path+"/"+token+"/revocations", map[string]any{"reason": "Synthetic unknown HTTPS revoke"}, "node.join_token_revoked"
					}
					x.admin.refresh()
					before := nodeHTTPStoredDigest(t, x)
					store, proxy := nodeHTTPFaultStore(t, x)
					isolated := *x.owner
					isolated.Storage, isolated.DB = store, nil
					original := x.handler.Nodes.Service
					x.handler.Nodes.Service = &nodes.Service{Identity: &isolated, Workspaces: &workspaces.Service{Identity: &isolated}}
					if e := proxy.Arm(fault.mode); e != nil {
						t.Fatal(e)
					}
					raw, _ := json.Marshal(body)
					r, e := http.NewRequestWithContext(x.ctx, "POST", x.admin.s.URL+path, bytes.NewReader(raw))
					if e != nil {
						t.Fatal(e)
					}
					r.Host = "admin.example.invalid"
					r.Header.Set("Origin", "https://"+r.Host)
					r.Header.Set("Content-Type", "application/json")
					r.Header.Set("X-CSRF-Token", x.admin.csrf)
					for _, c := range x.admin.cookies {
						r.AddCookie(c)
					}
					response, e := x.admin.s.Client().Do(r)
					if e != nil {
						t.Fatal("actual trusted TLS join wire response", e)
					}
					raw, e = io.ReadAll(io.LimitReader(response.Body, 1<<20))
					response.Body.Close()
					var problem map[string]any
					if e != nil || len(raw) == 1<<20 || response.StatusCode != 409 || response.Header.Get("Retry-After") != "" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Request-ID") == "" || json.Unmarshal(raw, &problem) != nil || problem["code"] != "STORAGE_COMMIT_OUTCOME_UNKNOWN" || problem["request_id"] != response.Header.Get("X-Request-ID") || problem["data"] != nil || problem["success"] != nil {
						t.Fatal("unknown HTTPS join lost sanitized409/request-ID contract")
					}
					if e = x.admin.schemas["Problem"].Validate(problem); e != nil {
						t.Fatal(e)
					}
					nodeHTTPSNoSecrets(t, problem)
					select {
					case observed := <-proxy.Outcomes():
						if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
							t.Fatal("actual HTTPS join protocol observation mismatch")
						}
					case <-time.After(3 * time.Second):
						t.Fatal("actual HTTPS join COMMIT observation missing")
					}
					want := 0
					if fault.committed {
						want = 1
					}
					request := problem["request_id"].(string)
					if proxy.Commits() != 1 || nodeJoinHTTPAudit(t, x, action, request) != want || (nodeHTTPStoredDigest(t, x) == before) == fault.committed {
						t.Fatal("actual HTTPS unknown state/audit/no retry mismatch")
					}
					if operation == "mint" && fault.committed {
						if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT target_id FROM audit_events WHERE request_id=$1 AND action='node.join_token_created'", "SELECT target_id FROM audit_events WHERE request_id=? AND action='node.join_token_created'", request)).Scan(&token); e != nil {
							t.Fatal(e)
						}
					} else if operation == "revoke" {
						var revoked bool
						if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT revoked_at IS NOT NULL FROM node_join_tokens WHERE id=$1", "SELECT revoked_at IS NOT NULL FROM node_join_tokens WHERE id=?", token)).Scan(&revoked); e != nil || revoked != fault.committed {
							t.Fatal("actual HTTPS revocation outcome mismatch", e)
						}
					}
					x.handler.Nodes.Service = original
					history := x.admin.request("GET", nodeJoinHTTPPath+"?limit=100", nil, 200, "JoinTokenListResponse", nil)
					nodeHTTPSNoSecrets(t, history)
					if operation == "mint" && !fault.committed {
						token = nodeJoinHTTPMint(t, x, wire.ReadOnlyProfile)["id"].(string)
					}
					if operation == "mint" || !fault.committed {
						nodeJoinHTTPRevoke(x, token)
					}
					if proxy.Commits() != 1 {
						t.Fatal("explicit HTTPS recovery resent original COMMIT")
					}
				})
			}
		})
	}
}

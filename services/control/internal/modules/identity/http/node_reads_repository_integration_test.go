//go:build repository_integration

package http_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

type pairedNodeHTTP struct {
	f                     repositoryfixture.Fixture
	ctx                   context.Context
	owner                 *identity.Service
	handler               *transport.Handler
	admin, portal         *client
	nodes                 []string
	adminAuth, portalAuth identity.Auth
	password              string
}

func newPairedNodeHTTP(t *testing.T) pairedNodeHTTP {
	t.Helper()
	return newPairedNodeHTTPWithTimeout(t, 75*time.Second)
}

func newPairedNodeHTTPWithTimeout(t *testing.T, timeout time.Duration) pairedNodeHTTP {
	t.Helper()
	f := repositoryfixture.NewUnbootstrapped(t)
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	key, trust, e := licensewire.GenerateKey("synthetic-node-http")
	if e != nil {
		t.Fatal(e)
	}
	trustPath := filepath.Join(root, "license-trust.json")
	raw, e := json.Marshal(trust)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(trustPath, raw, 0600); e != nil {
		t.Fatal(e)
	}
	blocklist := filepath.Join(root, "blocklist.txt")
	if e = os.WriteFile(blocklist, []byte("passwordpassword\n"), 0600); e != nil {
		t.Fatal(e)
	}
	passwords, e := security.NewPasswords(blocklist)
	if e != nil {
		t.Fatal(e)
	}
	hmac := make([]byte, 32)
	if _, e = rand.Read(hmac); e != nil {
		t.Fatal(e)
	}
	ring := filepath.Join(root, "mail-keyring.json")
	raw, e = json.Marshal(map[string]any{"active": "synthetic", "keys": map[string]string{"synthetic": base64.StdEncoding.EncodeToString(hmac)}})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(ring, raw, 0600); e != nil {
		t.Fatal(e)
	}
	keys, e := mail.LoadKeyring(ring)
	if e != nil {
		t.Fatal(e)
	}
	templates, e := mail.LoadTemplates()
	if e != nil {
		t.Fatal(e)
	}
	policy := config.DefaultRuntimePolicy()
	policy.UI.PageSize = 2
	c := config.Config{Runtime: &policy, Licensing: &config.LicensingConfiguration{TrustFile: trustPath}}
	c.Server.PortalOrigin, c.Server.AdminOrigin = "https://portal.example.invalid", "https://admin.example.invalid"
	c.SMTP.FromAddress = "platform@example.invalid"
	outbox := &mail.Outbox{Storage: f.Store, Keys: keys, Config: c, Templates: templates}
	owner := &identity.Service{Storage: f.Store, Passwords: passwords, Config: c, Mail: outbox, HMACKey: hmac}
	if f.PostgreSQL != nil {
		owner.Storage, owner.DB = nil, f.PostgreSQL
	}
	password := "Synthetic HTTPS passphrase " + uuid.NewString()
	if e = owner.Bootstrap(ctx, "admin@example.invalid", "Synthetic node HTTP Admin", password, "Synthetic node HTTP tenant", "Default"); e != nil {
		t.Fatal("actual paired HTTPS Bootstrap", e)
	}
	if e = f.Store.QueryRow(ctx, f.Statement("SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1", "SELECT tenant_id,default_workspace_id,primary_admin_user_id FROM installation_state WHERE id=1")).Scan(&f.Tenant, &f.Workspace, &f.User); e != nil {
		t.Fatal(e)
	}
	h := transport.New(owner)
	if e = h.LicenseInitializationError(); e != nil {
		t.Fatal(e)
	}
	server := accountTLSServer(t, h)
	ss := schemas(t)
	for name, schema := range httpfixture.Schemas(t, "../../../../../../api/openapi/v1/nodes/openapi.json") {
		ss[name] = schema
	}
	admin, portal := newClient(t, server, "admin", ss), newClient(t, server, "portal", ss)
	for _, c := range []*client{admin, portal} {
		c.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	}
	adminAuth, e := owner.Authenticate(ctx, admin.cookies["__Host-admin_session"].Value, "admin")
	if e != nil {
		t.Fatal(e)
	}
	portalAuth, e := owner.Authenticate(ctx, portal.cookies["__Host-portal_session"].Value, "portal")
	if e != nil {
		t.Fatal(e)
	}
	var deployment string
	if e = f.Store.QueryRow(ctx, f.Statement("SELECT deployment_id::text FROM platform_license_state WHERE id=1", "SELECT deployment_id FROM platform_license_state WHERE id=1")).Scan(&deployment); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := licensewire.Claims{FormatVersion: licensewire.FormatVersion, LicenseID: uuid.NewString(), CustomerReference: "Synthetic node HTTPS fixture", DeploymentID: deployment, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), GraceUntil: now.Add(2 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
	artifact, e := licensewire.Issue(claims, key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.Licensing.Service.Import(ctx, adminAuth, licensing.ImportInput{Artifact: string(artifact), Reason: "Synthetic paired node HTTP fixture"}, uuid.NewString()); e != nil {
		t.Fatal("actual signed whole-platform admission", e)
	}
	x := pairedNodeHTTP{f: f, ctx: ctx, owner: owner, handler: h, admin: admin, portal: portal, adminAuth: adminAuth, portalAuth: portalAuth, password: password}
	stamp := time.Date(2026, 10, 19, 1, 2, 3, 123456000, time.UTC)
	facts, e := json.Marshal(wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "Synthetic HTTPS \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", LogicalCPUs: 1, MemoryTotalBytes: 1048576, DiskPath: "/synthetic", DiskTotalBytes: 2097152, DiskAvailableBytes: 1048576, ObservedAt: stamp, AgentVersion: wire.AgentVersion})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		id := uuid.NewString()
		x.nodes = append(x.nodes, id)
		credential := sha256.Sum256([]byte(uuid.NewString()))
		x.exec(t, "INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,created_at) VALUES($1,$2,$3,$4,'active',$5,'1.0',$6,$7,$8)", "INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,created_at) VALUES(?,?,?,?,'active',?,'1.0',?,?,?)", id, f.Tenant, f.Workspace, "HTTPS node "+id, credential[:], facts, stamp, stamp)
		token := sha256.Sum256([]byte(uuid.NewString()))
		x.exec(t, "INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,created_at,expires_at) VALUES($1,$2,$3,'Synthetic HTTPS token',$4,$5,$6,$7)", "INSERT INTO node_join_tokens(id,tenant_id,workspace_id,node_name,token_hash,created_by,created_at,expires_at) VALUES(?,?,?,'Synthetic HTTPS token',?,?,?,?)", uuid.NewString(), f.Tenant, f.Workspace, token[:], f.User, stamp, stamp.Add(time.Hour))
		x.exec(t, "INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,created_at,deadline_at,completed_at) VALUES($1,$2,$3,$4,'collect_facts.v1','succeeded',$5,$6,$7,$8,$9)", "INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,created_at,deadline_at,completed_at) VALUES(?,?,?,?,'collect_facts.v1','succeeded',?,?,?,?,?)", uuid.NewString(), f.Tenant, f.Workspace, x.nodes[0], uuid.NewString(), f.User, stamp, stamp.Add(time.Hour), stamp.Add(time.Second))
	}
	return x
}
func (x pairedNodeHTTP) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := x.f.Store.Exec(x.ctx, x.f.Statement(pg, my, args...)); e != nil {
		t.Fatal("actual synthetic HTTPS setup", e)
	}
}
func nodeHTTPSNoSecrets(t *testing.T, body map[string]any) {
	t.Helper()
	raw, e := json.Marshal(body)
	if e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"credential_hash", "token_hash", "lease_token_hash", "result_hash", "payload", "request_fingerprint"} {
		if strings.Contains(string(raw), `"`+field+`"`) {
			t.Fatal("private node protocol field exposed by HTTPS", field)
		}
	}
}
func TestM20PairedNodeReadHTTPPaths(t *testing.T) {
	x := newPairedNodeHTTP(t)
	adminBase := "/api/v1/admin/nodes"
	portalBase := "/api/v1/workspaces/" + x.f.Workspace + "/nodes"
	for _, entry := range []struct {
		client *client
		base   string
	}{{x.admin, adminBase}, {x.portal, portalBase}} {
		page := entry.client.request("GET", entry.base, nil, 200, "NodeListResponse", nil)
		nodeHTTPSNoSecrets(t, page)
		if len(page["items"].([]any)) != 2 || page["next_cursor"] == nil {
			t.Fatal("configured HTTPS page size missing")
		}
		next := entry.client.request("GET", entry.base+"?cursor="+url.QueryEscape(page["next_cursor"].(string)), nil, 200, "NodeListResponse", nil)
		if len(next["items"].([]any)) != 1 || next["next_cursor"] != nil {
			t.Fatal("tied HTTPS catalog seek omitted/duplicated rows")
		}
		node := entry.client.request("GET", entry.base+"/"+x.nodes[0], nil, 200, "NodeResponse", nil)
		nodeHTTPSNoSecrets(t, node)
		if node["facts"].(map[string]any)["hostname"] != "Synthetic HTTPS \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || !strings.HasSuffix(node["last_heartbeat_at"].(string), "Z") {
			t.Fatal("HTTPS typed Unicode/UTC projection lost")
		}
		history := entry.client.request("GET", entry.base+"/"+x.nodes[0]+"/tasks?limit=100", nil, 200, "TaskListResponse", nil)
		nodeHTTPSNoSecrets(t, history)
		if len(history["items"].([]any)) != 3 {
			t.Fatal("real HTTPS task history missing")
		}
		entry.client.request("GET", entry.base+"?limit=0", nil, 400, "Problem", nil)
		entry.client.request("GET", entry.base+"?tenant_id="+x.f.Tenant, nil, 400, "Problem", nil)
		entry.client.request("GET", entry.base+"/invalid", nil, 400, "Problem", nil)
		entry.client.request("GET", entry.base+"/"+x.nodes[0]+"/tasks?cursor="+url.QueryEscape(page["next_cursor"].(string)), nil, 400, "Problem", nil)
	}
	tokens := x.admin.request("GET", "/api/v1/admin/node-join-tokens?limit=100", nil, 200, "JoinTokenListResponse", nil)
	nodeHTTPSNoSecrets(t, tokens)
	if len(tokens["items"].([]any)) != 3 {
		t.Fatal("real HTTPS join-token history missing")
	}
	x.portal.request("GET", adminBase, nil, 404, "Problem", nil)
	x.portal.request("GET", "/api/v1/workspaces/"+uuid.NewString()+"/nodes/"+x.nodes[0], nil, 404, "Problem", nil)
}

func nodeHTTPPrivateRefusal(t *testing.T, c *client, path string, status int) {
	t.Helper()
	body := c.request("GET", path, nil, status, "Problem", nil)
	if body["data"] != nil || body["items"] != nil || body["next_cursor"] != nil {
		t.Fatal("refused node HTTPS request exposed private result")
	}
}
func TestM20PairedNodeReadHTTPAuthority(t *testing.T) {
	for _, kind := range []string{"membership_revoked", "workspace_disabled", "session_revoked", "admin_demoted", "foreign_tenant", "wrong_bearer_and_entrance"} {
		t.Run(kind, func(t *testing.T) {
			x := newPairedNodeHTTP(t)
			base := "/api/v1/workspaces/" + x.f.Workspace + "/nodes"
			switch kind {
			case "membership_revoked", "workspace_disabled":
				if kind == "membership_revoked" {
					x.exec(t, "DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2", "DELETE FROM workspace_memberships WHERE user_id=? AND workspace_id=?", x.f.User, x.f.Workspace)
				} else {
					x.exec(t, "UPDATE workspaces SET status='disabled',version=version+1 WHERE id=$1", "UPDATE workspaces SET status='disabled',version=version+1 WHERE id=?", x.f.Workspace)
				}
				for _, path := range []string{base, base + "/" + x.nodes[0], base + "/" + x.nodes[0] + "/tasks"} {
					nodeHTTPPrivateRefusal(t, x.portal, path, 404)
				}
				x.admin.request("GET", "/api/v1/admin/nodes/"+x.nodes[0], nil, 200, "NodeResponse", nil)
			case "session_revoked", "admin_demoted":
				if kind == "session_revoked" {
					x.exec(t, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_node_read' WHERE id=$1", "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_node_read' WHERE id=?", x.adminAuth.SessionID)
				} else {
					x.exec(t, "UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", x.f.User)
				}
				for _, path := range []string{"/api/v1/admin/nodes", "/api/v1/admin/nodes/" + x.nodes[0], "/api/v1/admin/node-join-tokens", "/api/v1/admin/nodes/" + x.nodes[0] + "/tasks"} {
					nodeHTTPPrivateRefusal(t, x.admin, path, 401)
				}
				x.portal.request("GET", base+"/"+x.nodes[0], nil, 200, "NodeResponse", nil)
			case "foreign_tenant":
				tenant, workspace, node := uuid.NewString(), uuid.NewString(), uuid.NewString()
				credential := sha256.Sum256([]byte(uuid.NewString()))
				x.exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign HTTPS tenant','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Synthetic foreign HTTPS tenant','active')", tenant)
				x.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign HTTPS Workspace','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic foreign HTTPS Workspace','active')", workspace, tenant)
				x.exec(t, "INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at) VALUES($1,$2,$3,'Synthetic foreign HTTPS node','active',$4,'1.0','{}',clock_timestamp())", "INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at) VALUES(?,?,?,'Synthetic foreign HTTPS node','active',?,'1.0','{}',UTC_TIMESTAMP(6))", node, tenant, workspace, credential[:])
				nodeHTTPPrivateRefusal(t, x.admin, "/api/v1/admin/nodes/"+node, 404)
				nodeHTTPPrivateRefusal(t, x.portal, "/api/v1/workspaces/"+workspace+"/nodes/"+node, 404)
				page := x.admin.request("GET", "/api/v1/admin/nodes?limit=100", nil, 200, "NodeListResponse", nil)
				if len(page["items"].([]any)) != 3 {
					t.Fatal("foreign node escaped real HTTPS installed scope")
				}
			case "wrong_bearer_and_entrance":
				stranger := newClient(t, x.admin.s, "admin", x.admin.schemas)
				for _, path := range []string{"/api/v1/admin/nodes", "/api/v1/admin/nodes/" + x.nodes[0], "/api/v1/admin/node-join-tokens", "/api/v1/admin/nodes/" + x.nodes[0] + "/tasks"} {
					stranger.request("GET", path, nil, 401, "Problem", map[string]string{"Authorization": "Bearer " + security.Token()})
				}
				for _, path := range []string{"/api/v1/admin/nodes", "/api/v1/admin/node-join-tokens"} {
					nodeHTTPPrivateRefusal(t, x.portal, path, 404)
				}
			}
		})
	}
}

type nodeHTTPWireFault interface {
	Arm(commitfault.Mode) error
	Commits() int32
	Outcomes() <-chan commitfault.Observation
	Close(context.Context) error
}

func nodeHTTPFaultStore(t *testing.T, x pairedNodeHTTP) (transaction.Store, nodeHTTPWireFault) {
	t.Helper()
	closeProxy := func(proxy nodeHTTPWireFault) {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if e := proxy.Close(ctx); e != nil {
				t.Error("bounded actual node read proxy cleanup", e)
			}
		})
	}
	if x.f.Store.Dialect() == transaction.PostgreSQL {
		c := x.f.PostgreSQL.Config()
		if c.ConnConfig.TLSConfig != nil || len(c.ConnConfig.Fallbacks) != 0 || c.ConnConfig.Host != "127.0.0.1" {
			t.Fatal("only existing explicit plaintext loopback PG fixture allowed;never downgrade TLS")
		}
		proxy, e := commitfault.NewPlaintextPostgreSQL(c.ConnConfig.Host, c.ConnConfig.Port)
		if e != nil {
			t.Fatal(e)
		}
		closeProxy(proxy)
		proxied := c.Copy()
		proxied.ConnConfig.Port, proxied.MaxConns = proxy.Port(), 2
		pool, e := pgxpool.NewWithConfig(x.ctx, proxied)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(pool.Close)
		if e = pool.Ping(x.ctx); e != nil {
			t.Fatal("actual node read forwarding startup", e)
		}
		store, e := transaction.FromPostgreSQL(pool)
		if e != nil {
			t.Fatal(e)
		}
		return store, proxy
	}
	if x.f.Store.Dialect() != transaction.MySQL {
		t.Fatal("explicit actual paired engine required")
	}
	c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
	if e != nil {
		t.Fatal(e)
	}
	proxy, e := commitfault.NewMySQL(c)
	if e != nil {
		t.Fatal(e)
	}
	closeProxy(proxy)
	c.Port = proxy.Port()
	c.Policy.MaximumOpenConnections, c.Policy.MaximumIdleConnections = 2, 0
	kernel, observation, e := mysqlstore.Open(x.ctx, c)
	if e != nil || !observation.TLSAuthenticated {
		t.Fatal("actual verified TLS node read forwarding required", e)
	}
	t.Cleanup(func() {
		if e := kernel.Close(); e != nil {
			t.Error("actual node read forwarding pool cleanup", e)
		}
	})
	store, e := transaction.FromMySQL(kernel)
	if e != nil {
		t.Fatal(e)
	}
	return store, proxy
}
func nodeHTTPStoredDigest(t *testing.T, x pairedNodeHTTP) string {
	t.Helper()
	digest := sha256.New()
	queries := [][2]string{
		{`SELECT jsonb_build_array(id::text,tenant_id::text,workspace_id::text,name,status,version,encode(credential_hash,'hex'),protocol_version,facts,last_heartbeat_at,created_at,updated_at,execution_profile,capabilities,inference_fenced_at)::text FROM managed_nodes ORDER BY id`, `SELECT JSON_ARRAY(id,tenant_id,workspace_id,name,status,version,HEX(credential_hash),protocol_version,facts,last_heartbeat_at,created_at,updated_at,execution_profile,capabilities,inference_fenced_at) FROM managed_nodes ORDER BY id`},
		{`SELECT jsonb_build_array(id::text,tenant_id::text,workspace_id::text,node_name,encode(token_hash,'hex'),created_by::text,created_at,expires_at,consumed_at,revoked_at,execution_profile,node_id::text)::text FROM node_join_tokens ORDER BY id`, `SELECT JSON_ARRAY(id,tenant_id,workspace_id,node_name,HEX(token_hash),created_by,created_at,expires_at,consumed_at,revoked_at,execution_profile,node_id) FROM node_join_tokens ORDER BY id`},
		{`SELECT jsonb_build_array(id::text,tenant_id::text,workspace_id::text,node_id::text,operation,state,idempotency_key::text,requested_by::text,attempt,encode(lease_token_hash,'hex'),lease_expires_at,error_code,encode(completed_lease_hash,'hex'),encode(result_hash,'hex'),created_at,updated_at,deadline_at,completed_at,payload,encode(request_fingerprint,'hex'),inference_requested_epoch)::text FROM node_tasks ORDER BY id`, `SELECT JSON_ARRAY(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,attempt,HEX(lease_token_hash),lease_expires_at,error_code,HEX(completed_lease_hash),HEX(result_hash),created_at,updated_at,deadline_at,completed_at,payload,HEX(request_fingerprint),inference_requested_epoch) FROM node_tasks ORDER BY id`},
	}
	for _, query := range queries {
		rows, e := x.f.Store.Query(x.ctx, x.f.Statement(query[0], query[1]))
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				t.Fatal(e)
			}
			digest.Write(raw)
			digest.Write([]byte{'\n'})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// Only the node service uses the forwarding store. Ordinary authentication,
// CSRF and License admission retain their real independent original pools.
func TestM20PairedNodeReadHTTPUnknown(t *testing.T) {
	for _, operation := range []string{"admin_get", "admin_list", "admin_tokens", "admin_tasks", "portal_get", "portal_list", "portal_tasks"} {
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
					c, base := x.admin, "/api/v1/admin/nodes"
					if strings.HasPrefix(operation, "portal_") {
						c, base = x.portal, "/api/v1/workspaces/"+x.f.Workspace+"/nodes"
					}
					path, schema := base, "NodeListResponse"
					switch strings.SplitN(operation, "_", 2)[1] {
					case "get":
						path, schema = base+"/"+x.nodes[0], "NodeResponse"
					case "tokens":
						path, schema = "/api/v1/admin/node-join-tokens", "JoinTokenListResponse"
					case "tasks":
						path, schema = base+"/"+x.nodes[0]+"/tasks", "TaskListResponse"
					}
					before := nodeHTTPStoredDigest(t, x)
					store, proxy := nodeHTTPFaultStore(t, x)
					isolated := *x.owner
					isolated.Storage, isolated.DB = store, nil
					scope := &workspaces.Service{Identity: &isolated}
					original := x.handler.Nodes.Service
					x.handler.Nodes.Service = &nodes.Service{Identity: &isolated, Workspaces: scope}
					if e := proxy.Arm(fault.mode); e != nil {
						t.Fatal(e)
					}
					request, e := http.NewRequestWithContext(x.ctx, "GET", c.s.URL+path, nil)
					if e != nil {
						t.Fatal(e)
					}
					request.Host = c.audience + ".example.invalid"
					request.Header.Set("Origin", "https://"+request.Host)
					request.Header.Set("X-CSRF-Token", c.csrf)
					for _, cookie := range c.cookies {
						request.AddCookie(cookie)
					}
					response, e := c.s.Client().Do(request)
					if e != nil {
						t.Fatal("actual trusted TLS unknown node response", e)
					}
					raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
					response.Body.Close()
					if e != nil || len(raw) == 1<<20 || response.StatusCode != 409 || response.Header.Get("Retry-After") != "" || response.Header.Get("Content-Disposition") != "" || response.Header.Get("X-Artifact-SHA256") != "" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Request-ID") == "" {
						t.Fatal("unknown node response lost safe HTTP contract", response.StatusCode, e)
					}
					var problem map[string]any
					if json.Unmarshal(raw, &problem) != nil || problem["code"] != "STORAGE_COMMIT_OUTCOME_UNKNOWN" || problem["request_id"] != response.Header.Get("X-Request-ID") || problem["data"] != nil || problem["items"] != nil || problem["next_cursor"] != nil || problem["success"] != nil {
						t.Fatal("unknown node response exposed private success or lost request ID")
					}
					if e = c.schemas["Problem"].Validate(problem); e != nil {
						t.Fatal("unknown node Problem schema", e)
					}
					nodeHTTPSNoSecrets(t, problem)
					select {
					case observed := <-proxy.Outcomes():
						if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
							t.Fatal("actual node HTTPS COMMIT fault not established", observed)
						}
					case <-x.ctx.Done():
						t.Fatal("actual node HTTPS wire observation missing")
					}
					if proxy.Commits() != 1 || nodeHTTPStoredDigest(t, x) != before {
						t.Fatal("uncertain HTTP read resent node COMMIT or changed persisted node history")
					}
					x.handler.Nodes.Service = original
					recovered := c.request("GET", path, nil, 200, schema, nil)
					nodeHTTPSNoSecrets(t, recovered)
					if proxy.Commits() != 1 || nodeHTTPStoredDigest(t, x) != before {
						t.Fatal("explicit new HTTPS read retried proxy or changed node data")
					}
				})
			}
		})
	}
}

//go:build repository_integration && privatecluster_http_integration

package http_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/privateclusters"
	clusterhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/privateclusters/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

// Only the License owner uses this explicit synthetic clock. Session/CSRF/TLS
// verification and the host clock retain their ordinary production behavior.
type privateClusterHTTPClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *privateClusterHTTPClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}
func (c *privateClusterHTTPClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
}

type privateClusterHTTPStory struct {
	t             *testing.T
	ctx           context.Context
	f             repositoryfixture.Fixture
	owner         *identity.Service
	handler       *transport.Handler
	server        *httptest.Server
	clock         *privateClusterHTTPClock
	base          time.Time
	admin, portal *privateClusterHTTPClient
	connection    string
	configuration privateclusters.Config
	schemas       map[string]*jsonschema.Schema
}
type privateClusterHTTPClient struct {
	story    *privateClusterHTTPStory
	audience string
	cookies  map[string]*http.Cookie
	csrf     string
	auth     identity.Auth
	http     *http.Client
}
type privateClusterHTTPResult struct {
	data      json.RawMessage
	requestID string
	problem   map[string]any
}

func newPrivateClusterHTTPStory(t *testing.T) *privateClusterHTTPStory {
	t.Helper()
	f := repositoryfixture.New(t)
	if f.PostgreSQL == nil || f.Store.Dialect() != transaction.PostgreSQL {
		t.Fatal("explicit actual isolated PostgreSQL required; no dialect substitution")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	root := t.TempDir() // The native executor owns its external TMPDIR.
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal("private synthetic HTTP root", e)
	}
	key, trust, e := licensewire.GenerateKey("explicit-m28-synthetic-http")
	if e != nil {
		t.Fatal(e)
	}
	trustBytes, e := json.Marshal(trust)
	if e != nil {
		t.Fatal(e)
	}
	trustFile := filepath.Join(root, "license-public-trust.json")
	if e = os.WriteFile(trustFile, trustBytes, 0600); e != nil {
		t.Fatal(e)
	}
	policy := config.DefaultRuntimePolicy()
	policy.HTTP.RequestSeconds = 11
	c := config.Config{Runtime: &policy, Licensing: &config.LicensingConfiguration{TrustFile: trustFile}}
	c.Server.AdminOrigin, c.Server.PortalOrigin = "https://admin.example.invalid", "https://portal.example.invalid"
	owner := &identity.Service{DB: f.PostgreSQL, Config: c, HMACKey: make([]byte, 32)}
	if _, e = rand.Read(owner.HMACKey); e != nil {
		t.Fatal(e)
	}
	if _, e = f.PostgreSQL.Exec(ctx, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')`, f.User, f.Tenant, f.Workspace); e != nil {
		t.Fatal("actual synthetic membership insert", e)
	}
	h := transport.New(owner)
	if e = h.InitializationError(); e != nil {
		t.Fatal("original handler construction", e)
	}
	base := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	clock := &privateClusterHTTPClock{at: base}
	licensed, e := licensing.New(owner, trust, owner.Config.LicensePolicy(), clock.now)
	if e != nil {
		t.Fatal(e)
	}
	// Handler.New constructs a real owner and assigns BusinessAdmission. Replace
	// both references after construction, then reuse the same object in M28.
	h.Licensing.Service, owner.BusinessAdmission = licensed, licensed
	g := &privateClusterHTTPStory{t: t, ctx: ctx, f: f, owner: owner, handler: h, clock: clock, base: base,
		schemas: httpfixture.Schemas(t, "../../../../../../api/openapi/v1/privateclusters/openapi.json")}
	issued := map[string]identity.IssuedSession{}
	if e = f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		for _, audience := range []string{"admin", "portal"} {
			v, e := owner.IssueSessionWithin(ctx, q, f.User, 1, audience, "", uuid.NewString(), nil)
			if e != nil {
				return e
			}
			issued[audience] = v
		}
		return nil
	}); e != nil {
		t.Fatal("actual synthetic session issuance", e)
	}
	clients := map[string]*privateClusterHTTPClient{}
	for _, audience := range []string{"admin", "portal"} {
		auth, e := owner.Authenticate(ctx, issued[audience].Token, audience)
		if e != nil {
			t.Fatal("actual issued session authentication", e)
		}
		name := "__Host-" + audience + "_session"
		clients[audience] = &privateClusterHTTPClient{story: g, audience: audience, auth: auth,
			cookies: map[string]*http.Cookie{name: {Name: name, Value: issued[audience].Token, Secure: true, HttpOnly: true, Path: "/"}}}
	}
	g.admin, g.portal = clients["admin"], clients["portal"]
	state, e := licensed.StatusAdmin(ctx, g.admin.auth)
	if e != nil || state.State != licensing.Missing {
		t.Fatal("actual initial License state", e)
	}
	cfg := privateclusters.Defaults()
	cfg.Policy.RequestSeconds, cfg.Policy.OperationSeconds = 2, 8
	cfg.Policy.MaximumBodyBytes, cfg.Policy.PageSize = 8192, 2
	cfg.Verification.MountRoot = root
	cfg.Verification.TrustFile = filepath.Join(root, "absent-manufacturer-trust.json")
	profile := privateclusters.Profile{ID: "synthetic-http", Name: "Synthetic SQL connection; no cluster observation",
		ServerOrigin: "https://127.0.0.1:1", TLSServerName: "127.0.0.1", AllowedCIDRs: []string{"127.0.0.0/8"},
		CAFile: filepath.Join(root, "absent-provider-ca.pem"), TokenFile: filepath.Join(root, "absent-provider-token"),
		ImageProofFile: filepath.Join(root, "absent-image-proof.json"), ClusterUID: uuid.NewString(),
		Namespace: "projectone-test-synthetic-http", NamespaceUID: uuid.NewString(), ControlServiceAccountUID: uuid.NewString(),
		InstallationID: state.DeploymentID, WorkspaceID: f.Workspace, Profile: privateclusters.LocalARM64Profile,
		ImageRepository: "synthetic.example.invalid/probe"}
	cfg.Profiles = []privateclusters.Profile{profile}
	g.configuration = cfg
	cluster, e := privateclusters.New(owner, h.Workspaces.Service, licensed, cfg)
	if e != nil || !cluster.UsesOwners(owner, h.Workspaces.Service, licensed) {
		t.Fatal("actual matching M28 owners", e)
	}
	h.PrivateClusters = &clusterhttp.Handler{Service: cluster}
	g.connection = g.seedConnection(f.Tenant, f.Workspace, profile)
	// Dial the loopback httptest URL. Each audience client sets SNI to its
	// synthetic Host so the dispatcher can admit the request. The certificate is
	// the synthetic test certificate, not an advertised DNS-origin certificate.
	g.serve()
	for _, client := range []*privateClusterHTTPClient{g.admin, g.portal} {
		var csrf struct {
			Token string `json:"token"`
		}
		result := client.call(http.MethodGet, "/api/v1/auth/csrf", nil, 200, "Csrf", nil)
		if json.Unmarshal(result.data, &csrf) != nil || csrf.Token == "" {
			t.Fatal("actual CSRF token response")
		}
		client.csrf = csrf.Token
	}
	claims := licensewire.Claims{FormatVersion: licensewire.FormatVersion, LicenseID: uuid.NewString(),
		CustomerReference: "Explicit synthetic M28 HTTP License only", DeploymentID: state.DeploymentID,
		IssuedAt: base.Add(-time.Minute).Format(time.RFC3339), NotBefore: base.Add(-time.Minute).Format(time.RFC3339),
		ExpiresAt: base.Add(time.Minute).Format(time.RFC3339), GraceUntil: base.Add(2 * time.Minute).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
	artifact, e := licensewire.Issue(claims, key)
	if e != nil {
		t.Fatal(e)
	}
	g.admin.call(http.MethodPost, "/api/v1/admin/license", licensing.ImportInput{Artifact: string(artifact), Reason: "Explicit synthetic M28 HTTP fixture"}, 200, "", nil)
	var active licensing.State
	if json.Unmarshal(g.admin.call(http.MethodGet, "/api/v1/admin/license", nil, 200, "", nil).data, &active) != nil || active.State != licensing.Active || !active.NewBusinessAllowed || active.AcceptedLicenses != 1 {
		t.Fatal("real synthetic signed License did not become active")
	}
	return g
}

func (g *privateClusterHTTPStory) serve() {
	g.t.Helper()
	g.server = accountTLSServer(g.t, g.handler)
	for _, client := range []*privateClusterHTTPClient{g.admin, g.portal} {
		client.bindTransport()
	}
}

func (c *privateClusterHTTPClient) bindTransport() {
	g := c.story
	g.t.Helper()
	host := c.audience + ".example.invalid"
	if g.server.Certificate() == nil || g.server.Certificate().VerifyHostname(host) != nil {
		g.t.Fatal("actual audience certificate name")
	}
	base, ok := g.server.Client().Transport.(*http.Transport)
	if !ok || base == nil || base.TLSClientConfig == nil || base.TLSClientConfig.RootCAs == nil || base.TLSClientConfig.InsecureSkipVerify {
		g.t.Fatal("actual loopback certificate trust")
	}
	transport := base.Clone()
	transport.TLSClientConfig = base.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = host
	if c.http != nil {
		c.http.CloseIdleConnections()
	}
	c.http = &http.Client{
		Timeout:       config.Seconds(g.owner.Config.Policy().HTTP.RequestSeconds),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     transport,
	}
	g.t.Cleanup(c.http.CloseIdleConnections)
}

func (g *privateClusterHTTPStory) seedConnection(tenant, workspace string, p privateclusters.Profile) string {
	g.t.Helper()
	id, digest := uuid.NewString(), strings.Repeat("a", 64)
	// These direct SQL rows test existing projections and safety. NULL observation
	// makes no successful API/Kubernetes discovery or manufacturer approval claim.
	_, e := g.f.PostgreSQL.Exec(g.ctx, `INSERT INTO private_cluster_connections(id,tenant_id,workspace_id,profile_id,cluster_uid,namespace_uid,namespace_name,installation_id,profile,config_sha256,credential_sha256,manifest_sha256,image_digest,config_digest,state,revision,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10,$10,$11,$11,'active',1,$12)`,
		id, tenant, workspace, p.ID, p.ClusterUID, p.NamespaceUID, p.Namespace, p.InstallationID, p.Profile, digest, "sha256:"+digest, g.f.User)
	if e != nil {
		g.t.Fatal("actual synthetic private-cluster row", e)
	}
	return id
}
func (g *privateClusterHTTPStory) path(audience, workspace, suffix string) string {
	if audience == "admin" {
		return "/api/v1/admin/private-clusters" + suffix + "?workspace_id=" + workspace
	}
	return "/api/v1/workspaces/" + workspace + "/private-clusters" + suffix
}
func (c *privateClusterHTTPClient) call(method, path string, body any, status int, schema string, headers map[string]string) privateClusterHTTPResult {
	g := c.story
	g.t.Helper()
	var raw []byte
	if body != nil {
		var e error
		raw, e = json.Marshal(body)
		if e != nil {
			g.t.Fatal(e)
		}
	}
	r, e := http.NewRequestWithContext(g.ctx, method, g.server.URL+path, bytes.NewReader(raw))
	if e != nil {
		g.t.Fatal(e)
	}
	r.Host = c.audience + ".example.invalid"
	r.Header.Set("Origin", "https://"+r.Host)
	if raw != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" {
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	for _, cookie := range c.cookies {
		r.AddCookie(cookie)
	}
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	if c.http == nil {
		g.t.Fatal("actual audience TLS client")
	}
	response, e := c.http.Do(r)
	if e != nil {
		g.t.Fatal("actual verified loopback TLS request", method, e)
	}
	defer response.Body.Close()
	raw, e = io.ReadAll(io.LimitReader(response.Body, privateclusters.MaximumBodyBytes+1))
	if e != nil || int64(len(raw)) > privateclusters.MaximumBodyBytes || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
		g.t.Fatal("actual bounded verified TLS response", e)
	}
	request := response.Header.Get("X-Request-ID")
	id, e := uuid.Parse(request)
	media := "application/json"
	if status >= 400 {
		media = "application/problem+json"
	}
	if e != nil || id == uuid.Nil || id.String() != request || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Content-Type") != media || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		g.t.Fatal("original response security metadata", method, path, response.StatusCode, response.Header.Get("Content-Type"))
	}
	for _, cookie := range response.Cookies() {
		if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" {
			g.t.Fatal("unsafe original session/CSRF cookie")
		}
		if cookie.MaxAge < 0 {
			delete(c.cookies, cookie.Name)
		} else {
			c.cookies[cookie.Name] = cookie
		}
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		g.t.Fatal("non-JSON original response")
	}
	if response.StatusCode != status {
		var code string
		_ = json.Unmarshal(envelope["code"], &code)
		g.t.Fatal("actual HTTP status", method, path, "wanted", status, "observed", response.StatusCode, code)
	}
	out := privateClusterHTTPResult{requestID: request}
	if status >= 400 {
		if json.Unmarshal(raw, &out.problem) != nil || g.schemas["Problem"].Validate(out.problem) != nil || out.problem["request_id"] != request || out.problem["status"] != float64(status) {
			g.t.Fatal("closed canonical Problem/request contract")
		}
		for _, forbidden := range []string{"data", "success"} {
			if _, ok := envelope[forbidden]; ok {
				g.t.Fatal("error retained successful projection")
			}
		}
	} else {
		if len(envelope) != 1 || envelope["data"] == nil {
			g.t.Fatal("closed original data envelope")
		}
		out.data = envelope["data"]
		if schema != "" {
			var value any
			if json.Unmarshal(out.data, &value) != nil || g.schemas[schema].Validate(value) != nil {
				g.t.Fatal("closed M28 response contract", schema)
			}
		}
	}
	for _, protected := range []string{g.configuration.Verification.TrustFile, g.configuration.Profiles[0].CAFile, g.configuration.Profiles[0].TokenFile, g.configuration.Profiles[0].ImageProofFile, g.configuration.Profiles[0].ServerOrigin, "m28_http_audit_failure"} {
		if bytes.Contains(raw, []byte(protected)) {
			g.t.Fatal("protected provider or SQL diagnostic leaked")
		}
	}
	return out
}
func (c *privateClusterHTTPClient) problem(method, path string, body any, status int, code string, headers map[string]string) {
	c.story.t.Helper()
	if result := c.call(method, path, body, status, "Problem", headers); result.problem["code"] != code {
		c.story.t.Fatal("canonical Problem code", code, result.problem["code"])
	}
}
func (g *privateClusterHTTPStory) snapshot() string {
	g.t.Helper()
	var raw []byte
	e := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT jsonb_build_array(
	 (SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY id),'[]'::jsonb) FROM private_cluster_connections c),
	 (SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY id),'[]'::jsonb) FROM private_cluster_runs r),
	 (SELECT COALESCE(jsonb_agg(to_jsonb(i) ORDER BY tenant_id,workspace_id,actor_id,idempotency_key),'[]'::jsonb) FROM private_cluster_intents i),
	 (SELECT COALESCE(jsonb_agg(to_jsonb(a) ORDER BY id),'[]'::jsonb) FROM audit_events a WHERE action LIKE 'private_cluster.%'))`).Scan(&raw)
	if e != nil {
		g.t.Fatal("actual domain/audit snapshot", e)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func (g *privateClusterHTTPStory) unchanged(before string) {
	g.t.Helper()
	if g.snapshot() != before {
		g.t.Fatal("refusal/read changed connection, run, intent or scoped audit")
	}
}
func (g *privateClusterHTTPStory) read(c *privateClusterHTTPClient, state string, revision int64) privateclusters.Connection {
	g.t.Helper()
	path := g.path(c.audience, g.f.Workspace, "/connections")
	var page privateclusters.Page[privateclusters.Connection]
	if json.Unmarshal(c.call(http.MethodGet, path, nil, 200, "ConnectionPage", nil).data, &page) != nil || len(page.Items) != 1 || page.NextCursor != nil || page.Items[0].ID != g.connection {
		g.t.Fatal("actual scoped connection list")
	}
	var connection privateclusters.Connection
	path = g.path(c.audience, g.f.Workspace, "/connections/"+g.connection)
	if json.Unmarshal(c.call(http.MethodGet, path, nil, 200, "Connection", nil).data, &connection) != nil || connection.WorkspaceID != g.f.Workspace || connection.ID != g.connection || connection.State != state || connection.Revision != revision || connection.Observation != nil || connection.ActiveRunID != nil {
		g.t.Fatal("actual persisted scoped connection projection")
	}
	var runs privateclusters.Page[privateclusters.Run]
	path = g.path(c.audience, g.f.Workspace, "/connections/"+g.connection+"/runs")
	if json.Unmarshal(c.call(http.MethodGet, path, nil, 200, "RunPage", nil).data, &runs) != nil || len(runs.Items) != 0 || runs.NextCursor != nil {
		g.t.Fatal("read invented probe execution history")
	}
	return connection
}
func (g *privateClusterHTTPStory) disable(key, reason string, revision int64) privateClusterHTTPResult {
	g.t.Helper()
	return g.admin.call(http.MethodPost, g.path("admin", g.f.Workspace, "/connections/"+g.connection+"/disable"),
		map[string]any{"reason": reason, "expected_revision": revision}, 200, "Connection", map[string]string{"Idempotency-Key": key})
}
func (g *privateClusterHTTPStory) committedDisable(key, reason, request string) {
	g.t.Helper()
	var intents, audits, runs int
	var target, actor, operation string
	var revision int64
	if e := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT count(*) FROM private_cluster_intents`).Scan(&intents); e != nil || intents != 1 {
		g.t.Fatal("actual disable intent count", intents, e)
	}
	if e := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT target_id::text,actor_id::text,operation,result_revision FROM private_cluster_intents WHERE tenant_id=$1 AND workspace_id=$2 AND actor_id=$3 AND idempotency_key=$4`, g.f.Tenant, g.f.Workspace, g.f.User, key).Scan(&target, &actor, &operation, &revision); e != nil || target != g.connection || actor != g.f.User || operation != "disable" || revision != 2 {
		g.t.Fatal("actual disable intent binding", e)
	}
	if e := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT count(*) FROM audit_events WHERE action='private_cluster.disable'`).Scan(&audits); e != nil || audits != 1 {
		g.t.Fatal("actual disable audit count", audits, e)
	}
	if e := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT count(*) FROM audit_events WHERE action='private_cluster.disable' AND actor_user_id=$1 AND audience='admin' AND outcome='success' AND target_type='private_cluster' AND target_id=$2 AND reason=$3 AND request_id=$4 AND tenant_id=$5 AND workspace_id=$6`, g.f.User, g.connection, reason, request, g.f.Tenant, g.f.Workspace).Scan(&audits); e != nil || audits != 1 {
		g.t.Fatal("actual atomic actor/Workspace/request/reason audit", e)
	}
	if e := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT count(*) FROM private_cluster_runs`).Scan(&runs); e != nil || runs != 0 {
		g.t.Fatal("disable created a probe run", e)
	}
	for _, client := range []*privateClusterHTTPClient{g.admin, g.portal} {
		g.read(client, "disabled", 2)
	}
}

func TestActualNativePrivateClusterHTTP(t *testing.T) {
	cases := []struct {
		name string
		run  func(*privateClusterHTTPStory)
	}{
		{"dual_audience_read_and_public_policy", privateClusterHTTPRead},
		{"origin_csrf_and_entrance_refusal", privateClusterHTTPTransportRefusal},
		{"foreign_workspace_and_connection_isolation", privateClusterHTTPIsolation},
		{"revoked_sessions_refused", privateClusterHTTPRevokedSessions},
		{"revoked_workspace_and_platform_authority", privateClusterHTTPRevokedAuthority},
		{"disable_atomic_audit_and_exact_replay", privateClusterHTTPDisableReplay},
		{"audit_failure_rolls_back_disable", privateClusterHTTPAuditRollback},
		{"expired_license_read_disable_and_new_work_refusal", privateClusterHTTPExpiredLicense},
	}
	for _, entry := range cases {
		t.Run(entry.name, func(t *testing.T) {
			entry.run(newPrivateClusterHTTPStory(t))
			t.Log("actual_selected_PostgreSQL_original_dispatcher_loopback_TLS=true synthetic_SQL_and_License=true Kubernetes_OCI_probe_full_server_browser=false")
		})
	}
}

func privateClusterHTTPRead(g *privateClusterHTTPStory) {
	before := g.snapshot()
	for _, client := range []*privateClusterHTTPClient{g.admin, g.portal} {
		g.read(client, "active", 1)
		var runtime map[string]json.RawMessage
		if json.Unmarshal(client.call(http.MethodGet, "/runtime-config.json", nil, 200, "", nil).data, &runtime) != nil {
			g.t.Fatal("actual public runtime envelope")
		}
		var policy map[string]any
		if json.Unmarshal(runtime["private_clusters"], &policy) != nil || len(policy) != 3 || policy["enabled"] != true || policy["operation_timeout_milliseconds"] != float64(8000) || policy["maximum_response_bytes"] != float64(8192) || bytes.Contains(runtime["private_clusters"], []byte("file")) {
			g.t.Fatal("nondefault public M28 policy")
		}
	}
	var profiles []privateclusters.ProfileChoice
	if json.Unmarshal(g.admin.call(http.MethodGet, g.path("admin", g.f.Workspace, "/profiles"), nil, 200, "Profiles", nil).data, &profiles) != nil || len(profiles) != 1 || profiles[0].ID != g.configuration.Profiles[0].ID {
		g.t.Fatal("actual public profile choices")
	}
	// Join the original server before changing composition. A response read is
	// not used as a substitute for joining a handler still using shared fields.
	for _, client := range []*privateClusterHTTPClient{g.admin, g.portal} {
		client.http.CloseIdleConnections()
	}
	g.server.Close()
	g.handler.PrivateClusters = nil
	g.serve()
	for _, client := range []*privateClusterHTTPClient{g.admin, g.portal} {
		var runtime map[string]json.RawMessage
		if json.Unmarshal(client.call(http.MethodGet, "/runtime-config.json", nil, 200, "", nil).data, &runtime) != nil {
			g.t.Fatal("disabled public envelope")
		}
		var policy map[string]any
		if json.Unmarshal(runtime["private_clusters"], &policy) != nil || len(policy) != 3 || policy["enabled"] != false || policy["operation_timeout_milliseconds"] != float64(0) || policy["maximum_response_bytes"] != float64(0) {
			g.t.Fatal("disabled public policy did not retain false/zero")
		}
		client.problem(http.MethodGet, g.path(client.audience, g.f.Workspace, "/connections"), nil, 503, "PRIVATE_CLUSTER_OWNER_REQUIRED", nil)
	}
	g.unchanged(before)
}
func privateClusterHTTPTransportRefusal(g *privateClusterHTTPStory) {
	before := g.snapshot()
	path := g.path("admin", g.f.Workspace, "/connections/"+g.connection+"/disable")
	body := map[string]any{"reason": "Synthetic transport refusal", "expected_revision": 1}
	for _, input := range []struct{ header, value, code string }{
		{"Origin", "", "ORIGIN_REJECTED"}, {"Origin", "https://foreign.example.invalid", "ORIGIN_REJECTED"},
		{"Sec-Fetch-Site", "cross-site", "ORIGIN_REJECTED"}, {"X-CSRF-Token", "", "CSRF_INVALID"}, {"X-CSRF-Token", "invalid", "CSRF_INVALID"},
	} {
		g.admin.problem(http.MethodPost, path, body, 403, input.code, map[string]string{input.header: input.value, "Idempotency-Key": uuid.NewString()})
	}
	get := g.path("admin", g.f.Workspace, "/connections")
	g.admin.problem(http.MethodGet, get, nil, 403, "ORIGIN_REJECTED", map[string]string{"Origin": "https://foreign.example.invalid"})
	g.portal.problem(http.MethodGet, get, nil, 404, "RESOURCE_NOT_FOUND", nil)
	g.portal.problem(http.MethodPost, path, body, 404, "RESOURCE_NOT_FOUND", map[string]string{"Idempotency-Key": uuid.NewString()})
	anonymous := &privateClusterHTTPClient{story: g, audience: "admin", cookies: map[string]*http.Cookie{}}
	// Admin audience certificate and an empty session map.
	anonymous.bindTransport()
	anonymous.problem(http.MethodGet, get, nil, 401, "AUTH_REQUIRED", nil)
	// Correct cookie name with the other real audience's token still has no
	// matching server-side session; this is not a browser-port isolation claim.
	anonymous.cookies["__Host-admin_session"] = &http.Cookie{Name: "__Host-admin_session", Value: g.portal.cookies["__Host-portal_session"].Value}
	anonymous.problem(http.MethodGet, get, nil, 401, "AUTH_SESSION_EXPIRED", nil)
	g.unchanged(before)
}
func privateClusterHTTPIsolation(g *privateClusterHTTPStory) {
	tenant, foreign, ungranted := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, command := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign tenant','active')`, []any{tenant}},
		{`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign Workspace','active')`, []any{foreign, tenant}},
		{`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic ungranted Workspace','active')`, []any{ungranted, g.f.Tenant}},
	} {
		if _, e := g.f.PostgreSQL.Exec(g.ctx, command.query, command.args...); e != nil {
			g.t.Fatal("actual foreign isolation fixture", e)
		}
	}
	profile := g.configuration.Profiles[0]
	profile.ID, profile.ClusterUID, profile.NamespaceUID = "synthetic-foreign-http", uuid.NewString(), uuid.NewString()
	foreignConnection := g.seedConnection(tenant, foreign, profile)
	before := g.snapshot()
	for _, client := range []*privateClusterHTTPClient{g.admin, g.portal} {
		for _, suffix := range []string{"/connections", "/connections/" + foreignConnection, "/connections/" + foreignConnection + "/runs"} {
			client.problem(http.MethodGet, g.path(client.audience, foreign, suffix), nil, 404, "RESOURCE_NOT_FOUND", nil)
		}
		for _, suffix := range []string{"/connections/" + foreignConnection, "/connections/" + foreignConnection + "/runs"} {
			client.problem(http.MethodGet, g.path(client.audience, g.f.Workspace, suffix), nil, 404, "RESOURCE_NOT_FOUND", nil)
		}
		g.read(client, "active", 1)
	}
	g.portal.problem(http.MethodGet, g.path("portal", ungranted, "/connections"), nil, 404, "RESOURCE_NOT_FOUND", nil)
	g.admin.problem(http.MethodPost, g.path("admin", foreign, "/connections/"+foreignConnection+"/disable"), map[string]any{"reason": "Synthetic foreign refusal", "expected_revision": 1}, 404, "RESOURCE_NOT_FOUND", map[string]string{"Idempotency-Key": uuid.NewString()})
	g.unchanged(before)
}
func privateClusterHTTPRevokedSessions(g *privateClusterHTTPStory) {
	if _, e := g.f.PostgreSQL.Exec(g.ctx, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_m28_http' WHERE id IN($1,$2)`, g.admin.auth.SessionID, g.portal.auth.SessionID); e != nil {
		g.t.Fatal("actual paired session revocation", e)
	}
	before := g.snapshot()
	for _, client := range []*privateClusterHTTPClient{g.admin, g.portal} {
		client.problem(http.MethodGet, g.path(client.audience, g.f.Workspace, "/connections"), nil, 401, "AUTH_SESSION_EXPIRED", nil)
	}
	g.admin.problem(http.MethodPost, g.path("admin", g.f.Workspace, "/connections/"+g.connection+"/disable"), map[string]any{"reason": "Synthetic revoked refusal", "expected_revision": 1}, 401, "AUTH_SESSION_EXPIRED", map[string]string{"Idempotency-Key": uuid.NewString()})
	g.unchanged(before)
}
func privateClusterHTTPRevokedAuthority(g *privateClusterHTTPStory) {
	if _, e := g.f.PostgreSQL.Exec(g.ctx, `DELETE FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3`, g.f.Tenant, g.f.Workspace, g.f.User); e != nil {
		g.t.Fatal("actual membership removal", e)
	}
	before := g.snapshot()
	g.portal.problem(http.MethodGet, g.path("portal", g.f.Workspace, "/connections"), nil, 404, "RESOURCE_NOT_FOUND", nil)
	if _, e := g.f.PostgreSQL.Exec(g.ctx, `UPDATE users SET is_platform_admin=false WHERE id=$1`, g.f.User); e != nil {
		g.t.Fatal("actual platform authority revocation", e)
	}
	g.admin.problem(http.MethodGet, g.path("admin", g.f.Workspace, "/connections"), nil, 401, "AUTH_SESSION_EXPIRED", nil)
	g.admin.problem(http.MethodPost, g.path("admin", g.f.Workspace, "/connections/"+g.connection+"/disable"), map[string]any{"reason": "Synthetic demoted refusal", "expected_revision": 1}, 401, "AUTH_SESSION_EXPIRED", map[string]string{"Idempotency-Key": uuid.NewString()})
	g.unchanged(before)
}
func privateClusterHTTPDisableReplay(g *privateClusterHTTPStory) {
	key, reason := uuid.NewString(), "Explicit synthetic current-Admin safety disable"
	first := g.disable(key, reason, 1)
	g.committedDisable(key, reason, first.requestID)
	before := g.snapshot()
	if repeated := g.disable(key, reason, 1); !bytes.Equal(repeated.data, first.data) || repeated.requestID == first.requestID {
		g.t.Fatal("exact replay changed committed projection or reused server request ID")
	}
	g.admin.problem(http.MethodPost, g.path("admin", g.f.Workspace, "/connections/"+g.connection+"/disable"), map[string]any{"reason": reason + " changed", "expected_revision": 1}, 409, "IDEMPOTENCY_CONFLICT", map[string]string{"Idempotency-Key": key})
	g.unchanged(before)
	g.committedDisable(key, reason, first.requestID)
}
func privateClusterHTTPAuditRollback(g *privateClusterHTTPStory) {
	for _, query := range []string{
		`CREATE FUNCTION m28_http_refuse_disable_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='private_cluster.disable' THEN RAISE EXCEPTION 'm28_http_audit_failure'; END IF; RETURN NEW; END $$`,
		`CREATE TRIGGER m28_http_refuse_disable_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION m28_http_refuse_disable_audit()`,
	} {
		if _, e := g.f.PostgreSQL.Exec(g.ctx, query); e != nil {
			g.t.Fatal("actual test-schema audit refusal installation", e)
		}
	}
	before, key, reason := g.snapshot(), uuid.NewString(), "Explicit synthetic audited rollback recovery"
	g.admin.problem(http.MethodPost, g.path("admin", g.f.Workspace, "/connections/"+g.connection+"/disable"), map[string]any{"reason": reason, "expected_revision": 1}, 503, "STORAGE_UNAVAILABLE", map[string]string{"Idempotency-Key": key})
	g.unchanged(before)
	for _, query := range []string{`DROP TRIGGER m28_http_refuse_disable_audit ON audit_events`, `DROP FUNCTION m28_http_refuse_disable_audit()`} {
		if _, e := g.f.PostgreSQL.Exec(g.ctx, query); e != nil {
			g.t.Fatal("exact test-schema audit refusal removal", e)
		}
	}
	result := g.disable(key, reason, 1) // Explicit recovery after known SQL rollback.
	g.committedDisable(key, reason, result.requestID)
}
func privateClusterHTTPExpiredLicense(g *privateClusterHTTPStory) {
	g.clock.set(g.base.Add(3 * time.Minute))
	var expired licensing.State
	if json.Unmarshal(g.admin.call(http.MethodGet, "/api/v1/admin/license", nil, 200, "", nil).data, &expired) != nil || expired.State != licensing.Expired || expired.NewBusinessAllowed || expired.AcceptedLicenses != 1 {
		g.t.Fatal("actual signed License did not expire under explicit test clock")
	}
	before := g.snapshot()
	for _, client := range []*privateClusterHTTPClient{g.admin, g.portal} {
		g.read(client, "active", 1)
		for _, suffix := range []string{"/connections/" + g.connection + "/runs", "/connections/" + g.connection + "/runs/" + uuid.NewString() + "/continue"} {
			client.problem(http.MethodPost, g.path(client.audience, g.f.Workspace, suffix), map[string]any{"reason": "Synthetic expired new-work refusal", "expected_revision": 1}, 403, "LICENSE_NEW_BUSINESS_UNAVAILABLE", map[string]string{"Idempotency-Key": uuid.NewString()})
		}
	}
	for _, action := range []string{"bind", "observe", "rebind"} {
		body := map[string]any{"reason": "Synthetic expired new-work refusal", "expected_revision": 1}
		suffix := "/connections/" + g.connection + "/" + action
		if action == "bind" {
			suffix, body["profile_id"], body["expected_revision"] = "/connections", g.configuration.Profiles[0].ID, 0
		}
		g.admin.problem(http.MethodPost, g.path("admin", g.f.Workspace, suffix), body, 403, "LICENSE_NEW_BUSINESS_UNAVAILABLE", map[string]string{"Idempotency-Key": uuid.NewString()})
	}
	g.unchanged(before)
	key, reason := uuid.NewString(), "Explicit synthetic safety disable after real signed License expiry"
	first := g.disable(key, reason, 1)
	g.committedDisable(key, reason, first.requestID)
	before = g.snapshot()
	if repeated := g.disable(key, reason, 1); !bytes.Equal(first.data, repeated.data) {
		g.t.Fatal("expired License blocked or altered exact safety replay")
	}
	g.unchanged(before)
}

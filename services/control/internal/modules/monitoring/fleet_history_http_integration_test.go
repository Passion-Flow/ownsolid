//go:build repository_integration && monitoring_integration && linux

package monitoring_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	monitoringhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/monitoringmetrics"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// This small profile qualifies the registered module through the original browser
// dispatcher. Native stores supply successful data; explicit fault proxies supply
// failures. It does not replace the separate capacity or installed-delivery gates.
func TestFleetHistoryAuthenticatedHTTPOffline(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_DSN_FILE") == "" {
		t.Fatal("the authenticated offline fixture requires isolated PostgreSQL")
	}
	accounts := identityfixture.New(t)
	password := identityfixture.Bootstrap(t, accounts)
	ctx := t.Context()
	execSQL := func(query string, args ...any) {
		t.Helper()
		if _, err := accounts.DB.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := accounts.DB.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var tenant, workspace string
	if err := accounts.DB.QueryRow(ctx, "SELECT tenant_id::text,id::text FROM workspaces ORDER BY created_at,id LIMIT 1").Scan(&tenant, &workspace); err != nil {
		t.Fatal(err)
	}
	foreignTenant, foreignWorkspace, otherWorkspace := uuid.NewString(), uuid.NewString(), uuid.NewString()
	execSQL("INSERT INTO tenants(id,name,status) VALUES($1,'Foreign synthetic tenant','active')", foreignTenant)
	execSQL("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign synthetic workspace','active')", foreignWorkspace, foreignTenant)
	execSQL("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other synthetic workspace','active')", otherWorkspace, tenant)

	raw, rawDisk, _ := startHistoryVM(t, ctx, "2d")
	long, longDisk, _ := startHistoryVM(t, ctx, "90d")
	var rawReads atomic.Int32
	var rawFailure, longMode atomic.Int32
	var onRawRead atomic.Pointer[func()]
	rawURL, _ := url.Parse(raw)
	longURL, _ := url.Parse(long)
	rawProxy, longProxy := httputil.NewSingleHostReverseProxy(rawURL), httputil.NewSingleHostReverseProxy(longURL)
	rawFront := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawReads.Add(1)
		if hook := onRawRead.Load(); hook != nil {
			(*hook)()
		}
		if rawFailure.Load() != 0 {
			http.Error(w, "synthetic raw outage", http.StatusServiceUnavailable)
			return
		}
		rawProxy.ServeHTTP(w, r)
	}))
	t.Cleanup(rawFront.Close)
	longFront := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch longMode.Load() {
		case 1:
			http.Error(w, "synthetic long outage", http.StatusServiceUnavailable)
		case 2:
			oversizedHistoryReply(t, w, r, tenant, workspace)
		default:
			longProxy.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(longFront.Close)

	policy := monitoring.Defaults()
	policy.FleetHistory = monitoring.FleetHistoryPolicy{
		RawOrigin: rawFront.URL, LongOrigin: longFront.URL, RawHorizonSeconds: 3600,
		MaxRangeSeconds: 90 * 86400, SettlingSeconds: 30,
	}
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/monitoring/openapi.json")
	server := mountHistoryHTTP(t, accounts, policy)
	admin := newHistoryBrowser(t, server, httpfixture.AdminOrigin, schemas)
	admin.login(password)
	target := func(id string, from, to time.Time) string {
		return cellroute.Public(monitoringhttp.FleetHistoryPath, wire.LocalScope()) + "?" +
			url.Values{"workspace_id": {id}, "from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}.Encode()
	}
	get := func(browser *historyBrowser, path string, status int, code string) monitoring.FleetHistory {
		t.Helper()
		before := count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'")
		started := time.Now()
		reply, headers := browser.request(http.MethodGet, path, nil, status, code)
		requestID := headers.Get("X-Request-ID")
		audits := count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot' AND request_id=$1", requestID)
		after := count("SELECT count(*) FROM audit_events WHERE action='monitoring.snapshot'")
		if status != http.StatusOK {
			if audits != 0 || before != after {
				t.Fatal("HTTP refusal committed a successful history audit")
			}
			return monitoring.FleetHistory{}
		}
		if audits != 1 || after != before+1 {
			t.Fatal("HTTP success did not have exactly one committed request audit")
		}
		encoded, err := json.Marshal(reply["data"])
		var out monitoring.FleetHistory
		if err != nil || json.Unmarshal(encoded, &out) != nil {
			t.Fatal("history response data is invalid")
		}
		t.Logf("authenticated %s history including TLS, SQL, store and audit: %s", out.Tier, time.Since(started))
		return out
	}

	recent := time.Now().UTC().Truncate(time.Minute).Add(-4 * time.Minute)
	old := recent.Add(-2 * time.Hour)
	recentPath, oldPath := target(workspace, recent, recent.Add(2*time.Minute)), target(workspace, old, old.Add(2*time.Minute))
	anonymous := newHistoryBrowser(t, server, httpfixture.AdminOrigin, schemas)
	get(anonymous, recentPath, 401, "AUTH_REQUIRED")
	portal := newHistoryBrowser(t, server, httpfixture.PortalOrigin, schemas)
	portal.login(password)
	get(portal, recentPath, 404, "RESOURCE_NOT_FOUND")
	// A Portal token remains ineligible even when supplied under the Admin
	// cookie name at the real Admin origin.
	wrongAudience := newHistoryBrowser(t, server, httpfixture.AdminOrigin, schemas)
	portalOrigin, _ := url.Parse(httpfixture.PortalOrigin)
	adminOrigin, _ := url.Parse(httpfixture.AdminOrigin)
	cookiePolicy, err := httpserver.NewCookiePolicy("")
	if err != nil {
		t.Fatal(err)
	}
	copied := false
	for _, cookie := range portal.client.Jar.Cookies(portalOrigin) {
		if cookie.Name == cookiePolicy.Name("portal", httpserver.SessionCookie) {
			wrongAudience.client.Jar.SetCookies(adminOrigin, []*http.Cookie{{
				Name:  cookiePolicy.Name("admin", httpserver.SessionCookie),
				Value: cookie.Value, Secure: true, Path: "/",
			}})
			copied = true
		}
	}
	if !copied {
		t.Fatal("normal Portal login did not issue a session cookie")
	}
	get(wrongAudience, recentPath, 401, "AUTH_SESSION_EXPIRED")
	get(admin, strings.Replace(recentPath, "/cells/default/", "/cells/foreign/", 1), 404, "RESOURCE_NOT_FOUND")
	get(admin, monitoringhttp.FleetHistoryPath+"?"+strings.SplitN(recentPath, "?", 2)[1], 404, "RESOURCE_NOT_FOUND")
	get(admin, target(foreignWorkspace, recent, recent.Add(2*time.Minute)), 404, "RESOURCE_NOT_FOUND")
	admin.headers["Origin"] = "https://foreign.example.invalid"
	get(admin, recentPath, 403, "ORIGIN_REJECTED")
	delete(admin.headers, "Origin")
	get(admin, recentPath+"&tier=raw", 400, "VALIDATION_FAILED")
	get(admin, target(workspace, recent.Add(-100*24*time.Hour), recent), 400, "MONITORING_HISTORY_RANGE_INVALID")
	if rawReads.Load() != 0 {
		t.Fatal("invalid or unauthorized requests reached the raw store")
	}

	var samples bytes.Buffer
	for _, stamp := range []time.Time{old, recent} {
		for _, sample := range []struct {
			tenant, workspace, cell, node string
			up                            int
		}{
			{tenant, workspace, "down", "node-1", 0},
			{tenant, workspace, "healthy", "node-2", 1},
			{tenant, otherWorkspace, "foreign-workspace", "node-3", 1},
			{foreignTenant, foreignWorkspace, "foreign-tenant", "node-4", 1},
			{foreignTenant, workspace, "forged-workspace", "node-5", 1},
		} {
			fmt.Fprintf(&samples, "up{job=%q,site_id=%q,cell_id=%q,node_id=%q,tenant_id=%q,workspace_id=%q} %d %d\n",
				"ownsolid-node", "site-1", sample.cell, sample.node, sample.tenant, sample.workspace, sample.up, stamp.UnixMilli())
		}
	}
	fixtureRequest(t, ctx, http.MethodPost, raw+"/api/v1/import/prometheus", &samples)
	fixtureRequest(t, ctx, http.MethodGet, raw+"/internal/force_flush", nil)
	replay := exec.CommandContext(ctx, os.Getenv("OWNSOLID_TEST_VMALERT"),
		"-rule=alerting/fleet-rollup-rules.yaml", "-datasource.url="+raw, "-remoteWrite.url="+long,
		"-replay.timeFrom="+old.Format(time.RFC3339), "-replay.timeTo="+old.Add(3*time.Minute).Format(time.RFC3339),
		"-replay.rulesDelay=0", "-replay.disableProgressBar")
	if log, err := replay.CombinedOutput(); err != nil {
		t.Fatalf("native recording rule replay: %v\n%s", err, log)
	}
	fixtureRequest(t, ctx, http.MethodGet, long+"/internal/force_flush", nil)
	for _, test := range []struct {
		path, tier string
		step       int64
		points     int
	}{
		{recentPath, "raw", 15, 3}, {oldPath, "rollup", 60, 1},
	} {
		out := get(admin, test.path, 200, "")
		if out.Tier != test.tier || out.StepSeconds != test.step || len(out.Cells) != 2 {
			t.Fatalf("unexpected scoped native %s response: %+v", test.tier, out)
		}
		for i, cell := range out.Cells {
			if cell.SiteID != "site-1" || cell.CellID != []string{"down", "healthy"}[i] || len(cell.Points) != test.points {
				t.Fatalf("foreign cell or filled gap: %+v", cell)
			}
			for _, point := range cell.Points {
				if point.Observed != 1 || point.Reachable != uint64(i) {
					t.Fatalf("measured zero or count changed: %+v", point)
				}
			}
		}
	}
	empty := get(admin, target(workspace, old.Add(time.Minute), old.Add(2*time.Minute)), 200, "")
	if empty.Cells == nil || len(empty.Cells) != 0 {
		t.Fatal("missing native samples were not an empty success")
	}
	reads := rawReads.Load()
	longMode.Store(1)
	get(admin, oldPath, 503, "MONITORING_LONG_TIER_UNAVAILABLE")
	longMode.Store(2)
	get(admin, target(workspace, recent.Add(-5*24*time.Hour), recent), 422, "MONITORING_HISTORY_RESPONSE_LIMIT")
	if rawReads.Load() != reads {
		t.Fatal("a failed long-tier query fell back to raw")
	}
	longMode.Store(0)
	get(admin, oldPath, 200, "")
	rawFailure.Store(1)
	get(admin, recentPath, 503, "MONITORING_RAW_TIER_UNAVAILABLE")
	rawFailure.Store(0)
	get(admin, recentPath, 200, "")

	withoutLong := policy
	withoutLong.FleetHistory.LongOrigin = ""
	missingLong := newHistoryBrowser(t, mountHistoryHTTP(t, accounts, withoutLong), httpfixture.AdminOrigin, schemas)
	missingLong.login(password)
	reads = rawReads.Load()
	get(missingLong, oldPath, 503, "MONITORING_LONG_TIER_NOT_CONFIGURED")
	if rawReads.Load() != reads {
		t.Fatal("an unconfigured long tier fell back to raw")
	}
	unconfigured := newHistoryBrowser(t, mountHistoryHTTP(t, accounts, monitoring.Defaults()), httpfixture.AdminOrigin, schemas)
	unconfigured.login(password)
	get(unconfigured, recentPath, 503, "MONITORING_HISTORY_NOT_CONFIGURED")

	var revokeWorkspace sync.Once
	workspaceHook := func() {
		revokeWorkspace.Do(func() {
			if _, err := accounts.DB.Exec(ctx, "UPDATE workspaces SET status='disabled' WHERE id=$1", workspace); err != nil {
				t.Error("synthetic in-flight workspace revocation", err)
			}
		})
	}
	onRawRead.Store(&workspaceHook)
	get(admin, recentPath, 403, "ACCESS_DENIED")
	onRawRead.Store(nil)
	execSQL("UPDATE workspaces SET status='active' WHERE id=$1", workspace)
	get(admin, recentPath, 200, "")

	var adminToken string
	for _, cookie := range admin.client.Jar.Cookies(adminOrigin) {
		if cookie.Name == cookiePolicy.Name("admin", httpserver.SessionCookie) {
			adminToken = cookie.Value
		}
	}
	if adminToken == "" {
		t.Fatal("normal Admin login did not issue a session cookie")
	}
	var revokeSession sync.Once
	sessionHook := func() {
		revokeSession.Do(func() {
			if err := accounts.Logout(ctx, adminToken, "admin", uuid.NewString()); err != nil {
				t.Error("actual in-flight session revocation", err)
			}
		})
	}
	onRawRead.Store(&sessionHook)
	get(admin, recentPath, 401, "AUTH_SESSION_EXPIRED")
	onRawRead.Store(nil)
	reads = rawReads.Load()
	get(admin, recentPath, 401, "AUTH_SESSION_EXPIRED")
	if rawReads.Load() != reads {
		t.Fatal("an expired cookie reached the raw store")
	}
	fresh := newHistoryBrowser(t, server, httpfixture.AdminOrigin, schemas)
	fresh.login(password)
	get(fresh, recentPath, 200, "")
	t.Logf("small native HTTP fixture allocated bytes after graceful store stop: raw=%d long=%d", rawDisk(), longDisk())
}

func mountHistoryHTTP(t *testing.T, accounts *identity.Service, policy monitoring.Config) *httptest.Server {
	t.Helper()
	file := filepath.Join(t.TempDir(), "monitoring.json")
	body, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := monitoring.LoadEnvironment(map[string]string{monitoring.ConfigEnvironment: file})
	if err != nil {
		t.Fatal(err)
	}
	app := identityhttp.New(accounts)
	if err := app.InitializationError(); err != nil {
		t.Fatal("original application owner initialization", err)
	}
	recorder, err := monitoringmetrics.New(loaded.HTTP, nil)
	if err != nil {
		t.Fatal(err)
	}
	service, err := monitoring.New(accounts, app.Workspaces.Service, app.Licensing.Service,
		app.Components.Service, app.Distribution.Service, recorder, loaded)
	if err != nil {
		t.Fatal(err)
	}
	app.Monitoring = &monitoringhttp.Handler{Service: service}
	return httpfixture.NewTLSServer(t, recorder.Middleware(app))
}

type historyBrowser struct {
	t       *testing.T
	client  *http.Client
	origin  string
	headers map[string]string
	schemas map[string]*jsonschema.Schema
}

func newHistoryBrowser(t *testing.T, server *httptest.Server, origin string, schemas map[string]*jsonschema.Schema) *historyBrowser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := *server.Client()
	client.Jar = jar
	return &historyBrowser{t: t, client: &client, origin: origin, headers: map[string]string{}, schemas: schemas}
}

func (b *historyBrowser) login(password string) {
	b.t.Helper()
	csrf, _ := b.request(http.MethodGet, "/api/v1/auth/csrf", nil, 200, "")
	b.headers["X-CSRF-Token"] = csrf["data"].(map[string]any)["token"].(string)
	body, err := json.Marshal(map[string]string{"email": "admin@example.invalid", "password": password})
	if err != nil {
		b.t.Fatal(err)
	}
	b.request(http.MethodPost, "/api/v1/auth/login", body, 200, "")
	delete(b.headers, "X-CSRF-Token")
}

func (b *historyBrowser) request(method, path string, body []byte, status int, code string) (map[string]any, http.Header) {
	b.t.Helper()
	request, err := http.NewRequestWithContext(b.t.Context(), method, b.origin+path, bytes.NewReader(body))
	if err != nil {
		b.t.Fatal(err)
	}
	request.Header.Set("Origin", b.origin)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range b.headers {
		request.Header.Set(name, value)
	}
	response, err := b.client.Do(request)
	if err != nil {
		b.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		b.t.Fatal("served body exceeded its complete wire budget", err)
	}
	if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || response.StatusCode != status ||
		response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Request-ID") == "" ||
		response.Header.Get("X-Content-Type-Options") != "nosniff" {
		b.t.Fatalf("unexpected authenticated HTTP result: status=%d want=%d bytes=%s", response.StatusCode, status, raw)
	}
	var reply map[string]any
	if err := json.Unmarshal(raw, &reply); err != nil {
		b.t.Fatal(err)
	}
	schema := ""
	if status >= 400 {
		schema = "Problem"
		if reply["code"] != code || reply["request_id"] != response.Header.Get("X-Request-ID") ||
			response.Header.Get("Content-Type") != "application/problem+json" {
			b.t.Fatalf("incorrect public refusal: %s", raw)
		}
	} else if strings.Contains(path, "/admin/monitoring/fleet-history") {
		schema = "FleetHistoryResponse"
	}
	if schema != "" {
		validator := b.schemas[schema]
		if validator == nil {
			b.t.Fatalf("formal schema %s is missing", schema)
		}
		if err := validator.Validate(reply); err != nil {
			b.t.Fatalf("served response violates %s: %v", schema, err)
		}
	}
	return reply, response.Header.Clone()
}

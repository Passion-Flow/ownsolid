package http_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type client struct {
	t        *testing.T
	s        *httptest.Server
	audience string
	cookies  map[string]*http.Cookie
	csrf     string
	schemas  map[string]*jsonschema.Schema
	http     *http.Client
}

func newClient(t *testing.T, s *httptest.Server, a string, schemas map[string]*jsonschema.Schema) *client {
	t.Helper()
	host := a + ".example.invalid"
	if s.Certificate() == nil || s.Certificate().VerifyHostname(host) != nil {
		t.Fatal("ACCOUNT_HTTP_TEST_CERTIFICATE_NAME_REQUIRED")
	}
	transport := s.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = host
	t.Cleanup(transport.CloseIdleConnections)
	return &client{t: t, s: s, audience: a, cookies: map[string]*http.Cookie{}, schemas: schemas, http: &http.Client{Transport: transport}}
}

// The actual dispatcher requires DNS Host and TLS SNI to agree. Generate only
// synthetic test names; trust the exact certificate through httptest.Client.
func accountTLSServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal("ACCOUNT_HTTP_TEST_KEY_GENERATION_FAILED")
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		t.Fatal("ACCOUNT_HTTP_TEST_SERIAL_GENERATION_FAILED")
	}
	certificate := &x509.Certificate{SerialNumber: serial, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"portal.example.invalid", "admin.example.invalid"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if e != nil {
		t.Fatal("ACCOUNT_HTTP_TEST_CERTIFICATE_GENERATION_FAILED")
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}
func (c *client) request(method, path string, body any, status int, schema string, headers map[string]string) map[string]any {
	c.t.Helper()
	path = cellroute.Public(path, wire.LocalScope())
	var payload []byte
	if body != nil {
		switch b := body.(type) {
		case []byte:
			payload = b
		default:
			payload, _ = json.Marshal(body)
		}
	}
	r, e := http.NewRequest(method, c.s.URL+path, bytes.NewReader(payload))
	if e != nil {
		c.t.Fatal(e)
	}
	r.Host = c.audience + ".example.invalid"
	r.Header.Set("Origin", "https://"+r.Host)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" {
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	for _, cookie := range c.cookies {
		r.AddCookie(cookie)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	response, e := c.http.Do(r)
	if e != nil {
		c.t.Fatal(e)
	}
	defer response.Body.Close()
	if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
		c.t.Fatal("ACCOUNT_HTTP_VERIFIED_TLS_REQUIRED")
	}
	b, e := io.ReadAll(response.Body)
	if e != nil {
		c.t.Fatal(e)
	}
	if response.StatusCode != status {
		var p struct {
			Code string `json:"code"`
		}
		json.Unmarshal(b, &p)
		c.t.Fatalf("%s %s expected %d observed %d (%s)", method, path, status, response.StatusCode, p.Code)
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Request-ID") == "" {
		c.t.Fatal("security response headers missing")
	}
	for _, cookie := range response.Cookies() {
		if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" {
			c.t.Fatal("unsafe cookie")
		}
		if cookie.MaxAge < 0 {
			delete(c.cookies, cookie.Name)
		} else {
			c.cookies[cookie.Name] = cookie
		}
	}
	if status == 204 {
		if len(b) != 0 {
			c.t.Fatal("204 response has a body")
		}
		return nil
	}
	var out map[string]any
	if e = json.Unmarshal(b, &out); e != nil {
		c.t.Fatal("response is not JSON")
	}
	if schema != "" {
		if e = c.schemas[schema].Validate(out); e != nil {
			c.t.Fatal("response violates " + schema)
		}
	}
	if status >= 400 {
		return out
	}
	return out["data"].(map[string]any)
}
func (c *client) refresh() {
	data := c.request("GET", "/api/v1/auth/csrf", nil, 200, "CsrfResponse", nil)
	c.csrf = data["token"].(string)
}
func (c *client) mutate(method, path string, body any, status int, schema string) map[string]any {
	c.refresh()
	return c.request(method, path, body, status, schema, map[string]string{"Idempotency-Key": uuid.NewString()})
}
func schemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	path := filepath.Join("../../../../../../api/openapi/v1/identity/openapi.json")
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var contract map[string]any
	if e = json.Unmarshal(b, &contract); e != nil {
		t.Fatal(e)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	root := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "components": contract["components"]}
	if e = compiler.AddResource("https://projectone.invalid/identity", root); e != nil {
		t.Fatal(e)
	}
	out := map[string]*jsonschema.Schema{}
	for name := range contract["components"].(map[string]any)["schemas"].(map[string]any) {
		out[name], e = compiler.Compile("https://projectone.invalid/identity#/components/schemas/" + name)
		if e != nil {
			t.Fatal(e)
		}
	}
	return out
}
func setup(t *testing.T, policies ...config.RuntimePolicy) (*identity.Service, *httptest.Server, *client, *client, string) {
	t.Helper()
	s := fixture.New(t)
	if len(policies) > 1 {
		t.Fatal("ACCOUNT_HTTP_SINGLE_TEST_POLICY_REQUIRED")
	}
	if len(policies) == 1 {
		policy := policies[0]
		if e := policy.Validate(); e != nil {
			t.Fatal("ACCOUNT_HTTP_VALID_TEST_POLICY_REQUIRED", e)
		}
		s.Config.Runtime = &policy
		s.Mail.Config = s.Config
		blocklist := filepath.Join(t.TempDir(), "blocklist")
		if os.WriteFile(blocklist, []byte("passwordpassword\n"), 0600) != nil {
			t.Fatal("ACCOUNT_HTTP_TEST_BLOCKLIST_FAILED")
		}
		var e error
		s.Passwords, e = security.NewPasswords(blocklist, policy.Identity)
		if e != nil {
			t.Fatal("ACCOUNT_HTTP_TEST_HASH_POLICY_FAILED", e)
		}
	}
	p := fixture.Bootstrap(t, s)
	assets := t.TempDir()
	os.WriteFile(filepath.Join(assets, "index.html"), []byte("<!doctype html><title>Test assets</title>"), 0600)
	s.Config.Server.PortalAssets = assets
	s.Config.Server.AdminAssets = assets
	handler := transport.New(s)
	if e := handler.LicenseInitializationError(); e != nil {
		t.Fatal(e)
	}
	if e := handler.Licensing.Service.VerifyStored(context.Background()); e != nil {
		t.Fatal("real fixture license integrity", e)
	}
	if e := handler.Notifications.Service.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := handler.Notifications.Service.Stop(ctx); e != nil {
			t.Error(e)
		}
	})
	server := accountTLSServer(t, handler)
	ss := schemas(t)
	return s, server, newClient(t, server, "portal", ss), newClient(t, server, "admin", ss), p
}
func TestM01EveryHTTPResponseSchema(t *testing.T) {
	s, _, portal, admin, p := setup(t)
	for _, path := range []string{"/health/live", "/health/ready"} {
		portal.request("GET", path, nil, 200, "HealthResponse", nil)
	}
	admin.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": p}, 200, "LoginResponse")
	me := admin.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	user := me["user"].(map[string]any)
	admin.request("GET", "/api/v1/me/session", nil, 200, "SessionStatusResponse", nil)
	admin.request("GET", "/api/v1/admin/users", nil, 200, "UserListResponse", nil)
	admin.request("GET", "/api/v1/admin/users/"+user["id"].(string), nil, 200, "UserResponse", nil)
	invite := admin.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": "alice@example.invalid", "locale": "en"}, 201, "InvitationResponse")
	id := invite["id"].(string)
	token := fixture.InvitationToken(t, s, id)
	portal.mutate("POST", "/api/v1/auth/invitation-validations", map[string]any{"token": token}, 200, "InvitationValidationResponse")
	invite = admin.mutate("POST", "/api/v1/admin/invitations/"+id+"/resends", map[string]any{"version": invite["version"]}, 200, "InvitationResponse")
	token = fixture.InvitationToken(t, s, id)
	password := fixture.Password()
	portal.mutate("POST", "/api/v1/auth/register", map[string]any{"token": token, "display_name": "Alice", "password": password}, 201, "RegisterResponse")
	portal.request("GET", "/api/v1/me", nil, 401, "Problem", nil)
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "alice@example.invalid", "password": password}, 200, "LoginResponse")
	me = portal.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	user = me["user"].(map[string]any)
	me = portal.mutate("PATCH", "/api/v1/me", map[string]any{"display_name": "Alice <script>", "locale": "zh-CN", "version": user["version"]}, 200, "MeResponse")
	user = me["user"].(map[string]any)
	disabled := admin.mutate("PATCH", "/api/v1/admin/users/"+user["id"].(string)+"/status", map[string]any{"version": user["version"], "status": "disabled", "reason": "Synthetic fixture"}, 200, "UserResponse")
	portal.request("GET", "/api/v1/me", nil, 401, "Problem", nil)
	admin.mutate("PATCH", "/api/v1/admin/users/"+user["id"].(string)+"/status", map[string]any{"version": disabled["version"], "status": "active", "reason": "Synthetic fixture"}, 200, "UserResponse")
	portal.mutate("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": "alice@example.invalid"}, 202, "AcceptedResponse")
	reset := fixture.ResetToken(t, s, "alice@example.invalid")
	portal.mutate("POST", "/api/v1/auth/password-resets", map[string]any{"token": reset, "password": fixture.Password()}, 200, "ResetResponse")
	invite = admin.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": "bob@example.invalid", "locale": "en"}, 201, "InvitationResponse")
	admin.mutate("POST", "/api/v1/admin/invitations/"+invite["id"].(string)+"/revocations", map[string]any{"version": invite["version"], "reason": "Synthetic fixture"}, 200, "InvitationResponse")
	for path, schema := range map[string]string{"invitations": "InvitationListResponse", "audit-events": "AuditListResponse", "mail-deliveries": "MailListResponse"} {
		admin.request("GET", "/api/v1/admin/"+path, nil, 200, schema, nil)
	}
	admin.mutate("POST", "/api/v1/auth/logout", nil, 204, "")
	admin.mutate("POST", "/api/v1/auth/logout", nil, 204, "")
}
func TestM01TransportBoundaries(t *testing.T) {
	_, server, portal, admin, p := setup(t)
	portal.refresh()
	portal.request("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": p}, 403, "Problem", map[string]string{"Origin": "https://hostile.example.invalid"})
	portal.request("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": p}, 403, "Problem", map[string]string{"X-CSRF-Token": "invalid"})
	portal.mutate("POST", "/api/v1/auth/login", []byte(`{"email":"admin@example.invalid","email":"another@example.invalid","password":"synthetic"}`), 400, "Problem")
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": p, "is_platform_admin": true}, 400, "Problem")
	portal.mutate("POST", "/api/v1/auth/login", []byte("{\"email\":\"\xff\"}"), 400, "Problem")
	portal.mutate("POST", "/api/v1/auth/login", []byte(strings.Repeat(" ", 16385)), 413, "Problem")
	portal.refresh()
	portal.request("POST", "/api/v1/auth/login", []byte("{}"), 415, "Problem", map[string]string{"Content-Type": "text/plain"})
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": p}, 200, "LoginResponse")
	portal.request("GET", "/api/v1/admin/users", nil, 404, "Problem", nil)
	admin.cookies["__Host-admin_session"] = &http.Cookie{Name: "__Host-admin_session", Value: portal.cookies["__Host-portal_session"].Value}
	admin.request("GET", "/api/v1/admin/users", nil, 401, "Problem", nil)
	r, _ := http.NewRequest("GET", server.URL+"/health/live", nil)
	r.Host = "unknown.example.invalid"
	response, e := portal.http.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 421 {
		t.Fatal("unknown Host accepted")
	}
	id := uuid.NewString()
	routes := []struct{ method, path string }{
		{"POST", "/api/v1/auth/login"}, {"POST", "/api/v1/auth/logout"}, {"POST", "/api/v1/auth/invitation-validations"}, {"POST", "/api/v1/auth/register"}, {"POST", "/api/v1/auth/password-reset-requests"}, {"POST", "/api/v1/auth/password-resets"}, {"PATCH", "/api/v1/me"}, {"POST", "/api/v1/me/password-changes"},
		{"POST", "/api/v1/admin/invitations"}, {"POST", "/api/v1/admin/invitations/" + id + "/resends"}, {"POST", "/api/v1/admin/invitations/" + id + "/revocations"}, {"PATCH", "/api/v1/admin/users/" + id + "/status"},
	}
	for _, route := range routes {
		c := portal
		if strings.Contains(route.path, "/admin/") {
			c = admin
		}
		c.csrf = ""
		c.request(route.method, route.path, map[string]any{}, 403, "Problem", nil)
	}
}

func TestM01StorageFailureIsNotSuccess(t *testing.T) {
	s, _, portal, _, p := setup(t)
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": p}, 200, "LoginResponse")
	portal.refresh()
	before := portal.cookies["__Host-portal_session"].Value
	// Close a real separately selected pool, preserving the fixture/worker pool
	// so the outage assertion cannot prevent exact synthetic schema cleanup.
	closed, e := pgxpool.NewWithConfig(t.Context(), s.DB.Config().Copy())
	if e != nil {
		t.Fatal("ACCOUNT_HTTP_OUTAGE_POOL_SETUP_FAILED")
	}
	closed.Close()
	original := s.DB
	s.DB = closed
	t.Cleanup(func() { s.DB = original })
	portal.request("POST", "/api/v1/auth/logout", nil, 503, "Problem", nil)
	if portal.cookies["__Host-portal_session"] == nil || portal.cookies["__Host-portal_session"].Value != before {
		t.Fatal("outage cleared the cookie without confirmed revocation")
	}
	for _, email := range []string{"admin@example.invalid", "unknown@example.invalid"} {
		portal.request("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": email}, 503, "Problem", nil)
	}
}
func TestM01DuplicateCookiesAndCORS(t *testing.T) {
	_, server, portal, _, p := setup(t)
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": p}, 200, "LoginResponse")
	r, _ := http.NewRequest("GET", server.URL+"/api/v1/me", nil)
	r.Host = "portal.example.invalid"
	c := portal.cookies["__Host-portal_session"]
	r.AddCookie(c)
	r.AddCookie(c)
	r.Header.Set("Origin", "https://hostile.example.invalid")
	response, e := portal.http.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 401 || response.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("duplicate cookie or CORS boundary bypass")
	}
}
func TestM01BoundedLocalRequestWorkloads(t *testing.T) {
	service, server, portal, _, p := setup(t)
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": p}, 200, "LoginResponse")
	for _, concurrency := range []int{10, 50} {
		results := make(chan time.Duration, concurrency)
		failures := make(chan bool, concurrency)
		start := time.Now()
		for n := 0; n < concurrency; n++ {
			go func() {
				begin := time.Now()
				r, _ := http.NewRequest("GET", server.URL+"/api/v1/me", nil)
				r.Host = "portal.example.invalid"
				r.AddCookie(portal.cookies["__Host-portal_session"])
				response, e := portal.http.Do(r)
				if e != nil {
					failures <- true
				} else {
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
					failures <- response.StatusCode != 200
				}
				results <- time.Since(begin)
			}()
		}
		durations := make([]float64, 0, concurrency)
		errors := 0
		for n := 0; n < concurrency; n++ {
			durations = append(durations, float64((<-results).Microseconds())/1000)
			if <-failures {
				errors++
			}
		}
		sort.Float64s(durations)
		if errors != 0 {
			t.Fatal("bounded profile had errors")
		}
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		var usage syscall.Rusage
		syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
		pool := service.DB.Stat()
		rssBytes := usage.Maxrss
		if runtime.GOOS != "darwin" {
			rssBytes *= 1024
		}
		t.Logf("rss_max_bytes=%d goos=%s pool_wait_ms=%.3f pool_empty_acquires=%d pool_max=%d", rssBytes, runtime.GOOS, float64(pool.EmptyAcquireWaitTime().Microseconds())/1000, pool.EmptyAcquireCount(), pool.MaxConns())
		t.Logf("requests=%d concurrency=%d accounts=1 workspace=1 operation=getMe duration_ms=%.3f p95_ms=%.3f p99_ms=%.3f errors=%d heap_bytes=%d; local macOS Go + PostgreSQL arm64, not target capacity", concurrency, concurrency, float64(time.Since(start).Microseconds())/1000, durations[int(float64(concurrency-1)*.95)], durations[int(float64(concurrency-1)*.99)], errors, mem.HeapAlloc)
	}
}

func TestM01CredentialAndRecoveryClassesAreUniform(t *testing.T) {
	s, _, portal, admin, p := setup(t)
	actor := fixture.Login(t, s, "admin@example.invalid", p, "admin")
	ctx := context.Background()
	password := fixture.Password()
	users := map[string]string{}
	for _, email := range []string{"alice@example.invalid", "disabled@example.invalid"} {
		inv, e := s.Invite(ctx, actor, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		if e = s.Register(ctx, fixture.InvitationToken(t, s, inv.ID), "Fixture member", password, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		var id string
		if e = s.DB.QueryRow(ctx, "SELECT id::text FROM users WHERE email_canonical=$1", email).Scan(&id); e != nil {
			t.Fatal(e)
		}
		users[email] = id
	}
	u, e := s.GetUser(ctx, users["disabled@example.invalid"])
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetStatus(ctx, actor, u.ID, "disabled", u.Version, "Synthetic disable", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	var shape string
	for _, attempt := range []struct {
		client          *client
		email, password string
	}{{portal, "unknown@example.invalid", password}, {portal, "alice@example.invalid", password + "wrong"}, {portal, "disabled@example.invalid", password}, {admin, "alice@example.invalid", password}} {
		result := attempt.client.mutate("POST", "/api/v1/auth/login", map[string]any{"email": attempt.email, "password": attempt.password}, 401, "Problem")
		delete(result, "request_id")
		b, _ := json.Marshal(result)
		if shape == "" {
			shape = string(b)
		} else if shape != string(b) {
			t.Fatal("credential class disclosed a different public failure")
		}
	}
	for _, attempt := range []struct {
		client *client
		email  string
	}{{portal, "alice@example.invalid"}, {portal, "unknown@example.invalid"}, {portal, "disabled@example.invalid"}, {admin, "alice@example.invalid"}} {
		result := attempt.client.mutate("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": attempt.email}, 202, "AcceptedResponse")
		if len(result) != 1 || result["accepted"] != true {
			t.Fatal("recovery disclosed account metadata")
		}
	}
	var resets int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM password_reset_tokens").Scan(&resets); e != nil || resets != 1 {
		t.Fatal("ineligible recovery created secret authority")
	}
	portal.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "alice@example.invalid", "password": password}, 200, "LoginResponse")
	for _, path := range []string{"users", "users/" + users["alice@example.invalid"], "invitations", "audit-events", "mail-deliveries"} {
		portal.request("GET", "/api/v1/admin/"+path, nil, 404, "Problem", nil)
	}
}

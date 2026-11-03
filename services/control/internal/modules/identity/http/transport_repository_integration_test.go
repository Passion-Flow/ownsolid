//go:build repository_integration && transport_integration

package http_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

const transportHTTPTimeout = 10 * time.Second

// Each selected root creates exactly one real migrated identity schema. Helpers
// reconstruct handlers/listeners against that same DB; they never create a DB.
func TestTransportActualDirectAccountRecovery(t *testing.T) {
	transportAccountRecovery(t, fixture.New(t), config.TransportDirectHTTPS)
}

func TestTransportActualHTTPAccountRecovery(t *testing.T) {
	transportAccountRecovery(t, fixture.New(t), config.TransportHTTP)
}

func TestTransportActualProxyAccountRecovery(t *testing.T) {
	transportAccountRecovery(t, fixture.New(t), config.TransportTrustedProxyHTTPS)
}

type transportTopology struct {
	t       *testing.T
	owner   *identity.Service
	handler *transport.Handler
	server  *httptest.Server
	backend *httptest.Server
	clients []*http.Transport
	closed  bool
}

func transportConfigure(s *identity.Service, mode config.TransportMode) {
	s.Config.Server.TransportMode = mode
	scheme := "https"
	if mode == config.TransportHTTP {
		scheme = "http"
	}
	s.Config.Server.PortalOrigin = scheme + "://portal.example.invalid"
	s.Config.Server.AdminOrigin = scheme + "://admin.example.invalid"
	s.Config.Server.TrustedProxyCIDRs = nil
	if mode == config.TransportTrustedProxyHTTPS {
		s.Config.Server.TrustedProxyCIDRs = []string{"127.0.0.1/32"}
	}
	// Outbox owns a value copy; links must use the same declared runtime origins.
	s.Mail.Config = s.Config
}

func transportOpen(t *testing.T, s *identity.Service) *transportTopology {
	t.Helper()
	h := transport.New(s)
	if e := h.InitializationError(); e != nil {
		t.Fatal("original dispatcher initialization", e)
	}
	if e := h.Licensing.Service.VerifyStored(context.Background()); e != nil {
		t.Fatal("actual synthetic signed fixture License", e)
	}
	x := &transportTopology{t: t, owner: s, handler: h}
	switch s.Config.Server.EffectiveTransportMode() {
	case config.TransportHTTP:
		x.server = httptest.NewServer(h)
	case config.TransportDirectHTTPS:
		x.server = httpfixture.NewTLSServer(t, h)
	case config.TransportTrustedProxyHTTPS:
		x.backend = httptest.NewServer(h)
		x.server = transportProxy(t, x, "")
	default:
		t.Fatal("unsupported test mode")
	}
	listener := x.server
	if x.backend != nil {
		listener = x.backend
	}
	s.Config.Server.ListenAddress = listener.Listener.Addr().String()
	t.Cleanup(x.close)
	return x
}

func (x *transportTopology) close() {
	if x.closed {
		return
	}
	x.closed = true
	for _, client := range x.clients {
		client.CloseIdleConnections()
	}
	// No mail dispatcher or notification worker is started by these API tests.
	// Stop is still joined before the fixture's DB cleanup, including failure.
	ctx, cancel := context.WithTimeout(context.Background(), transportHTTPTimeout)
	defer cancel()
	if e := x.handler.Notifications.Service.Stop(ctx); e != nil {
		x.t.Error("notification owner shutdown", e)
	}
	if x.handler.SSO != nil && x.handler.SSO.Service != nil {
		x.handler.SSO.Service.Close()
	}
	x.server.Close()
	if x.backend != nil {
		x.backend.Close()
	}
}

// syntheticClient is an explicit test proxy observation, never a value selected
// by the caller's headers. Empty uses the actual ingress socket IP. TEST-NET
// identities make SQL key provenance testable without changing macOS aliases.
func transportProxy(t *testing.T, x *transportTopology, syntheticClient string) *httptest.Server {
	t.Helper()
	backend, e := url.Parse(x.backend.URL)
	if e != nil {
		t.Fatal(e)
	}
	direct := x.owner.Config.Server
	direct.TransportMode = config.TransportDirectHTTPS
	direct.TrustedProxyCIDRs = nil
	proxyTransport := http.DefaultTransport.(*http.Transport).Clone()
	proxyTransport.Proxy = nil
	x.clients = append(x.clients, proxyTransport)
	proxy := &httputil.ReverseProxy{
		Transport: proxyTransport,
		Rewrite: func(p *httputil.ProxyRequest) {
			for key := range p.Out.Header {
				lower := strings.ToLower(key)
				if lower == "forwarded" || lower == "x-real-ip" || lower == "x-forwarded" || strings.HasPrefix(lower, "x-forwarded-") {
					p.Out.Header.Del(key)
				}
			}
			client, _, e := net.SplitHostPort(p.In.RemoteAddr)
			if e != nil {
				panic("test ingress socket must be an actual IP endpoint")
			}
			if syntheticClient != "" {
				client = syntheticClient
			}
			p.SetURL(backend)
			p.Out.Host = p.In.Host
			p.Out.Header.Set("X-Forwarded-Proto", "https")
			p.Out.Header.Set("X-Forwarded-Host", p.In.Host)
			p.Out.Header.Set("X-Forwarded-For", client)
		},
	}
	// The proxy ingress verifies real TLS/SNI/authority as a direct HTTPS peer.
	// Caller forwarding fields are stripped only at the trusted proxy boundary.
	return httpfixture.NewTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		copy := r.Clone(r.Context())
		copy.Header = r.Header.Clone()
		for key := range copy.Header {
			lower := strings.ToLower(key)
			if lower == "forwarded" || lower == "x-real-ip" || lower == "x-forwarded" || strings.HasPrefix(lower, "x-forwarded-") {
				copy.Header.Del(key)
			}
		}
		if _, e := httpserver.ResolveBrowserRequest(copy, direct); e != nil {
			httpserver.Problem(w, uuid.NewString(), e)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
}

type transportBrowser struct {
	t        *testing.T
	client   *http.Client
	origin   string
	audience string
	secure   bool
	cookies  map[string]*http.Cookie
	csrf     string
	schemas  map[string]*jsonschema.Schema
	last     []*http.Cookie
	request  string
}

func transportClient(t *testing.T, x *transportTopology, server *httptest.Server, audience string) *transportBrowser {
	t.Helper()
	origin := x.owner.Config.Server.PortalOrigin
	if audience == "admin" {
		origin = x.owner.Config.Server.AdminOrigin
	}
	return transportClientOrigin(t, x, server, audience, origin)
}

func transportClientOrigin(t *testing.T, x *transportTopology, server *httptest.Server, audience, origin string) *transportBrowser {
	t.Helper()
	secure := strings.HasPrefix(origin, "https://")
	roundTrip := http.DefaultTransport.(*http.Transport).Clone()
	roundTrip.Proxy = nil
	if secure {
		if server.Certificate() == nil {
			t.Fatal("real TLS certificate required")
		}
		roots := x509.NewCertPool()
		roots.AddCert(server.Certificate())
		roundTrip.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	dial := &net.Dialer{Timeout: transportHTTPTimeout}
	roundTrip.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		expectedPort := "80"
		if secure {
			expectedPort = "443"
		}
		if e != nil || host != "portal.example.invalid" && host != "admin.example.invalid" || port != expectedPort {
			return nil, &net.AddrError{Err: "undeclared synthetic browser authority", Addr: address}
		}
		return dial.DialContext(ctx, network, server.Listener.Addr().String())
	}
	x.clients = append(x.clients, roundTrip)
	return &transportBrowser{t: t, client: &http.Client{Transport: roundTrip, Timeout: transportHTTPTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		origin: origin, audience: audience, secure: secure, cookies: map[string]*http.Cookie{}, schemas: schemas(t)}
}

func (c *transportBrowser) cookieName(purpose string) string {
	if c.secure {
		return "__Host-" + c.audience + "_" + purpose
	}
	return "projectone_http_" + c.audience + "_" + purpose
}

func (c *transportBrowser) call(method, path string, body any, status int, schema string, headers map[string]string) map[string]any {
	c.t.Helper()
	var raw []byte
	var e error
	if body != nil {
		raw, e = json.Marshal(body)
		if e != nil {
			c.t.Fatal(e)
		}
	}
	r, e := http.NewRequest(method, c.origin+path, bytes.NewReader(raw))
	if e != nil {
		c.t.Fatal(e)
	}
	r.Header.Set("Origin", c.origin)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" {
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	for _, cookie := range c.cookies {
		r.AddCookie(cookie)
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	response, e := c.client.Do(r)
	if e != nil {
		c.t.Fatal("actual declared-origin request failed", e)
	}
	defer response.Body.Close()
	raw, e = io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if e != nil {
		c.t.Fatal(e)
	}
	if response.StatusCode != status {
		var problem struct{ Code string }
		_ = json.Unmarshal(raw, &problem)
		c.t.Fatalf("%s %s: expected %d, observed %d (%s)", method, path, status, response.StatusCode, problem.Code)
	}
	c.request = response.Header.Get("X-Request-ID")
	if response.Header.Get("Cache-Control") != "no-store" || c.request == "" {
		c.t.Fatal("missing real dispatcher response security envelope")
	}
	if c.secure && (response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || response.TLS.ServerName != c.audience+".example.invalid") {
		c.t.Fatal("actual private-CA chain and declared DNS SNI were not verified")
	}
	if !c.secure && response.TLS != nil {
		c.t.Fatal("explicit HTTP unexpectedly used TLS")
	}
	c.last = response.Cookies()
	for _, cookie := range c.last {
		purpose := "session"
		sameSite := http.SameSiteLaxMode
		if cookie.Name == c.cookieName("csrf_context") {
			purpose, sameSite = "csrf_context", http.SameSiteStrictMode
		}
		if cookie.Name != c.cookieName(purpose) || cookie.Secure != c.secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != sameSite {
			c.t.Fatal("mode/audience cookie policy mismatch")
		}
		if cookie.MaxAge < 0 {
			delete(c.cookies, cookie.Name)
		} else {
			c.cookies[cookie.Name] = cookie
		}
	}
	if status == http.StatusNoContent {
		if len(raw) != 0 {
			c.t.Fatal("unexpected no-content body")
		}
		return nil
	}
	var result map[string]any
	if e = json.Unmarshal(raw, &result); e != nil {
		c.t.Fatal("invalid real JSON response")
	}
	if schema != "" {
		if e = c.schemas[schema].Validate(result); e != nil {
			c.t.Fatalf("response violates %s: %v", schema, e)
		}
	}
	if status >= 400 {
		return result
	}
	return result["data"].(map[string]any)
}

func (c *transportBrowser) write(method, path string, body any, status int, schema string) map[string]any {
	c.t.Helper()
	c.csrf = c.call("GET", "/api/v1/auth/csrf", nil, 200, "CsrfResponse", nil)["token"].(string)
	return c.call(method, path, body, status, schema, map[string]string{"Idempotency-Key": uuid.NewString()})
}

func transportProblem(t *testing.T, result map[string]any, code string) {
	t.Helper()
	if result["code"] != code {
		t.Fatalf("expected safe error %s, observed %v", code, result["code"])
	}
}

func transportCookie(t *testing.T, c *transportBrowser) string {
	t.Helper()
	cookie := c.cookies[c.cookieName("session")]
	if cookie == nil || !security.ValidToken(cookie.Value) {
		t.Fatal("real session cookie was not issued")
	}
	return cookie.Value
}

func transportCleared(t *testing.T, c *transportBrowser) {
	t.Helper()
	for _, purpose := range []string{"session", "csrf_context"} {
		found := false
		for _, cookie := range c.last {
			found = found || cookie.Name == c.cookieName(purpose) && cookie.MaxAge < 0 && cookie.Value == ""
		}
		if !found || c.cookies[c.cookieName(purpose)] != nil {
			t.Fatal("identity clear did not match issued transport/audience cookies")
		}
	}
}

func transportCount(t *testing.T, s *identity.Service, query string, args ...any) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), transportHTTPTimeout)
	defer cancel()
	var value int
	if e := s.DB.QueryRow(ctx, query, args...).Scan(&value); e != nil {
		t.Fatal("actual isolated SQL assertion", e)
	}
	return value
}

func transportOutboxToken(t *testing.T, s *identity.Service, email, purpose, origin, route string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), transportHTTPTimeout)
	defer cancel()
	var key, delivery string
	var nonce, ciphertext []byte
	if e := s.DB.QueryRow(ctx, `SELECT id::text,payload_key_id,payload_nonce,payload_ciphertext FROM mail_deliveries WHERE recipient_email=$1 AND purpose=$2 AND state='queued' ORDER BY created_at DESC,id DESC LIMIT 1`, email, purpose).Scan(&delivery, &key, &nonce, &ciphertext); e != nil {
		t.Fatal("actual synthetic queued encrypted outbox", e)
	}
	raw, e := s.Mail.Keys.Decrypt(key, delivery, purpose, nonce, ciphertext)
	if e != nil {
		t.Fatal(e)
	}
	var payload mail.Payload
	if e = json.Unmarshal(raw, &payload); e != nil {
		t.Fatal(e)
	}
	link, e := url.Parse(payload.Link)
	if e != nil || link.Scheme+"://"+link.Host != origin || link.Path != route || link.User != nil || link.RawQuery != "" || !strings.HasPrefix(link.Fragment, "token=") {
		t.Fatal("outbox did not retain the exact selected origin and recovery route")
	}
	token := strings.TrimPrefix(link.Fragment, "token=")
	if !security.ValidToken(token) || bytes.Contains(ciphertext, []byte(token)) || payload.Link != origin+route+"#token="+token {
		t.Fatal("outbox token/link/encryption invariant failed")
	}
	return token
}

func transportAccountRecovery(t *testing.T, s *identity.Service, mode config.TransportMode) {
	t.Helper()
	transportConfigure(s, mode)
	password := fixture.Bootstrap(t, s)
	x := transportOpen(t, s)
	admin := transportClient(t, x, x.server, "admin")
	portal := transportClient(t, x, x.server, "portal")
	for _, entrance := range []*transportBrowser{admin, portal} {
		public := entrance.call("GET", "/runtime-config.json", nil, 200, "", nil)["transport"].(map[string]any)
		if public["mode"] != string(mode) || public["plaintext"] != (mode == config.TransportHTTP) {
			t.Fatal("real dual-entrance public transport policy disagrees with typed owner")
		}
	}
	admin.write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	adminSession := transportCookie(t, admin)
	me := admin.call("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	adminID := me["user"].(map[string]any)["id"].(string)
	if transportCount(t, s, "SELECT count(*) FROM sessions WHERE token_hash=$1 AND audience='admin' AND user_id=$2 AND revoked_at IS NULL", security.Digest(adminSession), adminID) != 1 {
		t.Fatal("Admin login was not durable")
	}
	portal.csrf = portal.call("GET", "/api/v1/auth/csrf", nil, 200, "CsrfResponse", nil)["token"].(string)
	before := transportCount(t, s, "SELECT count(*) FROM sessions")
	transportProblem(t, portal.call("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 403, "Problem", map[string]string{"Origin": "https://foreign.example.invalid"}), "ORIGIN_REJECTED")
	transportProblem(t, portal.call("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 403, "Problem", map[string]string{"X-CSRF-Token": "invalid"}), "CSRF_INVALID")
	if transportCount(t, s, "SELECT count(*) FROM sessions") != before {
		t.Fatal("rejected transport/CSRF request changed sessions")
	}
	email, memberPassword := "transport-member@example.invalid", fixture.Password()
	invitation := admin.write("POST", "/api/v1/admin/invitations", map[string]any{"email": email, "locale": "en"}, 201, "InvitationResponse")
	token := transportOutboxToken(t, s, email, "invitation", s.Config.Server.PortalOrigin, "/register")
	portal.write("POST", "/api/v1/auth/invitation-validations", map[string]any{"token": token}, 200, "InvitationValidationResponse")
	transportProblem(t, admin.call("POST", "/api/v1/auth/register", map[string]any{}, 404, "Problem", nil), "RESOURCE_NOT_FOUND")
	portal.write("POST", "/api/v1/auth/register", map[string]any{"token": token, "display_name": "Transport member", "password": memberPassword}, 201, "RegisterResponse")
	transportProblem(t, portal.write("POST", "/api/v1/auth/invitation-validations", map[string]any{"token": token}, 400, "Problem"), "TOKEN_UNUSABLE")
	portal.write("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": memberPassword}, 200, "LoginResponse")
	portalSession := transportCookie(t, portal)
	me = portal.call("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	memberID := me["user"].(map[string]any)["id"].(string)
	transportProblem(t, portal.call("GET", "/api/v1/admin/users", nil, 404, "Problem", nil), "RESOURCE_NOT_FOUND")
	for _, item := range []struct{ audience, foreign string }{{"admin", portalSession}, {"portal", adminSession}} {
		forged := transportClient(t, x, x.server, item.audience)
		forged.cookies[forged.cookieName("session")] = &http.Cookie{Name: forged.cookieName("session"), Value: item.foreign}
		transportProblem(t, forged.call("GET", "/api/v1/me", nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	}
	if transportCount(t, s, "SELECT count(*) FROM sessions WHERE token_hash=$1 AND audience='portal' AND user_id=$2 AND revoked_at IS NULL", security.Digest(portalSession), memberID) != 1 {
		t.Fatal("session namespace refusal changed durable Portal authority")
	}
	var workspace string
	if e := s.DB.QueryRow(context.Background(), "SELECT default_workspace_id::text FROM installation_state WHERE id=1").Scan(&workspace); e != nil {
		t.Fatal(e)
	}
	portal.call("GET", "/api/v1/workspaces/"+workspace, nil, 200, "", nil)
	foreign := admin.write("POST", "/api/v1/admin/workspaces", map[string]any{"name": "Transport isolated Workspace", "owner_user_id": adminID}, 201, "")["id"].(string)
	transportProblem(t, portal.call("GET", "/api/v1/workspaces/"+foreign, nil, 404, "Problem", nil), "RESOURCE_NOT_FOUND")
	transportProblem(t, portal.write("POST", "/api/v1/workspaces/"+foreign+"/notes", map[string]any{"title": "Refused synthetic note", "text": "No cross-Workspace write"}, 404, "Problem"), "RESOURCE_NOT_FOUND")
	if transportCount(t, s, "SELECT count(*) FROM workspace_notes WHERE workspace_id=$1", foreign) != 0 || transportCount(t, s, "SELECT count(*) FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2", memberID, foreign) != 0 {
		t.Fatal("foreign Workspace refusal leaked membership or note effects")
	}
	// Same DB and keys, new original dispatcher and actual listeners. Neither a
	// full serve process nor an installed deployment restart is claimed here.
	oldAdmin, oldPortal := admin.cookies, portal.cookies
	x.close()
	x = transportOpen(t, s)
	admin, portal = transportClient(t, x, x.server, "admin"), transportClient(t, x, x.server, "portal")
	admin.cookies, portal.cookies = oldAdmin, oldPortal
	admin.call("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	portal.call("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	if transportCount(t, s, "SELECT count(*) FROM invitations WHERE id=$1 AND status='accepted' AND token_hash IS NULL", invitation["id"]) != 1 {
		t.Fatal("consumed invitation did not survive reconstruction")
	}
	admin.write("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": "admin@example.invalid"}, 202, "AcceptedResponse")
	transportOutboxToken(t, s, "admin@example.invalid", "password_reset", s.Config.Server.AdminOrigin, "/reset-password")
	portal.write("POST", "/api/v1/auth/password-reset-requests", map[string]any{"email": email}, 202, "AcceptedResponse")
	reset := transportOutboxToken(t, s, email, "password_reset", s.Config.Server.PortalOrigin, "/reset-password")
	newPassword := fixture.Password()
	portal.write("POST", "/api/v1/auth/password-resets", map[string]any{"token": reset, "password": newPassword}, 200, "ResetResponse")
	transportCleared(t, portal)
	old := transportClient(t, x, x.server, "portal")
	old.cookies[old.cookieName("session")] = &http.Cookie{Name: old.cookieName("session"), Value: portalSession}
	transportProblem(t, old.call("GET", "/api/v1/me", nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	if transportCount(t, s, "SELECT count(*) FROM sessions WHERE token_hash=$1 AND revoked_at IS NOT NULL AND revoke_reason='password_reset'", security.Digest(portalSession)) != 1 {
		t.Fatal("password reset did not durably revoke previous session")
	}
	transportProblem(t, portal.write("POST", "/api/v1/auth/password-resets", map[string]any{"token": reset, "password": fixture.Password()}, 400, "Problem"), "TOKEN_UNUSABLE")
	transportProblem(t, portal.write("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": memberPassword}, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	portal.write("POST", "/api/v1/auth/login", map[string]any{"email": email, "password": newPassword}, 200, "LoginResponse")
	newSession := transportCookie(t, portal)
	portal.write("POST", "/api/v1/auth/logout", nil, 204, "")
	transportCleared(t, portal)
	admin.write("POST", "/api/v1/auth/logout", nil, 204, "")
	transportCleared(t, admin)
	if transportCount(t, s, "SELECT count(*) FROM sessions WHERE token_hash=$1 AND revoked_at IS NOT NULL AND revoke_reason='logout'", security.Digest(newSession)) != 1 {
		t.Fatal("logout was not durable")
	}
	for action, expected := range map[string]int{"invitation.created": 1, "invitation.accepted": 1, "auth.login_succeeded": 3, "auth.password_reset": 1, "auth.logout": 2, "workspace.created": 1} {
		if transportCount(t, s, "SELECT count(*) FROM audit_events WHERE action=$1 AND outcome='success' AND request_id IS NOT NULL", action) != expected {
			t.Fatalf("actual successful SQL audit count mismatch for %s", action)
		}
	}
	if transportCount(t, s, "SELECT count(*) FROM mail_deliveries WHERE state NOT IN ('queued','cancelled') OR attempt_count<>0") != 0 {
		t.Fatal("synthetic outbox was dispatched or changed by an external mail worker")
	}
}

type transportAuditBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *transportAuditBuffer) Write(raw []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(raw)
}

func (b *transportAuditBuffer) provenance(t *testing.T, request, client string) {
	t.Helper()
	b.mu.Lock()
	raw := append([]byte(nil), b.b.Bytes()...)
	b.mu.Unlock()
	for _, line := range bytes.Split(raw, []byte("\n")) {
		var item map[string]any
		if json.Unmarshal(line, &item) == nil && item["msg"] == "HTTP_REQUEST_TRANSPORT" && item["request_id"] == request {
			if item["client_ip"] != client || item["socket_peer_ip"] != "127.0.0.1" || item["forwarded"] != true || item["admitted"] != true || item["transport_mode"] != string(config.TransportTrustedProxyHTTPS) || item["audience"] != "admin" {
				t.Fatal("transport audit merged forwarded client and actual proxy peer")
			}
			return
		}
	}
	t.Fatal("correlated actual transport audit missing")
}

func transportBucket(t *testing.T, s *identity.Service, purpose, client string, expected int) {
	t.Helper()
	key := security.MAC(s.HMACKey, "m01-rate", purpose, client)
	var rows, count int
	expectedRows := 1
	if expected == 0 {
		expectedRows = 0
	}
	if e := s.DB.QueryRow(context.Background(), "SELECT count(*),COALESCE(sum(attempt_count),0) FROM rate_limit_buckets WHERE bucket_key=$1", key).Scan(&rows, &count); e != nil || count != expected || rows != expectedRows {
		t.Fatalf("actual HMAC bucket %s/%s expected rows%d/count%d observed rows%d/count%d (%v)", purpose, client, expectedRows, expected, rows, count, e)
	}
}

func TestTransportAgentProxyRateIdentity(t *testing.T) {
	s := fixture.New(t)
	agentProblem := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/nodes/openapi.json")["Problem"]
	transportConfigure(s, config.TransportTrustedProxyHTTPS)
	fixture.Bootstrap(t, s)
	policy := s.Config.Policy()
	policy.Rates.AgentIPCount = 1
	policy.Rates.AgentTransferIPCount = 1
	policy.Rates.AgentEnrollIPCount = 1
	policy.Rates.AgentIPWindowSeconds = 300
	policy.Rates.AgentTransferIPWindowSeconds = 300
	policy.Rates.AgentEnrollIPWindowSeconds = 300
	if e := policy.Validate(); e != nil {
		t.Fatal("typed synthetic rate policy", e)
	}
	s.Config.Runtime = &policy
	log := &transportAuditBuffer{}
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(log, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	x := transportOpen(t, s)
	node, task := uuid.NewString(), uuid.NewString()
	for index, family := range []struct {
		purpose, path, code string
		status              int
	}{
		{"agent.request.ip", "/agent/v1/nodes/" + node + "/heartbeat", "AGENT_UNAUTHORIZED", 401},
		{"agent.transfer.ip", "/agent/v1/nodes/" + node + "/tasks/" + task + "/model-files", "AGENT_UNAUTHORIZED", 401},
		{"agent.enroll.ip", "/agent/v1/enroll", "TOKEN_UNUSABLE", 400},
	} {
		if family.purpose == "agent.enroll.ip" {
			// Change the typed fixture policy between sequential phases. This is
			// not evidence of a running production policy reload.
			x.close()
			policy.Rates.AgentIPCount = 3
			if e := policy.Validate(); e != nil {
				t.Fatal(e)
			}
			x = transportOpen(t, s)
		}
		clients := []string{"198.51.100." + []string{"10", "20", "30"}[index], "198.51.100." + []string{"11", "21", "31"}[index]}
		for _, clientIP := range clients {
			server := transportProxy(t, x, clientIP)
			t.Cleanup(server.Close)
			browser := transportClient(t, x, server, "admin")
			browser.schemas["AgentProblem"] = agentProblem
			transportProblem(t, browser.call("POST", family.path, map[string]any{}, family.status, "AgentProblem", map[string]string{"X-Forwarded-For": "203.0.113.250", "Forwarded": "for=203.0.113.250;proto=http", "X-Real-IP": "203.0.113.250"}), family.code)
			log.provenance(t, browser.request, clientIP)
			transportProblem(t, browser.call("POST", family.path, map[string]any{}, 429, "Problem", nil), "RATE_LIMITED")
			transportBucket(t, s, family.purpose, clientIP, 1)
			transportBucket(t, s, family.purpose, "203.0.113.250", 0)
			if family.purpose == "agent.enroll.ip" {
				transportBucket(t, s, "agent.request.ip", clientIP, 2)
			}
			server.Close()
		}
		transportBucket(t, s, family.purpose, "127.0.0.1", 0)
	}
	// A direct call to the actual HTTP backend cannot supply an alternate IP
	// without the required trusted proxy protocol. No agent bucket may change.
	backend := transportClientOrigin(t, x, x.backend, "admin", "http://admin.example.invalid")
	transportProblem(t, backend.call("POST", "/agent/v1/enroll", map[string]any{}, 403, "Problem", map[string]string{"X-Forwarded-For": "203.0.113.251"}), "ORIGIN_REJECTED")
	transportBucket(t, s, "agent.enroll.ip", "203.0.113.251", 0)
	transportBucket(t, s, "agent.enroll.ip", "127.0.0.1", 0)
	// The same complete proxy protocol from an actual untrusted socket is
	// rejected before SQL. No caller can promote headers into peer authority.
	x.close()
	s.Config.Server.TrustedProxyCIDRs = []string{"192.0.2.1/32"}
	x = transportOpen(t, s)
	untrusted := transportClient(t, x, x.server, "admin")
	transportProblem(t, untrusted.call("POST", "/agent/v1/enroll", map[string]any{}, 403, "Problem", nil), "ORIGIN_REJECTED")
	transportBucket(t, s, "agent.enroll.ip", "127.0.0.1", 0)
	x.close()
	transportConfigure(s, config.TransportDirectHTTPS)
	x = transportOpen(t, s)
	direct := transportClient(t, x, x.server, "admin")
	transportProblem(t, direct.call("POST", "/agent/v1/enroll", map[string]any{}, 403, "Problem", map[string]string{"X-Forwarded-For": "203.0.113.252", "X-Forwarded-Host": "admin.example.invalid", "X-Forwarded-Proto": "https"}), "ORIGIN_REJECTED")
	transportBucket(t, s, "agent.enroll.ip", "203.0.113.252", 0)
	transportBucket(t, s, "agent.enroll.ip", "127.0.0.1", 0)
	if transportCount(t, s, "SELECT (SELECT count(*) FROM managed_nodes)+(SELECT count(*) FROM node_join_tokens)+(SELECT count(*) FROM node_tasks)") != 0 || transportCount(t, s, "SELECT count(*) FROM audit_events WHERE action='node.enrolled'") != 0 {
		t.Fatal("denied agent requests changed node/join/task/audit state")
	}
}

//go:build sso_provider_integration

package http_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"html"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

type recoveryGitea struct {
	Origin       string `json:"origin"`
	CAFile       string `json:"ca_file"`
	Username     string `json:"username"`
	PasswordFile string `json:"password_file"`
	password     string
	transport    *http.Transport
}

func recoveryPrivateFixture(t *testing.T, path string) []byte {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !filepath.IsAbs(path) || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("protected actual provider fixture file required")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || len(raw) > 1<<20 {
		t.Fatal("bounded actual provider fixture file required")
	}
	return raw
}

func actualRecoveryGitea(t *testing.T) *recoveryGitea {
	t.Helper()
	var p recoveryGitea
	if json.Unmarshal(recoveryPrivateFixture(t, os.Getenv("OWNSOLID_IAM_GITEA_FIXTURE_FILE")), &p) != nil {
		t.Fatal("actual provider manifest required")
	}
	origin, err := url.Parse(p.Origin)
	if err != nil || origin.Scheme != "https" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.String() != "https://"+origin.Host || p.Username == "" {
		t.Fatal("isolated actual HTTPS provider required")
	}
	p.password = strings.TrimSpace(string(recoveryPrivateFixture(t, p.PasswordFile)))
	roots := x509.NewCertPool()
	if p.password == "" || !roots.AppendCertsFromPEM(recoveryPrivateFixture(t, p.CAFile)) {
		t.Fatal("actual provider credentials and CA required")
	}
	p.transport = &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	t.Cleanup(p.transport.CloseIdleConnections)
	response, raw := p.request(t, p.browser(), http.MethodGet, p.Origin+sso.GiteaVersionPath, nil, false)
	var version struct{ Version string }
	if response.StatusCode != 200 || json.Unmarshal(raw, &version) != nil || version.Version != sso.GiteaTestedRelease {
		t.Fatal("pinned actual provider version required")
	}
	return &p
}

func (p *recoveryGitea) browser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Transport: p.transport, Jar: jar, Timeout: 15 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme+"://"+r.URL.Host != p.Origin {
			return http.ErrUseLastResponse
		}
		if len(via) >= 8 {
			return http.ErrUseLastResponse
		}
		return nil
	}}
}

func (p *recoveryGitea) request(t *testing.T, browser *http.Client, method, target string, body io.Reader, basic bool) (*http.Response, []byte) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil || u.Scheme+"://"+u.Host != p.Origin {
		t.Fatal("provider fixture destination refused")
	}
	req, err := http.NewRequestWithContext(t.Context(), method, target, body)
	if err != nil {
		t.Fatal("provider fixture request invalid")
	}
	req.Header.Set("Origin", p.Origin)
	req.Header.Set("Referer", p.Origin+"/user/login")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if basic {
		req.Header.Set("Content-Type", "application/json")
		req.SetBasicAuth(p.Username, p.password)
	}
	response, err := browser.Do(req)
	if err != nil {
		t.Fatal("verified actual provider request failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
		t.Fatal("bounded verified provider response required")
	}
	return response, raw
}

func (p *recoveryGitea) application(t *testing.T, callback string) (string, string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"name": "IAM recovery " + uuid.NewString(), "redirect_uris": []string{callback}, "confidential_client": true})
	if err != nil {
		t.Fatal("provider application input invalid")
	}
	response, raw := p.request(t, p.browser(), http.MethodPost, p.Origin+sso.GiteaApplicationPath, bytes.NewReader(raw), true)
	var app struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if response.StatusCode != 201 || json.Unmarshal(raw, &app) != nil || app.ClientID == "" || app.ClientSecret == "" {
		t.Fatal("actual confidential application registration failed")
	}
	return app.ClientID, app.ClientSecret
}

func (p *recoveryGitea) authorize(t *testing.T, authorization string) string {
	t.Helper()
	u, err := url.Parse(authorization)
	if err != nil || u.Path != sso.GiteaAuthorizationPath {
		t.Fatal("provider authorization input invalid")
	}
	q := u.Query()
	browser := p.browser()
	response, raw := p.request(t, browser, http.MethodGet, authorization, nil, false)
	if response.StatusCode != 200 || response.Request.URL.Path != "/user/login" {
		t.Fatal("actual provider password form required")
	}
	form := url.Values{"user_name": {p.Username}, "password": {p.password}}
	response, raw = p.request(t, browser, http.MethodPost, response.Request.URL.String(), strings.NewReader(form.Encode()), false)
	if response.StatusCode == 200 {
		// Match only the pinned upstream consent template. A changed form fails
		// instead of bypassing consent or manufacturing provider state.
		if !bytes.Contains(raw, []byte(`action="/login/oauth/grant"`)) {
			t.Fatal("actual provider consent form required")
		}
		form = url.Values{}
		namePattern, valuePattern := regexp.MustCompile(`name="([^"]+)"`), regexp.MustCompile(`value="([^"]*)"`)
		for _, input := range regexp.MustCompile(`<input\b[^>]*>`).FindAll(raw, -1) {
			name, value := namePattern.FindSubmatch(input), valuePattern.FindSubmatch(input)
			if len(name) == 2 && len(value) == 2 {
				form.Set(string(name[1]), html.UnescapeString(string(value[1])))
			}
		}
		for _, field := range []string{"client_id", "state", "redirect_uri", "scope"} {
			if form.Get(field) != q.Get(field) || q.Get(field) == "" {
				t.Fatal("provider consent binding changed")
			}
		}
		form.Set("granted", "true")
		response, _ = p.request(t, browser, http.MethodPost, p.Origin+"/login/oauth/grant", strings.NewReader(form.Encode()), false)
	}
	callback, err := url.Parse(response.Header.Get("Location"))
	expected, parseErr := url.Parse(q.Get("redirect_uri"))
	if err != nil || parseErr != nil || response.StatusCode != 302 && response.StatusCode != 303 || callback.Scheme != expected.Scheme || callback.Host != expected.Host || callback.Path != expected.Path || len(callback.Query()) != 2 || callback.Query().Get("state") != q.Get("state") || callback.Query().Get("code") == "" {
		t.Fatal("actual provider callback binding invalid")
	}
	return callback.RequestURI()
}

func recoveryCallback(t *testing.T, c *client, path, want string, issued bool) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.s.URL+path, nil)
	if err != nil {
		t.Fatal("actual callback request invalid")
	}
	req.Host = c.audience + ".example.invalid"
	for _, cookie := range c.cookies {
		req.AddCookie(cookie)
	}
	httpClient := *c.http
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := httpClient.Do(req)
	if err != nil {
		t.Fatal("actual verified callback failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 303 || response.Header.Get("Location") != want || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" || response.Header.Get("X-Request-ID") == "" || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
		t.Fatal("actual callback outcome or security headers invalid")
	}
	session := false
	for _, cookie := range response.Cookies() {
		if !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" {
			t.Fatal("actual callback cookie boundary invalid")
		}
		if cookie.Name == "__Host-"+c.audience+"_session" && cookie.Value != "" && cookie.MaxAge > 0 {
			session = true
		}
		if cookie.MaxAge < 0 {
			delete(c.cookies, cookie.Name)
		} else {
			c.cookies[cookie.Name] = cookie
		}
	}
	if session != issued {
		t.Fatal("actual callback session issuance changed")
	}
}

func TestSSOActualGiteaRecoveryAndDeviceRevocationHTTPS(t *testing.T) {
	p := actualRecoveryGitea(t)
	s, _, _, template, password := setup(t)
	contracts := maps.Clone(template.schemas)
	for name, schema := range httpfixture.Schemas(t, "../../../../../../api/openapi/v1/sso/openapi.json") {
		contracts["sso:"+name] = schema
	}
	original, _, active, _, _ := ssoRecoveryKeyrings(t)
	serve := func(path string, recovery bool) (*identity.Service, *client) {
		t.Helper()
		cfg := s.Config
		cfg.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: path, Providers: []config.SSOProvider{{Origin: p.Origin, Issuer: p.Origin, DialAddresses: []string{"127.0.0.1"}, CAFile: p.CAFile}}}
		accounts := &identity.Service{DB: s.DB, Config: cfg, Passwords: s.Passwords, Mail: s.Mail, HMACKey: s.HMACKey}
		var owner *sso.Service
		var err error
		if recovery {
			owner, err = sso.NewWithKeyringRecovery(t.Context(), accounts)
		} else {
			owner, err = sso.New(accounts)
		}
		if err != nil {
			t.Fatal("actual provider identity composition failed", err)
		}
		t.Cleanup(owner.Close)
		accounts.Sources = owner
		handler := transport.New(accounts)
		if handler.InitializationError() != nil {
			t.Fatal("actual provider HTTP composition failed")
		}
		admin := newClient(t, accountTLSServer(t, handler), "admin", contracts)
		admin.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
		return accounts, admin
	}
	_, admin := serve(original, false)
	id := uuid.NewString()
	base := "/api/v1/auth/sso/" + id
	callback := s.Config.Server.AdminOrigin + base + "/callback"
	clientID, secret := p.application(t, callback)
	const catalog = "/api/v1/admin/sso/connectors"
	admin.mutate(http.MethodPost, catalog, sso.CreateConnector{ID: id, Name: "Actual provider recovery", Provider: sso.ProviderGitea, Origin: p.Origin, ClientID: clientID, ClientSecret: secret, Enabled: true, Reason: "Actual isolated provider recovery acceptance"}, 201, "sso:SSOConnectorResponse")
	link := admin.mutate(http.MethodPost, base+"/starts", map[string]any{"mode": "link", "password": password, "reason": "Link actual synthetic provider identity"}, 200, "sso:SSOStartResponse")
	recoveryCallback(t, admin, p.authorize(t, link["authorization_url"].(string)), "/account/sso-result?result=linked", true)
	signIn := func(server *client) (*client, *client, string) {
		t.Helper()
		browser := newClient(t, server.s, "admin", contracts)
		start := browser.mutate(http.MethodPost, base+"/starts", map[string]any{"mode": "login", "password": nil, "reason": nil}, 200, "sso:SSOStartResponse")
		path := p.authorize(t, start["authorization_url"].(string))
		replay := newClient(t, server.s, "admin", contracts)
		replay.cookies = maps.Clone(browser.cookies)
		recoveryCallback(t, browser, path, "/login/sso-result?result=authenticated", true)
		browser.request(http.MethodGet, "/api/v1/me", nil, 200, "MeResponse", nil)
		sessionID := ownSession(t, browser)
		var sourced bool
		if err := s.DB.QueryRow(t.Context(), `SELECT se.sso_connector_id=$2 AND b.user_id=se.user_id
			AND f.state='succeeded' AND f.issued_session_id=se.id AND f.authenticated_user_id=se.user_id
			FROM sessions se JOIN sso_bindings b ON b.id=se.sso_binding_id AND b.tenant_id=se.sso_tenant_id
			JOIN sso_flows f ON f.id=se.sso_flow_id AND f.tenant_id=se.sso_tenant_id
			WHERE se.id=$1`, sessionID, id).Scan(&sourced); err != nil || !sourced {
			t.Fatal("actual provider sign-in lacks durable source authority", err)
		}
		return browser, replay, path
	}
	old, _, _ := signIn(admin)
	_, recovered := serve(active, true)
	expired := newClient(t, recovered.s, "admin", contracts)
	expired.cookies = maps.Clone(old.cookies)
	problemCode(t, expired.request(http.MethodGet, "/api/v1/me", nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	clientID, secret = p.application(t, callback)
	recovered.mutate(http.MethodPatch, catalog+"/"+id, sso.UpdateConnector{Revision: 1, Name: "Actual provider recovery", Enabled: true, ClientID: &clientID, ClientSecret: &secret, Reason: "Replace lost-key actual provider credentials"}, 200, "sso:SSOConnectorResponse")
	manager, _, _ := signIn(recovered)
	target, replay, replayPath := signIn(recovered)
	targetID := ownSession(t, target)
	manager.mutate(http.MethodDelete, managedSessionsPath+"/"+targetID, nil, 204, "")
	problemCode(t, target.request(http.MethodGet, "/api/v1/me", nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	manager.request(http.MethodGet, "/api/v1/me", nil, 200, "MeResponse", nil)
	var before, after, events int
	if err := s.DB.QueryRow(t.Context(), "SELECT count(*) FROM sessions").Scan(&before); err != nil {
		t.Fatal(err)
	}
	recoveryCallback(t, replay, replayPath, "/login/sso-result?error=SSO_FLOW_UNUSABLE", false)
	if err := s.DB.QueryRow(t.Context(), "SELECT count(*) FROM sessions").Scan(&after); err != nil || before != after {
		t.Fatal("replayed actual callback issued another session", err)
	}
	if err := s.DB.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE target_type='session' AND target_id=$1 AND action='auth.logout' AND reason='session_revoked'", targetID).Scan(&events); err != nil || events != 1 {
		t.Fatal("actual sourced device revocation audit invalid", err)
	}
	t.Log("Actual pinned Gitea TLS/password/consent/PKCE link and sign-in, local credential repair after key loss, new sourced sign-ins and targeted device revocation with callback replay refusal. Explicit identity HTTP composition; no installed startup or rendered browser claim.")
}

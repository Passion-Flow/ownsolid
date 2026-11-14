package prototype

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureConfiguration struct {
	Issuer   string `json:"issuer"`
	Client   string `json:"client_id"`
	Secret   string `json:"client_secret"`
	Username string `json:"username"`
	Password string `json:"password"`
	Subject  string `json:"subject"`
	Callback string `json:"callback"`
}
type countTransport struct {
	base      http.RoundTripper
	tokenPath string
	calls     atomic.Int64
}

func (c *countTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == c.tokenPath {
		c.calls.Add(1)
	}
	return c.base.RoundTrip(r)
}
func liveClient(t *testing.T) (fixtureConfiguration, *http.Client, *countTransport) {
	t.Helper()
	path := os.Getenv("IAM102_FIXTURE_CONFIG")
	if path == "" {
		t.Skip("explicit private provider fixture required")
	}
	var conf fixtureConfiguration
	raw, e := os.ReadFile(path)
	if e != nil || json.Unmarshal(raw, &conf) != nil {
		t.Fatal("private fixture configuration unavailable")
	}
	cert, e := os.ReadFile(os.Getenv("IAM102_FIXTURE_CA"))
	pool := x509.NewCertPool()
	if e != nil || !pool.AppendCertsFromPEM(cert) {
		t.Fatal("private fixture CA unavailable")
	}
	approved, _ := url.Parse(conf.Issuer)
	expected := approved.Host
	dialer := net.Dialer{Timeout: time.Second}
	base := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DisableCompression: true, MaxConnsPerHost: 2, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: 5 * time.Second}
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != expected {
			return nil, rejected
		}
		return dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", approved.Port()))
	}
	t.Cleanup(base.CloseIdleConnections)
	counter := &countTransport{base: base, tokenPath: approved.Path + "/protocol/openid-connect/token"}
	client := &http.Client{Transport: counter, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return conf, client, counter
}
func randomToken(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, e := rand.Read(raw); e != nil {
		t.Fatal("private randomness unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
func readResponse(t *testing.T, r *http.Response) []byte {
	t.Helper()
	defer r.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(r.Body, 512<<10))
	if e != nil || len(raw) >= 512<<10 {
		t.Fatal("provider response unavailable or oversized")
	}
	return raw
}

var formPattern = regexp.MustCompile(`(?is)<form\b([^>]*)>`)
var attributePattern = regexp.MustCompile(`([a-zA-Z_-]+)\s*=\s*"([^"]*)"`)

func passwordForm(t *testing.T, raw []byte) string {
	t.Helper()
	var found string
	for _, form := range formPattern.FindAllSubmatch(raw, -1) {
		values := map[string]string{}
		for _, a := range attributePattern.FindAllSubmatch(form[1], -1) {
			name := strings.ToLower(string(a[1]))
			if _, exists := values[name]; exists {
				t.Fatal("ambiguous provider form")
			}
			values[name] = html.UnescapeString(string(a[2]))
		}
		if values["id"] == "kc-form-login" {
			if found != "" || values["method"] != "post" {
				t.Fatal("ambiguous password form")
			}
			found = values["action"]
		}
	}
	if found == "" {
		t.Fatal("real provider password form missing")
	}
	return found
}
func authorize(t *testing.T, conf fixtureConfiguration, client *http.Client, m metadata) (string, string, string) {
	t.Helper()
	jar, e := cookiejar.New(nil)
	if e != nil {
		t.Fatal("browser cookie fixture failed")
	}
	browser := *client
	browser.Jar = jar
	state, nonce, verifier := randomToken(t), randomToken(t), randomToken(t)
	sum := sha256.Sum256([]byte(verifier))
	query := url.Values{"client_id": {conf.Client}, "redirect_uri": {conf.Callback}, "response_type": {"code"}, "scope": {"openid profile email"}, "state": {state}, "nonce": {nonce}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}}
	r, e := browser.Get(m.Authorization + "?" + query.Encode())
	if e != nil {
		t.Fatal("real provider authorization request failed")
	}
	body := readResponse(t, r)
	if r.StatusCode != 200 {
		t.Fatal("real provider did not present login form", r.StatusCode)
	}
	action := passwordForm(t, body)
	target, e := url.Parse(action)
	issuer, _ := url.Parse(conf.Issuer)
	if e != nil || target.Scheme != "https" || target.Host != issuer.Host || target.Path != issuer.Path+"/login-actions/authenticate" {
		t.Fatal("provider form destination rejected")
	}
	request, e := http.NewRequest("POST", action, strings.NewReader(url.Values{"username": {conf.Username}, "password": {conf.Password}, "credentialId": {""}}.Encode()))
	if e != nil {
		t.Fatal("password form request failed")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", issuer.Scheme+"://"+issuer.Host)
	r, e = browser.Do(request)
	if e != nil {
		t.Fatal("real provider password form failed")
	}
	readResponse(t, r)
	if r.StatusCode != 302 {
		t.Fatal("real provider password did not complete code flow", r.StatusCode)
	}
	callback, e := url.Parse(r.Header.Get("Location"))
	expected, _ := url.Parse(conf.Callback)
	if e != nil || callback.Scheme != expected.Scheme || callback.Host != expected.Host || callback.Path != expected.Path || callback.Fragment != "" || callback.Query().Get("state") != state || callback.Query().Get("iss") != conf.Issuer || callback.Query().Get("code") == "" {
		t.Fatal("provider code/state/issuer callback rejected")
	}
	return callback.Query().Get("code"), nonce, verifier
}
func exchange(t *testing.T, conf fixtureConfiguration, client *http.Client, m metadata, code, verifier, callback string) (int, map[string]json.RawMessage) {
	t.Helper()
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {callback}, "code_verifier": {verifier}}
	request, e := http.NewRequest("POST", m.Token, strings.NewReader(form.Encode()))
	if e != nil {
		t.Fatal("code exchange request failed")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth(url.QueryEscape(conf.Client), url.QueryEscape(conf.Secret))
	request.GetBody = nil
	response, e := client.Do(request)
	if e != nil {
		t.Fatal("real provider exchange transport failed")
	}
	raw := readResponse(t, response)
	if uniqueJSON(raw) != nil {
		t.Fatal("provider exchange response invalid")
	}
	var out map[string]json.RawMessage
	if json.Unmarshal(raw, &out) != nil {
		t.Fatal("provider token response invalid")
	}
	return response.StatusCode, out
}
func TestLiveKeycloakAuthorizationCode(t *testing.T) {
	conf, client, counter := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	m, e := discover(ctx, client, conf.Issuer)
	if e != nil {
		t.Fatal("actual private realm discovery failed")
	}
	if !strings.Contains(m.Issuer, "/realms/iam102") {
		t.Fatal("realm issuer path lost")
	}
	keys := newKeys(client, m.JWKS, time.Now)
	a := adapter{issuer: conf.Issuer, client: conf.Client, keys: keys, now: time.Now}
	code, nonce, verifier := authorize(t, conf, client, m)
	before := counter.calls.Load()
	status, response := exchange(t, conf, client, m, code, verifier, conf.Callback)
	if status != 200 || counter.calls.Load() != before+1 {
		t.Fatal("single actual code exchange did not succeed")
	}
	raw, ok := text(response, "id_token")
	if !ok {
		t.Fatal("actual provider ID token missing")
	}
	token, e := a.verify(ctx, raw, nonce)
	if e != nil || token.Subject != conf.Subject || token.Issuer != conf.Issuer {
		t.Fatal("actual signed issuer/subject/nonce validation failed")
	}
	if _, e := strconv.ParseInt(token.Subject, 10, 64); e == nil {
		t.Fatal("fixture failed to exercise nonnumeric subject")
	}
	if _, e := a.verify(ctx, raw, "wrong-flow-nonce"); e == nil {
		t.Fatal("actual token survived nonce substitution")
	}
	wrong := a
	wrong.client = "another-client"
	if _, e := wrong.verify(ctx, raw, nonce); e == nil {
		t.Fatal("actual token survived audience substitution")
	}
	wrong = a
	wrong.issuer += "-other"
	if _, e := wrong.verify(ctx, raw, nonce); e == nil {
		t.Fatal("actual token survived issuer substitution")
	}
	parts := strings.Split(raw, ".")
	if parts[2][0] == 'A' {
		parts[2] = "B" + parts[2][1:]
	} else {
		parts[2] = "A" + parts[2][1:]
	}
	if _, e := a.verify(ctx, strings.Join(parts, "."), nonce); e == nil {
		t.Fatal("actual token signature modification accepted")
	}
	access, ok := text(response, "access_token")
	if !ok {
		t.Fatal("actual provider access token missing")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", m.UserInfo, nil)
	if e != nil {
		t.Fatal("userinfo request failed")
	}
	req.Header.Set("Authorization", "Bearer "+access)
	info, e := client.Do(req)
	if e != nil {
		t.Fatal("actual userinfo transport failed")
	}
	body := readResponse(t, info)
	var user map[string]json.RawMessage
	if info.StatusCode != 200 || uniqueJSON(body) != nil || json.Unmarshal(body, &user) != nil {
		t.Fatal("actual userinfo rejected")
	}
	subject, ok := text(user, "sub")
	if !ok || subject != token.Subject {
		t.Fatal("actual UserInfo subject did not match ID token")
	}
	before = counter.calls.Load()
	status, _ = exchange(t, conf, client, m, code, verifier, conf.Callback)
	if status != 400 || counter.calls.Load() != before+1 {
		t.Fatal("provider code replay or automatic exchange retry accepted")
	}
	t.Log("Actual pinned Keycloak TLS password form, realm discovery, form-encoded Basic credentials, S256 code exchange, exact opaque subject, signed token and UserInfo binding passed; modified issuer/audience/nonce/signature and consumed-code replay were refused. Standalone protocol prototype only.")
}
func TestLiveKeycloakExchangeRefusals(t *testing.T) {
	conf, client, counter := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m, e := discover(ctx, client, conf.Issuer)
	if e != nil {
		t.Fatal("actual private realm discovery failed")
	}
	for _, name := range []string{"wrong_pkce", "wrong_redirect", "wrong_secret"} {
		t.Run(name, func(t *testing.T) {
			code, _, verifier := authorize(t, conf, client, m)
			callback := conf.Callback
			changed := conf
			switch name {
			case "wrong_pkce":
				verifier = randomToken(t)
			case "wrong_redirect":
				callback = "https://other.iam102.test/callback"
			case "wrong_secret":
				changed.Secret = randomToken(t)
			}
			before := counter.calls.Load()
			status, response := exchange(t, changed, client, m, code, verifier, callback)
			if (status != 400 && status != 401) || counter.calls.Load() != before+1 {
				t.Fatal("invalid exchange passed or was retried")
			}
			if _, ok := response["id_token"]; ok {
				t.Fatal("rejected exchange released ID token")
			}
			if _, ok := response["access_token"]; ok {
				t.Fatal("rejected exchange released access token")
			}
		})
	}
}
func TestLiveKeycloakReady(t *testing.T) {
	conf, client, _ := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for {
		if _, e := discover(ctx, client, conf.Issuer); e == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("private Keycloak did not become ready within its startup bound")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

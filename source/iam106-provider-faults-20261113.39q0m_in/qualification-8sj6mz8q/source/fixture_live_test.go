package boundary

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	relyingparty "github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"golang.org/x/oauth2"
)

//go:embed schema.sql
var protocolSchema string

type protocolFixture struct {
	evidence                                                                           *fixtureEvidence
	ctx                                                                                context.Context
	folder, configuration, opOrigin, rpOrigin, user, subject, client, secret, password string
	config                                                                             childConfiguration
	pool                                                                               *pgxpool.Pool
	listener                                                                           *net.TCPListener
	child                                                                              *childProcess
	rpServer                                                                           *httptest.Server
	browser, serverClient                                                              *http.Client
	browserTransport, serverTransport                                                  *approvedTransport
	app                                                                                *relyingApp
}
type rpPending struct {
	nonce, verifier string
	expires         time.Time
}
type rpNonceContext struct{}
type rpResult struct {
	tokens  *oidc.Tokens[*oidc.IDTokenClaims]
	subject string
}
type relyingApp struct {
	mu      sync.Mutex
	remote  relyingparty.RelyingParty
	pending map[string]rpPending
	results chan rpResult
	policy  providerPolicy
	host    string
}

func (a *relyingApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != "GET" || r.Host != a.host {
		protocolError(w, 400, "invalid_request")
		return
	}
	a.mu.Lock()
	remote := a.remote
	a.mu.Unlock()
	if remote == nil {
		protocolError(w, 503, "server_error")
		return
	}
	if r.URL.Path == "/start" {
		state, e := tokenRandom()
		if e != nil {
			protocolError(w, 500, "server_error")
			return
		}
		nonce, e := tokenRandom()
		if e != nil {
			protocolError(w, 500, "server_error")
			return
		}
		verifier, e := tokenRandom()
		if e != nil {
			protocolError(w, 500, "server_error")
			return
		}
		a.mu.Lock()
		if len(a.pending) >= a.policy.MaxPending {
			a.mu.Unlock()
			protocolError(w, 503, "server_error")
			return
		}
		a.pending[state] = rpPending{nonce: nonce, verifier: verifier, expires: time.Now().Add(a.policy.RequestLifetime)}
		a.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "__Host-RPState", Value: state, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(a.policy.RequestLifetime.Seconds())})
		destination := remote.OAuthConfig().AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce), oauth2.S256ChallengeOption(verifier))
		http.Redirect(w, r, destination, 302)
		return
	}
	if r.URL.Path != "/callback" {
		protocolError(w, 404, "invalid_request")
		return
	}
	values, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		protocolError(w, 400, "invalid_request")
		return
	}
	state := values.Get("state")
	cookie, ok := cookieValue(r, "__Host-RPState")
	if !ok || cookie != state {
		protocolError(w, 403, "access_denied")
		return
	}
	a.mu.Lock()
	pending, exists := a.pending[state]
	delete(a.pending, state)
	a.mu.Unlock()
	if !exists || time.Now().After(pending.expires) || values.Get("code") == "" || values.Get("error") != "" {
		protocolError(w, 401, "access_denied")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.policy.Operation)
	defer cancel()
	ctx = context.WithValue(ctx, rpNonceContext{}, pending.nonce)
	tokens, e := relyingparty.CodeExchange[*oidc.IDTokenClaims](ctx, values.Get("code"), remote, relyingparty.WithCodeVerifier(pending.verifier))
	if e != nil {
		protocolError(w, 401, "access_denied")
		return
	}
	info, e := relyingparty.Userinfo[*oidc.UserInfo](ctx, tokens.AccessToken, tokens.TokenType, tokens.IDTokenClaims.GetSubject(), remote)
	if e != nil {
		protocolError(w, 401, "access_denied")
		return
	}
	select {
	case a.results <- rpResult{tokens: tokens, subject: info.Subject}:
	default:
		protocolError(w, 503, "server_error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"signed_in": true, "sub": info.Subject})
}
func fixturePolicy() providerPolicy {
	return providerPolicy{Operation: 5 * time.Second, RequestLifetime: 30 * time.Second, CodeLifetime: 10 * time.Second, TokenLifetime: time.Minute, SessionLifetime: 2 * time.Minute, IDLifetime: 30 * time.Second, MaxHeaders: 16384, MaxConnections: 6, MaxPending: 32, MaxResponse: 32768, Request: rp()}
}
func newProtocolFixture(t *testing.T, change func(*providerPolicy)) *protocolFixture {
	t.Helper()
	dsnFile := os.Getenv("IAM_OP_DSN_FILE")
	if dsnFile == "" {
		t.Skip("explicit private PostgreSQL protocol fixture not selected")
	}
	policy := fixturePolicy()
	if change != nil {
		change(&policy)
	}
	folder := t.TempDir()
	if os.Chmod(folder, 0700) != nil {
		t.Fatal("private protocol directory unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	f := &protocolFixture{ctx: ctx, folder: folder, user: uuid.NewString(), subject: random(t), client: "catalog app :+/%", secret: random(t) + " :+/%", password: random(t)}
	f.config = childConfiguration{DSNFile: dsnFile, Tenant: uuid.NewString(), Schema: "iam106_" + strings.ReplaceAll(uuid.NewString(), "-", ""), LoginUser: f.user, LoginName: "fixture", SigningID: "signing-current", PublicFiles: map[string]string{}, CodeFiles: map[string]string{}, BearerFiles: map[string]string{}, CodeActive: "code-current", BearerActive: "bearer-current", Policy: policy, Crypto: cp()}
	f.evidence = newFixtureEvidence(t, f.config.Schema)
	f.evidence.record(f.password, f.secret)
	adminConfig := f.config
	adminConfig.Schema = "public"
	admin, err := fixturePool(ctx, adminConfig)
	if err != nil {
		cancel()
		t.Fatal("private PostgreSQL connection refused")
	}
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+f.config.Schema); err != nil {
		admin.Close()
		cancel()
		t.Fatal("owned protocol schema unavailable")
	}
	admin.Close()
	t.Cleanup(func() {
		if f.child != nil {
			stopChild(t, f.child, policy.Operation)
		}
		if f.rpServer != nil {
			f.rpServer.Close()
		}
		f.evidence.save(t, f.folder)
		if f.browserTransport != nil {
			f.browserTransport.inner.CloseIdleConnections()
		}
		if f.serverTransport != nil {
			f.serverTransport.inner.CloseIdleConnections()
		}
		if f.listener != nil {
			f.listener.Close()
		}
		if f.pool != nil {
			f.pool.Close()
		}
		cleanup, done := context.WithTimeout(context.Background(), policy.Operation)
		defer done()
		admin, err := fixturePool(cleanup, adminConfig)
		if err == nil {
			_, err = admin.Exec(cleanup, `DROP SCHEMA `+f.config.Schema+` CASCADE`)
			admin.Close()
		}
		if err != nil {
			t.Error("owned protocol schema cleanup failed")
		}
		cancel()
	})
	f.pool, err = fixturePool(ctx, f.config)
	if err != nil {
		t.Fatal("owned protocol pool unavailable")
	}
	for name, value := range map[string]string{"client-secret": f.secret, "fixture-password": f.password} {
		path := filepath.Join(folder, name)
		privateWrite(t, path, []byte(value))
		raw, e := readPrivateFile(path, 8192)
		if e != nil {
			t.Fatal("private credential file refused")
		}
		if name == "client-secret" {
			f.secret = string(raw)
		} else {
			f.password = string(raw)
		}
		clear(raw)
	}
	if _, err = f.pool.Exec(ctx, protocolSchema); err != nil {
		t.Fatal("protocol schema did not initialize")
	}
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal("owned OP listener unavailable")
	}
	f.listener = listener
	_, opPort, _ := net.SplitHostPort(listener.Addr().String())
	f.opOrigin = "https://op.iam106.test:" + opPort
	f.config.Origin = f.opOrigin
	authority := testAuthority(t)
	_, certFile, keyFile := authority.leaf(t, folder, "op.iam106.test")
	f.config.CertificateFile, f.config.CertificateKeyFile = certFile, keyFile
	pair, _, _ := authority.leaf(t, folder, "rp.iam106.test")
	f.app = &relyingApp{pending: map[string]rpPending{}, results: make(chan rpResult, policy.MaxPending), policy: policy}
	f.rpServer = httptest.NewUnstartedServer(f.app)
	f.rpServer.Config.ReadHeaderTimeout = policy.Operation
	f.rpServer.Config.ReadTimeout = policy.Operation
	f.rpServer.Config.WriteTimeout = policy.Operation
	f.rpServer.Config.MaxHeaderBytes = policy.MaxHeaders
	f.rpServer.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	_, rpPort, _ := net.SplitHostPort(f.rpServer.Listener.Addr().String())
	f.rpOrigin = "https://rp.iam106.test:" + rpPort
	f.app.host = "rp.iam106.test:" + rpPort
	f.rpServer.StartTLS()
	signing, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("private signing key generation failed")
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(signing)
	if err != nil {
		t.Fatal("private signing key encoding failed")
	}
	f.config.SigningFile = filepath.Join(folder, "signing-key.pem")
	privateWrite(t, f.config.SigningFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
	clear(encoded)
	public, err := x509.MarshalPKIXPublicKey(&signing.PublicKey)
	if err != nil {
		t.Fatal("public signing key encoding failed")
	}
	publicFile := filepath.Join(folder, "signing-public.pem")
	privateWrite(t, publicFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))
	f.config.PublicFiles[f.config.SigningID] = publicFile
	for id, files := range map[string]map[string]string{f.config.CodeActive: f.config.CodeFiles, f.config.BearerActive: f.config.BearerFiles} {
		value := key(t, id)
		path := filepath.Join(folder, id+".key")
		privateWrite(t, path, value.Value[:])
		files[id] = path
	}
	workspace, installation, membership := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO op_fixture_tenants VALUES($1,true)`, []any{f.config.Tenant}},
		{`INSERT INTO op_fixture_installations VALUES($1,$2,$3,1,true)`, []any{f.config.Tenant, workspace, installation}},
		{`INSERT INTO op_fixture_principals VALUES($1,$2,$3,1,true,$4)`, []any{f.config.Tenant, f.user, f.subject, digest(f.password)}},
		{`INSERT INTO op_fixture_memberships VALUES($1,$2,$3,$4)`, []any{f.config.Tenant, workspace, f.user, membership}},
		{`INSERT INTO op_fixture_clients VALUES($1,$2,$3,$4,$5,1,$6,true)`, []any{f.config.Tenant, f.client, workspace, installation, digest(f.secret), f.rpOrigin + "/callback"}},
	} {
		if _, err = f.pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal("scoped protocol seed failed")
		}
	}
	f.configuration = filepath.Join(folder, "child.json")
	f.saveConfiguration(t)
	f.child = startChild(t, f.listener, f.configuration, folder, policy.Operation)
	f.evidence.child(f.child)
	opHost := "op.iam106.test:" + opPort
	rpHost := "rp.iam106.test:" + rpPort
	f.serverTransport = newApprovedTransport(authority.roots, map[string]string{opHost: listener.Addr().String()}, policy.MaxResponse, policy.Operation)
	f.serverTransport.record = f.evidence.record
	f.serverClient = &http.Client{Transport: f.serverTransport, Timeout: policy.Operation}
	f.browserTransport = newApprovedTransport(authority.roots, map[string]string{opHost: listener.Addr().String(), rpHost: f.rpServer.Listener.Addr().String()}, policy.MaxResponse, policy.Operation)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal("private browser jar unavailable")
	}
	f.browser = &http.Client{Transport: f.browserTransport, Timeout: policy.Operation, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	remote, err := relyingparty.NewRelyingPartyOIDC(ctx, f.opOrigin, f.client, f.secret, f.rpOrigin+"/callback", []string{oidc.ScopeOpenID}, relyingparty.WithHTTPClient(f.serverClient), relyingparty.WithSigningAlgsFromDiscovery(), relyingparty.WithVerifierOpts(relyingparty.WithIssuedAtOffset(0), relyingparty.WithNonce(func(ctx context.Context) string { value, _ := ctx.Value(rpNonceContext{}).(string); return value })))
	if err != nil {
		t.Fatal("actual RP discovery refused private provider")
	}
	remote.OAuthConfig().Endpoint.AuthStyle = oauth2.AuthStyleInHeader
	f.app.mu.Lock()
	f.app.remote = remote
	f.app.mu.Unlock()
	return f
}
func (f *protocolFixture) saveConfiguration(t *testing.T) {
	t.Helper()
	encoded, err := json.Marshal(f.config)
	if err != nil {
		t.Fatal("private configuration serialization failed")
	}
	privateWrite(t, f.configuration, encoded)
}
func (f *protocolFixture) restart(t *testing.T) {
	t.Helper()
	stopChild(t, f.child, f.config.Policy.Operation)
	f.child = nil
	f.serverTransport.inner.CloseIdleConnections()
	f.browserTransport.inner.CloseIdleConnections()
	f.saveConfiguration(t)
	f.child = startChild(t, f.listener, f.configuration, f.folder, f.config.Policy.Operation)
	f.evidence.child(f.child)
}
func (f *protocolFixture) request(t *testing.T, client *http.Client, method, target string, body url.Values, origin string) *http.Response {
	t.Helper()
	var input io.Reader
	if body != nil {
		input = strings.NewReader(body.Encode())
	}
	r, err := http.NewRequestWithContext(f.ctx, method, target, input)
	if err != nil {
		t.Fatal("private request construction failed")
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for name, values := range body {
		switch name {
		case "password", "code", "csrf", "code_verifier", "client_secret", "state", "nonce", "token":
			f.evidence.record(values...)
		}
	}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal("private HTTPS request failed")
	}
	if destination, e := url.Parse(response.Header.Get("Location")); e == nil {
		for name, values := range destination.Query() {
			switch name {
			case "code", "state", "nonce", "code_verifier":
				f.evidence.record(values...)
			}
		}
	}
	return response
}

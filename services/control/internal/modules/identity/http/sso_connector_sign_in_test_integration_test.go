package http_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

type connectorSignInFixture struct {
	accounts                                                 *identity.Service
	owner                                                    *sso.ConnectorSignInTests
	admin                                                    *client
	connector, base, clientID, secret, token, code, password string
	challenge, callback                                      string
	exchanges                                                atomic.Int32
	mu                                                       sync.Mutex
	entered, release                                         chan struct{}
}

func connectorSignInFixtureNew(t *testing.T) *connectorSignInFixture {
	t.Helper()
	s, _, _, template, password := setup(t)
	ddl, err := os.ReadFile("../../sso/testdata/connector_sign_in_tests.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	original, _, _, _, _ := ssoRecoveryKeyrings(t)
	f := &connectorSignInFixture{accounts: s, password: password, clientID: uuid.NewString(), secret: security.Token(), token: security.Token(), code: security.Token()}
	var provider *httptest.Server
	provider = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case sso.GiteaTokenPath:
			f.exchanges.Add(1)
			user, password, ok := r.BasicAuth()
			r.ParseForm()
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || user != f.clientID || password != f.secret || r.Form.Get("code") != f.code || r.Form.Get("redirect_uri") != f.callback || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				http.Error(w, "invalid fixture exchange", 400)
				return
			}
			f.mu.Lock()
			entered, release := f.entered, f.release
			f.mu.Unlock()
			if entered != nil {
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": f.token, "token_type": "bearer", "expires_in": 300})
		case sso.GiteaIntrospectionPath:
			user, password, ok := r.BasicAuth()
			r.ParseForm()
			if !ok || user != f.clientID || password != f.secret || r.Form.Get("token") != f.token {
				http.Error(w, "invalid fixture introspection", 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"active": true, "scope": sso.GiteaScope, "iss": provider.URL, "aud": []string{f.clientID}, "sub": "73"})
		case sso.GiteaUserPath:
			if r.Header.Get("Authorization") != "Bearer "+f.token {
				http.Error(w, "invalid fixture user", 401)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"id": 73, "active": true, "prohibit_login": false, "type": "User", "login": "provider-member", "full_name": "Provider member"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	ca := filepath.Join(filepath.Dir(original), "connector-test-ca.pem")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: provider.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	s.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: original, Providers: []config.SSOProvider{{Origin: provider.URL, Issuer: provider.URL, DialAddresses: []string{"127.0.0.1"}, CAFile: ca}}}
	owner, err := sso.New(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	s.Sources = owner
	f.owner, err = sso.NewConnectorSignInTests(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	contracts := maps.Clone(template.schemas)
	for name, schema := range httpfixture.Schemas(t, "../../../../../../api/openapi/v1/sso/openapi.json") {
		contracts["sso:"+name] = schema
	}
	handler := transport.New(s)
	handler.SSO.ConnectorTests = f.owner
	f.admin = newClient(t, accountTLSServer(t, handler), "admin", contracts)
	f.admin.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	f.connector = uuid.NewString()
	f.base = "/api/v1/admin/sso/connectors/" + f.connector + "/sign-in-tests"
	f.admin.mutate(http.MethodPost, "/api/v1/admin/sso/connectors", sso.CreateConnector{ID: f.connector, Name: "Diagnostic fixture", Provider: sso.ProviderGitea, Origin: provider.URL, ClientID: f.clientID, ClientSecret: f.secret, Enabled: false, Reason: "Verify a disabled connector interactively"}, 201, "sso:SSOConnectorResponse")
	return f
}

func (f *connectorSignInFixture) start(t *testing.T, key string) map[string]any {
	t.Helper()
	f.admin.refresh()
	out := f.admin.request(http.MethodPost, f.base, sso.ConnectorSignInTestInput{Revision: 1, Reason: "Inspect actual upstream identity"}, 200, "", map[string]string{"Idempotency-Key": key})
	u, err := url.Parse(out["authorization_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	f.challenge, f.callback = u.Query().Get("code_challenge"), u.Query().Get("redirect_uri")
	if f.callback != f.accounts.Config.Server.AdminOrigin+f.base+"/callback" || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("diagnostic callback or PKCE changed")
	}
	return out
}

func (f *connectorSignInFixture) callbackPath(out map[string]any) string {
	u, _ := url.Parse(out["authorization_url"].(string))
	return f.base + "/callback?" + url.Values{"state": {u.Query().Get("state")}, "code": {f.code}}.Encode()
}

func (f *connectorSignInFixture) blockExchange(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	entered, release := make(chan struct{}), make(chan struct{})
	f.mu.Lock()
	f.entered, f.release = entered, release
	f.mu.Unlock()
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	return entered, unblock
}

func (f *connectorSignInFixture) authority(t *testing.T) string {
	t.Helper()
	var snapshot string
	err := f.accounts.DB.QueryRow(t.Context(), `SELECT jsonb_build_array(
 (SELECT coalesce(jsonb_agg(to_jsonb(s)-'last_seen_at' ORDER BY id),'[]'::jsonb) FROM sessions s),
 (SELECT coalesce(jsonb_agg(to_jsonb(b) ORDER BY id),'[]'::jsonb) FROM sso_bindings b),
 (SELECT coalesce(jsonb_agg(to_jsonb(f) ORDER BY id),'[]'::jsonb) FROM sso_flows f))::text`).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// A known foreign row makes the isolation check independent of an unknown-ID
// refusal. Its copied ciphertext is never opened by a correctly scoped request.
func (f *connectorSignInFixture) foreignTest(t *testing.T, source string) (connector, test, state string) {
	t.Helper()
	tenant := uuid.NewString()
	connector, test, state = uuid.NewString(), uuid.NewString(), security.Token()
	if _, err := f.accounts.DB.Exec(t.Context(), "INSERT INTO tenants(id,name,status) VALUES($1,'Foreign diagnostic tenant','active')", tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := f.accounts.DB.Exec(t.Context(), `INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by)
 SELECT $1,$2,'Foreign diagnostic connector',provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by FROM sso_connectors WHERE id=$3`, connector, tenant, f.connector); err != nil {
		t.Fatal(err)
	}
	if _, err := f.accounts.DB.Exec(t.Context(), `INSERT INTO sso_connector_sign_in_tests(id,tenant_id,connector_id,connector_revision,auth_version,actor_user_id,actor_session_id,actor_epoch,idempotency_key,fingerprint,reason,state_hash,browser_hash,secret_key_id,secret_nonce,secret_ciphertext,created_at,expires_at)
 SELECT $1,$2,$3,connector_revision,auth_version,actor_user_id,actor_session_id,actor_epoch,$4,fingerprint,reason,$5,browser_hash,secret_key_id,secret_nonce,secret_ciphertext,created_at,expires_at FROM sso_connector_sign_in_tests WHERE id=$6`, test, tenant, connector, uuid.NewString(), security.Digest(state), source); err != nil {
		t.Fatal(err)
	}
	return connector, test, state
}

func TestConnectorSignInTestHTTPS(t *testing.T) {
	f := connectorSignInFixtureNew(t)
	key := uuid.NewString()
	unmounted := newClient(t, accountTLSServer(t, transport.New(f.accounts)), "admin", f.admin.schemas)
	unmounted.cookies = maps.Clone(f.admin.cookies)
	problemCode(t, unmounted.mutate(http.MethodPost, f.base, sso.ConnectorSignInTestInput{Revision: 1, Reason: "Unpublished route remains closed"}, 404, ""), "RESOURCE_NOT_FOUND")
	before := f.authority(t)
	out := f.start(t, key)
	proof := f.admin.cookies["__Host-admin_sso_flow"].Value
	replayStart := f.start(t, key)
	if replayStart["id"] != out["id"] || replayStart["authorization_url"] != out["authorization_url"] || f.admin.cookies["__Host-admin_sso_flow"].Value != proof {
		t.Fatal("pending start replay changed its intent")
	}
	saved := newClient(t, f.admin.s, "admin", f.admin.schemas)
	saved.cookies = maps.Clone(f.admin.cookies)
	path := f.callbackPath(out)
	u, _ := url.Parse(out["authorization_url"].(string))
	if _, _, err := f.accounts.Sources.(*sso.Service).Complete(t.Context(), f.connector, "admin", u.Query().Get("state"), proof, f.admin.cookies["__Host-admin_session"].Value, f.code, false, uuid.NewString()); err == nil || err.Error() != "SSO_FLOW_INVALID" {
		t.Fatal("diagnostic state entered ordinary login completion")
	}
	recoveryCallback(t, f.admin, path, "/sso?test_id="+out["id"].(string), false)
	result := f.admin.request(http.MethodGet, f.base+"/"+out["id"].(string), nil, 200, "", nil)
	if result["state"] != "succeeded" || result["subject"] != "73" || result["login"] != "provider-member" || result["display_name"] != "Provider member" {
		t.Fatal("diagnostic identity result missing")
	}
	recoveryCallback(t, saved, path, "/sso?test_error=SSO_FLOW_UNUSABLE", false)
	if f.exchanges.Load() != 1 || before != f.authority(t) {
		t.Fatal("diagnostic test changed login authority or exchanged twice")
	}
	raw, _ := json.Marshal(result)
	for _, value := range []string{f.secret, f.token, f.code, proof} {
		if strings.Contains(string(raw), value) {
			t.Fatal("diagnostic result exposed private material")
		}
	}
	var leaked bool
	if err := f.accounts.DB.QueryRow(t.Context(), `SELECT coalesce(bool_or(strpos(to_jsonb(a)::text,$2)>0 OR strpos(to_jsonb(a)::text,$3)>0 OR strpos(to_jsonb(a)::text,$4)>0 OR strpos(to_jsonb(a)::text,$5)>0),false) FROM audit_events a WHERE target_id=$1`, f.connector, f.secret, f.token, f.code, proof).Scan(&leaked); err != nil || leaked {
		t.Fatal("diagnostic audit exposed private material", err)
	}
	var starts, success int
	if err := f.accounts.DB.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE outcome='accepted'),count(*) FILTER(WHERE outcome='success') FROM audit_events WHERE action='sso.connector_tested' AND target_id=$1`, f.connector).Scan(&starts, &success); err != nil || starts != 1 || success != 1 {
		t.Fatal("diagnostic audit missing or repeated", err)
	}
	var clear bool
	if err := f.accounts.DB.QueryRow(t.Context(), "SELECT secret_ciphertext IS NULL FROM sso_connector_sign_in_tests WHERE id=$1", out["id"]).Scan(&clear); err != nil || !clear {
		t.Fatal("consumed diagnostic bundle retained", err)
	}
	problemCode(t, f.admin.request(http.MethodPost, f.base, sso.ConnectorSignInTestInput{Revision: 1, Reason: "Inspect actual upstream identity"}, 409, "", map[string]string{"Idempotency-Key": key}), "SSO_FLOW_UNUSABLE")
}

func TestConnectorSignInTestBoundaryHTTPS(t *testing.T) {
	f := connectorSignInFixtureNew(t)
	out := f.start(t, uuid.NewString())
	id := out["id"].(string)
	other := newClient(t, f.admin.s, "admin", f.admin.schemas)
	other.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": f.password}, 200, "LoginResponse")
	problemCode(t, other.request(http.MethodGet, f.base+"/"+id, nil, 400, "", nil), "SSO_FLOW_INVALID")
	other.cookies["__Host-admin_sso_flow"] = f.admin.cookies["__Host-admin_sso_flow"]
	recoveryCallback(t, other, f.callbackPath(out), "/sso?test_error=SSO_FLOW_UNUSABLE", false)
	actor, err := f.accounts.Authenticate(t.Context(), f.admin.cookies["__Host-admin_session"].Value, "admin")
	if err != nil {
		t.Fatal(err)
	}
	denied := actor
	denied.Me.Audience = "portal"
	if _, err = f.owner.Get(t.Context(), denied, f.connector, id); err == nil || err.Error() != "ACCESS_DENIED" {
		t.Fatal("Portal gained diagnostic authority")
	}
	denied = actor
	denied.Me.User.IsPlatformAdmin = false
	if _, err = f.owner.Get(t.Context(), denied, f.connector, id); err == nil || err.Error() != "ACCESS_DENIED" {
		t.Fatal("member gained diagnostic authority")
	}
	problemCode(t, f.admin.request(http.MethodGet, f.base+"/"+id+"?tenant_id="+uuid.NewString(), nil, 400, "", nil), "VALIDATION_FAILED")
	f.admin.refresh()
	problemCode(t, f.admin.request(http.MethodPost, f.base, sso.ConnectorSignInTestInput{Revision: 1, Reason: "CSRF boundary"}, 403, "", map[string]string{"X-CSRF-Token": "", "Idempotency-Key": uuid.NewString()}), "CSRF_INVALID")
	problemCode(t, f.admin.request(http.MethodPost, f.base, map[string]any{"revision": 1, "reason": "Scope injection", "tenant_id": uuid.NewString()}, 400, "", map[string]string{"Idempotency-Key": uuid.NewString()}), "VALIDATION_FAILED")
	foreignConnector, foreignTest, foreignState := f.foreignTest(t, id)
	foreignBase := "/api/v1/admin/sso/connectors/" + foreignConnector + "/sign-in-tests"
	problemCode(t, f.admin.mutate(http.MethodPost, foreignBase, sso.ConnectorSignInTestInput{Revision: 1, Reason: "Cross-tenant refusal"}, 404, ""), "SSO_CONNECTOR_NOT_FOUND")
	problemCode(t, f.admin.request(http.MethodGet, foreignBase+"/"+foreignTest, nil, 400, "", nil), "SSO_FLOW_INVALID")
	recoveryCallback(t, f.admin, foreignBase+"/callback?"+url.Values{"state": {foreignState}, "code": {f.code}}.Encode(), "/sso?test_error=SSO_FLOW_UNUSABLE", false)
	var intact int
	if err = f.accounts.DB.QueryRow(t.Context(), "SELECT count(*) FROM sso_connector_sign_in_tests WHERE id IN ($1,$2) AND state='pending' AND claim_id IS NULL AND secret_ciphertext IS NOT NULL AND subject IS NULL", id, foreignTest).Scan(&intact); err != nil || intact != 2 {
		t.Fatal("scope refusal changed an owned or foreign intent", err)
	}
	if f.exchanges.Load() != 0 {
		t.Fatal("scope refusals contacted provider")
	}
}

func TestConnectorSignInTestFailureRetryHTTPS(t *testing.T) {
	f := connectorSignInFixtureNew(t)
	before := f.authority(t)
	first := f.start(t, uuid.NewString())
	u, _ := url.Parse(first["authorization_url"].(string))
	state := u.Query().Get("state")
	wrong := newClient(t, f.admin.s, "admin", f.admin.schemas)
	wrong.cookies = maps.Clone(f.admin.cookies)
	cookie := *wrong.cookies["__Host-admin_sso_flow"]
	cookie.Value = security.Token()
	wrong.cookies[cookie.Name] = &cookie
	recoveryCallback(t, wrong, f.callbackPath(first), "/sso?test_error=SSO_BROWSER_CHANGED", false)
	recoveryCallback(t, f.admin, f.base+"/callback?"+url.Values{"state": {state}, "error": {"access_denied"}, "error_description": {"untrusted description"}}.Encode(), "/sso?test_error=SSO_PROVIDER_DENIED", false)
	result := f.admin.request(http.MethodGet, f.base+"/"+first["id"].(string), nil, 200, "", nil)
	if result["state"] != "failed" || result["subject"] != nil || result["error_code"] != "SSO_PROVIDER_DENIED" || f.exchanges.Load() != 0 {
		t.Fatal("denied test retained identity or contacted provider")
	}
	next := f.start(t, uuid.NewString())
	recoveryCallback(t, f.admin, f.callbackPath(next), "/sso?test_id="+next["id"].(string), false)
	if f.exchanges.Load() != 1 || before != f.authority(t) {
		t.Fatal("fresh test retry changed login authority")
	}
}

func TestConnectorSignInTestAuditRollbackHTTPS(t *testing.T) {
	f := connectorSignInFixtureNew(t)
	before := f.authority(t)
	key := uuid.NewString()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.accounts.DB.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("ALTER TABLE audit_events ADD CONSTRAINT fixture_test_start_refusal CHECK(action<>'sso.connector_tested') NOT VALID")
	f.admin.refresh()
	problemCode(t, f.admin.request(http.MethodPost, f.base, sso.ConnectorSignInTestInput{Revision: 1, Reason: "Inspect actual upstream identity"}, 503, "", map[string]string{"Idempotency-Key": key}), "STORAGE_UNAVAILABLE")
	var count int
	if err := f.accounts.DB.QueryRow(t.Context(), "SELECT count(*) FROM sso_connector_sign_in_tests").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed start audit left a test intent", err)
	}
	exec("ALTER TABLE audit_events DROP CONSTRAINT fixture_test_start_refusal")
	out := f.start(t, key)
	exec("ALTER TABLE audit_events ADD CONSTRAINT fixture_test_success_refusal CHECK(action<>'sso.connector_tested' OR outcome<>'success') NOT VALID")
	recoveryCallback(t, f.admin, f.callbackPath(out), "/sso?test_error=STORAGE_UNAVAILABLE", false)
	result := f.admin.request(http.MethodGet, f.base+"/"+out["id"].(string), nil, 200, "", nil)
	if result["state"] != "failed" || result["subject"] != nil || f.exchanges.Load() != 1 {
		t.Fatal("failed completion audit exposed a result or repeated exchange")
	}
	exec("ALTER TABLE audit_events DROP CONSTRAINT fixture_test_success_refusal")
	retry := f.start(t, uuid.NewString())
	recoveryCallback(t, f.admin, f.callbackPath(retry), "/sso?test_id="+retry["id"].(string), false)
	if before != f.authority(t) {
		t.Fatal("audit refusal or retry changed local authority")
	}
}

func TestConnectorSignInTestParallelCallbackHTTPS(t *testing.T) {
	f := connectorSignInFixtureNew(t)
	before := f.authority(t)
	out := f.start(t, uuid.NewString())
	u, _ := url.Parse(out["authorization_url"].(string))
	actor, err := f.accounts.Authenticate(t.Context(), f.admin.cookies["__Host-admin_session"].Value, "admin")
	if err != nil {
		t.Fatal(err)
	}
	state, browser := u.Query().Get("state"), f.admin.cookies["__Host-admin_sso_flow"].Value
	entered, unblock := f.blockExchange(t)
	finished := make(chan error, 1)
	go func() {
		_, e := f.owner.Complete(t.Context(), actor, f.connector, state, browser, f.code, false, uuid.NewString())
		finished <- e
	}()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("test exchange did not start within its fixture deadline")
	}
	_, err = f.owner.Complete(t.Context(), actor, f.connector, state, browser, f.code, false, uuid.NewString())
	if err == nil || err.Error() != "SSO_FLOW_UNUSABLE" {
		unblock()
		t.Fatal("parallel callback claimed the same test", err)
	}
	unblock()
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	if f.exchanges.Load() != 1 || before != f.authority(t) {
		t.Fatal("parallel callbacks repeated exchange or issued a session")
	}
}

func TestConnectorSignInTestRevokedActorHTTPS(t *testing.T) {
	f := connectorSignInFixtureNew(t)
	out := f.start(t, uuid.NewString())
	u, _ := url.Parse(out["authorization_url"].(string))
	actor, err := f.accounts.Authenticate(t.Context(), f.admin.cookies["__Host-admin_session"].Value, "admin")
	if err != nil {
		t.Fatal(err)
	}
	manager := newClient(t, f.admin.s, "admin", f.admin.schemas)
	manager.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": f.password}, 200, "LoginResponse")
	entered, unblock := f.blockExchange(t)
	done := make(chan error, 1)
	browser := f.admin.cookies["__Host-admin_sso_flow"].Value
	go func() {
		_, e := f.owner.Complete(t.Context(), actor, f.connector, u.Query().Get("state"), browser, f.code, false, uuid.NewString())
		done <- e
	}()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("test exchange did not start within its fixture deadline")
	}
	manager.mutate(http.MethodDelete, managedSessionsPath+"/"+actor.SessionID, nil, 204, "")
	unblock()
	if err = <-done; err == nil || err.Error() != "AUTH_SESSION_EXPIRED" {
		t.Fatal("revoked actor received diagnostic identity", err)
	}
	var refused bool
	if err = f.accounts.DB.QueryRow(t.Context(), "SELECT state='failed' AND subject IS NULL AND secret_ciphertext IS NULL FROM sso_connector_sign_in_tests WHERE id=$1", out["id"]).Scan(&refused); err != nil || !refused {
		t.Fatal("revoked completion did not fail durably", err)
	}
	if f.exchanges.Load() != 1 {
		t.Fatal("revocation repeated provider exchange")
	}
}

// Expiry is a controlled fault in this test's disposable schema. Production
// transition triggers remain enabled outside this explicitly scoped transaction.
func expireConnectorTestFixture(t *testing.T, f *connectorSignInFixture, id string) {
	t.Helper()
	tx, err := f.accounts.DB.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), "ALTER TABLE sso_connector_sign_in_tests DISABLE TRIGGER sso_connector_sign_in_test_transition"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "UPDATE sso_connector_sign_in_tests SET created_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "ALTER TABLE sso_connector_sign_in_tests ENABLE TRIGGER sso_connector_sign_in_test_transition"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestConnectorSignInTestExpiryAndCleanupHTTPS(t *testing.T) {
	f := connectorSignInFixtureNew(t)
	before := f.authority(t)
	out := f.start(t, uuid.NewString())
	expireConnectorTestFixture(t, f, out["id"].(string))
	recoveryCallback(t, f.admin, f.callbackPath(out), "/sso?test_error=SSO_FLOW_EXPIRED", false)
	result := f.admin.request(http.MethodGet, f.base+"/"+out["id"].(string), nil, 200, "", nil)
	if result["state"] != "failed" || result["error_code"] != "SSO_FLOW_EXPIRED" {
		t.Fatal("expired callback did not terminate")
	}
	next := f.start(t, uuid.NewString())
	_, foreignTest, _ := f.foreignTest(t, next["id"].(string))
	expireConnectorTestFixture(t, f, next["id"].(string))
	expireConnectorTestFixture(t, f, foreignTest)
	if err := f.owner.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	var owned, foreign string
	if err := f.accounts.DB.QueryRow(t.Context(), "SELECT state FROM sso_connector_sign_in_tests WHERE id=$1", next["id"]).Scan(&owned); err != nil {
		t.Fatal(err)
	}
	if err := f.accounts.DB.QueryRow(t.Context(), "SELECT state FROM sso_connector_sign_in_tests WHERE id=$1", foreignTest).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	if owned != "expired" || foreign != "pending" || f.exchanges.Load() != 0 || before != f.authority(t) {
		t.Fatal("cleanup crossed tenant authority or expiry contacted provider")
	}
}

func TestConnectorSignInTestStaleConnectorHTTPS(t *testing.T) {
	f := connectorSignInFixtureNew(t)
	out := f.start(t, uuid.NewString())
	u, _ := url.Parse(out["authorization_url"].(string))
	actor, err := f.accounts.Authenticate(t.Context(), f.admin.cookies["__Host-admin_session"].Value, "admin")
	if err != nil {
		t.Fatal(err)
	}
	entered, unblock := f.blockExchange(t)
	done := make(chan error, 1)
	browser := f.admin.cookies["__Host-admin_sso_flow"].Value
	go func() {
		_, e := f.owner.Complete(t.Context(), actor, f.connector, u.Query().Get("state"), browser, f.code, false, uuid.NewString())
		done <- e
	}()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("test exchange did not start")
	}
	f.admin.mutate(http.MethodPatch, "/api/v1/admin/sso/connectors/"+f.connector, sso.UpdateConnector{Revision: 1, Name: "Changed during test", Enabled: false, Reason: "Recheck the observed revision"}, 200, "sso:SSOConnectorResponse")
	unblock()
	if err = <-done; err == nil || err.Error() != "SSO_FLOW_UNUSABLE" {
		t.Fatal("stale connector published a diagnostic identity", err)
	}
	var rejected bool
	if err = f.accounts.DB.QueryRow(t.Context(), "SELECT state='failed' AND subject IS NULL FROM sso_connector_sign_in_tests WHERE id=$1", out["id"]).Scan(&rejected); err != nil || !rejected {
		t.Fatal("stale test was not failed durably", err)
	}
	problemCode(t, f.admin.request(http.MethodGet, f.base+"/"+out["id"].(string), nil, 409, "", nil), "SSO_REVISION_CONFLICT")
}

func TestConnectorSignInTestCanceledExchangeHTTPS(t *testing.T) {
	f := connectorSignInFixtureNew(t)
	before := f.authority(t)
	out := f.start(t, uuid.NewString())
	u, _ := url.Parse(out["authorization_url"].(string))
	actor, err := f.accounts.Authenticate(t.Context(), f.admin.cookies["__Host-admin_session"].Value, "admin")
	if err != nil {
		t.Fatal(err)
	}
	entered, unblock := f.blockExchange(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	state, browser := u.Query().Get("state"), f.admin.cookies["__Host-admin_sso_flow"].Value
	done := make(chan error, 1)
	go func() {
		_, e := f.owner.Complete(ctx, actor, f.connector, state, browser, f.code, false, uuid.NewString())
		done <- e
	}()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("test exchange did not start")
	}
	cancel()
	unblock()
	select {
	case err = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("canceled exchange did not join its bounded cleanup")
	}
	if err == nil || err.Error() != "SSO_PROVIDER_UNAVAILABLE" {
		t.Fatal("canceled exchange returned identity", err)
	}
	var rejected bool
	if err = f.accounts.DB.QueryRow(t.Context(), "SELECT state='failed' AND subject IS NULL AND secret_ciphertext IS NULL FROM sso_connector_sign_in_tests WHERE id=$1", out["id"]).Scan(&rejected); err != nil || !rejected {
		t.Fatal("canceled exchange retained authority or secrets", err)
	}
	if _, err = f.owner.Complete(t.Context(), actor, f.connector, state, browser, f.code, false, uuid.NewString()); err == nil || err.Error() != "SSO_FLOW_UNUSABLE" {
		t.Fatal("canceled exchange became retryable", err)
	}
	if f.exchanges.Load() != 1 || before != f.authority(t) {
		t.Fatal("cancellation repeated exchange or changed login authority")
	}
}

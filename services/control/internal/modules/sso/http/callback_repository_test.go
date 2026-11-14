//go:build repository_integration

package http_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// This bounded HTTPS provider is an explicit protocol double. SQL,License,
// HTTP,cookies,PKCE and COMMIT forwarding use the actual production paths.
type callbackProtocolDouble struct {
	origin, client, secret, code, challenge, callback string
	requests                                          atomic.Int32
	beforeUser                                        func()
}

func (p *callbackProtocolDouble) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.requests.Add(1)
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == sso.GiteaUserPath && r.Method == http.MethodGet && r.Header.Get("Authorization") == "Bearer synthetic-test-access" {
		if p.beforeUser != nil {
			p.beforeUser()
		}
		json.NewEncoder(w).Encode(map[string]any{"id": int64(9007199254740993), "active": true, "prohibit_login": false, "type": "User"})
		return
	}
	client, secret, ok := r.BasicAuth()
	if !ok || client != p.client || secret != p.secret || r.Method != http.MethodPost || r.ParseForm() != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case sso.GiteaTokenPath:
		digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != p.code || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != p.callback || base64.RawURLEncoding.EncodeToString(digest[:]) != p.challenge {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-test-access", "token_type": "Bearer", "expires_in": 60})
	case sso.GiteaIntrospectionPath:
		if r.Form.Get("token") != "synthetic-test-access" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"active": true, "scope": sso.GiteaScope, "iss": p.origin, "aud": []string{p.client}, "sub": "9007199254740993"})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

type callbackTLSFixture struct {
	accounts      *identity.Service
	admin, portal *httpfixture.Client
	provider      *callbackProtocolDouble
	id, password  string
}

func callbackTLS(t *testing.T) callbackTLSFixture {
	t.Helper()
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("private callback fixture directory")
	}
	p := &callbackProtocolDouble{client: uuid.NewString(), secret: security.Token(), code: security.Token()}
	provider := httptest.NewTLSServer(p)
	t.Cleanup(provider.Close)
	p.origin = provider.URL
	ca := filepath.Join(dir, "provider-ca.pem")
	if os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: provider.Certificate().Raw}), 0600) != nil {
		t.Fatal("private TLS provider CA")
	}
	keyring := filepath.Join(dir, "sso-keyring.json")
	if e := sso.InitializeKeyring(keyring, ""); e != nil {
		t.Fatal(e)
	}
	accounts.Config.SSO = config.SSOConfiguration{Enabled: true, KeyringFile: keyring, Providers: []config.SSOProvider{{Origin: p.origin, Issuer: p.origin, DialAddresses: []string{"127.0.0.1"}, CAFile: ca}}}
	service, e := sso.New(accounts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(service.Close)
	accounts.Sources = service
	server := httpfixture.NewTLSServer(t, identityhttp.New(accounts))
	t.Cleanup(server.Close)
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/sso/openapi.json")
	admin, portal := httpfixture.New(t, server, "admin", schemas), httpfixture.New(t, server, "portal", schemas)
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	id := uuid.NewString()
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	admin.Write("POST", "/api/v1/admin/sso/connectors", sso.CreateConnector{ID: id, Name: "Synthetic callback TLS protocol fixture", Provider: sso.ProviderGitea, Origin: p.origin, ClientID: p.client, ClientSecret: p.secret, Enabled: true, Reason: "Synthetic actual callback HTTP prerequisite"}, 201, "SSOConnectorResponse")
	admin.Headers = nil
	return callbackTLSFixture{accounts, admin, portal, p, id, password}
}

func (f callbackTLSFixture) start(t *testing.T, audience string) (*httpfixture.Client, string, []byte) {
	t.Helper()
	client := f.portal
	if audience == "admin" {
		client = f.admin
	} else {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": f.password}, 200, "")
	}
	start := client.Write("POST", "/api/v1/auth/sso/"+f.id+"/starts", sso.StartInput{Mode: sso.ModeLink, Password: &f.password, Reason: stringPointer("Synthetic callback protocol acceptance")}, 200, "SSOStartResponse")
	authorization, e := url.Parse(start["authorization_url"].(string))
	if e != nil {
		t.Fatal("actual authorization URL shape")
	}
	q := authorization.Query()
	f.provider.challenge, f.provider.callback = q.Get("code_challenge"), q.Get("redirect_uri")
	if q.Get("code_challenge_method") != "S256" || f.provider.challenge == "" {
		t.Fatal("actual Start omitted PKCE")
	}
	return client, "/api/v1/auth/sso/" + f.id + "/callback?" + url.Values{"state": {q.Get("state")}, "code": {f.provider.code}}.Encode(), security.Digest(q.Get("state"))
}

func stringPointer(value string) *string { return &value }

func TestActualSSOCallbackTLSRevokedPendingFlowUsesOnlyFixedBoundResult(t *testing.T) {
	for _, audience := range []string{"portal", "admin"} {
		t.Run(audience, func(t *testing.T) {
			f := callbackTLS(t)
			client, path, state := f.start(t, audience)
			f.admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
			f.admin.Write("PATCH", "/api/v1/admin/sso/connectors/"+f.id, sso.UpdateConnector{Revision: 1, Name: "Revoked browser callback", Enabled: false, Reason: "Synthetic pending flow revocation"}, 200, "SSOConnectorResponse")
			f.admin.Headers = nil
			status, location, cookies := client.Redirect(path)
			if status != 303 || location != "/account/sso-result?error=SSO_FLOW_UNUSABLE" || f.provider.requests.Load() != 0 {
				t.Fatal("revoked callback retained code URL, exchanged, or changed redirect")
			}
			assertCallbackCookies(t, cookies, audience, false)
			var terminal bool
			if e := f.accounts.DB.QueryRow(t.Context(), "SELECT state='failed' AND verifier_ciphertext IS NULL AND issued_session_id IS NULL FROM sso_flows WHERE state_hash=$1", state).Scan(&terminal); e != nil || !terminal {
				t.Fatal("revoked flow regained authority", e)
			}
		})
	}
}

func callbackWire(t *testing.T, accounts *identity.Service) *commitfault.PlaintextPostgreSQL {
	t.Helper()
	original := accounts.DB
	c := original.Config()
	if c.ConnConfig.Host != "127.0.0.1" || len(c.ConnConfig.Fallbacks) != 0 {
		t.Fatal("exact isolated loopback PG profile required; transport remains unchanged")
	}
	proxy, e := commitfault.NewPostgreSQL(c.ConnConfig.Host, c.ConnConfig.Port, c.ConnConfig.TLSConfig, os.Getenv("PROJECT_ONE_TEST_PG_TLS_PROXY_CERTIFICATE_FILE"), os.Getenv("PROJECT_ONE_TEST_PG_TLS_PROXY_PRIVATE_KEY_FILE"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := proxy.Close(ctx); e != nil {
			t.Error("actual HTTP wire cleanup", e)
		}
	})
	c.ConnConfig.Port, c.MaxConns = proxy.Port(), 2
	pool, e := pgxpool.NewWithConfig(t.Context(), c)
	if e != nil || pool.Ping(t.Context()) != nil {
		t.Fatal("actual callback forwarding pool unavailable")
	}
	accounts.DB = pool
	t.Cleanup(func() { accounts.DB = original; pool.Close() })
	return proxy
}

type callbackClaimFault struct {
	identity.BusinessAdmission
	proxy    *commitfault.PlaintextPostgreSQL
	mode     commitfault.Mode
	baseline int32
	armed    bool
}

func (f *callbackClaimFault) VerifyStored(ctx context.Context) error {
	if e := f.BusinessAdmission.VerifyStored(ctx); e != nil {
		return e
	}
	if f.armed {
		return nil
	}
	f.armed, f.baseline = true, f.proxy.Commits()
	return f.proxy.Arm(f.mode)
}

func assertCallbackCookies(t *testing.T, cookies []*http.Cookie, audience string, issued bool) {
	t.Helper()
	cleared, session := false, false
	for _, cookie := range cookies {
		if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" {
			t.Fatal("callback cookie authority escaped host/HTTPS boundary")
		}
		if cookie.Name == "__Host-"+audience+"_sso_flow" {
			cleared = cookie.MaxAge < 0 && cookie.Value == "" && cookie.SameSite == http.SameSiteLaxMode
		}
		if cookie.Name == "__Host-"+audience+"_session" {
			session = cookie.Value != "" && cookie.MaxAge > 0 && cookie.SameSite == http.SameSiteLaxMode
		}
	}
	if !cleared || session != issued {
		t.Fatal("uncertain callback cookie clearing/session issuance mismatch")
	}
}

func TestActualSSOCallbackTLSUnknownClaimAndLocalRecovery(t *testing.T) {
	for _, audience := range []string{"portal", "admin"} {
		t.Run(audience, func(t *testing.T) {
			for _, mode := range []commitfault.Mode{commitfault.DropBeforeCommit, commitfault.DropCommitAcknowledgement} {
				name := "commit_not_forwarded"
				if mode == commitfault.DropCommitAcknowledgement {
					name = "actual_server_ACK_suppressed"
				}
				t.Run(name, func(t *testing.T) {
					f := callbackTLS(t)
					client, path, state := f.start(t, audience)
					proxy := callbackWire(t, f.accounts)
					original := f.accounts.BusinessAdmission
					fault := &callbackClaimFault{BusinessAdmission: original, proxy: proxy, mode: mode}
					f.accounts.BusinessAdmission = fault
					status, location, cookies := client.Redirect(path)
					f.accounts.BusinessAdmission = original
					if status != 303 || location != "/login/sso-result?error=STORAGE_COMMIT_OUTCOME_UNKNOWN" || !fault.armed || proxy.Commits() != fault.baseline+1 || f.provider.requests.Load() != 0 {
						t.Fatal("uncertain claim must redirect safely without provider exchange or retry")
					}
					assertCallbackCookies(t, cookies, audience, false)
					var stored string
					var erased, issued bool
					if e := f.accounts.DB.QueryRow(t.Context(), `SELECT state,verifier_key_id IS NULL,issued_session_id IS NOT NULL FROM sso_flows WHERE state_hash=$1`, state).Scan(&stored, &erased, &issued); e != nil {
						t.Fatal(e)
					}
					committed := mode == commitfault.DropCommitAcknowledgement
					want := "pending"
					if committed {
						want = "claimed"
					}
					if stored != want || erased != committed || issued {
						t.Fatal("independent uncertain claim state/verifier/session mismatch")
					}
					client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": f.password}, 200, "")
					client.Call("GET", "/api/v1/account/sso/bindings", nil, 200, "SSOBindingListResponse")
					client.Write("POST", "/api/v1/auth/sso/"+f.id+"/starts", sso.StartInput{Mode: sso.ModeLogin}, 200, "SSOStartResponse")
					if f.provider.requests.Load() != 0 {
						t.Fatal("local recovery resent an old provider code")
					}
				})
			}
		})
	}
}

func TestActualSSOCallbackTLSCompletionAtomicOutcomeAndRecovery(t *testing.T) {
	for _, audience := range []string{"portal", "admin"} {
		t.Run(audience, func(t *testing.T) {
			for _, mode := range []commitfault.Mode{commitfault.Pass, commitfault.DropBeforeCommit, commitfault.DropCommitAcknowledgement} {
				name := "normal_ACK"
				if mode == commitfault.DropBeforeCommit {
					name = "commit_not_forwarded"
				}
				if mode == commitfault.DropCommitAcknowledgement {
					name = "actual_server_ACK_suppressed"
				}
				t.Run(name, func(t *testing.T) {
					f := callbackTLS(t)
					client, path, state := f.start(t, audience)
					proxy := callbackWire(t, f.accounts)
					var baseline int32
					f.provider.beforeUser = func() {
						baseline = proxy.Commits()
						if mode != commitfault.Pass {
							if e := proxy.Arm(mode); e != nil {
								t.Error(e)
							}
						}
					}
					status, location, cookies := client.Redirect(path)
					want := "/account/sso-result?result=linked"
					if mode != commitfault.Pass {
						want = "/account/sso-result?error=STORAGE_COMMIT_OUTCOME_UNKNOWN"
					}
					if status != 303 || location != want || proxy.Commits() != baseline+1 || f.provider.requests.Load() != 3 {
						t.Fatal("actual callback outcome/one provider exchange/one completion mismatch")
					}
					assertCallbackCookies(t, cookies, audience, mode == commitfault.Pass)
					var stored string
					var issued bool
					var bindings, sessions, audits int
					if e := f.accounts.DB.QueryRow(t.Context(), `SELECT state,issued_session_id IS NOT NULL FROM sso_flows WHERE state_hash=$1`, state).Scan(&stored, &issued); e != nil {
						t.Fatal(e)
					}
					for query, destination := range map[string]*int{`SELECT COUNT(*) FROM sso_bindings`: &bindings, `SELECT COUNT(*) FROM sessions WHERE sso_flow_id IS NOT NULL`: &sessions, `SELECT COUNT(*) FROM audit_events WHERE action IN ('auth.sso_login_succeeded','sso.binding_linked')`: &audits} {
						if e := f.accounts.DB.QueryRow(t.Context(), query).Scan(destination); e != nil {
							t.Fatal(e)
						}
					}
					committed := mode != commitfault.DropBeforeCommit
					wantState, wantRows, wantAudits := "claimed", 0, 0
					if committed {
						wantState, wantRows, wantAudits = "succeeded", 1, 2
					}
					if stored != wantState || issued != committed || bindings != wantRows || sessions != wantRows || audits != wantAudits {
						t.Fatal("independent HTTP completion binding/session/flow/audit atomicity")
					}
					if mode == commitfault.Pass {
						client.Call("GET", "/api/v1/me", nil, 200, "")
					}
					client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": f.password}, 200, "")
					client.Call("GET", "/api/v1/account/sso/bindings", nil, 200, "SSOBindingListResponse")
					if f.provider.requests.Load() != 3 {
						t.Fatal("recovery automatically retried provider exchange")
					}
				})
			}
		})
	}
}

func TestActualSSOCallbackTLSStoredLicenseIntegrityAndAcceptedExpiry(t *testing.T) {
	for _, audience := range []string{"portal", "admin"} {
		t.Run(audience, func(t *testing.T) {
			for _, kind := range []string{"missing_configuration", "corrupt_stored_artifact", "expired_accepted_work"} {
				t.Run(kind, func(t *testing.T) {
					f := callbackTLS(t)
					client, path, state := f.start(t, audience)
					original := f.accounts.BusinessAdmission
					defer func() { f.accounts.BusinessAdmission = original }()
					if kind == "missing_configuration" {
						f.accounts.BusinessAdmission = nil
					}
					if kind == "corrupt_stored_artifact" {
						if _, e := f.accounts.DB.Exec(t.Context(), `UPDATE platform_license_artifacts SET artifact_sha256=$1`, strings.Repeat("0", 64)); e != nil {
							t.Fatal("isolated stored corruption fixture", e)
						}
					}
					if kind == "expired_accepted_work" {
						trust, e := f.accounts.Config.LicenseTrust()
						if e != nil {
							t.Fatal(e)
						}
						// Only this explicit test clock advances License policy. Flow/session
						// freshness still uses the real DB clock;this is not real TTL evidence.
						license, e := licensing.New(f.accounts, trust, f.accounts.Config.LicensePolicy(), func() time.Time { return time.Now().Add(26 * time.Hour) })
						if e != nil {
							t.Fatal(e)
						}
						f.accounts.BusinessAdmission = license
						if e := f.accounts.RequireNewBusiness(t.Context()); e == nil || e.Error() != "LICENSE_NEW_BUSINESS_UNAVAILABLE" {
							t.Fatal("expired signed License must refuse new business", e)
						}
					}
					status, location, cookies := client.Redirect(path)
					if kind == "expired_accepted_work" {
						if status != 303 || location != "/account/sso-result?result=linked" || f.provider.requests.Load() != 3 {
							t.Fatal("expiry abandoned accepted callback")
						}
						assertCallbackCookies(t, cookies, audience, true)
					} else {
						if status != 503 || location != "" || len(cookies) != 0 || f.provider.requests.Load() != 0 {
							t.Fatal("invalid License reached provider or changed callback authority")
						}
						var stored string
						if e := f.accounts.DB.QueryRow(t.Context(), `SELECT state FROM sso_flows WHERE state_hash=$1`, state).Scan(&stored); e != nil || stored != "pending" {
							t.Fatal("License refusal consumed accepted flow", e)
						}
					}
				})
			}
		})
	}
}

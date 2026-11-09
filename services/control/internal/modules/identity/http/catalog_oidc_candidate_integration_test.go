//go:build catalog_oidc_candidate

package http_test

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	sample "github.com/zitadel/oidc/v3/example/server/storage"
	"github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

//go:embed testdata/catalog_oidc_candidate.sql
var catalogOIDCCandidateSQL string

type catalogCandidateSigner struct {
	*sample.Storage
	refuse atomic.Bool
}

func (s *catalogCandidateSigner) SigningKey(ctx context.Context) (op.SigningKey, error) {
	if s.refuse.Load() {
		return nil, errors.New("fixture signing refusal")
	}
	return s.Storage.SigningKey(ctx)
}

func (*catalogCandidateSigner) SetUserinfoFromScopes(_ context.Context, info *oidc.UserInfo, subject, _ string, _ []string) error {
	info.Subject = subject
	return nil
}

func (*catalogCandidateSigner) SetUserinfoFromRequest(context.Context, *oidc.UserInfo, op.IDTokenRequest, []string) error {
	return nil
}

type catalogExchangeResult struct {
	status int
	data   oidc.AccessTokenResponse
	code   string
	err    error
}

func exchangeCatalogCode(ctx context.Context, server *httptest.Server, id, secret, code, redirect, verifier string) catalogExchangeResult {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return catalogExchangeResult{err: err}
	}
	r.SetBasicAuth(id, secret)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := server.Client().Do(r)
	if err != nil {
		return catalogExchangeResult{err: err}
	}
	defer res.Body.Close()
	if res.TLS == nil || len(res.TLS.VerifiedChains) == 0 || res.Header.Get("Cache-Control") != "no-store" {
		return catalogExchangeResult{err: errors.New("token response needs verified TLS and cache prevention")}
	}
	var body struct {
		oidc.AccessTokenResponse
		Error string `json:"error"`
	}
	err = json.NewDecoder(res.Body).Decode(&body)
	return catalogExchangeResult{status: res.StatusCode, data: body.AccessTokenResponse, code: body.Error, err: err}
}

func TestCatalogOIDCCandidatePortalHTTPS(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(catalogOIDCCandidateSQL)

	admin.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	const email = "catalog-member@example.invalid"
	invitation := admin.mutate(http.MethodPost, "/api/v1/admin/invitations", map[string]string{"email": email, "locale": "en"}, 201, "InvitationResponse")
	invite := fixture.InvitationToken(t, s, invitation["id"].(string))
	memberPassword := fixture.Password()
	portal.mutate(http.MethodPost, "/api/v1/auth/register", map[string]string{"token": invite, "display_name": "Catalog member", "password": memberPassword}, 201, "RegisterResponse")
	login := map[string]string{"email": email, "password": memberPassword}
	member := portal.mutate(http.MethodPost, "/api/v1/auth/login", login, 200, "LoginResponse")["user"].(map[string]any)
	userID := member["id"].(string)
	if member["is_platform_admin"] == true {
		t.Fatal("expected ordinary Portal member")
	}

	handler := transport.New(s)
	candidateServer := accountTLSServer(t, handler)
	candidate := newClient(t, candidateServer, "portal", portal.schemas)
	candidate.cookies = portal.cookies

	var tenant, workspace string
	if err := s.DB.QueryRow(ctx, "SELECT tenant_id::text,default_workspace_id::text FROM installation_state WHERE id=1").Scan(&tenant, &workspace); err != nil {
		t.Fatal(err)
	}
	foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
	exec("INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign tenant','active')", foreignTenant)
	exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign workspace','active')", foreignWorkspace, foreignTenant)
	exec("INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')", userID, foreignTenant, foreignWorkspace)
	clientID, otherID, foreignID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	secret, otherSecret, foreignSecret := security.Token(), security.Token(), security.Token()
	verifier, state, nonce := security.Token(), security.Token(), security.Token()
	callback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/callback" || q.Get("install") != "fixture" ||
			q.Get("state") != state || !security.ValidToken(q.Get("code")) || r.Header.Get("Cookie") != "" {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(callback.Close)
	redirect := callback.URL + "/callback?install=fixture"
	for _, reg := range []struct{ id, tenant, secret string }{{clientID, tenant, secret}, {otherID, tenant, otherSecret}, {foreignID, foreignTenant, foreignSecret}} {
		exec("INSERT INTO catalog_oidc_candidate_clients(id,tenant_id,redirect_uri,secret_digest) VALUES($1,$2,$3,$4)", reg.id, reg.tenant, redirect, security.Digest(reg.secret))
	}

	input := identity.CatalogOIDCCandidateApproval{ClientID: clientID, RedirectURI: redirect, State: state, Nonce: nonce,
		Challenge: base64.RawURLEncoding.EncodeToString(security.Digest(verifier)), ChallengeMethod: "S256"}
	const path = "/api/v1/me/catalog-oidc/approvals"
	problemCode(t, candidate.mutate(http.MethodPost, path, input, 404, "Problem"), "RESOURCE_NOT_FOUND")
	// Construct the explicitly mounted listener before serving concurrent requests.
	handler = transport.New(s)
	handler.EnableCatalogOIDCCandidate()
	mounted := accountTLSServer(t, handler)
	candidate = newClient(t, mounted, "portal", portal.schemas)
	candidate.cookies = portal.cookies
	anonymous := newClient(t, mounted, "portal", portal.schemas)
	adminCandidate := newClient(t, mounted, "admin", admin.schemas)
	adminCandidate.cookies = admin.cookies
	problemCode(t, anonymous.mutate(http.MethodPost, path, input, 401, "Problem"), "AUTH_REQUIRED")
	problemCode(t, adminCandidate.mutate(http.MethodPost, path, input, 404, "Problem"), "RESOURCE_NOT_FOUND")
	candidate.refresh()
	problemCode(t, candidate.request(http.MethodPost, path, input, 403, "Problem", map[string]string{"Origin": "https://catalog.example.invalid"}), "ORIGIN_REJECTED")
	problemCode(t, candidate.request(http.MethodPost, path, input, 403, "Problem", map[string]string{"X-CSRF-Token": ""}), "CSRF_INVALID")
	problemCode(t, candidate.mutate(http.MethodPost, path+"?tenant_id="+foreignTenant, input, 400, "Problem"), "VALIDATION_FAILED")
	problemCode(t, candidate.mutate(http.MethodPost, path, struct {
		identity.CatalogOIDCCandidateApproval
		TenantID string `json:"tenant_id"`
	}{input, foreignTenant}, 400, "Problem"), "VALIDATION_FAILED")
	bad := input
	bad.ClientID = foreignID
	problemCode(t, candidate.mutate(http.MethodPost, path, bad, 403, "Problem"), "ACCESS_DENIED")
	bad = input
	bad.RedirectURI = redirect + "&extra=1"
	problemCode(t, candidate.mutate(http.MethodPost, path, bad, 403, "Problem"), "ACCESS_DENIED")
	bad = input
	bad.ChallengeMethod = "plain"
	problemCode(t, candidate.mutate(http.MethodPost, path, bad, 400, "Problem"), "VALIDATION_FAILED")

	approve := func() string {
		t.Helper()
		reply := candidate.mutate(http.MethodPost, path, input, 201, "")
		raw := reply["redirect_uri"].(string)
		u, err := url.Parse(raw)
		if err != nil || !strings.HasPrefix(raw, redirect+"&") || u.Query().Get("state") != state || !security.ValidToken(u.Query().Get("code")) {
			t.Fatal("approval did not return the exact registered redirect, state and opaque code")
		}
		res, err := callback.Client().Get(raw)
		if err != nil {
			t.Fatal("registered HTTPS callback was unreachable")
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusNoContent || res.TLS == nil || len(res.TLS.VerifiedChains) == 0 {
			t.Fatal("registered HTTPS callback refused the authorization response")
		}
		return u.Query().Get("code")
	}

	signer := &catalogCandidateSigner{Storage: sample.NewStorageWithClients(sample.NewUserStore("https://fixture.example.invalid"),
		map[string]*sample.Client{clientID: sample.WebClient(clientID, secret, redirect)})}
	var config op.Config
	config.CodeMethodS256 = true
	if _, err := rand.Read(config.CryptoKey[:]); err != nil {
		t.Fatal(err)
	}
	primary := httptest.NewUnstartedServer(nil)
	// The listener's address is allocated before building fixed issuer metadata.
	provider, err := op.NewProvider(&config, signer, op.StaticIssuer("https://"+primary.Listener.Addr().String()))
	if err != nil {
		primary.Close()
		t.Fatal(err)
	}
	primary.Config.Handler = transport.CatalogOIDCExchangeCandidate{Identity: s, Provider: provider}
	primary.StartTLS()
	t.Cleanup(primary.Close)
	pool, err := pgxpool.NewWithConfig(ctx, s.DB.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	replica := *s
	replica.DB = pool
	secondary := httptest.NewTLSServer(transport.CatalogOIDCExchangeCandidate{Identity: &replica, Provider: provider})
	t.Cleanup(secondary.Close)
	relyingParty, err := rp.NewRelyingPartyOIDC(ctx, primary.URL, clientID, secret, redirect, []string{oidc.ScopeOpenID},
		rp.WithHTTPClient(primary.Client()), rp.WithVerifierOpts(rp.WithNonce(func(context.Context) string { return nonce })))
	if err != nil {
		t.Fatal(err)
	}

	expectError := func(res catalogExchangeResult, status int, code string) {
		t.Helper()
		if res.err != nil || res.status != status || res.code != code || res.data.IDToken != "" || res.data.AccessToken != "" {
			t.Fatalf("expected %d/%s without tokens, observed %d/%s, transport error %v", status, code, res.status, res.code, res.err)
		}
	}
	exchange := func(code string) catalogExchangeResult {
		return exchangeCatalogCode(ctx, primary, clientID, secret, code, redirect, verifier)
	}
	pending := approve()
	expectError(exchangeCatalogCode(ctx, primary, clientID, secret, pending, redirect, ""), 400, "invalid_grant")
	expectError(exchangeCatalogCode(ctx, primary, clientID, secret, pending, redirect, security.Token()), 400, "invalid_grant")
	expectError(exchangeCatalogCode(ctx, primary, clientID, secret, pending, redirect+"&extra=1", verifier), 400, "invalid_grant")
	expectError(exchangeCatalogCode(ctx, primary, clientID, security.Token(), pending, redirect, verifier), 401, "invalid_client")
	expectError(exchangeCatalogCode(ctx, primary, otherID, otherSecret, pending, redirect, verifier), 400, "invalid_grant")
	expectError(exchangeCatalogCode(ctx, primary, foreignID, foreignSecret, pending, redirect, verifier), 400, "invalid_grant")
	exec("UPDATE installation_state SET tenant_id=$1,default_workspace_id=$2 WHERE id=1", foreignTenant, foreignWorkspace)
	expectError(exchange(pending), 400, "invalid_grant")
	exec("UPDATE installation_state SET tenant_id=$1,default_workspace_id=$2 WHERE id=1", tenant, workspace)

	assertState := func(used bool, count int) {
		t.Helper()
		var consumed bool
		var tokens, audits int
		err := s.DB.QueryRow(ctx, `SELECT used_at IS NOT NULL,(SELECT count(*) FROM catalog_oidc_candidate_tokens),
			(SELECT count(*) FROM audit_events WHERE action='catalog_oidc.code_exchanged' AND actor_user_id=$2 AND tenant_id=$3)
			FROM catalog_oidc_candidate_codes WHERE code_hash=$1`, security.Digest(pending), userID, tenant).Scan(&consumed, &tokens, &audits)
		if err != nil || consumed != used || tokens != count || audits != count {
			t.Fatalf("unexpected persisted state: used=%t tokens=%d audits=%d error=%v", consumed, tokens, audits, err)
		}
	}
	signer.refuse.Store(true)
	expectError(exchange(pending), 503, "server_error")
	signer.refuse.Store(false)
	assertState(false, 0)
	exec("ALTER TABLE audit_events ADD CONSTRAINT fixture_refuses_oidc_exchange CHECK(action<>'catalog_oidc.code_exchanged') NOT VALID")
	expectError(exchange(pending), 503, "server_error")
	assertState(false, 0)
	exec("ALTER TABLE audit_events DROP CONSTRAINT fixture_refuses_oidc_exchange")

	results := make(chan catalogExchangeResult, 2)
	start := make(chan struct{})
	for _, replica := range []*httptest.Server{primary, secondary} {
		go func() {
			<-start
			results <- exchangeCatalogCode(ctx, replica, clientID, secret, pending, redirect, verifier)
		}()
	}
	close(start)
	successes := 0
	for range 2 {
		res := <-results
		if res.status != http.StatusOK {
			expectError(res, 400, "invalid_grant")
			continue
		}
		successes++
		claims, err := rp.VerifyTokens[*oidc.IDTokenClaims](ctx, res.data.AccessToken, res.data.IDToken, relyingParty.IDTokenVerifier())
		if res.err != nil || err != nil || claims.GetSubject() != userID || res.data.ExpiresIn < 1 || res.data.RefreshToken != "" {
			t.Fatal("relying party did not verify the expected Portal subject and access token", err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected one successful redemption, got %d", successes)
	}
	assertState(true, 1)

	expired := approve()
	exec("UPDATE catalog_oidc_candidate_codes SET expires_at=clock_timestamp()-interval '1 second' WHERE code_hash=$1", security.Digest(expired))
	expectError(exchange(expired), 400, "invalid_grant")
	stale := approve()
	exec("UPDATE catalog_oidc_candidate_clients SET enabled=false WHERE id=$1", clientID)
	expectError(exchange(stale), 401, "invalid_client")
	exec("UPDATE catalog_oidc_candidate_clients SET enabled=true,version=version+1 WHERE id=$1", clientID)
	expectError(exchange(stale), 400, "invalid_grant")
	revoked := approve()
	candidate.mutate(http.MethodPost, "/api/v1/auth/logout", nil, 204, "")
	expectError(exchange(revoked), 400, "invalid_grant")
	assertState(true, 1)
}

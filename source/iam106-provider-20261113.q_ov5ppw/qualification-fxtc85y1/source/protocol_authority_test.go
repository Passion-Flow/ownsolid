package boundary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	relyingparty "github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"
)

func noTokenFields(t *testing.T, fields map[string]json.RawMessage) {
	t.Helper()
	if fields["access_token"] != nil || fields["id_token"] != nil || fields["sub"] != nil {
		t.Fatal("refusal released protected token or subject")
	}
}
func (f *protocolFixture) rejectUserInfo(t *testing.T, token string) {
	t.Helper()
	r, err := http.NewRequestWithContext(f.ctx, "GET", f.opOrigin+"/userinfo", nil)
	if err != nil {
		t.Fatal("private UserInfo construction failed")
	}
	r.Header.Set("Authorization", "Bearer "+token)
	response, err := f.serverClient.Do(r)
	if err != nil {
		t.Fatal("private UserInfo transport failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 401 && response.StatusCode != 403 {
		t.Fatal("revoked/foreign UserInfo authority accepted")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		t.Fatal("private UserInfo refusal not JSON")
	}
	noTokenFields(t, fields)
}
func (f *protocolFixture) exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, statement, args...); err != nil {
		t.Fatal("owned authority fixture mutation failed")
	}
}
func (f *protocolFixture) reloadRP(t *testing.T, secret string) {
	t.Helper()
	remote, err := relyingparty.NewRelyingPartyOIDC(f.ctx, f.opOrigin, f.client, secret, f.rpOrigin+"/callback", []string{oidc.ScopeOpenID}, relyingparty.WithHTTPClient(f.serverClient), relyingparty.WithSigningAlgsFromDiscovery(), relyingparty.WithVerifierOpts(relyingparty.WithIssuedAtOffset(0), relyingparty.WithNonce(func(ctx context.Context) string { value, _ := ctx.Value(rpNonceContext{}).(string); return value })))
	if err != nil {
		t.Fatal("private RP client reload failed")
	}
	f.app.mu.Lock()
	f.app.remote = remote
	f.app.mu.Unlock()
	f.secret = secret
	privateWrite(t, filepath.Join(f.folder, "client-secret"), []byte(secret))
}

func TestLiveOPClientCodeRefusals(t *testing.T) {
	f := newProtocolFixture(t, nil)
	foreignTenant, foreignWorkspace, foreignInstallation := uuid.NewString(), uuid.NewString(), uuid.NewString()
	foreignClient, foreignSecret := "foreign-"+uuid.NewString(), random(t)
	f.exec(t, `INSERT INTO op_fixture_tenants VALUES($1,true)`, foreignTenant)
	f.exec(t, `INSERT INTO op_fixture_installations VALUES($1,$2,$3,1,true)`, foreignTenant, foreignWorkspace, foreignInstallation)
	f.exec(t, `INSERT INTO op_fixture_clients VALUES($1,$2,$3,$4,$5,1,$6,true)`, foreignTenant, foreignClient, foreignWorkspace, foreignInstallation, digest(foreignSecret), f.rpOrigin+"/callback")
	cases := []string{"secret", "foreign_tenant_client", "other_client", "redirect", "pkce", "query_tenant", "duplicate_code"}
	var localWorkspace, localInstallation string
	if err := f.pool.QueryRow(f.ctx, `SELECT workspace_id::text,installation_id::text FROM op_fixture_clients WHERE tenant_id=$1 AND client_id=$2`, f.config.Tenant, f.client).Scan(&localWorkspace, &localInstallation); err != nil {
		t.Fatal("own client scope missing")
	}
	otherClient, otherSecret := "other-"+uuid.NewString(), random(t)
	f.exec(t, `INSERT INTO op_fixture_clients VALUES($1,$2,$3,$4,$5,1,$6,true)`, f.config.Tenant, otherClient, localWorkspace, localInstallation, digest(otherSecret), f.rpOrigin+"/callback")
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			trace := f.issueCode(t, nil)
			before := f.scalar(t, `SELECT count(*) FROM op_fixture_tokens`)
			secret, verifier, redirect, client := f.secret, trace.Verifier, f.rpOrigin+"/callback", f.client
			expected := 400
			switch name {
			case "secret":
				secret = random(t)
				expected = 401
			case "foreign_tenant_client":
				client, secret = foreignClient, foreignSecret
				expected = 401
			case "other_client":
				client, secret = otherClient, otherSecret
			case "redirect":
				redirect = f.rpOrigin + "/other"
			case "pkce":
				verifier = random(t)
			}
			var status int
			var fields map[string]json.RawMessage
			if name == "query_tenant" || name == "duplicate_code" {
				body := url.Values{"grant_type": {"authorization_code"}, "code": {trace.Code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}.Encode()
				endpoint := f.opOrigin + "/oauth/token"
				if name == "query_tenant" {
					endpoint += "?tenant_id=" + foreignTenant
				} else {
					body += "&co%64e=" + url.QueryEscape(trace.Code)
				}
				r, err := http.NewRequestWithContext(f.ctx, "POST", endpoint, strings.NewReader(body))
				if err != nil {
					t.Fatal("private negative request construction failed")
				}
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.SetBasicAuth(url.QueryEscape(client), url.QueryEscape(secret))
				response, err := f.serverClient.Do(r)
				if err != nil {
					t.Fatal("private negative request failed")
				}
				status = response.StatusCode
				raw, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if json.Unmarshal(raw, &fields) != nil {
					t.Fatal("private negative response invalid")
				}
			} else {
				status, fields = f.exchange(t, trace, secret, verifier, redirect, client)
			}
			if status != expected {
				t.Fatal("invalid exchange returned wrong refusal class")
			}
			noTokenFields(t, fields)
			if f.scalar(t, `SELECT count(*) FROM op_fixture_tokens`) != before || f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE id=$1 AND consumed_at IS NOT NULL`, trace.RequestID) != 0 {
				t.Fatal("refused exchange consumed code or created token")
			}
			status, fields = f.exchange(t, trace, f.secret, trace.Verifier, f.rpOrigin+"/callback", f.client)
			if status != 200 || fields["access_token"] == nil || fields["id_token"] == nil {
				t.Fatal("explicit corrected exchange failed")
			}
		})
	}
}

func TestLiveOPCurrentAuthorityAndLifetime(t *testing.T) {
	for _, name := range []string{"client_disable", "client_rotation", "installation", "principal", "session", "membership_recreation"} {
		t.Run(name, func(t *testing.T) {
			f := newProtocolFixture(t, nil)
			trace := f.issueCode(t, nil)
			response := f.request(t, f.browser, "GET", trace.Callback, nil, "")
			responseBody(t, response, 200)
			old := f.rpResult(t)
			pending := f.issueCode(t, nil)
			var workspace string
			if err := f.pool.QueryRow(f.ctx, `SELECT workspace_id::text FROM op_fixture_clients WHERE tenant_id=$1 AND client_id=$2`, f.config.Tenant, f.client).Scan(&workspace); err != nil {
				t.Fatal("own workspace missing")
			}
			switch name {
			case "client_disable":
				f.exec(t, `UPDATE op_fixture_clients SET enabled=false,revision=revision+1 WHERE tenant_id=$1 AND client_id=$2`, f.config.Tenant, f.client)
				f.rejectUserInfo(t, old.tokens.AccessToken)
				f.exec(t, `UPDATE op_fixture_clients SET enabled=true,revision=revision+1 WHERE tenant_id=$1 AND client_id=$2`, f.config.Tenant, f.client)
			case "client_rotation":
				fresh := random(t) + " :+/%"
				f.exec(t, `UPDATE op_fixture_clients SET secret_digest=$3,revision=revision+1 WHERE tenant_id=$1 AND client_id=$2`, f.config.Tenant, f.client, digest(fresh))
				f.reloadRP(t, fresh)
			case "installation":
				f.exec(t, `UPDATE op_fixture_installations SET enabled=false,revision=revision+1 WHERE tenant_id=$1`, f.config.Tenant)
				f.rejectUserInfo(t, old.tokens.AccessToken)
				f.exec(t, `UPDATE op_fixture_installations SET enabled=true,revision=revision+1 WHERE tenant_id=$1`, f.config.Tenant)
			case "principal":
				f.exec(t, `UPDATE op_fixture_principals SET enabled=false,auth_epoch=auth_epoch+1 WHERE tenant_id=$1 AND user_id=$2`, f.config.Tenant, f.user)
				f.rejectUserInfo(t, old.tokens.AccessToken)
				f.exec(t, `UPDATE op_fixture_principals SET enabled=true,auth_epoch=auth_epoch+1 WHERE tenant_id=$1 AND user_id=$2`, f.config.Tenant, f.user)
			case "session":
				f.exec(t, `UPDATE op_fixture_sessions SET revoked_at=clock_timestamp() WHERE tenant_id=$1 AND user_id=$2`, f.config.Tenant, f.user)
			case "membership_recreation":
				f.exec(t, `DELETE FROM op_fixture_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3`, f.config.Tenant, workspace, f.user)
				f.exec(t, `INSERT INTO op_fixture_memberships VALUES($1,$2,$3,$4)`, f.config.Tenant, workspace, f.user, uuid.NewString())
			}
			f.rejectUserInfo(t, old.tokens.AccessToken)
			status, fields := f.exchange(t, pending, f.secret, pending.Verifier, f.rpOrigin+"/callback", f.client)
			if status != 400 {
				t.Fatal("stale source code was accepted after authority repair")
			}
			noTokenFields(t, fields)
			if f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE id=$1 AND consumed_at IS NOT NULL`, pending.RequestID) != 0 {
				t.Fatal("stale source refusal consumed code")
			}
			fresh := f.issueCode(t, nil)
			response = f.request(t, f.browser, "GET", fresh.Callback, nil, "")
			responseBody(t, response, 200)
			current := f.rpResult(t)
			if current.subject != f.subject || current.tokens.AccessToken == old.tokens.AccessToken {
				t.Fatal("fresh verified authority did not recover")
			}
		})
	}
}

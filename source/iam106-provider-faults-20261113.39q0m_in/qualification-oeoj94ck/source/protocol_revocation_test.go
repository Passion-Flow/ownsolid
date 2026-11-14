package boundary

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func (f *protocolFixture) revoke(t *testing.T, token, hint, client, secret string) int {
	t.Helper()
	f.evidence.record(token, secret)
	body := url.Values{"token": {token}, "token_type_hint": {hint}}
	r, err := http.NewRequestWithContext(f.ctx, "POST", f.opOrigin+"/revoke", strings.NewReader(body.Encode()))
	if err != nil {
		t.Fatal("private revocation construction failed")
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth(url.QueryEscape(client), url.QueryEscape(secret))
	response, err := f.serverClient.Do(r)
	if err != nil {
		t.Fatal("private revocation HTTPS failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("bounded revocation response failed")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		t.Fatal("private revocation response invalid")
	}
	noTokenFields(t, fields)
	return response.StatusCode
}
func TestLiveOPRevocationScopeAuditAndIdempotence(t *testing.T) {
	f := newProtocolFixture(t, nil)
	trace := f.issueCode(t, nil)
	response := f.request(t, f.browser, "GET", trace.Callback, nil, "")
	responseBody(t, response, 200)
	issued := f.rpResult(t)
	var workspace, installation string
	if f.pool.QueryRow(f.ctx, `SELECT workspace_id::text,installation_id::text FROM op_fixture_clients WHERE tenant_id=$1 AND client_id=$2`, f.config.Tenant, f.client).Scan(&workspace, &installation) != nil {
		t.Fatal("own revocation scope unavailable")
	}
	other, secret := "other-"+uuid.NewString(), random(t)
	f.exec(t, `INSERT INTO op_fixture_clients VALUES($1,$2,$3,$4,$5,1,$6,true)`, f.config.Tenant, other, workspace, installation, digest(secret), f.rpOrigin+"/callback")
	foreignTenant, foreignWorkspace, foreignInstallation := uuid.NewString(), uuid.NewString(), uuid.NewString()
	foreignClient, foreignSecret := "foreign-"+uuid.NewString(), random(t)
	f.exec(t, `INSERT INTO op_fixture_tenants VALUES($1,true)`, foreignTenant)
	f.exec(t, `INSERT INTO op_fixture_installations VALUES($1,$2,$3,1,true)`, foreignTenant, foreignWorkspace, foreignInstallation)
	f.exec(t, `INSERT INTO op_fixture_clients VALUES($1,$2,$3,$4,$5,1,$6,true)`, foreignTenant, foreignClient, foreignWorkspace, foreignInstallation, digest(foreignSecret), f.rpOrigin+"/callback")
	for _, item := range []struct {
		name, token, hint, client, secret string
		status                            int
	}{
		{"wrong_secret", issued.tokens.AccessToken, "access_token", f.client, random(t), 401},
		{"foreign_tenant_client", issued.tokens.AccessToken, "access_token", foreignClient, foreignSecret, 401},
		{"other_client", issued.tokens.AccessToken, "access_token", other, secret, 200},
		{"invalid_token", random(t), "unrecognized", f.client, f.secret, 200},
		{"invalid_token_empty_hint", random(t), "", f.client, f.secret, 200},
		{"invalid_token_space_hint", random(t), "unknown hint", f.client, f.secret, 200},
		{"id_token", issued.tokens.IDToken, "access_token", f.client, f.secret, 200},
	} {
		t.Run(item.name, func(t *testing.T) {
			if f.revoke(t, item.token, item.hint, item.client, item.secret) != item.status {
				t.Fatal("scoped revocation returned wrong class")
			}
			if f.scalar(t, `SELECT count(*) FROM op_fixture_tokens WHERE revoked_at IS NOT NULL`) != 0 || f.scalar(t, `SELECT count(*) FROM op_fixture_events WHERE action='token_revoked'`) != 0 {
				t.Fatal("foreign/invalid revocation changed token")
			}
		})
	}
	f.exec(t, `CREATE FUNCTION refuse_revoke_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='token_revoked' THEN RAISE EXCEPTION 'PRIVATE_REVOCATION_AUDIT_REFUSAL'; END IF; RETURN NEW; END; $$; CREATE TRIGGER refuse_revoke_event BEFORE INSERT ON op_fixture_events FOR EACH ROW EXECUTE FUNCTION refuse_revoke_event()`)
	if f.revoke(t, issued.tokens.AccessToken, "access_token", f.client, f.secret) != 500 || f.scalar(t, `SELECT count(*) FROM op_fixture_tokens WHERE revoked_at IS NOT NULL`) != 0 {
		t.Fatal("revocation audit refusal did not roll back")
	}
	f.exec(t, `DROP TRIGGER refuse_revoke_event ON op_fixture_events; DROP FUNCTION refuse_revoke_event()`)
	// An unrecognized hint must not prevent lookup of the supported access token.
	if f.revoke(t, issued.tokens.AccessToken, "unrecognized", f.client, f.secret) != 200 {
		t.Fatal("explicit revocation with unknown hint failed")
	}
	f.rejectUserInfo(t, issued.tokens.AccessToken)
	if f.revoke(t, issued.tokens.AccessToken, "refresh_token", f.client, f.secret) != 200 || f.scalar(t, `SELECT count(*) FROM op_fixture_tokens WHERE revoked_at IS NOT NULL`) != 1 || f.scalar(t, `SELECT count(*) FROM op_fixture_events WHERE action='token_revoked'`) != 1 {
		t.Fatal("repeated revocation changed outcome")
	}
	fresh := f.issueCode(t, nil)
	response = f.request(t, f.browser, "GET", fresh.Callback, nil, "")
	responseBody(t, response, 200)
	if f.rpResult(t).subject != f.subject {
		t.Fatal("explicit authorization after revocation failed")
	}
}

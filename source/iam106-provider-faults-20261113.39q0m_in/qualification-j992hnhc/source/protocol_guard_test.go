package boundary

import (
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestLiveOPTLSAndHeaderOnlyUserInfo(t *testing.T) {
	f := newProtocolFixture(t, nil)
	trace := f.issueCode(t, nil)
	response := f.request(t, f.browser, "GET", trace.Callback, nil, "")
	responseBody(t, response, 200)
	issued := f.rpResult(t)
	for _, name := range []string{"wrong_ca", "wrong_name"} {
		t.Run(name, func(t *testing.T) {
			transport := f.serverTransport.inner.Clone()
			transport.TLSClientConfig = transport.TLSClientConfig.Clone()
			if name == "wrong_ca" {
				transport.TLSClientConfig.RootCAs = x509.NewCertPool()
			} else {
				transport.TLSClientConfig.ServerName = "foreign.iam106.test"
			}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: f.config.Policy.Operation}
			r, err := http.NewRequestWithContext(f.ctx, "GET", f.opOrigin+"/.well-known/openid-configuration", nil)
			if err != nil {
				t.Fatal("private TLS probe construction failed")
			}
			response, err := client.Do(r)
			if response != nil {
				response.Body.Close()
			}
			if err == nil {
				t.Fatal("untrusted CA or foreign certificate name accepted")
			}
		})
	}
	for _, name := range []string{"query_token", "form_token", "id_token", "authorization_code", "foreign_host", "duplicate_bearer"} {
		t.Run(name, func(t *testing.T) {
			method, target, token := "GET", f.opOrigin+"/userinfo", issued.tokens.AccessToken
			var body io.Reader
			switch name {
			case "query_token":
				target += "?access_token=" + url.QueryEscape(token)
			case "form_token":
				method = "POST"
				body = strings.NewReader(url.Values{"access_token": {token}}.Encode())
			case "id_token":
				token = issued.tokens.IDToken
			case "authorization_code":
				token = trace.Code
			}
			r, err := http.NewRequestWithContext(f.ctx, method, target, body)
			if err != nil {
				t.Fatal("private UserInfo guard construction failed")
			}
			r.Header.Set("Authorization", "Bearer "+token)
			if name == "form_token" {
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			if name == "foreign_host" {
				r.Host = "foreign.iam106.test"
			}
			if name == "duplicate_bearer" {
				r.Header.Add("Authorization", "Bearer "+token)
			}
			response, err := f.serverClient.Do(r)
			if err != nil {
				t.Fatal("private UserInfo guard HTTPS failed")
			}
			status := response.StatusCode
			raw := responseBody(t, response, status)
			if status != 400 && status != 401 {
				t.Fatal("forbidden UserInfo credential placement accepted")
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil {
				t.Fatal("UserInfo refusal invalid")
			}
			noTokenFields(t, fields)
		})
	}
	r, err := http.NewRequestWithContext(f.ctx, "GET", f.opOrigin+"/userinfo", nil)
	if err != nil {
		t.Fatal("private valid UserInfo construction failed")
	}
	r.Header.Set("Authorization", "Bearer "+issued.tokens.AccessToken)
	response, err = f.serverClient.Do(r)
	if err != nil {
		t.Fatal("valid UserInfo retry HTTPS failed")
	}
	var info struct {
		Subject string `json:"sub"`
	}
	if json.Unmarshal(responseBody(t, response, 200), &info) != nil || info.Subject != f.subject {
		t.Fatal("valid UserInfo retry changed identity")
	}
}

func TestLiveOPForeignTenantAuthorizationRequest(t *testing.T) {
	f := newProtocolFixture(t, nil)
	pending := f.pendingLogin(t, f.browser)
	tenant, workspace, installation := uuid.NewString(), uuid.NewString(), uuid.NewString()
	client, secret := "foreign-"+uuid.NewString(), random(t)
	id := "ar_" + random(t)
	f.exec(t, `INSERT INTO op_fixture_tenants VALUES($1,true)`, tenant)
	f.exec(t, `INSERT INTO op_fixture_installations VALUES($1,$2,$3,1,true)`, tenant, workspace, installation)
	f.exec(t, `INSERT INTO op_fixture_clients VALUES($1,$2,$3,$4,$5,1,$6,true)`, tenant, client, workspace, installation, digest(secret), f.rpOrigin+"/callback")
	f.exec(t, `INSERT INTO op_fixture_requests(tenant_id,id,client_id,client_revision,installation_revision,workspace_id,installation_id,browser_digest,csrf_digest,redirect_uri,state,nonce,challenge,scope,expires_at) SELECT $1,$2,$3,1,1,$4,$5,browser_digest,csrf_digest,redirect_uri,state,nonce,challenge,scope,expires_at FROM op_fixture_requests WHERE tenant_id=$6 AND id=$7`, tenant, id, client, workspace, installation, f.config.Tenant, pending.id)
	for _, path := range []string{"/login?id=" + id, "/authorize/callback?id=" + id} {
		response := f.request(t, f.browser, "GET", f.opOrigin+path, nil, "")
		if response.Header.Get("Location") != "" {
			t.Fatal("foreign request opened a redirect")
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(responseBody(t, response, 400), &fields) != nil {
			t.Fatal("foreign request refusal invalid")
		}
		noTokenFields(t, fields)
	}
	response := f.request(t, f.browser, "POST", f.opOrigin+"/login", url.Values{"id": {id}, "csrf": {pending.csrf}, "username": {f.config.LoginName}, "password": {f.password}}, f.opOrigin)
	var refusal map[string]json.RawMessage
	if json.Unmarshal(responseBody(t, response, 403), &refusal) != nil {
		t.Fatal("foreign login refusal invalid")
	}
	noTokenFields(t, refusal)
	if f.scalar(t, `SELECT count(*) FROM op_fixture_sessions`) != 0 || f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE approved_at IS NOT NULL OR code_digest IS NOT NULL`) != 0 {
		t.Fatal("foreign request changed own or foreign authority")
	}
	response = f.request(t, f.browser, "POST", f.opOrigin+"/login", url.Values{"id": {pending.id}, "csrf": {pending.csrf}, "username": {f.config.LoginName}, "password": {f.password}}, f.opOrigin)
	responseBody(t, response, 303)
	if f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE tenant_id=$1 AND id=$2 AND approved_at IS NOT NULL`, f.config.Tenant, pending.id) != 1 || f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE tenant_id=$1 AND id=$2 AND approved_at IS NOT NULL`, tenant, id) != 0 {
		t.Fatal("own request repair changed foreign tenant")
	}
}

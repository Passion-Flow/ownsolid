package boundary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	relyingparty "github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"
)

type codeTrace struct{ Code, State, Nonce, Verifier, Callback, RequestID string }

var csrfField = regexp.MustCompile(`name="csrf" value="([A-Za-z0-9_-]+)"`)

func responseBody(t *testing.T, response *http.Response, status int) []byte {
	t.Helper()
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf("private protocol response mismatch: want %d, got %d", status, response.StatusCode)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("private protocol response lacks no-store")
	}
	return raw
}
func (f *protocolFixture) issueCode(t *testing.T, change func(url.Values)) codeTrace {
	t.Helper()
	response := f.request(t, f.browser, "GET", f.rpOrigin+"/start", nil, "")
	destination := response.Header.Get("Location")
	responseBody(t, response, 302)
	u, err := url.Parse(destination)
	if err != nil || u.Scheme+"://"+u.Host != f.opOrigin || u.Path != "/authorize" {
		t.Fatal("RP authorization destination mismatch")
	}
	values := u.Query()
	state := values.Get("state")
	f.app.mu.Lock()
	pending, ok := f.app.pending[state]
	f.app.mu.Unlock()
	if !ok {
		t.Fatal("RP pending state missing")
	}
	if change != nil {
		change(values)
		u.RawQuery = values.Encode()
	}
	response = f.request(t, f.browser, "GET", u.String(), nil, "")
	login := response.Header.Get("Location")
	responseBody(t, response, 302)
	loginURL, err := url.Parse(login)
	if err != nil || loginURL.Scheme+"://"+loginURL.Host != f.opOrigin || loginURL.Path != "/login" {
		t.Fatal("OP login destination mismatch")
	}
	id := loginURL.Query().Get("id")
	if !validRequestID(id) {
		t.Fatal("OP request ID shape mismatch")
	}
	response = f.request(t, f.browser, "GET", login, nil, "")
	form := responseBody(t, response, 200)
	csrf := csrfField.FindSubmatch(form)
	if len(csrf) != 2 {
		t.Fatal("private login CSRF field missing")
	}
	response = f.request(t, f.browser, "POST", f.opOrigin+"/login", url.Values{"id": {id}, "csrf": {string(csrf[1])}, "username": {f.config.LoginName}, "password": {f.password}}, f.opOrigin)
	callback := response.Header.Get("Location")
	responseBody(t, response, 303)
	response = f.request(t, f.browser, "GET", callback, nil, "")
	rpCallback := response.Header.Get("Location")
	responseBody(t, response, 302)
	target, err := url.Parse(rpCallback)
	if err != nil || target.Scheme+"://"+target.Host != f.rpOrigin || target.Path != "/callback" || target.Query().Get("state") != state || target.Query().Get("code") == "" {
		t.Fatal("OP code response not bound to RP")
	}
	return codeTrace{Code: target.Query().Get("code"), State: state, Nonce: pending.nonce, Verifier: pending.verifier, Callback: rpCallback, RequestID: id}
}
func (f *protocolFixture) exchange(t *testing.T, trace codeTrace, secret, verifier, redirect, client string) (int, map[string]json.RawMessage) {
	t.Helper()
	fields := url.Values{"grant_type": {"authorization_code"}, "code": {trace.Code}, "redirect_uri": {redirect}, "code_verifier": {verifier}}
	r, err := http.NewRequestWithContext(f.ctx, "POST", f.opOrigin+"/oauth/token", strings.NewReader(fields.Encode()))
	if err != nil {
		t.Fatal("private exchange request construction failed")
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth(url.QueryEscape(client), url.QueryEscape(secret))
	response, err := f.serverClient.Do(r)
	if err != nil {
		t.Fatal("private exchange HTTPS failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal("private exchange response unreadable")
	}
	var parsed map[string]json.RawMessage
	if json.Unmarshal(raw, &parsed) != nil {
		t.Fatal("private exchange JSON invalid")
	}
	return response.StatusCode, parsed
}
func (f *protocolFixture) scalar(t *testing.T, query string, args ...any) int {
	t.Helper()
	var result int
	if err := f.pool.QueryRow(f.ctx, query, args...).Scan(&result); err != nil {
		t.Fatal("private database readback failed")
	}
	return result
}
func (f *protocolFixture) rpResult(t *testing.T) rpResult {
	t.Helper()
	select {
	case result := <-f.app.results:
		return result
	case <-time.After(f.config.Policy.Operation):
		t.Fatal("actual RP result deadline")
		return rpResult{}
	}
}

func TestLiveOPProcessCodeFlow(t *testing.T) {
	f := newProtocolFixture(t, nil)
	trace := f.issueCode(t, nil)
	response := f.request(t, f.browser, "GET", trace.Callback, nil, "")
	var signedIn struct {
		SignedIn bool   `json:"signed_in"`
		Subject  string `json:"sub"`
	}
	if json.Unmarshal(responseBody(t, response, 200), &signedIn) != nil || !signedIn.SignedIn || signedIn.Subject != f.subject {
		t.Fatal("actual RP sign-in did not preserve stable subject")
	}
	first := f.rpResult(t)
	if first.subject != f.subject || first.tokens.IDTokenClaims.GetNonce() != trace.Nonce || first.tokens.IDTokenClaims.GetIssuer() != f.opOrigin {
		t.Fatal("actual RP claims mismatch")
	}
	status, fields := f.exchange(t, trace, f.secret, trace.Verifier, f.rpOrigin+"/callback", f.client)
	if status != 400 || fields["access_token"] != nil || fields["id_token"] != nil {
		t.Fatal("consumed code was replayed")
	}
	oldPID := f.child.command.Process.Pid
	f.restart(t)
	if f.child.command.Process.Pid == oldPID {
		t.Fatal("OP process was not replaced")
	}
	f.app.mu.Lock()
	remote := f.app.remote
	f.app.mu.Unlock()
	info, err := relyingparty.Userinfo[*oidc.UserInfo](context.Background(), first.tokens.AccessToken, first.tokens.TokenType, first.subject, remote)
	if err != nil || info.Subject != f.subject {
		t.Fatal("retained token authority did not survive actual OP process restart")
	}
	secondTrace := f.issueCode(t, nil)
	response = f.request(t, f.browser, "GET", secondTrace.Callback, nil, "")
	responseBody(t, response, 200)
	second := f.rpResult(t)
	if second.subject != first.subject || second.tokens.IDToken == first.tokens.IDToken || second.tokens.AccessToken == first.tokens.AccessToken || secondTrace.RequestID == trace.RequestID {
		t.Fatal("fresh flow identity or credential lifetime mismatch")
	}
	if f.scalar(t, `SELECT count(*) FROM op_fixture_tokens`) != 2 || f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE consumed_at IS NOT NULL`) != 2 || f.scalar(t, `SELECT count(*) FROM op_fixture_events WHERE action='code_redeemed'`) != 2 {
		t.Fatal("durable code/token/audit outcome mismatch")
	}
	response = f.request(t, f.serverClient, "GET", f.opOrigin+"/.well-known/openid-configuration", nil, "")
	var discovery map[string]json.RawMessage
	if json.Unmarshal(responseBody(t, response, 200), &discovery) != nil || discovery["introspection_endpoint"] != nil || discovery["end_session_endpoint"] != nil || discovery["device_authorization_endpoint"] != nil {
		t.Fatal("closed profile advertised unsupported routes")
	}
}

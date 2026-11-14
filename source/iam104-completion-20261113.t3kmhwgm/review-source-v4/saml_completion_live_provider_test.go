//go:build repository_integration

package sso

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/crewjam/saml"
	"github.com/google/uuid"
	dsig "github.com/russellhaering/goxmldsig"
	"golang.org/x/net/html"
)

type samlCompletionLiveConfiguration struct{ Issuer, Endpoint, Metadata, SP, ACS, Username, PasswordFile, CAFile, ConnectorID, PortalOrigin string }
type samlCompletionLiveForm struct {
	attrs  map[string]string
	values url.Values
}

func samlCompletionLiveAttrs(t *testing.T, values []html.Attribute) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, a := range values {
		key := strings.ToLower(a.Key)
		if _, ok := out[key]; ok {
			t.Fatal("duplicate fixture HTML attribute")
		}
		out[key] = a.Val
	}
	return out
}
func samlCompletionLiveForms(t *testing.T, raw []byte) []samlCompletionLiveForm {
	t.Helper()
	root, e := html.Parse(strings.NewReader(string(raw)))
	if e != nil {
		t.Fatal("provider HTML unavailable")
	}
	var out []samlCompletionLiveForm
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			f := samlCompletionLiveForm{attrs: samlCompletionLiveAttrs(t, n.Attr), values: url.Values{}}
			var inputs func(*html.Node)
			inputs = func(x *html.Node) {
				if x.Type == html.ElementNode && x.Data == "input" {
					a := samlCompletionLiveAttrs(t, x.Attr)
					if a["name"] != "" {
						if _, ok := f.values[a["name"]]; ok {
							t.Fatal("duplicate provider form field")
						}
						f.values.Set(a["name"], a["value"])
					}
				}
				for child := x.FirstChild; child != nil; child = child.NextSibling {
					inputs(child)
				}
			}
			inputs(n)
			out = append(out, f)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}
func samlCompletionLiveClient(t *testing.T) (samlCompletionLiveConfiguration, *http.Client) {
	t.Helper()
	path := os.Getenv("IAM104_FIXTURE_CONFIG")
	if path == "" {
		t.Fatal("explicit private SAML provider required")
	}
	raw, e := os.ReadFile(path)
	var c samlCompletionLiveConfiguration
	if e != nil || json.Unmarshal(raw, &c) != nil {
		t.Fatal("private SAML fixture unavailable")
	}
	ca, e := os.ReadFile(c.CAFile)
	pool := x509.NewCertPool()
	if e != nil || !pool.AppendCertsFromPEM(ca) {
		t.Fatal("fixture CA unavailable")
	}
	origin, e := url.Parse(c.Issuer)
	if e != nil {
		t.Fatal("fixture issuer unavailable")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DisableCompression: true, MaxConnsPerHost: 2, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: 5 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != origin.Host {
			return nil, refused
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", origin.Port()))
	}
	t.Cleanup(transport.CloseIdleConnections)
	return c, &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func samlCompletionLiveBody(t *testing.T, r *http.Response) []byte {
	t.Helper()
	defer r.Body.Close()
	b, e := io.ReadAll(io.LimitReader(r.Body, 512<<10+1))
	if e != nil || len(b) > 512<<10 {
		t.Fatal("provider response unavailable or oversized")
	}
	return b
}
func samlCompletionLiveVerifier(t *testing.T, c samlCompletionLiveConfiguration, httpClient *http.Client) *verifier {
	t.Helper()
	r, e := httpClient.Get(c.Metadata)
	if e != nil {
		t.Fatal("private metadata unavailable")
	}
	raw := samlCompletionLiveBody(t, r)
	if r.StatusCode != 200 {
		t.Fatal("private metadata refused")
	}
	return makeVerifier(t, configuration{SP: c.SP, ACS: c.ACS, Issuer: c.Issuer, Endpoint: c.Endpoint, Metadata: raw, TrustedUntil: time.Now().UTC().Truncate(time.Microsecond).Add(time.Hour), Clock: dsig.NewRealClock(), Limits: limits{128 << 10, 24, 2048, 32}, MaxAge: 60 * time.Second, Skew: 30 * time.Second, MaxAssertionLifetime: 5 * time.Minute})
}
func samlCompletionLiveExchange(t *testing.T, c samlCompletionLiveConfiguration, httpClient *http.Client, v *verifier, requestID, relay string) string {
	t.Helper()
	request, e := v.sp.MakeAuthenticationRequest(c.Endpoint, saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if e != nil {
		t.Fatal("SP request unavailable")
	}
	request.ID = requestID
	target, e := request.Redirect(relay, v.sp)
	if e != nil {
		t.Fatal("SP redirect unavailable")
	}
	jar, e := cookiejar.New(nil)
	if e != nil {
		t.Fatal(e)
	}
	browser := *httpClient
	browser.Jar = jar
	r, e := browser.Get(target.String())
	if e != nil {
		t.Fatal("private authorization unavailable")
	}
	body := samlCompletionLiveBody(t, r)
	if r.StatusCode != 200 {
		t.Fatal("private password page refused")
	}
	var passwordForm *samlCompletionLiveForm
	for _, f := range samlCompletionLiveForms(t, body) {
		if f.attrs["id"] == "kc-form-login" {
			if passwordForm != nil {
				t.Fatal("ambiguous password form")
			}
			copied := f
			passwordForm = &copied
		}
	}
	if passwordForm == nil || strings.ToLower(passwordForm.attrs["method"]) != "post" {
		t.Fatal("provider password form absent")
	}
	action, e := url.Parse(passwordForm.attrs["action"])
	issuer, _ := url.Parse(c.Issuer)
	if e != nil || action.Scheme != "https" || action.Host != issuer.Host || action.Path != issuer.Path+"/login-actions/authenticate" || action.User != nil || action.Fragment != "" {
		t.Fatal("password destination refused")
	}
	password, e := os.ReadFile(c.PasswordFile)
	if e != nil || len(password) == 0 || len(password) > 16384 {
		t.Fatal("fixture credential unavailable")
	}
	values := passwordForm.values
	values.Set("username", c.Username)
	values.Set("password", string(password))
	values.Set("credentialId", "")
	req, e := http.NewRequest("POST", action.String(), strings.NewReader(values.Encode()))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", issuer.Scheme+"://"+issuer.Host)
	r, e = browser.Do(req)
	if e != nil {
		t.Fatal("private password exchange unavailable")
	}
	body = samlCompletionLiveBody(t, r)
	if r.StatusCode != 200 {
		t.Fatal("private SAML result refused")
	}
	var response *samlCompletionLiveForm
	for _, f := range samlCompletionLiveForms(t, body) {
		if f.values.Has("SAMLResponse") {
			if response != nil {
				t.Fatal("ambiguous SAML response form")
			}
			copied := f
			response = &copied
		}
	}
	if response == nil || strings.ToLower(response.attrs["method"]) != "post" || response.attrs["action"] != c.ACS || response.values.Get("RelayState") != relay || len(response.values.Get("SAMLResponse")) == 0 {
		t.Fatal("SAML result destination or correlation refused")
	}
	// No ACS request or local session is sent/issued by this HTTP protocol probe.
	return response.values.Get("SAMLResponse")
}

func TestSAMLCompletionLiveProviderReady(t *testing.T) {
	c, client := samlCompletionLiveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, c.Metadata, nil)
		if e != nil {
			t.Fatal("private provider metadata scope unavailable")
		}
		response, e := client.Do(req)
		if e == nil {
			body := samlCompletionLiveBody(t, response)
			if response.StatusCode == http.StatusOK && len(body) > 0 {
				return
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("private provider did not become ready within its bounded fixture deadline")
}

// This root joins a real private IdP to the actual product completion/source
// owners. It does not claim rendered-browser, HTTP ACS or installed-UI coverage.
func TestSAMLCompletionLiveProvider(t *testing.T) {
	c, client := samlCompletionLiveClient(t)
	if !canonicalID(c.ConnectorID) || !strings.HasPrefix(c.PortalOrigin, "https://") {
		t.Fatal("private first-party scope unavailable")
	}
	v := samlCompletionLiveVerifier(t, c, client)
	f, password := bindingRepositoryFixture(t)
	up, e := os.ReadFile(os.Getenv("IAM_SAML_CLAIM_SQL_FILE"))
	if e != nil {
		t.Fatal("private candidate SQL unavailable")
	}
	if _, e = f.f.PostgreSQL.Exec(f.ctx, string(up)); e != nil {
		t.Fatal("private source candidate", e)
	}
	f.accounts.Config.Server.PortalOrigin = c.PortalOrigin
	issuer, e := url.Parse(c.Issuer)
	if e != nil {
		t.Fatal(e)
	}
	f.exec(t, transaction.SQL(transaction.PostgreSQL, `INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,enabled,created_by) VALUES($1,$2,'Actual private provider completion','saml',$3,true,$4)`, c.ConnectorID, f.f.Tenant, issuer.Scheme+"://"+issuer.Host, f.f.User))
	digest := sha256.Sum256(v.cfg.Metadata)
	f.exec(t, transaction.SQL(transaction.PostgreSQL, `INSERT INTO sso_saml_profiles(tenant_id,connector_id,issuer,sp_entity,name_id_format,metadata_digest,trust_until,maximum_session_seconds) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, f.f.Tenant, c.ConnectorID, c.Issuer, c.SP, string(saml.PersistentNameIDFormat), digest[:], v.cfg.TrustedUntil, 3600))
	owner, e := NewSAMLCompletion(f.ctx, f.s)
	if e != nil {
		t.Fatal(e)
	}
	f.accounts.Sources = owner
	producer := samlClaimProbe{service: f.s, connector: c.ConnectorID, reason: "Explicit actual private provider account linking", ttl: time.Duration(f.s.policy.FlowSeconds) * time.Second}
	callback, e := producer.callback(c.ConnectorID, "portal")
	if e != nil || callback != c.ACS {
		t.Fatal("real provider callback differs from captured first-party scope", e)
	}
	start, previous, target := samlProbeStarted(t, f, password, producer, "portal", false)
	encoded := samlCompletionLiveExchange(t, c, client, v, start.request, start.relay)
	claim, e := producer.claim(f.ctx, target, "portal", start.relay, start.browser, uuid.NewString())
	if e != nil {
		t.Fatal("actual provider result claim", e)
	}
	verified, e := verifySAMLProbeClaim(claim, v.cfg, encoded)
	if e != nil {
		t.Fatal("actual provider request/context proof", e)
	}
	result, e := producer.stage(f.ctx, claim, verified, uuid.NewString())
	if e != nil {
		t.Fatal("actual provider persistent result staging", e)
	}
	input := SAMLCompleteInput{ConnectorID: target, Audience: "portal", FlowID: start.id, Continuation: result.continuation, PreviousSession: previous}
	issued, e := owner.Complete(f.ctx, input, uuid.NewString())
	if e != nil || issued.Token == "" {
		t.Fatal("actual product private provider completion", e)
	}
	actor, e := f.accounts.Authenticate(f.ctx, issued.Token, "portal")
	if e != nil || actor.Me.User.ID != f.f.User {
		t.Fatal("actual sourced session authority", e)
	}
	var tenant, connector, binding, flow string
	var version int64
	if e = f.f.PostgreSQL.QueryRow(f.ctx, `SELECT sso_tenant_id::text,sso_connector_id::text,sso_binding_id::text,sso_flow_id::text,sso_auth_version FROM sessions WHERE id=$1 AND user_id=$2`, issued.SessionID, f.f.User).Scan(&tenant, &connector, &binding, &flow, &version); e != nil {
		t.Fatal(e)
	}
	if tenant != f.f.Tenant || connector != target || binding != result.identity || flow != start.id || version != claim.version {
		t.Fatal("real provider session lost its exact durable source")
	}
	if out, e := owner.Complete(f.ctx, input, uuid.NewString()); e == nil || out.Token != "" {
		t.Fatal("real provider completion reissued its opaque token", e)
	}
	f.exec(t, transaction.SQL(transaction.PostgreSQL, `UPDATE sso_connectors SET enabled=false,revision=revision+1,auth_version=auth_version+1 WHERE tenant_id=$1 AND id=$2`, f.f.Tenant, target))
	if _, e = f.accounts.Authenticate(f.ctx, issued.Token, "portal"); e == nil {
		t.Fatal("actual private-provider source survived connector revocation")
	}
}

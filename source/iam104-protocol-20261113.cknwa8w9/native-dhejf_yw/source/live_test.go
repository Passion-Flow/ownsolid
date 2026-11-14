package samlprofile

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
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

	"github.com/beevik/etree"
	"github.com/crewjam/saml"
	dsig "github.com/russellhaering/goxmldsig"
	"golang.org/x/net/html"
)

type liveConfiguration struct{ Issuer, Endpoint, Metadata, SP, ACS, Username, PasswordFile, CAFile string }
type form struct {
	attrs  map[string]string
	values url.Values
}

func attrs(t *testing.T, values []html.Attribute) map[string]string {
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
func forms(t *testing.T, raw []byte) []form {
	t.Helper()
	root, e := html.Parse(strings.NewReader(string(raw)))
	if e != nil {
		t.Fatal("provider HTML unavailable")
	}
	var out []form
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			f := form{attrs: attrs(t, n.Attr), values: url.Values{}}
			var inputs func(*html.Node)
			inputs = func(x *html.Node) {
				if x.Type == html.ElementNode && x.Data == "input" {
					a := attrs(t, x.Attr)
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
func liveClient(t *testing.T) (liveConfiguration, *http.Client) {
	t.Helper()
	path := os.Getenv("IAM104_FIXTURE_CONFIG")
	if path == "" {
		t.Skip("explicit private SAML provider required")
	}
	raw, e := os.ReadFile(path)
	var c liveConfiguration
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
func readBody(t *testing.T, r *http.Response) []byte {
	t.Helper()
	defer r.Body.Close()
	b, e := io.ReadAll(io.LimitReader(r.Body, 512<<10+1))
	if e != nil || len(b) > 512<<10 {
		t.Fatal("provider response unavailable or oversized")
	}
	return b
}
func nonce(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		t.Fatal(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func TestLiveKeycloakReady(t *testing.T) {
	c, httpClient := liveClient(t)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		r, e := httpClient.Get(c.Metadata)
		if e == nil {
			status := r.StatusCode
			raw := readBody(t, r)
			if status == 200 && len(raw) > 0 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("private SAML provider did not become ready")
}
func liveVerifier(t *testing.T, c liveConfiguration, httpClient *http.Client) *verifier {
	t.Helper()
	r, e := httpClient.Get(c.Metadata)
	if e != nil {
		t.Fatal("private metadata unavailable")
	}
	raw := readBody(t, r)
	if r.StatusCode != 200 {
		t.Fatal("private metadata refused")
	}
	return makeVerifier(t, configuration{SP: c.SP, ACS: c.ACS, Issuer: c.Issuer, Endpoint: c.Endpoint, Metadata: raw, TrustedUntil: time.Now().Add(time.Hour), Clock: dsig.NewRealClock(), Limits: limits{128 << 10, 24, 2048, 32}, MaxAge: 60 * time.Second, Skew: 30 * time.Second, MaxAssertionLifetime: 5 * time.Minute})
}
func liveExchange(t *testing.T, c liveConfiguration, httpClient *http.Client, v *verifier) (string, string) {
	t.Helper()
	request, e := v.sp.MakeAuthenticationRequest(c.Endpoint, saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if e != nil {
		t.Fatal("SP request unavailable")
	}
	relay := nonce(t)
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
	body := readBody(t, r)
	if r.StatusCode != 200 {
		t.Fatal("private password page refused")
	}
	var passwordForm *form
	for _, f := range forms(t, body) {
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
	body = readBody(t, r)
	if r.StatusCode != 200 {
		t.Fatal("private SAML result refused")
	}
	var response *form
	for _, f := range forms(t, body) {
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
	return response.values.Get("SAMLResponse"), request.ID
}
func TestLiveKeycloakSignedSAML(t *testing.T) {
	c, httpClient := liveClient(t)
	v := liveVerifier(t, c, httpClient)
	first, request := liveExchange(t, c, httpClient, v)
	p, e := v.verify(first, request)
	if e != nil || p.Subject.Value == "" || p.Subject.Format != string(saml.PersistentNameIDFormat) {
		t.Fatal("real persistent SAML proof refused", e)
	}
	second, nextRequest := liveExchange(t, c, httpClient, v)
	next, e := v.verify(second, nextRequest)
	if e != nil || p.Subject != next.Subject || p.AssertionID == next.AssertionID || request == nextRequest {
		t.Fatal("real persistent subject/correlation changed", e)
	}
	mustRefuse(t, v, first, nextRequest)
	decoded, e := base64.StdEncoding.DecodeString(first)
	if e != nil {
		t.Fatal(e)
	}
	doc := etree.NewDocument()
	if e = doc.ReadFromBytes(decoded); e != nil {
		t.Fatal(e)
	}
	id := doc.Root().FindElement("./Assertion/Subject/NameID")
	if id == nil {
		t.Fatal("real NameID absent")
	}
	id.SetText("substituted")
	mustRefuse(t, v, encoded(t, doc.Root()), request)
	// Keep only non-sensitive qualification dimensions in the public test log.
	digest := sha256.Sum256([]byte(p.Subject.Value))
	t.Logf("persistent subject bytes=%d digest-prefix=%x qualifiers=(%t,%t)", len(p.Subject.Value), digest[:4], p.Subject.NameQualifier != "", p.Subject.SPNameQualifier != "")
}

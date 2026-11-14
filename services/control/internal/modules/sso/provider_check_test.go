package sso

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/google/uuid"
)

// fakeGitea answers the token endpoint the way Gitea 28.3.0's handleAuthorizationCode does: client
// ID, then secret, then redirect URI, then the code (docs/research/sso-page.md).
type fakeGitea struct {
	clientID, secret, callback string
	// answer, when set, replaces the whole response.
	answer   func(http.ResponseWriter)
	requests atomic.Int64
}

func (f *fakeGitea) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	if r.Method != http.MethodPost || r.URL.Path != GiteaTokenPath || r.ParseForm() != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if f.answer != nil {
		f.answer(w)
		return
	}
	refuse := func(code, description string) {
		w.Header().Set("Content-Type", "application/json;charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": description})
	}
	id, secret, _ := r.BasicAuth()
	switch {
	case r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") == "" || r.Form.Get("code_verifier") == "":
		refuse("invalid_request", "malformed")
	case id != f.clientID:
		refuse("invalid_client", "cannot load client with client id: '"+id+"'")
	case secret != f.secret:
		refuse("unauthorized_client", "invalid client secret")
	case r.Form.Get("redirect_uri") != f.callback:
		refuse("unauthorized_client", "unexpected redirect URI")
	default:
		refuse("unauthorized_client", "client is not authorized")
	}
}

type checkFixture struct {
	provider *giteaProvider
	fake     *fakeGitea
	server   *httptest.Server
	callback string
}

func newCheckFixture(t *testing.T, registered bool) checkFixture {
	t.Helper()
	callback := "https://admin.example.invalid/api/v1/auth/sso/" + uuid.NewString() + "/callback"
	fake := &fakeGitea{clientID: uuid.NewString(), secret: "gto_synthetic", callback: callback}
	if !registered {
		fake.callback = "https://elsewhere.example.invalid/callback"
	}
	server := httptest.NewTLSServer(fake)
	t.Cleanup(server.Close)
	return checkFixture{provider: providerFor(t, server, server.Certificate().Raw, callback), fake: fake, server: server, callback: callback}
}

// providerFor builds a provider for the test server that trusts the certificate given.
func providerFor(t *testing.T, server *httptest.Server, trusted []byte, callback string) *giteaProvider {
	t.Helper()
	endpoint, _ := url.Parse(server.URL)
	dialIP, port, _ := net.SplitHostPort(endpoint.Host)
	origin := "https://" + net.JoinHostPort(server.Certificate().DNSNames[0], port)
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	ca := filepath.Join(root, "ca.pem")
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: trusted}), 0600); e != nil {
		t.Fatal(e)
	}
	policy := config.DefaultSSOPolicy()
	policy.ResponseBytes = 4096
	provider, e := newGiteaProvider(config.SSOProvider{Origin: origin, Issuer: origin, DialAddresses: []string{dialIP}, CAFile: ca}, policy, []string{callback}, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(provider.close)
	return provider
}

func TestCheckClientReadsEachOfGiteasRefusals(t *testing.T) {
	cases := []struct {
		name       string
		registered bool
		clientID   func(checkFixture) string
		secret     string
		want       string
	}{
		{"everything accepted", true, func(f checkFixture) string { return f.fake.clientID }, "gto_synthetic", CheckPassed},
		{"wrong secret", true, func(f checkFixture) string { return f.fake.clientID }, "gto_wrong", CheckCredentialsRejected},
		{"unknown client", true, func(checkFixture) string { return uuid.NewString() }, "gto_synthetic", CheckCredentialsRejected},
		{"callback not registered", false, func(f checkFixture) string { return f.fake.clientID }, "gto_synthetic", CheckCallbackNotRegistered},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newCheckFixture(t, c.registered)
			got, e := f.provider.checkClient(context.Background(), c.clientID(f), c.secret, f.callback)
			if e != nil || got.outcome != c.want || got.providerError != "" {
				t.Fatalf("got %+v %v, want %s", got, e, c.want)
			}
			if f.fake.requests.Load() != 1 {
				t.Fatalf("%d requests, want one", f.fake.requests.Load())
			}
		})
	}
}

func TestCheckClientNeverPassesAnAnswerItDoesNotKnow(t *testing.T) {
	cases := []struct {
		name          string
		answer        func(http.ResponseWriter)
		providerError string
	}{
		{"a token, which a made-up code cannot earn", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"x","token_type":"bearer","expires_in":3600}`))
		}, ""},
		{"a server error", func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) }, ""},
		{"a page instead of JSON", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("<html>sign in</html>"))
		}, ""},
		{"reworded refusal", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unauthorized_client","error_description":"Client is not authorized."}`))
		}, "unauthorized_client"},
		{"another OAuth error", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		}, "invalid_grant"},
		{"an error code that is not an OAuth code", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"<script>alert(1)</script>"}`))
		}, ""},
		{"duplicate keys", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error":"unauthorized_client","error_description":"client is not authorized"}`))
		}, ""},
		{"an answer larger than the policy allows", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unauthorized_client","error_description":"client is not authorized","padding":"` + string(make([]byte, 5000)) + `"}`))
		}, ""},
		{"a redirect", func(w http.ResponseWriter) {
			w.Header().Set("Location", "https://elsewhere.example.invalid/")
			w.WriteHeader(http.StatusFound)
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newCheckFixture(t, true)
			f.fake.answer = c.answer
			got, e := f.provider.checkClient(context.Background(), f.fake.clientID, f.fake.secret, f.callback)
			if e != nil || got.outcome != CheckUnexpectedAnswer || got.providerError != c.providerError {
				t.Fatalf("got %+v %v", got, e)
			}
		})
	}
}

// otherCertificate is a self-signed certificate the test server was not issued, since every
// httptest server shares one built-in certificate.
func otherCertificate(t *testing.T) []byte {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.com"}, DNSNames: []string{"example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	raw, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestCheckClientTellsAnUntrustedCertificateFromAnUnreachableProvider(t *testing.T) {
	f := newCheckFixture(t, true)
	untrusted := providerFor(t, f.server, otherCertificate(t), f.callback)
	got, e := untrusted.checkClient(context.Background(), f.fake.clientID, f.fake.secret, f.callback)
	if e != nil || got.outcome != CheckCertificateUntrusted {
		t.Fatalf("untrusted: %+v %v", got, e)
	}
	f.server.Close()
	got, e = f.provider.checkClient(context.Background(), f.fake.clientID, f.fake.secret, f.callback)
	if e != nil || got.outcome != CheckUnreachable {
		t.Fatalf("closed: %+v %v", got, e)
	}
	if f.fake.requests.Load() != 0 {
		t.Fatal("an untrusted provider received a request")
	}
}

func TestCheckClientSharesTheExchangeSlotsAndRefusesBadInput(t *testing.T) {
	f := newCheckFixture(t, true)
	for range cap(f.provider.slots) {
		f.provider.slots <- struct{}{}
	}
	if _, e := f.provider.checkClient(context.Background(), f.fake.clientID, f.fake.secret, f.callback); e == nil || e.Error() != "SSO_CAPACITY_EXCEEDED" {
		t.Fatalf("full slots: %v", e)
	}
	for range cap(f.provider.slots) {
		<-f.provider.slots
	}
	for name, input := range map[string][3]string{
		"client ID":        {"not-a-uuid", f.fake.secret, f.callback},
		"secret":           {f.fake.clientID, "has space", f.callback},
		"unknown callback": {f.fake.clientID, f.fake.secret, "https://other.example.invalid/callback"},
	} {
		if _, e := f.provider.checkClient(context.Background(), input[0], input[1], input[2]); e == nil || e.Error() != "SSO_AUTHORIZATION_INPUT_INVALID" {
			t.Errorf("%s: %v", name, e)
		}
	}
	if f.fake.requests.Load() != 0 {
		t.Fatal("refused input reached the provider")
	}
}

func TestCheckStoredSaysAnOriginLeftTheConfigurationWithoutAsking(t *testing.T) {
	s := &Service{enabled: true, approved: map[string]config.SSOProvider{}}
	got, e := s.checkStored(context.Background(), storedConnector{Connector: Connector{ID: uuid.NewString(), Origin: "https://gone.example.invalid"}})
	if e != nil || got.Outcome != CheckNotConfigured {
		t.Fatalf("%+v %v", got, e)
	}
}

func TestConfigurationListsApprovedOriginsInOrderAndOneCallbackOriginPerEntrance(t *testing.T) {
	admin := identity.Auth{}
	admin.Me.Audience, admin.Me.User.IsPlatformAdmin = "admin", true
	accounts := &identity.Service{}
	accounts.Config.Server.PortalOrigin = "https://ownsolid.example.invalid"
	accounts.Config.Server.AdminOrigin = "https://ownsolid.example.invalid"
	s := &Service{Identity: accounts, enabled: true, origins: []string{"https://b.example.invalid", "https://a.example.invalid"}}
	got, e := s.Configuration(admin)
	if e != nil || !got.Enabled || len(got.Providers) != 2 || got.Providers[0].Origin != "https://b.example.invalid" || got.Providers[0].Provider != ProviderGitea {
		t.Fatalf("%+v %v", got, e)
	}
	if len(got.CallbackOrigins) != 1 || got.CallbackOrigins[0] != "https://ownsolid.example.invalid" {
		t.Fatalf("one entrance origin: %v", got.CallbackOrigins)
	}
	accounts.Config.Server.AdminOrigin = "https://admin.example.invalid"
	if got, _ = s.Configuration(admin); len(got.CallbackOrigins) != 2 {
		t.Fatalf("two entrance origins: %v", got.CallbackOrigins)
	}
	accounts.Config.Server.PortalOrigin = "http://ownsolid.example.invalid"
	if got, _ = s.Configuration(admin); len(got.CallbackOrigins) != 0 {
		t.Fatalf("plain HTTP has no callback: %v", got.CallbackOrigins)
	}
	portal := admin
	portal.Me.Audience = "portal"
	member := admin
	member.Me.User.IsPlatformAdmin = false
	for name, a := range map[string]identity.Auth{"portal": portal, "not an administrator": member} {
		if _, e := s.Configuration(a); e == nil || e.Error() != "ACCESS_DENIED" {
			t.Errorf("%s: %v", name, e)
		}
		if _, e := s.TestConnector(context.Background(), a, uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "ACCESS_DENIED" {
			t.Errorf("%s test: %v", name, e)
		}
	}
	s.enabled = false
	if _, e := s.TestConnector(context.Background(), admin, uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "SSO_DISABLED" {
		t.Fatalf("disabled: %v", e)
	}
}

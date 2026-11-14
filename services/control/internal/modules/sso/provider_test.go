package sso

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// These are explicit protocol fault doubles over real local HTTPS, not actual Gitea qualification.
func TestM11ProviderTLSCodeIntrospectionExactScopeAndStableInt64Subject(t *testing.T) {
	clientID := uuid.NewString()
	callback := "https://portal.example.invalid/api/v1/auth/sso/" + uuid.NewString() + "/callback"
	secret := "synthetic-private-client-credential"
	verifier := security.Token()
	code := "synthetic-once-only-code"
	access := "synthetic-bearer-token"
	var mode atomic.Int64
	var posts atomic.Int64
	var userCalls atomic.Int64
	var origin string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.URL.Path == GiteaUserPath {
			userCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+access {
				t.Error("missing exact bearer authority")
				w.WriteHeader(401)
				return
			}
			if mode.Load() == 7 {
				w.Write([]byte(`{"id":9007199254740993,"id":1,"active":true,"prohibit_login":false,"type":"User"}`))
				return
			}
			kind := "User"
			if mode.Load() == 8 {
				kind = "Organization"
			}
			json.NewEncoder(w).Encode(map[string]any{"id": int64(9007199254740993), "active": true, "prohibit_login": false, "type": kind, "is_admin": true, "email": "matching@example.invalid", "login": "Ignored provider claims"})
			return
		}
		id, password, ok := r.BasicAuth()
		if !ok || id != clientID || password != secret {
			t.Error("missing confidential-client authentication")
			w.WriteHeader(401)
			return
		}
		if r.Method != "POST" || r.ParseForm() != nil || len(r.URL.Query()) != 0 {
			t.Error("unsafe protocol request")
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case GiteaTokenPath:
			posts.Add(1)
			if r.Form.Get("code") != code || r.Form.Get("redirect_uri") != callback || r.Form.Get("code_verifier") != verifier || r.Form.Get("grant_type") != "authorization_code" {
				t.Error("code/PKCE/exact redirect lost")
			}
			if mode.Load() == 9 {
				w.Header().Set("Location", origin+GiteaUserPath)
				w.WriteHeader(302)
				return
			}
			if mode.Load() == 10 {
				w.Write([]byte(strings.Repeat("x", 5000)))
				return
			}
			if mode.Load() == 11 {
				conn, _, e := w.(http.Hijacker).Hijack()
				if e == nil {
					conn.Close()
				}
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": access, "token_type": "bearer", "expires_in": 3600, "refresh_token": "ignored-not-persisted"})
		case GiteaIntrospectionPath:
			if r.Form.Get("token") != access {
				t.Error("wrong introspected token")
			}
			value := map[string]any{"active": true, "scope": "read:user", "iss": origin, "aud": []string{clientID}, "sub": "9007199254740993"}
			switch mode.Load() {
			case 1:
				value["scope"] = "all"
			case 2:
				value["iss"] = "https://wrong.example.invalid"
			case 3:
				value["aud"] = []string{uuid.NewString()}
			case 4:
				value["active"] = false
			case 5:
				value["sub"] = "09007199254740993"
			case 6:
				value["sub"] = "2"
			}
			json.NewEncoder(w).Encode(value)
		default:
			t.Error("unexpected destination")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	dialIP, port, _ := net.SplitHostPort(endpoint.Host)
	if len(server.Certificate().DNSNames) == 0 {
		t.Fatal("test TLS certificate needs a non-loopback DNS identity")
	}
	origin = "https://" + net.JoinHostPort(server.Certificate().DNSNames[0], port)
	caRoot := t.TempDir()
	if e := os.Chmod(caRoot, 0700); e != nil {
		t.Fatal(e)
	}
	ca := filepath.Join(caRoot, "ca.pem")
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	policy := config.DefaultSSOPolicy()
	policy.ResponseBytes = 4096
	configuration := config.SSOProvider{Origin: origin, Issuer: origin, DialAddresses: []string{dialIP}, CAFile: ca}
	if e := (config.SSOConfiguration{Providers: []config.SSOProvider{configuration}}).Validate(); e != nil {
		t.Fatal("actual test destination policy", e)
	}
	if _, e := readPrivateFile(ca, maximumProviderCertificateBytes); e != nil {
		t.Fatal("actual private test certificate boundary", e)
	}
	provider, e := newGiteaProvider(configuration, policy, []string{callback}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer provider.close()
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	state := security.Token()
	authorization, e := provider.authorizationURL(clientID, callback, state, verifier)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := url.Parse(authorization)
	if a.Query().Get("scope") != "read:user" || a.Query().Get("code_challenge_method") != "S256" || a.Query().Get("state") != state || a.Query().Get("redirect_uri") != callback || strings.Contains(authorization, verifier) || strings.Contains(authorization, secret) {
		t.Fatal("authorization URL lost fixed scope/PKCE or exposed secrets")
	}
	identity, e := provider.exchange(context.Background(), clientID, secret, callback, code, verifier)
	if e != nil || identity.subject != "9007199254740993" {
		t.Fatal("real HTTPS with narrow authenticated introspection and exact int64 subject", e)
	}
	for n := int64(1); n <= 11; n++ {
		mode.Store(n)
		before := posts.Load()
		_, e := provider.exchange(context.Background(), clientID, secret, callback, code, verifier)
		if e == nil || posts.Load() != before+1 {
			t.Fatalf("fault%d accepted or code retried", n)
		}
	}
	before := posts.Load()
	if _, e := provider.exchange(context.Background(), clientID, secret, "https://wrong.example.invalid/callback", code, verifier); e == nil || posts.Load() != before {
		t.Fatal("unregistered callback made external request")
	}
	provider.slots <- struct{}{}
	for len(provider.slots) < cap(provider.slots) {
		provider.slots <- struct{}{}
	}
	if _, e := provider.exchange(context.Background(), clientID, secret, callback, code, verifier); e == nil || e.Error() != "SSO_CAPACITY_EXCEEDED" || posts.Load() != before {
		t.Fatal("unbounded external exchange admission")
	}
	for len(provider.slots) > 0 {
		<-provider.slots
	}
	if userCalls.Load() < 1 {
		t.Fatal("user identity API never exercised")
	}
	configuration.CAFile = ""
	untrusted, e := newGiteaProvider(configuration, policy, []string{callback}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer untrusted.close()
	if _, e = untrusted.exchange(context.Background(), clientID, secret, callback, code, verifier); e == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
}

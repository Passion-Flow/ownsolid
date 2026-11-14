package prototype

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
)

func privateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal("fixture key generation failed")
	}
	return key
}
func signed(t *testing.T, key *rsa.PrivateKey, id string, payload []byte) string {
	t.Helper()
	signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", id).WithType("JWT"))
	if e != nil {
		t.Fatal("fixture signer failed")
	}
	token, e := signer.Sign(payload)
	if e != nil {
		t.Fatal("fixture signing failed")
	}
	raw, e := token.CompactSerialize()
	if e != nil {
		t.Fatal("fixture compact encoding failed")
	}
	return raw
}
func rawClaims(now time.Time) map[string]any {
	return map[string]any{"iss": "https://issuer.iam102.test/realms/private", "sub": "Opaque:Subject_001", "aud": "ownsolid-iam102", "azp": "ownsolid-iam102", "nonce": "expected-protected-nonce", "iat": now.Add(-time.Second).Unix(), "exp": now.Add(time.Minute).Unix()}
}
func jsonBytes(t *testing.T, value any) []byte {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal("fixture JSON failed")
	}
	return raw
}
func TestSignedClaimPolicy(t *testing.T) {
	key := privateKey(t)
	other := privateKey(t)
	now := time.Now().UTC().Truncate(time.Second)
	a := adapter{issuer: "https://issuer.iam102.test/realms/private", client: "ownsolid-iam102", now: func() time.Time { return now }, keys: &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}}

	valid := rawClaims(now)
	raw := signed(t, key, "active", jsonBytes(t, valid))
	token, e := a.verify(context.Background(), raw, "expected-protected-nonce")
	if e != nil || token.Subject != "Opaque:Subject_001" {
		t.Fatal("exact opaque subject positive control failed")
	}
	tests := []struct {
		name   string
		change func(map[string]any)
		signer *rsa.PrivateKey
		nonce  string
	}{
		{"wrong_issuer", func(m map[string]any) { m["iss"] = "https://issuer.iam102.test/realms/other" }, key, "expected-protected-nonce"},
		{"wrong_audience", func(m map[string]any) { m["aud"] = "another-client" }, key, "expected-protected-nonce"},
		{"wrong_signature", func(m map[string]any) {}, other, "expected-protected-nonce"},
		{"wrong_nonce", func(m map[string]any) { m["nonce"] = "other-nonce" }, key, "expected-protected-nonce"},
		{"missing_nonce", func(m map[string]any) { delete(m, "nonce") }, key, "expected-protected-nonce"},
		{"case_alias_nonce", func(m map[string]any) { m["nonce"] = "wrong-nonce"; m["NONCE"] = "expected-protected-nonce" }, key, "expected-protected-nonce"},
		{"uppercase_only_nonce", func(m map[string]any) { delete(m, "nonce"); m["NONCE"] = "expected-protected-nonce" }, key, "expected-protected-nonce"},
		{"case_alias_subject", func(m map[string]any) { m["SUB"] = "substituted" }, key, "expected-protected-nonce"},
		{"case_alias_issuer", func(m map[string]any) { m["ISS"] = m["iss"] }, key, "expected-protected-nonce"},

		{"empty_flow_nonce", func(m map[string]any) {}, key, ""},
		{"wrong_azp", func(m map[string]any) { m["azp"] = "other-client" }, key, "expected-protected-nonce"},
		{"multiple_audiences_without_azp", func(m map[string]any) { m["aud"] = []string{"ownsolid-iam102", "other"}; delete(m, "azp") }, key, "expected-protected-nonce"},
		{"expired", func(m map[string]any) {
			m["iat"] = now.Add(-time.Minute).Unix()
			m["exp"] = now.Add(-time.Second).Unix()
		}, key, "expected-protected-nonce"},
		{"future_iat", func(m map[string]any) { m["iat"] = now.Add(45 * time.Second).Unix() }, key, "expected-protected-nonce"},
		{"future_nbf_inside_library_skew", func(m map[string]any) { m["nbf"] = now.Add(45 * time.Second).Unix() }, key, "expected-protected-nonce"},
		{"missing_iat", func(m map[string]any) { delete(m, "iat") }, key, "expected-protected-nonce"},
		{"empty_subject", func(m map[string]any) { m["sub"] = "" }, key, "expected-protected-nonce"},
		{"non_ascii_subject", func(m map[string]any) { m["sub"] = "opaque-\u00e9" }, key, "expected-protected-nonce"},
		{"numeric_subject", func(m map[string]any) { m["sub"] = 123 }, key, "expected-protected-nonce"},
		{"control_subject", func(m map[string]any) { m["sub"] = "bad\u0000subject" }, key, "expected-protected-nonce"},
		{"oversized_subject", func(m map[string]any) { m["sub"] = strings.Repeat("s", 256) }, key, "expected-protected-nonce"},
		{"distributed_claims", func(m map[string]any) { m["_claim_names"] = map[string]any{"email": "external"} }, key, "expected-protected-nonce"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := rawClaims(now)
			tc.change(m)
			if _, e := a.verify(context.Background(), signed(t, tc.signer, "active", jsonBytes(t, m)), tc.nonce); e == nil {
				t.Fatal("hostile signed claim accepted")
			}
		})
	}
	t.Run("multiple_audiences_with_exact_azp", func(t *testing.T) {
		m := rawClaims(now)
		m["aud"] = []string{"ownsolid-iam102", "other"}
		if _, e := a.verify(context.Background(), signed(t, key, "active", jsonBytes(t, m)), "expected-protected-nonce"); e != nil {
			t.Fatal("authorized multiple audience control failed")
		}
	})
	t.Run("duplicate_claim", func(t *testing.T) {
		payload := strings.TrimSuffix(string(jsonBytes(t, rawClaims(now))), "}") + `,"sub":"substituted"}`
		if _, e := a.verify(context.Background(), signed(t, key, "active", []byte(payload)), "expected-protected-nonce"); e == nil {
			t.Fatal("duplicate signed subject accepted")
		}
	})
	t.Run("duplicate_header", func(t *testing.T) {
		bad := rawSignedHeader(t, key, `{"alg":"RS256","kid":"active","kid":"substituted"}`, jsonBytes(t, rawClaims(now)))
		if _, e := a.verify(context.Background(), bad, "expected-protected-nonce"); e == nil {
			t.Fatal("validly signed duplicate JOSE key accepted")
		}
	})
	for _, header := range []struct{ name, raw string }{
		{"forbidden_algorithm", `{"alg":"HS256","kid":"active"}`},
		{"wrong_token_type", `{"alg":"RS256","kid":"active","typ":"at+jwt"}`},
		{"unapproved_key_url", `{"alg":"RS256","kid":"active","jku":"https://unapproved.test/keys"}`},
		{"unprocessed_critical_extension", `{"alg":"RS256","kid":"active","crit":["extra"],"extra":true}`},
	} {
		t.Run(header.name, func(t *testing.T) {
			if _, e := a.verify(context.Background(), rawSignedHeader(t, key, header.raw, jsonBytes(t, rawClaims(now))), "expected-protected-nonce"); e == nil {
				t.Fatal("signed prohibited JOSE policy accepted")
			}
		})
	}

}
func TestBoundedJWKSRotationAndFlood(t *testing.T) {
	first, second := privateKey(t), privateKey(t)
	var generation atomic.Int64
	var requests atomic.Int64
	var clock atomic.Int64
	clock.Store(time.Now().UTC().Truncate(time.Second).Unix())
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		key, id := first, "first"
		if generation.Load() == 2 {
			key, id = second, "second"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: id, Algorithm: "RS256", Use: "sig"}}})
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	keys := newKeys(client, server.URL, now)
	a := adapter{issuer: "https://issuer.iam102.test/realms/private", client: "ownsolid-iam102", now: now, keys: keys}
	firstToken := signed(t, first, "first", jsonBytes(t, rawClaims(now())))
	if _, e := a.verify(context.Background(), firstToken, "expected-protected-nonce"); e != nil {
		t.Fatal("initial key failed")
	}
	if requests.Load() != 1 {
		t.Fatal("initial key fetch count changed")
	}
	for i := 0; i < 20; i++ {
		raw := signed(t, second, "unknown-"+strings.Repeat("x", i+1), jsonBytes(t, rawClaims(now())))
		if _, e := a.verify(context.Background(), raw, "expected-protected-nonce"); e == nil {
			t.Fatal("unknown key accepted")
		}
	}
	if requests.Load() != 1 {
		t.Fatal("unknown-key flood escaped refresh cooldown")
	}
	generation.Store(2)
	clock.Add(2)
	secondToken := signed(t, second, "second", jsonBytes(t, rawClaims(now())))
	if _, e := a.verify(context.Background(), secondToken, "expected-protected-nonce"); e != nil {
		t.Fatal("rotated key did not refresh")
	}
	if requests.Load() != 2 {
		t.Fatal("key rotation fetch count changed")
	}
	if _, e := a.verify(context.Background(), firstToken, "expected-protected-nonce"); e == nil {
		t.Fatal("removed key accepted after refreshed set")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := a.verify(context.Background(), secondToken, "expected-protected-nonce")
			if e != nil {
				failures <- e
			}
		}()
	}
	wg.Wait()
	close(failures)
	for range failures {
		t.Fatal("parallel cached key verification failed")
	}
	if requests.Load() != 2 {
		t.Fatal("parallel cache made unbounded refreshes")
	}
}
func TestJWKSRefusalAndCancellation(t *testing.T) {
	key := privateKey(t)
	now := time.Now().UTC().Truncate(time.Second)
	raw := signed(t, key, "active", jsonBytes(t, rawClaims(now)))
	cases := []struct {
		name string
		body string
	}{
		{"oversized", strings.Repeat(" ", maxDocument+1)},
		{"duplicate_property", `{"keys":[],"keys":[]}`},
		{"empty_keys", `{"keys":[]}`},
		{"malformed", `{"keys":[`},
		{"excessive_rsa_modulus", `{"keys":[{"kty":"RSA","kid":"active","use":"sig","alg":"RS256","e":"AQAB","n":"` + base64.RawURLEncoding.EncodeToString(append([]byte{1}, make([]byte, 512)...)) + `"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, tc.body)
			}))
			defer s.Close()
			c := s.Client()
			c.Timeout = time.Second
			a := adapter{issuer: "https://issuer.iam102.test/realms/private", client: "ownsolid-iam102", now: func() time.Time { return now }, keys: newKeys(c, s.URL, func() time.Time { return now })}
			if _, e := a.verify(context.Background(), raw, "expected-protected-nonce"); e == nil {
				t.Fatal("invalid JWKS released identity")
			}
		})
	}
	t.Run("cancel_actual_request", func(t *testing.T) {
		entered := make(chan struct{})
		finished := make(chan struct{})
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(finished) }))
		defer s.Close()
		c := s.Client()
		c.Timeout = 2 * time.Second
		k := newKeys(c, s.URL, func() time.Time { return now })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, e := k.VerifySignature(ctx, raw); done <- e }()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("request did not enter")
		}
		cancel()
		select {
		case e := <-done:
			if e == nil {
				t.Fatal("canceled verification passed")
			}
		case <-time.After(time.Second):
			t.Fatal("cancellation did not stop verification")
		}
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("request survived caller cancellation")
		}
	})
}
func TestDiscoveryBoundaries(t *testing.T) {
	for _, tc := range []string{"wrong_issuer", "foreign_jwks", "query_endpoint", "empty_query", "dot_segments", "encoded_path", "oversized", "duplicate_issuer", "redirect"} {
		t.Run(tc, func(t *testing.T) {
			var requests atomic.Int64
			var s *httptest.Server
			s = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				issuer := s.URL + "/realms/private"
				m := metadata{Issuer: issuer, Authorization: issuer + "/auth", Token: issuer + "/token", JWKS: issuer + "/keys", UserInfo: issuer + "/userinfo"}
				w.Header().Set("Content-Type", "application/json")
				switch tc {
				case "wrong_issuer":
					m.Issuer += "-other"
				case "foreign_jwks":
					m.JWKS = "https://unapproved.example.test/keys"
				case "query_endpoint":
					m.JWKS += "?secret=unapproved"
				case "empty_query":
					m.JWKS += "?"
				case "dot_segments":
					m.JWKS = issuer + "/../keys"
				case "encoded_path":
					m.JWKS = issuer + "/%2fkeys"
				case "oversized":
					io.WriteString(w, strings.Repeat(" ", maxDocument+1))
					return
				case "duplicate_issuer":
					b := strings.TrimSuffix(string(jsonBytes(t, m)), "}") + `,"issuer":"https://substituted.test"}`
					io.WriteString(w, b)
					return
				case "redirect":
					w.Header().Set("Location", "https://unapproved.example.test/keys")
					w.WriteHeader(302)
					return
				}
				json.NewEncoder(w).Encode(m)
			}))
			defer s.Close()
			c := s.Client()
			c.Timeout = time.Second
			c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			if _, e := discover(context.Background(), c, s.URL+"/realms/private"); e == nil {
				t.Fatal("malicious discovery accepted")
			}
			if requests.Load() != 1 {
				t.Fatal("discovery followed a provider-selected destination")
			}
		})
	}
}

func rawSignedHeader(t *testing.T, key *rsa.PrivateKey, header string, payload []byte) string {
	t.Helper()
	input := base64.RawURLEncoding.EncodeToString([]byte(header)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(input))
	signature, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if e != nil {
		t.Fatal("raw fixture signing failed")
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}
func TestJWKSKeyStructurePolicy(t *testing.T) {
	strong := privateKey(t)
	weak, e := rsa.GenerateKey(rand.Reader, 1024)
	if e != nil {
		t.Fatal("weak fixture key generation failed")
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, name := range []string{"weak_key", "excessive_key_count", "duplicate_key_id", "wrong_key_algorithm", "encryption_key"} {
		t.Run(name, func(t *testing.T) {
			signer := strong
			if name == "weak_key" {
				signer = weak
			}
			set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &signer.PublicKey, KeyID: "active", Algorithm: "RS256", Use: "sig"}}}
			switch name {
			case "excessive_key_count":
				for i := 0; i < 16; i++ {
					set.Keys = append(set.Keys, jose.JSONWebKey{Key: &strong.PublicKey, KeyID: "another-" + strings.Repeat("k", i+1), Algorithm: "RS256", Use: "sig"})
				}
			case "duplicate_key_id":
				set.Keys = append(set.Keys, set.Keys[0])
			case "wrong_key_algorithm":
				set.Keys[0].Algorithm = "RS512"
			case "encryption_key":
				set.Keys[0].Use = "enc"
			}
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(set)
			}))
			defer s.Close()
			client := s.Client()
			client.Timeout = time.Second
			a := adapter{issuer: "https://issuer.iam102.test/realms/private", client: "ownsolid-iam102", now: func() time.Time { return now }, keys: newKeys(client, s.URL, func() time.Time { return now })}
			raw := signed(t, signer, "active", jsonBytes(t, rawClaims(now)))
			if _, e := a.verify(context.Background(), raw, "expected-protected-nonce"); e == nil {
				t.Fatal("invalid JWK structure accepted")
			}
		})
	}
}
func TestDiscoveryPreservesLogicalIssuer(t *testing.T) {
	for _, issuerPath := range []string{"", "/", "/realms/private/"} {
		t.Run("issuer_"+strings.ReplaceAll(issuerPath, "/", "_"), func(t *testing.T) {
			var requests atomic.Int64
			var s *httptest.Server
			s = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				issuer := s.URL + issuerPath
				if r.URL.Path != strings.TrimSuffix(issuerPath, "/")+"/.well-known/openid-configuration" {
					t.Error("discovery location did not follow the configured issuer")
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(metadata{Issuer: issuer, Authorization: s.URL + "/oauth/authorize", Token: s.URL + "/oauth/token", JWKS: s.URL + "/oauth/keys", UserInfo: s.URL + "/oauth/userinfo"})
			}))
			defer s.Close()
			client := s.Client()
			client.Timeout = time.Second
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			m, e := discover(context.Background(), client, s.URL+issuerPath)
			if e != nil || m.Issuer != s.URL+issuerPath {
				t.Fatal("valid root/trailing-slash issuer or sibling endpoint paths rejected")
			}
			if requests.Load() != 1 {
				t.Fatal("metadata caused another destination request")
			}
		})
	}
}
func TestSameKeyIDRotationAndKnownIDFlood(t *testing.T) {
	first, second := privateKey(t), privateKey(t)
	var generation atomic.Int64
	var requests atomic.Int64
	var clock atomic.Int64
	clock.Store(time.Now().UTC().Truncate(time.Second).Unix())
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		key := first
		if generation.Load() == 2 {
			key = second
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "stable-id", Algorithm: "RS256", Use: "sig"}}})
	}))
	defer s.Close()
	client := s.Client()
	client.Timeout = time.Second
	a := adapter{issuer: "https://issuer.iam102.test/realms/private", client: "ownsolid-iam102", now: now, keys: newKeys(client, s.URL, now)}
	original := signed(t, first, "stable-id", jsonBytes(t, rawClaims(now())))
	replacement := signed(t, second, "stable-id", jsonBytes(t, rawClaims(now())))
	if _, e := a.verify(context.Background(), original, "expected-protected-nonce"); e != nil {
		t.Fatal("initial stable key failed")
	}
	for i := 0; i < 20; i++ {
		if _, e := a.verify(context.Background(), replacement, "expected-protected-nonce"); e == nil {
			t.Fatal("unpublished replacement key accepted")
		}
	}
	if requests.Load() != 1 {
		t.Fatal("known-ID signature flood escaped refresh cooldown")
	}
	generation.Store(2)
	clock.Add(2)
	if _, e := a.verify(context.Background(), replacement, "expected-protected-nonce"); e != nil {
		t.Fatal("same-ID replacement did not refresh within policy")
	}
	if requests.Load() != 2 {
		t.Fatal("replacement required unbounded refresh")
	}
	if _, e := a.verify(context.Background(), original, "expected-protected-nonce"); e == nil {
		t.Fatal("removed same-ID key retained authority")
	}
}

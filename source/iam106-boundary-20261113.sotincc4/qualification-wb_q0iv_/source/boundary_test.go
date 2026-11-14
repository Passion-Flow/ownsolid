package boundary

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/zitadel/oidc/v3/pkg/op"
)

func cp() cryptoPolicy {
	return cryptoPolicy{MaxEncoded: 4096, MaxHeader: 512, MaxPlain: 128, MaxKeys: 3}
}
func rp() requestPolicy {
	return requestPolicy{MaxBody: 4096, MaxCode: 1024, MaxRedirect: 2048, MaxClient: 256, MaxSecret: 4096}
}
func random(t *testing.T) string {
	t.Helper()
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		t.Fatal(e)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func key(t *testing.T, id string) cryptoKey {
	t.Helper()
	k := cryptoKey{ID: id}
	if _, e := rand.Read(k.Value[:]); e != nil {
		t.Fatal(e)
	}
	return k
}
func makeCrypto(t *testing.T, k cryptoKey) *strictCrypto {
	t.Helper()
	c, e := newCrypto(cp(), k.ID, []cryptoKey{k})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func payload(t *testing.T) string { return random(t) + ":" + random(t) }
func headerChange(t *testing.T, token string, change func(map[string]any)) string {
	t.Helper()
	parts := strings.Split(token, ".")
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		t.Fatal(e)
	}
	var h map[string]any
	if json.Unmarshal(raw, &h) != nil {
		t.Fatal("generated header invalid")
	}
	change(h)
	raw, e = json.Marshal(h)
	if e != nil {
		t.Fatal(e)
	}
	parts[0] = base64.RawURLEncoding.EncodeToString(raw)
	return strings.Join(parts, ".")
}

func TestStrictGCMSDKRoundTrip(t *testing.T) {
	k := key(t, "active")
	c := makeCrypto(t, k)
	plain := payload(t)
	token, e := c.Encrypt(plain)
	if e != nil {
		t.Fatal(e)
	}
	upstream := op.NewAES256GCMCrypto(k.Value, k.ID)
	got, e := upstream.Decrypt(token)
	if e != nil || got != plain {
		t.Fatal("strict output not SDK compatible", e)
	}
	token, e = upstream.Encrypt(plain)
	if e != nil {
		t.Fatal(e)
	}
	got, e = c.Decrypt(token)
	if e != nil || got != plain {
		t.Fatal("SDK GCM refused", e)
	}
}
func TestLegacyDefaultFallbackRefused(t *testing.T) {
	k := key(t, "active")
	plain := payload(t)
	legacy, e := op.NewAESCrypto(k.Value).Encrypt(plain)
	if e != nil {
		t.Fatal(e)
	}
	provider, e := op.NewOpenIDProvider("https://op.example.invalid", &op.Config{CryptoKey: k.Value, CryptoKeyId: k.ID}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if got, e := provider.Crypto().Decrypt(legacy); e != nil || got != plain {
		t.Fatal("default compatibility positive control failed", e)
	}
	c := makeCrypto(t, k)
	if got, e := c.Decrypt(legacy); !errors.Is(e, errToken) || got != "" {
		t.Fatal("legacy ciphertext accepted")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(legacy)
	raw[len(raw)-1] ^= 1
	tampered := base64.RawURLEncoding.EncodeToString(raw)
	if got, e := provider.Crypto().Decrypt(tampered); e != nil || got == plain {
		t.Fatal("legacy unauthenticated positive control failed")
	}
	if got, e := c.Decrypt(tampered); !errors.Is(e, errToken) || got != "" {
		t.Fatal("tampered legacy accepted")
	}
	strictProvider, e := op.NewOpenIDProvider("https://op.example.invalid", &op.Config{CryptoKey: k.Value, CryptoKeyId: k.ID}, nil, op.WithCrypto(c))
	if e != nil {
		t.Fatal(e)
	}
	if got, e := strictProvider.Crypto().Decrypt(legacy); e == nil || got != "" {
		t.Fatal("factory retained fallback")
	}
	r := httptest.NewRequest("GET", "https://foreign.example.invalid/.well-known/openid-configuration", nil)
	r.Header.Set("Forwarded", "host=untrusted.example.invalid")
	if strictProvider.IssuerFromRequest(r) != "https://op.example.invalid" {
		t.Fatal("untrusted issuer authority")
	}
}
func TestCompactJWERefusals(t *testing.T) {
	k := key(t, "active")
	c := makeCrypto(t, k)
	token, e := c.Encrypt(payload(t))
	if e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		name string
		edit func(string) string
	}{
		{"unknown_kid", func(s string) string { return headerChange(t, s, func(h map[string]any) { h["kid"] = "unavailable" }) }},
		{"wrong_alg", func(s string) string { return headerChange(t, s, func(h map[string]any) { h["alg"] = "dir" }) }},
		{"wrong_enc", func(s string) string { return headerChange(t, s, func(h map[string]any) { h["enc"] = "A128GCM" }) }},
		{"zip", func(s string) string { return headerChange(t, s, func(h map[string]any) { h["zip"] = "DEF" }) }},
		{"crit", func(s string) string {
			return headerChange(t, s, func(h map[string]any) { h["crit"] = []string{"kid"} })
		}},
		{"remote_key", func(s string) string {
			return headerChange(t, s, func(h map[string]any) { h["jku"] = "https://untrusted.example.invalid/key" })
		}},
		{"missing_kid", func(s string) string { return headerChange(t, s, func(h map[string]any) { delete(h, "kid") }) }},
		{"null_header", func(s string) string { return headerChange(t, s, func(h map[string]any) { h["kid"] = nil }) }},
		{"duplicate_protected_header", func(s string) string {
			parts := strings.Split(s, ".")
			raw, _ := base64.RawURLEncoding.DecodeString(parts[0])
			parts[0] = base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSuffix(string(raw), "}") + `,"kid":"active"}`))
			return strings.Join(parts, ".")
		}},
		{"duplicate_escaped_header", func(s string) string {
			parts := strings.Split(s, ".")
			raw, _ := base64.RawURLEncoding.DecodeString(parts[0])
			parts[0] = base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSuffix(string(raw), "}") + `,"k\u0069d":"active"}`))
			return strings.Join(parts, ".")
		}},
		{"oversize", func(s string) string { return s + strings.Repeat("x", cp().MaxEncoded) }},
		{"padding", func(s string) string {
			parts := strings.Split(s, ".")
			parts[0] += "="
			return strings.Join(parts, ".")
		}},
		{"full_json", func(s string) string { return `{"protected":"` + s + `"}` }},
		{"signed_jwt_shape", func(s string) string { return strings.Join(strings.Split(s, ".")[:3], ".") }},
	}
	for part := 0; part < 5; part++ {
		position := part
		cases = append(cases, struct {
			name string
			edit func(string) string
		}{"tampered_part_" + string(rune('0'+part)), func(s string) string {
			parts := strings.Split(s, ".")
			raw, _ := base64.RawURLEncoding.DecodeString(parts[position])
			raw[len(raw)-1] ^= 1
			parts[position] = base64.RawURLEncoding.EncodeToString(raw)
			return strings.Join(parts, ".")
		}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, e := c.Decrypt(tc.edit(token)); !errors.Is(e, errToken) || got != "" {
				t.Fatal("invalid compact token accepted")
			}
		})
	}
	t.Run("invalid_plaintext_shape", func(t *testing.T) {
		token, e := op.NewAES256GCMCrypto(k.Value, k.ID).Encrypt(random(t) + ":" + random(t) + ":extra")
		if e != nil {
			t.Fatal(e)
		}
		if got, e := c.Decrypt(token); e == nil || got != "" {
			t.Fatal("ambiguous subject accepted")
		}
	})
}
func TestRotationAndConcurrentCrypto(t *testing.T) {
	old, current := key(t, "old"), key(t, "current")
	plain := payload(t)
	oldToken, e := makeCrypto(t, old).Encrypt(plain)
	if e != nil {
		t.Fatal(e)
	}
	rotated, e := newCrypto(cp(), current.ID, []cryptoKey{current, old})
	if e != nil {
		t.Fatal(e)
	}
	if got, e := rotated.Decrypt(oldToken); e != nil || got != plain {
		t.Fatal("retiring key unreadable")
	}
	if got, e := makeCrypto(t, current).Decrypt(oldToken); e == nil || got != "" {
		t.Fatal("retired key retained")
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, e := rotated.Encrypt(plain)
			if e != nil {
				t.Error(e)
				return
			}
			got, e := rotated.Decrypt(token)
			if e != nil || got != plain {
				t.Error("concurrent crypto mismatch")
			}
		}()
	}
	wg.Wait()
}
func TestCryptoConfigurationAndPayloadRefusals(t *testing.T) {
	k := key(t, "active")
	for _, keys := range [][]cryptoKey{nil, {k, k}, {{ID: "active"}}, {k, {ID: "alias", Value: k.Value}}} {
		if c, e := newCrypto(cp(), k.ID, keys); e == nil || c != nil {
			t.Fatal("invalid key configuration accepted")
		}
	}
	c := makeCrypto(t, k)
	for _, plain := range []string{"", random(t), random(t) + ":" + random(t) + ":extra", strings.Repeat("a", 43) + ":" + strings.Repeat("a", 43)} {
		if got, e := c.Encrypt(plain); e == nil || got != "" {
			t.Fatal("invalid plaintext accepted")
		}
	}
}

func request(t *testing.T) (*http.Request, string, string) {
	t.Helper()
	client := "catalog app :+/%"
	secret := random(t) + " :+/%"
	fields := url.Values{"grant_type": {"authorization_code"}, "code": {random(t)}, "redirect_uri": {"https://rp.example.invalid/callback?fixed=registered"}, "code_verifier": {random(t)}}
	r := httptest.NewRequest("POST", "https://op.example.invalid/oauth/token", strings.NewReader(fields.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth(url.QueryEscape(client), url.QueryEscape(secret))
	return r, client, secret
}
func replaceBody(r *http.Request, body string) {
	r.Body = io.NopCloser(strings.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Form = nil
	r.PostForm = nil
}
func TestTokenRequestSDKInteroperability(t *testing.T) {
	r, client, secret := request(t)
	parsed, e := parseToken(r, rp())
	if e != nil || parsed.Client != client || parsed.Secret != secret {
		t.Fatal("strict special-character Basic parse", e)
	}
	provider, e := op.NewOpenIDProvider("https://op.example.invalid", &op.Config{}, nil)
	if e != nil {
		t.Fatal(e)
	}
	upstream, e := op.ParseAccessTokenRequest(r, provider.Decoder())
	if e != nil || upstream.ClientID != client || upstream.ClientSecret != secret || upstream.Code != parsed.Form.Get("code") || upstream.CodeVerifier != parsed.Form.Get("code_verifier") {
		t.Fatal("SDK parsed different credential/form", e)
	}
}
func TestTokenRequestRefusals(t *testing.T) {
	cases := []struct {
		name string
		edit func(*http.Request)
	}{
		{"query_credentials", func(r *http.Request) { r.URL.RawQuery = "client_secret=refused" }},
		{"query_code", func(r *http.Request) { r.URL.RawQuery = "code=refused" }},
		{"empty_query", func(r *http.Request) { r.URL.ForceQuery = true }},
		{"method", func(r *http.Request) { r.Method = "GET" }},
		{"content_type", func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }},
		{"charset", func(r *http.Request) {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=latin1")
		}},
		{"duplicate_authorization", func(r *http.Request) { r.Header.Add("Authorization", r.Header.Get("Authorization")) }},
		{"noncanonical_header_alias", func(r *http.Request) { r.Header["authorization"] = []string{r.Header.Get("Authorization")} }},
		{"no_basic", func(r *http.Request) { r.Header.Del("Authorization") }},
		{"invalid_basic", func(r *http.Request) { r.Header.Set("Authorization", "Basic %") }},
		{"basic_no_separator", func(r *http.Request) {
			r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("no_separator")))
		}},
		{"basic_bad_escape", func(r *http.Request) { r.SetBasicAuth("%", "invalid") }},
		{"basic_control", func(r *http.Request) { r.SetBasicAuth("client", url.QueryEscape("invalid\n")) }},
		{"unsupported_grant", func(r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			v, _ := url.ParseQuery(string(b))
			v.Set("grant_type", "refresh_token")
			replaceBody(r, v.Encode())
		}},
		{"empty_secret", func(r *http.Request) { r.SetBasicAuth("client", "") }},
		{"huge_body", func(r *http.Request) { replaceBody(r, strings.Repeat("x", rp().MaxBody+1)) }},
		{"chunked_huge_body", func(r *http.Request) { replaceBody(r, strings.Repeat("x", rp().MaxBody+1)); r.ContentLength = -1 }},
		{"malformed_form", func(r *http.Request) { replaceBody(r, "code=%") }},
		{"semicolon", func(r *http.Request) { replaceBody(r, "code=one;two") }},
	}
	for _, suffix := range []string{"&code=again", "&co%64e=again", "&client_id=body", "&client_secret=body", "&client_assertion=body", "&unknown=value"} {
		value := suffix
		cases = append(cases, struct {
			name string
			edit func(*http.Request)
		}{"extra_" + strings.Split(suffix, "=")[0], func(r *http.Request) { b, _ := io.ReadAll(r.Body); replaceBody(r, string(b)+value) }})
	}
	for name, value := range map[string]string{"short_verifier": "x", "long_verifier": strings.Repeat("a", 129), "invalid_verifier": strings.Repeat("a", 42) + "+"} {
		label, ver := name, value
		cases = append(cases, struct {
			name string
			edit func(*http.Request)
		}{label, func(r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			v, _ := url.ParseQuery(string(b))
			v.Set("code_verifier", ver)
			replaceBody(r, v.Encode())
		}})
	}
	for name, value := range map[string]string{"plain_callback": "http://rp.example.invalid/callback", "fragment": "https://rp.example.invalid/callback#bad", "userinfo": "https://user@rp.example.invalid/callback"} {
		label, redirect := name, value
		cases = append(cases, struct {
			name string
			edit func(*http.Request)
		}{label, func(r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			v, _ := url.ParseQuery(string(b))
			v.Set("redirect_uri", redirect)
			replaceBody(r, v.Encode())
		}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _, _ := request(t)
			tc.edit(r)
			out, e := parseToken(r, rp())
			if e == nil || out.Client != "" || out.Secret != "" || out.Form != nil {
				t.Fatal("invalid request released parsed credentials")
			}
		})
	}
}
func TestPreauthenticationOrder(t *testing.T) {
	r, client, secret := request(t)
	authCalls, nextCalls := 0, 0
	err := tokenGate(r, rp(), func(ctx context.Context, id, pw string) (string, error) {
		authCalls++
		if id != client || pw != secret {
			t.Fatal("client credential changed")
		}
		return id, nil
	}, func(r *http.Request, id string) error {
		nextCalls++
		if authCalls != 1 || id != client {
			t.Fatal("code owner ran before resolved client")
		}
		return nil
	})
	if err != nil || authCalls != 1 || nextCalls != 1 {
		t.Fatal("valid ordered boundary", err)
	}
	for _, kind := range []string{"invalid_request", "bad_client", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			r, _, _ := request(t)
			authCalls, nextCalls = 0, 0
			if kind == "invalid_request" {
				r.URL.RawQuery = "code=refused"
			}
			if kind == "canceled" {
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
			}
			err := tokenGate(r, rp(), func(context.Context, string, string) (string, error) {
				authCalls++
				return "", errors.New("AUTH_REFUSED")
			}, func(*http.Request, string) error { nextCalls++; return nil })
			if err == nil || nextCalls != 0 || ((kind == "invalid_request" || kind == "canceled") && authCalls != 0) {
				t.Fatal("untrusted request reached code owner")
			}
		})
	}
}
func TestUserInfoHeaderOnlyOpaqueProfile(t *testing.T) {
	k := key(t, "active")
	c := makeCrypto(t, k)
	token, e := c.Encrypt(payload(t))
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"valid", "query", "form", "jwt", "legacy", "duplicate", "missing"} {
		t.Run(kind, func(t *testing.T) {
			r := httptest.NewRequest("GET", "https://op.example.invalid/userinfo", nil)
			r.Header.Set("Authorization", "Bearer "+token)
			switch kind {
			case "query":
				r.URL.RawQuery = "access_token=" + url.QueryEscape(token)
			case "form":
				r.Method = "POST"
				replaceBody(r, "access_token="+url.QueryEscape(token))
			case "jwt":
				r.Header.Set("Authorization", "Bearer "+strings.Join(strings.Split(token, ".")[:3], "."))
			case "legacy":
				v, _ := op.NewAESCrypto(k.Value).Encrypt(payload(t))
				r.Header.Set("Authorization", "Bearer "+v)
			case "duplicate":
				r.Header.Add("Authorization", r.Header.Get("Authorization"))
			case "missing":
				r.Header.Del("Authorization")
			}
			out, e := userInfoToken(r, c)
			if kind == "valid" {
				if e != nil || out != token {
					t.Fatal(e)
				}
			} else if e == nil || out != "" {
				t.Fatal("alternate UserInfo bearer path accepted")
			}
		})
	}
}

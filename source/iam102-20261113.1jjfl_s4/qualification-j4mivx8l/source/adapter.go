package prototype

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
)

var rejected = errors.New("OIDC_PROTOTYPE_REJECTED")

const maxDocument = 128 << 10
const maxToken = 32 << 10

// Objects and arrays are bounded by the document limit; all nested duplicates
// fail before a downstream decoder can apply last-value semantics.
func uniqueJSON(raw []byte) error {
	if len(raw) == 0 || len(raw) > maxDocument {
		return rejected
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 16 {
			return rejected
		}
		token, e := d.Token()
		if e != nil {
			return rejected
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			names := map[string]bool{}
			for d.More() {
				name, e := d.Token()
				if e != nil {
					return rejected
				}
				key, ok := name.(string)
				if !ok || names[key] || len(names) >= 128 {
					return rejected
				}
				names[key] = true
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
			token, e = d.Token()
			if e != nil || token != json.Delim('}') {
				return rejected
			}
		case '[':
			n := 0
			for d.More() {
				n++
				if n > 128 {
					return rejected
				}
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
			token, e = d.Token()
			if e != nil || token != json.Delim(']') {
				return rejected
			}
		default:
			return rejected
		}
		return nil
	}
	if e := walk(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return rejected
	}
	return nil
}

func compact(raw string) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maxToken {
		return nil, nil, rejected
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return nil, nil, rejected
		}
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, nil, rejected
	}
	var objects [2]map[string]json.RawMessage
	for i := 0; i < 2; i++ {
		data, e := base64.RawURLEncoding.Strict().DecodeString(parts[i])
		if e != nil || uniqueJSON(data) != nil {
			return nil, nil, rejected
		}
		if json.Unmarshal(data, &objects[i]) != nil || objects[i] == nil {
			return nil, nil, rejected
		}
	}
	return objects[0], objects[1], nil
}
func text(m map[string]json.RawMessage, k string) (string, bool) {
	var s string
	e := json.Unmarshal(m[k], &s)
	return s, e == nil
}
func subject(s string) bool {
	if len(s) == 0 || len(s) > 255 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
func opaque(s string, n int) bool {
	return len(s) > 0 && len(s) <= n && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

type boundedKeys struct {
	client                *http.Client
	endpoint              string
	now                   func() time.Time
	mu                    sync.Mutex
	gate                  chan struct{}
	keys                  map[string]jose.JSONWebKey
	expires, refreshAfter time.Time
}

func newKeys(c *http.Client, endpoint string, now func() time.Time) *boundedKeys {
	return &boundedKeys{client: c, endpoint: endpoint, now: now, gate: make(chan struct{}, 1), keys: map[string]jose.JSONWebKey{}}
}
func (k *boundedKeys) cached(id string) (jose.JSONWebKey, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.keys[id]
	return v, ok && k.now().Before(k.expires)
}
func (k *boundedKeys) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	head, _, e := compact(raw)
	if e != nil {
		return nil, rejected
	}
	id, ok := text(head, "kid")
	if !ok || !opaque(id, 128) || ctx.Err() != nil {
		return nil, rejected
	}
	signed, e := jose.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
	if e != nil || len(signed.Signatures) != 1 {
		return nil, rejected
	}
	verify := func() ([]byte, bool) {
		key, ok := k.cached(id)
		if !ok {
			return nil, false
		}
		body, e := signed.Verify(key.Key)
		return body, e == nil
	}
	if body, ok := verify(); ok {
		return body, nil
	}
	select {
	case k.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, rejected
	}
	defer func() { <-k.gate }()
	// Another caller may have refreshed while this caller was waiting.
	if body, ok := verify(); ok {
		return body, nil
	}
	k.mu.Lock()
	allowed := !k.now().Before(k.refreshAfter)
	if allowed {
		k.refreshAfter = k.now().Add(time.Second)
	}
	k.mu.Unlock()
	if !allowed {
		return nil, rejected
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, k.endpoint, nil)
	if e != nil {
		return nil, rejected
	}
	req.Header.Set("Accept", "application/json")
	response, e := k.client.Do(req)
	if e != nil {
		return nil, rejected
	}
	defer response.Body.Close()
	if !jsonResponse(response) {
		return nil, rejected
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, maxDocument+1))
	if e != nil || uniqueJSON(body) != nil {
		return nil, rejected
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil || aliases(envelope, "keys") {
		return nil, rejected
	}
	var rawKeys []map[string]json.RawMessage
	if json.Unmarshal(envelope["keys"], &rawKeys) != nil || len(rawKeys) == 0 || len(rawKeys) > 16 {
		return nil, rejected
	}
	for _, fields := range rawKeys {
		if aliases(fields, "kty", "kid", "alg", "use", "n", "e") {
			return nil, rejected
		}
	}
	var set struct {
		Keys []jose.JSONWebKey `json:"keys"`
	}
	if json.Unmarshal(body, &set) != nil || len(set.Keys) == 0 || len(set.Keys) > 16 {
		return nil, rejected
	}
	next := map[string]jose.JSONWebKey{}
	for _, v := range set.Keys {
		if v.Use != "sig" || v.Algorithm != "RS256" {
			continue
		}
		pub, ok := v.Key.(*rsa.PublicKey)
		if !ok || pub.N.BitLen() < 2048 || pub.N.BitLen() > 4096 || !opaque(v.KeyID, 128) || next[v.KeyID].Key != nil {
			return nil, rejected
		}
		next[v.KeyID] = v
	}
	if len(next) == 0 {
		return nil, rejected
	}
	k.mu.Lock()
	k.keys = next
	k.expires = k.now().Add(5 * time.Minute)
	k.mu.Unlock()
	if body, ok := verify(); ok {
		return body, nil
	}
	return nil, rejected
}

type adapter struct {
	issuer, client string
	keys           oidc.KeySet
	now            func() time.Time
}

func (a adapter) verify(ctx context.Context, raw, nonce string) (*oidc.IDToken, error) {
	head, claims, e := compact(raw)
	if e != nil {
		return nil, rejected
	}
	if aliases(head, "alg", "kid", "typ", "crit", "jku", "x5u") || aliases(claims, "iss", "sub", "aud", "exp", "iat", "nbf", "nonce", "azp", "_claim_names", "_claim_sources") {
		return nil, rejected
	}
	alg, _ := text(head, "alg")
	kid, _ := text(head, "kid")
	if alg != "RS256" || !opaque(kid, 128) || !opaque(nonce, 128) {
		return nil, rejected
	}
	if typ, present := head["typ"]; present {
		var value string
		if json.Unmarshal(typ, &value) != nil || value != "JWT" {
			return nil, rejected
		}
	}
	if _, ok := head["crit"]; ok {
		return nil, rejected
	}
	if _, ok := head["jku"]; ok {
		return nil, rejected
	}
	if _, ok := head["x5u"]; ok {
		return nil, rejected
	}
	if _, ok := claims["_claim_names"]; ok {
		return nil, rejected
	}
	token, e := oidc.NewVerifier(a.issuer, a.keys, &oidc.Config{ClientID: a.client, SupportedSigningAlgs: []string{"RS256"}, Now: a.now}).Verify(ctx, raw)
	if e != nil {
		return nil, rejected
	}
	if token.Issuer != a.issuer || !subject(token.Subject) || subtle.ConstantTimeCompare([]byte(token.Nonce), []byte(nonce)) != 1 {
		return nil, rejected
	}
	azp, present := claims["azp"]
	if len(token.Audience) > 1 && !present {
		return nil, rejected
	}
	if present {
		var party string
		if json.Unmarshal(azp, &party) != nil || party != a.client {
			return nil, rejected
		}
	}
	var issued, expires int64
	if json.Unmarshal(claims["iat"], &issued) != nil || json.Unmarshal(claims["exp"], &expires) != nil || issued <= 0 || expires <= issued || issued > a.now().Add(30*time.Second).Unix() || expires <= a.now().Unix() {
		return nil, rejected
	}
	if raw, ok := claims["nbf"]; ok {
		var before int64
		if json.Unmarshal(raw, &before) != nil || before > a.now().Add(30*time.Second).Unix() || before >= expires {
			return nil, rejected
		}
	}
	if ctx.Err() != nil {
		return nil, rejected
	}
	return token, nil
}

type metadata struct {
	Issuer        string `json:"issuer"`
	Authorization string `json:"authorization_endpoint"`
	Token         string `json:"token_endpoint"`
	JWKS          string `json:"jwks_uri"`
	UserInfo      string `json:"userinfo_endpoint"`
}

func discover(ctx context.Context, client *http.Client, issuer string) (metadata, error) {
	var out metadata
	u, e := url.Parse(issuer)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || !canonicalPath(u.Path) || strings.ContainsAny(issuer, "\\%") {
		return out, rejected
	}
	req, e := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(issuer, "/")+"/.well-known/openid-configuration", nil)
	if e != nil {
		return out, rejected
	}
	response, e := client.Do(req)
	if e != nil {
		return out, rejected
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, maxDocument+1))
	if e != nil || !jsonResponse(response) || uniqueJSON(body) != nil {
		return out, rejected
	}
	if json.Unmarshal(body, &out) != nil || out.Issuer != issuer {
		return metadata{}, rejected
	}
	for _, endpoint := range []string{out.Authorization, out.Token, out.JWKS, out.UserInfo} {
		v, e := url.Parse(endpoint)
		if e != nil || v.Scheme != "https" || v.Host != u.Host || v.User != nil || v.Fragment != "" || v.RawQuery != "" || v.ForceQuery || v.RawPath != "" || !canonicalPath(v.Path) || strings.ContainsAny(endpoint, "\\%") {
			return metadata{}, rejected
		}
	}
	return out, nil
}

func jsonResponse(r *http.Response) bool {
	if r.StatusCode != 200 || r.Header.Get("Content-Encoding") != "" || len(r.Header.Values("Content-Type")) != 1 {
		return false
	}
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return e == nil && media == "application/json" && (len(params) == 0 || len(params) == 1 && strings.EqualFold(params["charset"], "utf-8"))
}

func canonicalPath(value string) bool {
	if value == "" || value == "/" {
		return true
	}
	clean := path.Clean(value)
	return value == clean || value == clean+"/"
}

func aliases(fields map[string]json.RawMessage, names ...string) bool {
	for key := range fields {
		for _, name := range names {
			if key != name && strings.EqualFold(key, name) {
				return true
			}
		}
	}
	return false
}

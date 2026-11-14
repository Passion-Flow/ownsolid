package boundary

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/zitadel/oidc/v3/pkg/op"
)

var errToken = errors.New("OP_TOKEN_REFUSED")
var errConfig = errors.New("OP_CONFIGURATION_INVALID")
var keyID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type cryptoPolicy struct{ MaxEncoded, MaxHeader, MaxPlain, MaxKeys int }
type cryptoKey struct {
	ID    string
	Value [32]byte
}
type strictCrypto struct {
	policy cryptoPolicy
	active string
	keys   map[string]op.Crypto
	valid  func(string) bool
}

func newCrypto(p cryptoPolicy, active string, keys []cryptoKey) (*strictCrypto, error) {
	if p.MaxEncoded < 1024 || p.MaxEncoded > 16384 || p.MaxHeader < 128 || p.MaxHeader > 1024 || p.MaxPlain < 87 || p.MaxPlain > 1024 || p.MaxKeys < 1 || p.MaxKeys > 3 || len(keys) < 1 || len(keys) > p.MaxKeys || !keyID.MatchString(active) {
		return nil, errConfig
	}
	c := &strictCrypto{policy: p, active: active, keys: map[string]op.Crypto{}, valid: validPayload}
	for i, k := range keys {
		if !keyID.MatchString(k.ID) || c.keys[k.ID] != nil || subtle.ConstantTimeCompare(k.Value[:], make([]byte, 32)) == 1 {
			return nil, errConfig
		}
		for _, prior := range keys[:i] {
			if subtle.ConstantTimeCompare(prior.Value[:], k.Value[:]) == 1 {
				return nil, errConfig
			}
		}
		c.keys[k.ID] = op.NewAES256GCMCrypto(k.Value, k.ID)
	}
	if c.keys[active] == nil {
		return nil, errConfig
	}
	return c, nil
}
func canonicalURL(s string) ([]byte, error) {
	b, e := base64.RawURLEncoding.Strict().DecodeString(s)
	if e != nil || base64.RawURLEncoding.EncodeToString(b) != s {
		return nil, errToken
	}
	return b, nil
}
func validPayload(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if len(part) != 43 {
			return false
		}
		b, e := canonicalURL(part)
		if e != nil || len(b) != 32 {
			return false
		}
	}
	return true
}

func (c *strictCrypto) compact(s string) (op.Crypto, error) {
	if c == nil || len(s) > c.policy.MaxEncoded {
		return nil, errToken
	}
	parts := strings.Split(s, ".")
	if len(parts) != 5 {
		return nil, errToken
	}
	decoded := make([][]byte, 5)
	for i, part := range parts {
		b, e := canonicalURL(part)
		if e != nil {
			return nil, errToken
		}
		decoded[i] = b
	}
	if len(decoded[0]) > c.policy.MaxHeader || len(decoded[1]) != 32 || len(decoded[2]) != 12 || len(decoded[3]) < 1 || len(decoded[3]) > c.policy.MaxPlain || len(decoded[4]) != 16 {
		return nil, errToken
	}
	d := json.NewDecoder(strings.NewReader(string(decoded[0])))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return nil, errToken
	}
	headers := map[string]string{}
	for d.More() {
		token, e = d.Token()
		if e != nil {
			return nil, errToken
		}
		name, ok := token.(string)
		if !ok || len(headers) >= 5 {
			return nil, errToken
		}
		if _, seen := headers[name]; seen {
			return nil, errToken
		}
		switch name {
		case "alg", "enc", "kid", "iv", "tag":
		default:
			return nil, errToken
		}
		var v string
		if d.Decode(&v) != nil || v == "" {
			return nil, errToken
		}
		headers[name] = v
	}
	if token, e = d.Token(); e != nil || token != json.Delim('}') {
		return nil, errToken
	}
	if d.Decode(new(any)) != io.EOF || len(headers) != 5 || headers["alg"] != "A256GCMKW" || headers["enc"] != "A256GCM" {
		return nil, errToken
	}
	for name, size := range map[string]int{"iv": 12, "tag": 16} {
		b, e := canonicalURL(headers[name])
		if e != nil || len(b) != size {
			return nil, errToken
		}
	}
	key := c.keys[headers["kid"]]
	if key == nil {
		return nil, errToken
	}
	return key, nil
}
func (c *strictCrypto) Encrypt(plain string) (string, error) {
	if c == nil || c.valid == nil || !c.valid(plain) || len(plain) > c.policy.MaxPlain {
		return "", errToken
	}
	out, e := c.keys[c.active].Encrypt(plain)
	if e != nil {
		return "", errToken
	}
	if _, e = c.compact(out); e != nil {
		return "", errToken
	}
	return out, nil
}
func (c *strictCrypto) Decrypt(s string) (string, error) {
	key, e := c.compact(s)
	if e != nil {
		return "", errToken
	}
	plain, e := key.Decrypt(s)
	if e != nil || c.valid == nil || len(plain) > c.policy.MaxPlain || !c.valid(plain) {
		return "", errToken
	}
	return plain, nil
}

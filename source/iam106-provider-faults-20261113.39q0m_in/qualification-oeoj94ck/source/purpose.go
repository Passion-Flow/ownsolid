package boundary

import (
	"crypto/subtle"
	"strings"
)

type providerCrypto struct{ code, bearer *strictCrypto }

func validRequestID(id string) bool {
	if !strings.HasPrefix(id, "ar_") || len(id) != 46 {
		return false
	}
	raw, err := canonicalURL(strings.TrimPrefix(id, "ar_"))
	return err == nil && len(raw) == 32
}

func newProviderCrypto(p cryptoPolicy, codeActive string, codeKeys []cryptoKey, bearerActive string, bearerKeys []cryptoKey) (*providerCrypto, error) {
	for _, a := range codeKeys {
		for _, b := range bearerKeys {
			if a.ID == b.ID || subtle.ConstantTimeCompare(a.Value[:], b.Value[:]) == 1 {
				return nil, errConfig
			}
		}
	}
	code, err := newCrypto(p, codeActive, codeKeys)
	if err != nil {
		return nil, err
	}
	bearer, err := newCrypto(p, bearerActive, bearerKeys)
	if err != nil {
		return nil, err
	}
	code.valid = validRequestID
	return &providerCrypto{code: code, bearer: bearer}, nil
}
func (c *providerCrypto) Encrypt(plain string) (string, error) {
	if c == nil {
		return "", errToken
	}
	if validRequestID(plain) {
		return c.code.Encrypt(plain)
	}
	if validPayload(plain) {
		return c.bearer.Encrypt(plain)
	}
	return "", errToken
}

// The SDK looks up a received code's digest in storage. Incoming decryption has
// no code purpose; it can return only the opaque access-bearer payload.
func (c *providerCrypto) Decrypt(token string) (string, error) {
	if c == nil {
		return "", errToken
	}
	return c.bearer.Decrypt(token)
}

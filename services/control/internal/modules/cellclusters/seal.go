package cellclusters

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

// sealInfo binds the derived key to this one purpose, so the key is never used for
// anything else.
const sealInfo = "ownsolid/cell-cluster-join-token/v1"

// sealer protects join tokens at rest. The key is derived from a secret file outside the
// database, so a copy of the database alone does not reveal a token. A token is short
// lived: if the secret is replaced, the cluster asks its server for a new one.
type sealer struct{ aead cipher.AEAD }

func newSealer(secret []byte) (sealer, error) {
	if len(secret) < 32 {
		return sealer{}, errors.New("KUBERNETES_CELLS_SECRET_INVALID")
	}
	key, e := hkdf.Key(sha256.New, secret, nil, sealInfo, 32)
	if e != nil {
		return sealer{}, errors.New("KUBERNETES_CELLS_SECRET_INVALID")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return sealer{}, errors.New("KUBERNETES_CELLS_SECRET_INVALID")
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return sealer{}, errors.New("KUBERNETES_CELLS_SECRET_INVALID")
	}
	return sealer{aead: aead}, nil
}

// seal encrypts token for cluster: the nonce comes first, and the cluster ID is the
// associated data, so a sealed token moved to another cluster's row does not open.
func (s sealer) seal(cluster, token string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, e := rand.Read(nonce); e != nil {
		return nil, errors.New("KUBERNETES_JOIN_TOKEN_UNAVAILABLE")
	}
	return s.aead.Seal(nonce, nonce, []byte(token), []byte(cluster)), nil
}

func (s sealer) open(cluster string, sealed []byte) (string, error) {
	size := s.aead.NonceSize()
	if len(sealed) <= size {
		return "", errors.New("KUBERNETES_JOIN_TOKEN_UNAVAILABLE")
	}
	plain, e := s.aead.Open(nil, sealed[:size], sealed[size:], []byte(cluster))
	if e != nil {
		return "", errors.New("KUBERNETES_JOIN_TOKEN_UNAVAILABLE")
	}
	return string(plain), nil
}

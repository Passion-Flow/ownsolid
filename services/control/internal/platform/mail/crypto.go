package mail

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

type Keyring struct {
	Active  string            `json:"active"`
	Keys    map[string]string `json:"keys"`
	decoded map[string][]byte
}

func LoadKeyring(path string) (*Keyring, error) {
	b, e := config.Secret(path)
	if e != nil {
		return nil, e
	}
	var k Keyring
	if e = json.Unmarshal(b, &k); e != nil {
		return nil, errors.New("MAIL_KEYRING_INVALID")
	}
	k.decoded = map[string][]byte{}
	if len(k.Keys) < 1 || len(k.Keys) > 8 {
		return nil, errors.New("MAIL_KEYRING_INVALID")
	}
	for id, v := range k.Keys {
		raw, e := base64.StdEncoding.DecodeString(v)
		if e != nil || len(raw) != 32 || len(id) < 1 || len(id) > 64 {
			return nil, errors.New("MAIL_KEYRING_INVALID")
		}
		k.decoded[id] = raw
	}
	if k.decoded[k.Active] == nil {
		return nil, errors.New("MAIL_ACTIVE_KEY_MISSING")
	}
	return &k, nil
}
func (k *Keyring) aead(id string) (cipher.AEAD, error) {
	key := k.decoded[id]
	if key == nil {
		return nil, errors.New("mail_key_missing")
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func (k *Keyring) Encrypt(id, purpose string, b []byte) (string, []byte, []byte, error) {
	a, e := k.aead(k.Active)
	if e != nil {
		return "", nil, nil, e
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return "", nil, nil, e
	}
	return k.Active, nonce, a.Seal(nil, nonce, b, []byte(id+":"+purpose+":1")), nil
}
func (k *Keyring) Decrypt(key, id, purpose string, nonce, b []byte) ([]byte, error) {
	a, e := k.aead(key)
	if e != nil {
		return nil, e
	}
	if len(nonce) != a.NonceSize() {
		return nil, errors.New("mail_ciphertext_invalid")
	}
	out, e := a.Open(nil, nonce, b, []byte(id+":"+purpose+":1"))
	if e != nil {
		return nil, errors.New("mail_ciphertext_invalid")
	}
	return out, nil
}

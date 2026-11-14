package sso

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	keyringSchemaVersion = 1
	maximumKeyringBytes  = 65536
	maximumRetainedKeys  = 8
	maximumSecretBytes   = 4096
	clientSecretPurpose  = "connector_client_secret"
	flowVerifierPurpose  = "flow_pkce_verifier"
)

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var errEncryptedState = errors.New("SSO_ENCRYPTED_STATE_INVALID")

// Keyring keeps decoded key material private to the owning SSO module.
type Keyring struct {
	active string
	keys   map[string][32]byte
}

type secretScope struct{ tenant, connector, record, purpose string }
type sealedSecret struct {
	keyID             string
	nonce, ciphertext []byte
}

func canonicalID(value string) bool {
	id, e := uuid.Parse(value)
	return e == nil && id != uuid.Nil && id.String() == value
}

func (s secretScope) associatedData() ([]byte, error) {
	if !canonicalID(s.tenant) || !canonicalID(s.connector) || !canonicalID(s.record) || s.purpose != clientSecretPurpose && s.purpose != flowVerifierPurpose || s.purpose == clientSecretPurpose && s.record != s.connector {
		return nil, errors.New("SSO_SECRET_SCOPE_INVALID")
	}
	// A JSON array gives unambiguous field boundaries and an explicit schema/domain discriminator.
	return json.Marshal([]any{"projectone-sso", keyringSchemaVersion, s.tenant, s.connector, s.record, s.purpose})
}

// objectFields is exact-case and duplicate-aware. Unknown fields are checked by its owning protocol.
func objectFields(raw []byte, maximum int) (map[string]json.RawMessage, error) {
	bad := errors.New("SSO_JSON_INVALID")
	if !utf8.Valid(raw) {
		return nil, bad
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return nil, bad
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, e := d.Token()
		if e != nil {
			return nil, bad
		}
		name, ok := token.(string)
		if !ok || len(fields) >= maximum {
			return nil, bad
		}
		if _, found := fields[name]; found {
			return nil, bad
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, bad
		}
		fields[name] = value
	}
	if token, e = d.Token(); e != nil || token != json.Delim('}') {
		return nil, bad
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, bad
	}
	return fields, nil
}

func LoadKeyring(path string) (*Keyring, error) {
	ring, e := loadKeyring(path)
	if errors.Is(e, errPrivateFileAbsent) {
		return nil, fail("SSO_KEYRING_INVALID")
	}
	return ring, e
}

func loadKeyring(path string) (*Keyring, error) {
	bad := errors.New("SSO_KEYRING_INVALID")
	raw, e := readPrivateFile(path, maximumKeyringBytes)
	if errors.Is(e, errPrivateFileAbsent) {
		return nil, e
	}
	if e != nil {
		return nil, bad
	}
	defer clear(raw)
	fields, e := objectFields(raw, 3)
	if e != nil || len(fields) != 3 {
		return nil, bad
	}
	var version int
	var active string
	if json.Unmarshal(fields["schema_version"], &version) != nil || version != keyringSchemaVersion || json.Unmarshal(fields["active_key_id"], &active) != nil || !keyIDPattern.MatchString(active) {
		return nil, bad
	}
	encoded, e := objectFields(fields["keys"], maximumRetainedKeys)
	if e != nil || len(encoded) == 0 {
		return nil, bad
	}
	k := &Keyring{active: active, keys: map[string][32]byte{}}
	for id, raw := range encoded {
		var value string
		if !keyIDPattern.MatchString(id) || json.Unmarshal(raw, &value) != nil {
			return nil, bad
		}
		decoded, e := base64.StdEncoding.Strict().DecodeString(value)
		if e != nil || len(decoded) != 32 || base64.StdEncoding.EncodeToString(decoded) != value {
			return nil, bad
		}
		var key [32]byte
		copy(key[:], decoded)
		clear(decoded)
		k.keys[id] = key
	}
	if _, found := k.keys[active]; !found {
		return nil, bad
	}
	return k, nil
}

func (k *Keyring) aead(id string) (cipher.AEAD, error) {
	bad := errors.New("SSO_ENCRYPTED_STATE_INVALID")
	if k == nil {
		return nil, bad
	}
	key, found := k.keys[id]
	if !found {
		return nil, bad
	}
	block, e := aes.NewCipher(key[:])
	if e != nil {
		return nil, bad
	}
	return cipher.NewGCM(block)
}

func (k *Keyring) seal(scope secretScope, plaintext []byte) (sealedSecret, error) {
	var out sealedSecret
	if k == nil || len(plaintext) == 0 || len(plaintext) > maximumSecretBytes {
		return out, errors.New("SSO_ENCRYPTED_STATE_INVALID")
	}
	associated, e := scope.associatedData()
	if e != nil {
		return out, e
	}
	a, e := k.aead(k.active)
	if e != nil {
		return out, e
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return out, errors.New("SSO_ENTROPY_UNAVAILABLE")
	}
	return sealedSecret{keyID: k.active, nonce: nonce, ciphertext: a.Seal(nil, nonce, plaintext, associated)}, nil
}

func (k *Keyring) open(scope secretScope, value sealedSecret) ([]byte, error) {
	associated, e := scope.associatedData()
	if e != nil {
		return nil, e
	}
	a, e := k.aead(value.keyID)
	if e != nil || len(value.nonce) != a.NonceSize() || len(value.ciphertext) <= a.Overhead() || len(value.ciphertext) > maximumSecretBytes+a.Overhead() {
		return nil, errEncryptedState
	}
	plaintext, e := a.Open(nil, value.nonce, value.ciphertext, associated)
	if e != nil {
		return nil, errEncryptedState
	}
	return plaintext, nil
}

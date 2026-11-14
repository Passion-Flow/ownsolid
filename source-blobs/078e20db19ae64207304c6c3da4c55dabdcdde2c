package sso

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func fixtureKey(t *testing.T) string {
	t.Helper()
	var raw [32]byte
	if _, e := rand.Read(raw[:]); e != nil {
		t.Fatal(e)
	}
	return base64.StdEncoding.EncodeToString(raw[:])
}

func fixtureKeyring(t *testing.T, active string, keys map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(map[string]any{"schema_version": 1, "active_key_id": active, "keys": keys})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "sso-keyring.json")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	return path
}

func TestM11ActualScopedEncryptionTamperingRotationAndNoPublicSerialization(t *testing.T) {
	first, second := fixtureKey(t), fixtureKey(t)
	k, e := LoadKeyring(fixtureKeyring(t, "first", map[string]string{"first": first}))
	if e != nil {
		t.Fatal(e)
	}
	connector := uuid.NewString()
	scope := secretScope{tenant: uuid.NewString(), connector: connector, record: connector, purpose: clientSecretPurpose}
	plaintext := []byte("Synthetic transient OAuth credential")
	a, e := k.seal(scope, plaintext)
	if e != nil {
		t.Fatal(e)
	}
	b, e := k.seal(scope, plaintext)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Equal(a.nonce, b.nonce) || bytes.Equal(a.ciphertext, b.ciphertext) || bytes.Contains(a.ciphertext, plaintext) {
		t.Fatal("encryption failed nonce/confidentiality assertion")
	}
	opened, e := k.open(scope, a)
	if e != nil || !bytes.Equal(opened, plaintext) {
		t.Fatal("actual authenticated roundtrip", e)
	}
	for _, changed := range []secretScope{{tenant: uuid.NewString(), connector: connector, record: connector, purpose: clientSecretPurpose}, {tenant: scope.tenant, connector: uuid.NewString(), record: uuid.NewString(), purpose: flowVerifierPurpose}, {tenant: scope.tenant, connector: connector, record: uuid.NewString(), purpose: flowVerifierPurpose}, {tenant: scope.tenant, connector: connector, record: connector, purpose: flowVerifierPurpose}} {
		if value, e := k.open(changed, a); e == nil || value != nil {
			t.Fatal("foreign tenant/connector/record/purpose decrypted")
		}
	}
	for _, mutate := range []func(*sealedSecret){func(v *sealedSecret) { v.nonce = nil }, func(v *sealedSecret) { v.ciphertext = append([]byte(nil), v.ciphertext...); v.ciphertext[0] ^= 1 }, func(v *sealedSecret) { v.keyID = "missing" }} {
		bad := a
		mutate(&bad)
		if value, e := k.open(scope, bad); e == nil || value != nil {
			t.Fatal("tampered or incomplete ciphertext accepted")
		}
	}
	rotated, e := LoadKeyring(fixtureKeyring(t, "second", map[string]string{"first": first, "second": second}))
	if e != nil {
		t.Fatal(e)
	}
	if value, e := rotated.open(scope, a); e != nil || !bytes.Equal(value, plaintext) {
		t.Fatal("retained old key cannot recover", e)
	}
	newValue, e := rotated.seal(scope, plaintext)
	if e != nil || newValue.keyID != "second" {
		t.Fatal("active key rotation failed", e)
	}
	if value, e := k.open(scope, newValue); e == nil || value != nil {
		t.Fatal("unavailable decryption key accepted")
	}
	for _, length := range []int{0, maximumSecretBytes + 1} {
		if _, e := k.seal(scope, make([]byte, length)); e == nil {
			t.Fatal("unbounded plaintext accepted")
		}
	}
	for _, value := range []any{k, a} {
		raw, e := json.Marshal(value)
		if e != nil || string(raw) != "{}" {
			t.Fatal("private crypto state publicly serialized", e)
		}
	}
}

func TestM11KeyringRejectsAmbiguousJSONAndUnprotectedFiles(t *testing.T) {
	key := fixtureKey(t)
	path := fixtureKeyring(t, "active", map[string]string{"active": key})
	valid, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	invalid := []string{
		`{"schema_version":1,"active_key_id":"active","active_key_id":"other","keys":{"active":"` + key + `"}}`,
		`{"schema_version":1,"active_key_id":"active","keys":{"active":"` + key + `","active":"` + key + `"}}`,
		`{"schema_version":1,"Active_key_id":"active","keys":{"active":"` + key + `"}}`,
		`{"schema_version":2,"active_key_id":"active","keys":{"active":"` + key + `"}}`,
		`{"schema_version":1,"active_key_id":"missing","keys":{"active":"` + key + `"}}`,
		`{"schema_version":1,"active_key_id":"active","keys":null}`,
		`{"schema_version":1,"active_key_id":"active","keys":{"active":"short"}}`,
		string(valid) + ` {}`, strings.Repeat("x", maximumKeyringBytes+1),
	}
	for _, raw := range invalid {
		if e = os.WriteFile(path, []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = LoadKeyring(path); e == nil || e.Error() != "SSO_KEYRING_INVALID" {
			t.Fatal("ambiguous/invalid keyring or raw diagnostic accepted")
		}
	}
	if e = os.WriteFile(path, valid, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadKeyring(path); e == nil {
		t.Fatal("public key material accepted")
	}
	if e = os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(filepath.Dir(path), "linked.json")
	if e = os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadKeyring(link); e == nil {
		t.Fatal("symbolic key file accepted")
	}
	if e = os.Remove(link); e != nil {
		t.Fatal(e)
	}
	if e = os.Link(path, link); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadKeyring(path); e == nil {
		t.Fatal("multiply linked key file accepted")
	}
	if e = os.Remove(link); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(filepath.Dir(path), 0755); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadKeyring(path); e == nil {
		t.Fatal("unprotected parent accepted")
	}
	if e = os.Chmod(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadKeyring(path); e != nil {
		t.Fatal("restored original private key file unavailable", e)
	}
}

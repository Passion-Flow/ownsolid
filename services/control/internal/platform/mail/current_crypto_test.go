package mail_test

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

func TestM01MailAEADBindings(t *testing.T) {
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		t.Fatal("MAIL_AEAD_SYNTHETIC_KEY_FAILED")
	}
	raw, e := json.Marshal(mail.Keyring{Active: "synthetic", Keys: map[string]string{"synthetic": base64.StdEncoding.EncodeToString(key)}})
	path := filepath.Join(t.TempDir(), "ring.json")
	if e != nil || os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("MAIL_AEAD_PRIVATE_KEYRING_FAILED")
	}
	ring, e := mail.LoadKeyring(path)
	if e != nil {
		t.Fatal("MAIL_AEAD_KEYRING_REFUSED")
	}
	id := "88020709-b62d-429c-963e-f07da375e90b"
	kid, nonce, cipher, e := ring.Encrypt(id, "invitation", []byte("synthetic payload"))
	if e != nil {
		t.Fatal("MAIL_AEAD_VALID_ENCRYPTION_FAILED")
	}
	for _, kind := range []string{"nonce_short", "nonce_flip", "purpose", "identity", "cipher", "missing_key"} {
		actualNonce, actualCipher := append([]byte(nil), nonce...), append([]byte(nil), cipher...)
		actualID, purpose, actualKey := id, "invitation", kid
		switch kind {
		case "nonce_short":
			actualNonce = actualNonce[:len(actualNonce)-1]
		case "nonce_flip":
			actualNonce[0] ^= 1
		case "purpose":
			purpose = "password_reset"
		case "identity":
			actualID = "different-synthetic-identity"
		case "cipher":
			actualCipher[0] ^= 1
		case "missing_key":
			actualKey = "missing"
		}
		plain, e := ring.Decrypt(actualKey, actualID, purpose, actualNonce, actualCipher)
		if e == nil || len(plain) != 0 {
			t.Fatal("MAIL_AEAD_BINDING_BYPASSED", kind)
		}
	}
	plain, e := ring.Decrypt(kid, id, "invitation", nonce, cipher)
	if e != nil || string(plain) != "synthetic payload" {
		t.Fatal("MAIL_AEAD_VALID_RECOVERY_FAILED")
	}
}

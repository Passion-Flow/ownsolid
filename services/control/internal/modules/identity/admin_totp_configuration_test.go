package identity

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

func adminTOTPTestDirectory(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAdminTOTPStartupRejectsUnsafeKeyReferences(t *testing.T) {
	dir := adminTOTPTestDirectory(t)
	path := filepath.Join(dir, "private-key-file-marker")
	const marker = "fixture-invalid-value-marker"
	assertRefusal := func(service *Service, cfg AdminTOTPConfiguration, want string) {
		t.Helper()
		err := service.ConfigureAdminTOTP(t.Context(), cfg)
		if err == nil || err.Error() != want || service.AdminTOTPCandidateReady() {
			t.Fatalf("expected %s and an unmounted service", want)
		}
		var output bytes.Buffer
		slog.New(slog.NewTextHandler(&output, nil)).Error("configuration refused", "error", err)
		if strings.Contains(output.String(), marker) || strings.Contains(output.String(), path) {
			t.Fatal("startup error leaked private material")
		}
	}
	key := func() string {
		var raw [adminTOTPKeyBytes]byte
		if _, err := rand.Read(raw[:]); err != nil {
			t.Fatal(err)
		}
		defer clear(raw[:])
		return base64.StdEncoding.EncodeToString(raw[:])
	}()
	valid := fmt.Sprintf(`{"active":"primary","keys":{"primary":%q}}`, key)
	write := func(contents string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	cfg := AdminTOTPConfiguration{Enabled: true, KeyringFile: path}
	assertRefusal(&Service{}, AdminTOTPConfiguration{Enabled: true}, "ADMIN_TOTP_KEYRING_REQUIRED")
	assertRefusal(&Service{}, AdminTOTPConfiguration{KeyringFile: path}, "ADMIN_TOTP_CONFIGURATION_INVALID")
	assertRefusal(&Service{}, cfg, "ADMIN_TOTP_KEYRING_FILE_INVALID")
	assertRefusal(&Service{}, AdminTOTPConfiguration{Enabled: true, KeyringFile: "relative-file"}, "ADMIN_TOTP_KEYRING_FILE_INVALID")
	write(valid, 0640)
	assertRefusal(&Service{}, cfg, "ADMIN_TOTP_KEYRING_FILE_INVALID")
	write(valid, 0600)

	shared := &Service{}
	shared.Config.Security.MailKeyringFile = path
	assertRefusal(shared, cfg, "ADMIN_TOTP_KEYRING_FILE_INVALID")
	assertRefusal(&Service{}, cfg, "ADMIN_TOTP_POSTGRESQL_REQUIRED")

	link := filepath.Join(dir, "linked-file")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	assertRefusal(&Service{}, AdminTOTPConfiguration{Enabled: true, KeyringFile: link}, "ADMIN_TOTP_KEYRING_FILE_INVALID")
	shared.Config.Security.MailKeyringFile = link
	assertRefusal(shared, cfg, "ADMIN_TOTP_KEYRING_FILE_INVALID")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, link); err != nil {
		t.Fatal(err)
	}
	assertRefusal(&Service{}, cfg, "ADMIN_TOTP_KEYRING_FILE_INVALID")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(link, 0600); err != nil {
		t.Fatal(err)
	}
	assertRefusal(&Service{}, AdminTOTPConfiguration{Enabled: true, KeyringFile: link}, "ADMIN_TOTP_KEYRING_FILE_INVALID")
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	assertRefusal(&Service{}, cfg, "ADMIN_TOTP_KEYRING_FILE_INVALID")
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	write(strings.Repeat(" ", maxAdminTOTPKeyFileBytes+1), 0600)
	assertRefusal(&Service{}, cfg, "ADMIN_TOTP_KEYRING_FILE_INVALID")

	for _, test := range []struct{ name, document string }{
		{"malformed", `{"active":"` + marker},
		{"invalid base64", fmt.Sprintf(`{"active":"primary","keys":{"primary":%q}}`, marker)},
		{"missing active key", fmt.Sprintf(`{"active":"missing","keys":{"primary":%q}}`, key)},
		{"unknown field", fmt.Sprintf(`{"active":"primary","keys":{"primary":%q},"extra":%q}`, key, marker)},
		{"case alias", fmt.Sprintf(`{"Active":"primary","keys":{"primary":%q}}`, key)},
		{"duplicate field", fmt.Sprintf(`{"active":"primary","active":"primary","keys":{"primary":%q}}`, key)},
		{"duplicate key", fmt.Sprintf(`{"active":"primary","keys":{"primary":%q,"primary":%q}}`, key, key)},
		{"trailing object", valid + "{}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			write(test.document, 0600)
			assertRefusal(&Service{}, cfg, "ADMIN_TOTP_KEYRING_INVALID")
		})
	}
}

func TestAdminTOTPKeyringReadsCandidateCiphertextWithoutExposingKeys(t *testing.T) {
	dir := adminTOTPTestDirectory(t)
	path := filepath.Join(dir, "keyring.json")
	var material [adminTOTPKeyBytes]byte
	if _, err := rand.Read(material[:]); err != nil {
		t.Fatal(err)
	}
	defer clear(material[:])
	encoded := base64.StdEncoding.EncodeToString(material[:])
	if err := os.WriteFile(path, fmt.Appendf(nil, `{"active":"primary","keys":{"primary":%q}}`, encoded), 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	legacy, err := mail.LoadKeyring(path)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := loadAdminTOTPKeyring(path)
	if err != nil {
		t.Fatal(err)
	}
	var seed [adminTOTPSeedBytes]byte
	if _, err := rand.Read(seed[:]); err != nil {
		t.Fatal(err)
	}
	defer clear(seed[:])
	const scope = "fixture-tenant:fixture-user:fixture-factor"
	key, nonce, encrypted, err := legacy.Encrypt(scope, "admin-totp", seed[:])
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ring.decrypt(key, scope, nonce, encrypted)
	defer clear(plain)
	if err != nil || !bytes.Equal(plain, seed[:]) {
		t.Fatal("existing candidate ciphertext did not open")
	}
	if fmt.Sprintf("%v %#v", ring, ring) != "ADMIN_TOTP_KEYRING_REDACTED ADMIN_TOTP_KEYRING_REDACTED" {
		t.Fatal("keyring diagnostics must be redacted")
	}
}

package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	json "encoding/json/v2"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unicode"
)

const (
	maxAdminTOTPKeyFileBytes = 65536
	maxAdminTOTPKeys         = 8
	adminTOTPKeyBytes        = 32
	adminTOTPKeyScopeSuffix  = ":admin-totp:1"
)

var adminTOTPKeyID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

type adminTOTPKeyring struct {
	active string
	keys   map[string]cipher.AEAD
}

func (*adminTOTPKeyring) String() string   { return "ADMIN_TOTP_KEYRING_REDACTED" }
func (*adminTOTPKeyring) GoString() string { return "ADMIN_TOTP_KEYRING_REDACTED" }

func loadAdminTOTPKeyring(path string, otherKeyPaths ...string) (*adminTOTPKeyring, error) {
	raw, err := readAdminTOTPKeyFile(path, otherKeyPaths)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	bad := failure("ADMIN_TOTP_KEYRING_INVALID")
	var document struct {
		Active string            `json:"active"`
		Keys   map[string]string `json:"keys"`
	}
	if json.Unmarshal(raw, &document, json.RejectUnknownMembers(true)) != nil ||
		!adminTOTPKeyID.MatchString(document.Active) || len(document.Keys) == 0 || len(document.Keys) > maxAdminTOTPKeys {
		return nil, bad
	}
	defer clear(document.Keys)
	ring := &adminTOTPKeyring{active: document.Active, keys: make(map[string]cipher.AEAD, len(document.Keys))}
	for id, encoded := range document.Keys {
		if !adminTOTPKeyID.MatchString(id) || len(encoded) != base64.StdEncoding.EncodedLen(adminTOTPKeyBytes) {
			return nil, bad
		}
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(key) != adminTOTPKeyBytes {
			clear(key)
			return nil, bad
		}
		block, err := aes.NewCipher(key)
		clear(key)
		if err != nil {
			return nil, bad
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, bad
		}
		ring.keys[id] = aead
	}
	if ring.keys[ring.active] == nil {
		return nil, bad
	}
	return ring, nil
}

func adminTOTPPrivateFile(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky|os.ModeSymlink) != 0 {
		return false
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0700
	}
	mode := info.Mode().Perm()
	return info.Mode().IsRegular() && (mode == 0400 || mode == 0600) && owner.Nlink == 1
}

func sameAdminTOTPKeyFile(before, after os.FileInfo) bool {
	return adminTOTPPrivateFile(after, false) && os.SameFile(before, after) &&
		before.Mode() == after.Mode() && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

func readAdminTOTPKeyFile(path string, otherKeyPaths []string) ([]byte, error) {
	bad := failure("ADMIN_TOTP_KEYRING_FILE_INVALID")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsFunc(path, unicode.IsControl) {
		return nil, bad
	}
	parent := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return nil, bad
	}
	beforeDirectory, err := os.Lstat(parent)
	if err != nil || !adminTOTPPrivateFile(beforeDirectory, true) {
		return nil, bad
	}
	// Keep the final dot so a replaced parent must still resolve as a directory.
	root, err := os.OpenRoot(parent + string(filepath.Separator) + ".")
	if err != nil {
		return nil, bad
	}
	defer root.Close()
	openedDirectory, err := root.Stat(".")
	if err != nil || !os.SameFile(beforeDirectory, openedDirectory) {
		return nil, bad
	}
	name := filepath.Base(path)
	before, err := root.Lstat(name)
	if err != nil || !adminTOTPPrivateFile(before, false) || before.Size() < 1 || before.Size() > maxAdminTOTPKeyFileBytes {
		return nil, bad
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, bad
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameAdminTOTPKeyFile(before, opened) {
		return nil, bad
	}
	for _, otherPath := range otherKeyPaths {
		if otherPath == "" {
			continue
		}
		// Existing secret readers may follow symbolic links. Compare against the pinned file.
		other, err := os.Stat(otherPath)
		if err == nil && os.SameFile(opened, other) {
			return nil, bad
		}
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxAdminTOTPKeyFileBytes+1))
	after, statErr := file.Stat()
	current, pathErr := root.Lstat(name)
	currentDirectory, dirErr := os.Lstat(parent)
	if err != nil || int64(len(raw)) != before.Size() || statErr != nil || !sameAdminTOTPKeyFile(before, after) ||
		pathErr != nil || !sameAdminTOTPKeyFile(before, current) || dirErr != nil || !adminTOTPPrivateFile(currentDirectory, true) ||
		!os.SameFile(beforeDirectory, currentDirectory) {
		clear(raw)
		return nil, bad
	}
	return raw, nil
}

func (k *adminTOTPKeyring) encrypt(scope string, seed []byte) (string, []byte, []byte, error) {
	aead := k.keys[k.active]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", nil, nil, err
	}
	return k.active, nonce, aead.Seal(nil, nonce, seed, []byte(scope+adminTOTPKeyScopeSuffix)), nil
}

func (k *adminTOTPKeyring) decrypt(key, scope string, nonce, seed []byte) ([]byte, error) {
	aead := k.keys[key]
	if aead == nil || len(nonce) != aead.NonceSize() {
		return nil, failure("ADMIN_TOTP_STORED_FACTOR_INVALID")
	}
	plain, err := aead.Open(nil, nonce, seed, []byte(scope+adminTOTPKeyScopeSuffix))
	if err != nil || len(plain) != adminTOTPSeedBytes {
		clear(plain)
		return nil, failure("ADMIN_TOTP_STORED_FACTOR_INVALID")
	}
	return plain, nil
}

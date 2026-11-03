package sso

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"

	"github.com/google/uuid"
)

// InitializeKeyring writes a new private file, optionally retaining every key from
// a protected previous ring. It never overwrites a file, creates a directory,
// retires a decryption key, changes configuration, or prints secret material.
func InitializeKeyring(destination, previous string) error {
	bad := fail("SSO_PRIVATE_FILE_INVALID")
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return bad
	}
	parent := filepath.Dir(destination)
	resolved, e := filepath.EvalSymlinks(parent)
	if e != nil || resolved != parent {
		return bad
	}
	info, e := os.Lstat(parent)
	if e != nil || !privateOwned(info, true) {
		return bad
	}
	keys := map[string]string{}
	if previous != "" {
		ring, e := LoadKeyring(previous)
		if e != nil {
			return e
		}
		if len(ring.keys) >= maximumRetainedKeys {
			return fail("SSO_KEYRING_RETAINED_KEY_LIMIT")
		}
		for id, key := range ring.keys {
			keys[id] = base64.StdEncoding.EncodeToString(key[:])
		}
	}
	var key [32]byte
	if _, e = rand.Read(key[:]); e != nil {
		return fail("SSO_ENTROPY_UNAVAILABLE")
	}
	active := "key-" + uuid.NewString()
	keys[active] = base64.StdEncoding.EncodeToString(key[:])
	raw, e := json.Marshal(map[string]any{"schema_version": keyringSchemaVersion, "active_key_id": active, "keys": keys})
	if e != nil {
		return fail("SSO_KEYRING_INVALID")
	}
	root, e := os.OpenRoot(parent)
	if e != nil {
		return bad
	}
	defer root.Close()
	opened, e := root.Stat(".")
	if e != nil || !os.SameFile(info, opened) || !privateOwned(opened, true) {
		return bad
	}
	file, e := root.OpenFile(filepath.Base(destination), os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return bad
	}
	_, writeError := file.Write(append(raw, '\n'))
	syncError := file.Sync()
	closeError := file.Close()
	if writeError != nil || syncError != nil || closeError != nil {
		return bad
	}
	directory, e := root.Open(".")
	if e != nil {
		return bad
	}
	syncError = directory.Sync()
	directory.Close()
	if syncError != nil {
		return bad
	}
	if _, e = LoadKeyring(destination); e != nil {
		return e
	}
	return nil
}

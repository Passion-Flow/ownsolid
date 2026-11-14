package taskbus

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nats-io/nkeys"
)

func privateTestRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestTaskBusProtectedFileRejectsModesLinksBoundsAndForeignPaths(t *testing.T) {
	root := privateTestRoot(t)
	path := filepath.Join(root, "input")
	value := []byte("synthetic private file")
	if err := os.WriteFile(path, value, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := readPrivateFile(path, int64(len(value)))
	if err != nil || !bytes.Equal(raw, value) {
		t.Fatal("private bounded source refused", err)
	}
	wipe(raw)
	for _, limit := range []int64{0, int64(len(value) - 1)} {
		if raw, err := readPrivateFile(path, limit); err == nil || raw != nil {
			t.Fatal("invalid bound returned bytes")
		}
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if raw, err := readPrivateFile(path, 1024); err == nil || raw != nil {
		t.Fatal("nonprivate file returned bytes")
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err = os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if raw, err := readPrivateFile(alias, 1024); err == nil || raw != nil {
		t.Fatal("symlink returned bytes")
	}
	if err = os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if raw, err := readPrivateFile(path, 1024); err == nil || raw != nil {
		t.Fatal("multiple-link file returned bytes")
	}
	if err = os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if raw, err := readPrivateFile(path, 1024); err == nil || raw != nil {
		t.Fatal("nonprivate parent returned bytes")
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"$HOME/input", "~/input", "relative/input", root + "/../" + filepath.Base(root) + "/input"} {
		if raw, err := readPrivateFile(path, 1024); err == nil || raw != nil {
			t.Fatal("noncanonical/ambient path returned bytes")
		}
	}
}

func TestTaskBusNKeyNonceSigningReopensAndPinsOriginalIdentity(t *testing.T) {
	root := privateTestRoot(t)
	path := filepath.Join(root, "identity.nk")
	key, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	defer key.Wipe()
	seed, err := key.Seed()
	if err != nil {
		t.Fatal(err)
	}
	defer wipe(seed)
	if err = os.WriteFile(path, append(append([]byte{}, seed...), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	public, err := userPublicKey(path)
	if err != nil {
		t.Fatal(err)
	}
	signer := signUserNonce(path, public)
	nonce := []byte("synthetic server challenge")
	signature, err := signer(nonce)
	if err != nil || key.Verify(nonce, signature) != nil {
		t.Fatal("actual NKey proof failed", err)
	}
	wipe(signature)
	for _, nonce := range [][]byte{nil, bytes.Repeat([]byte{'x'}, maximumServerNonceBytes+1)} {
		if signature, err := signer(nonce); err == nil || signature != nil {
			t.Fatal("invalid nonce emitted signature")
		}
	}
	replacement, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Wipe()
	changed, err := replacement.Seed()
	if err != nil {
		t.Fatal(err)
	}
	defer wipe(changed)
	if err = os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if signature, err := signer(nonce); err == nil || signature != nil {
		t.Fatal("replaced key silently acquired original identity")
	}
	if err = os.WriteFile(path, []byte("synthetic invalid seed"), 0600); err != nil {
		t.Fatal(err)
	}
	if public, err := userPublicKey(path); err == nil || public != "" {
		t.Fatal("invalid seed exposed identity")
	}
}

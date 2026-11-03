package sso

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestM11ActualKeyringInitializationRetainsOldKeysAndNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	first, second := filepath.Join(dir, "first.json"), filepath.Join(dir, "second.json")
	if e := InitializeKeyring(first, ""); e != nil {
		t.Fatal(e)
	}
	original, e := os.ReadFile(first)
	if e != nil {
		t.Fatal(e)
	}
	one, e := LoadKeyring(first)
	if e != nil {
		t.Fatal(e)
	}
	id := uuid.NewString()
	scope := secretScope{uuid.NewString(), id, id, clientSecretPurpose}
	sealed, e := one.seal(scope, []byte("synthetic retained secret"))
	if e != nil {
		t.Fatal(e)
	}
	if e = InitializeKeyring(second, first); e != nil {
		t.Fatal(e)
	}
	two, e := LoadKeyring(second)
	if e != nil {
		t.Fatal(e)
	}
	if len(two.keys) != 2 || one.active == two.active {
		t.Fatal("rotation did not retain old key and select a new key")
	}
	plain, e := two.open(scope, sealed)
	if e != nil || string(plain) != "synthetic retained secret" {
		t.Fatal("old ciphertext was lost", e)
	}
	if e = InitializeKeyring(first, second); e == nil {
		t.Fatal("existing keyring was overwritten")
	}
	current, e := os.ReadFile(first)
	if e != nil || !bytes.Equal(original, current) {
		t.Fatal("original private keyring changed", e)
	}
	link := filepath.Join(dir, "link.json")
	if e = os.Symlink(first, link); e != nil {
		t.Fatal(e)
	}
	if e = InitializeKeyring(link, ""); e == nil {
		t.Fatal("symlink destination accepted")
	}
	third := filepath.Join(dir, "exclusive.json")
	var group sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() { defer group.Done(); results <- InitializeKeyring(third, "") }()
	}
	group.Wait()
	close(results)
	wins := 0
	for e := range results {
		if e == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatal("exclusive concurrent key creation did not have one winner")
	}
	if _, e = LoadKeyring(third); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(dir, 0755); e != nil {
		t.Fatal(e)
	}
	if e = InitializeKeyring(filepath.Join(dir, "public-parent.json"), ""); e == nil {
		t.Fatal("unprotected parent accepted")
	}
}

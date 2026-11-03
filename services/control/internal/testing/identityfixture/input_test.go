package identityfixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtectedTestDatabaseInput(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "connection")
	secret := "postgres://synthetic:private-value@127.0.0.1:15433/synthetic_test?sslmode=disable"
	if e = os.WriteFile(path, []byte(secret+"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if got, e := databaseInput(path, ""); e != nil || got != secret {
		t.Fatal("actual private test input rejected", e)
	}
	if got, e := databaseInput("", secret); e != nil || got != secret {
		t.Fatal("legacy test compatibility", e)
	}
	for _, pair := range [][2]string{{path, secret}, {"relative", ""}, {filepath.Join(root, "absent"), ""}} {
		if _, e := databaseInput(pair[0], pair[1]); e == nil || strings.Contains(e.Error(), "private-value") {
			t.Fatal("unsafe input or secret error", e)
		}
	}
	if e = os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = databaseInput(path, ""); e == nil {
		t.Fatal("public secret accepted")
	}
	os.Chmod(path, 0600)
	alias := filepath.Join(root, "alias")
	if e = os.Symlink(path, alias); e != nil {
		t.Fatal(e)
	}
	if _, e = databaseInput(alias, ""); e == nil {
		t.Fatal("symbolic secret accepted")
	}
	hard := filepath.Join(root, "hard")
	if e = os.Link(path, hard); e != nil {
		t.Fatal(e)
	}
	if _, e = databaseInput(path, ""); e == nil {
		t.Fatal("linked secret accepted")
	}
	os.Remove(hard)
	for _, raw := range []string{"", strings.Repeat("x", maximumTestDSNBytes+1), "first\nsecond"} {
		if e = os.WriteFile(path, []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = databaseInput(path, ""); e == nil {
			t.Fatal("empty/oversize/multiline secret accepted")
		}
	}
}

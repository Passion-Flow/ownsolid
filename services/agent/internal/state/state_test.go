package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProtectedIdentityAndExclusiveProcessLock(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	value := Identity{NodeID: "synthetic-id", Credential: "synthetic-secret", ControlOrigin: "https://control.invalid", ProtocolVersion: "1.0"}
	if e := SaveNew(dir, value); e != nil {
		t.Fatal(e)
	}
	if e := SaveNew(dir, Identity{}); e == nil || e.Error() != "IDENTITY_ALREADY_EXISTS" {
		t.Fatal("identity overwritten", e)
	}
	read, e := Load(dir)
	if e != nil || read != value {
		t.Fatal("identity not durable", e)
	}
	if e = os.Chmod(filepath.Join(dir, "identity.json"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = Load(dir); e == nil {
		t.Fatal("world-readable identity accepted")
	}
	lock, e := Lock(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Lock(dir); e == nil || e.Error() != "AGENT_ALREADY_RUNNING" {
		t.Fatal("duplicate process admitted", e)
	}
	lock.Close()
	lock, e = Lock(dir)
	if e != nil {
		t.Fatal("lock did not recover", e)
	}
	lock.Close()
	link := filepath.Join(t.TempDir(), "identity-link")
	if e = os.Symlink(filepath.Join(dir, "identity.json"), link); e != nil {
		t.Fatal(e)
	}
	if _, e = ProtectedFile(link); e == nil {
		t.Fatal("identity symlink accepted")
	}
	if e = os.Chmod(dir, 0755); e != nil {
		t.Fatal(e)
	}
	if Directory(dir) == nil {
		t.Fatal("unprotected state directory accepted")
	}
}

package storeinitialization

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
)

func TestInitializationBorrowsActualNamespaceOwnerLockAndRejectsDrift(t *testing.T) {
	uid, gid := os.Geteuid(), os.Getegid()
	if uid == 0 {
		uid = 65534
	}
	if gid == 0 {
		gid = 65534
	}
	root := t.TempDir()
	if e := datasets.InitializeStore(root, uuid.NewString(), uid, gid); e != nil {
		t.Fatal(e)
	}
	o := Options{Root: root, UID: uid, GID: gid}
	if e := withNamespaceLock(o, func(recheck func() error) error {
		called := false
		if e := withNamespaceLock(o, func(func() error) error { called = true; return nil }); e == nil || called {
			t.Fatal("existing owner lock bypassed")
		}
		return recheck()
	}); e != nil {
		t.Fatal(e)
	}
	e := withNamespaceLock(o, func(func() error) error {
		return os.WriteFile(filepath.Join(root, "namespace.json"), []byte("synthetic retained tamper"), 0600)
	})
	if e == nil {
		t.Fatal("changed actual namespace descriptor accepted")
	}
	raw, e := os.ReadFile(filepath.Join(root, "namespace.json"))
	if e != nil || string(raw) != "synthetic retained tamper" {
		t.Fatal("drift silently repaired")
	}
}

package models

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func emptyRuntimeOwner() (int, int) {
	uid, gid := os.Geteuid(), os.Getegid()
	if uid == 0 {
		uid = 65534
	}
	if gid == 0 {
		gid = 65534
	}
	return uid, gid
}

func TestEmptyModelIdentityIsStableDirectoryProofWithExclusiveCallback(t *testing.T) {
	path := t.TempDir()
	uid, gid := emptyRuntimeOwner()
	var first string
	if e := WithEmptyStoreIdentity(path, uid, gid, true, func(proof string, recheck func() error) error {
		if len(proof) != 64 {
			t.Fatal("missing directory digest")
		}
		first = proof
		called := false
		if e := WithEmptyStoreIdentity(path, uid, gid, false, func(string, func() error) error { called = true; return nil }); e == nil || called {
			t.Fatal("competing directory operator accepted")
		}
		return recheck()
	}); e != nil {
		t.Fatal(e)
	}
	if e := WithEmptyStoreIdentity(path, uid, gid, false, func(proof string, recheck func() error) error {
		if proof != first {
			t.Fatal("unchanged directory changed identity")
		}
		return recheck()
	}); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(path)
	if e != nil || len(entries) != 0 {
		t.Fatal("identity created an initialization/lock marker")
	}
}

func TestEmptyModelIdentityRejectsMutationsAndDoesNotRepair(t *testing.T) {
	uid, gid := emptyRuntimeOwner()
	for _, mutation := range []string{"file", "mode", "mtime", "rename"} {
		t.Run(mutation, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, "store")
			if e := os.Mkdir(path, 0700); e != nil {
				t.Fatal(e)
			}
			e := WithEmptyStoreIdentity(path, uid, gid, true, func(string, func() error) error {
				switch mutation {
				case "file":
					return os.WriteFile(filepath.Join(path, "retained-input"), []byte("synthetic mutation"), 0600)
				case "mode":
					return os.Chmod(path, 0750)
				case "mtime":
					return os.Chtimes(path, time.Unix(1, 0), time.Unix(1, 0))
				case "rename":
					if e := os.Rename(path, path+"-retained"); e != nil {
						return e
					}
					return os.Mkdir(path, 0700)
				}
				return nil
			})
			if e == nil {
				t.Fatal("mutated directory accepted")
			}
			if mutation == "file" {
				if _, e = os.Stat(filepath.Join(path, "retained-input")); e != nil {
					t.Fatal("mutation evidence removed")
				}
			}
			if mutation == "mode" {
				info, e := os.Stat(path)
				if e != nil || info.Mode().Perm() != 0750 {
					t.Fatal("changed permissions silently repaired")
				}
			}
		})
	}
}

func TestEmptyModelIdentityMissingSymlinkAndCallbackFailure(t *testing.T) {
	uid, gid := emptyRuntimeOwner()
	parent := t.TempDir()
	path := filepath.Join(parent, "store")
	called := false
	fn := func(string, func() error) error { called = true; return nil }
	if WithEmptyStoreIdentity(path, uid, gid, false, fn) == nil || called {
		t.Fatal("missing directory adopted")
	}
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(parent, "link")
	if e := os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if WithEmptyStoreIdentity(link, uid, gid, true, fn) == nil || called {
		t.Fatal("symlink adopted")
	}
	wanted := errors.New("SYNTHETIC_AUDIT_OUTCOME_UNKNOWN")
	e := WithEmptyStoreIdentity(path, uid, gid, true, func(string, func() error) error { return wanted })
	if !errors.Is(e, wanted) || !strings.Contains(e.Error(), wanted.Error()) {
		t.Fatal("callback uncertainty lost")
	}
	if e = WithEmptyStoreIdentity(path, uid, gid, false, fn); e != nil || !called {
		t.Fatal("exclusive descriptor leaked after failure", e)
	}
}

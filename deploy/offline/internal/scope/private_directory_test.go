package scope

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestScopePrivateDirectoryOpenPinsIdentity(t *testing.T) {
	for _, fault := range []string{"valid", "replacement", "regular", "fifo", "symlink", "mode", "nil", "noncanonical"} {
		t.Run(fault, func(t *testing.T) {
			base := filepath.Join(t.TempDir(), "private")
			if e := os.Mkdir(base, 0700); e != nil {
				t.Fatal(e)
			}
			before, e := os.Lstat(base)
			if e != nil {
				t.Fatal(e)
			}
			if fault == "nil" {
				before = nil
			}
			if fault == "noncanonical" {
				base += "/."
			}
			if fault == "mode" {
				e = os.Chmod(base, 0755)
			}
			if fault == "replacement" || fault == "regular" || fault == "fifo" || fault == "symlink" {
				e = os.Rename(base, base+"-saved")
				if e == nil {
					switch fault {
					case "replacement":
						e = os.Mkdir(base, 0700)
					case "regular":
						e = os.WriteFile(base, []byte("synthetic-preserve"), 0600)
					case "fifo":
						e = syscall.Mkfifo(base, 0600)
					case "symlink":
						e = os.Symlink("private-saved", base)
					}
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			root, e := openPinnedPrivateDirectory(base, before)
			if fault == "valid" {
				if e != nil || root == nil {
					t.Fatal("private directory refused", e)
				}
				if e := root.Close(); e != nil {
					t.Fatal(e)
				}
				root, e = openPrivateDirectory(base)
				if e != nil || root == nil {
					t.Fatal("private wrapper refused", e)
				}
				root.Close()
				return
			}
			if root != nil {
				root.Close()
			}
			if e == nil || root != nil || e.Error() != "OFFLINE_SCOPE_DIRECTORY_CHANGED" {
				t.Fatal("changed directory admitted", e)
			}
			if fault != "nil" && fault != "replacement" {
				root, e = openPrivateDirectory(base)
				if root != nil {
					root.Close()
				}
				if e == nil || root != nil {
					t.Fatal("unsafe initial directory admitted")
				}
			}
		})
	}
}

func TestScopePrivateDirectoryEntrancesRejectUnsafeObjects(t *testing.T) {
	for _, fault := range []string{"mode", "regular", "fifo", "symlink"} {
		t.Run(fault, func(t *testing.T) {
			c := fixture(t)
			if _, e := Create(context.Background(), c); e != nil {
				t.Fatal(e)
			}
			journal, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
			if e != nil {
				t.Fatal(e)
			}
			saved := c.ScopeRoot
			if fault == "mode" {
				e = os.Chmod(c.ScopeRoot, 0755)
			} else {
				saved += "-saved"
				e = os.Rename(c.ScopeRoot, saved)
				if e == nil {
					switch fault {
					case "regular":
						e = os.WriteFile(c.ScopeRoot, []byte("synthetic-preserve"), 0600)
					case "fifo":
						e = syscall.Mkfifo(c.ScopeRoot, 0600)
					case "symlink":
						e = os.Symlink(filepath.Base(saved), c.ScopeRoot)
					}
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e := Read(context.Background(), c); e == nil || e.Error() != "OFFLINE_SCOPE_PRIVATE_ROOT_REQUIRED" {
				t.Fatal("unsafe scope read", e)
			}
			root, e := openBackupDirectory(c.ScopeRoot)
			if root != nil {
				root.Close()
			}
			if e == nil || root != nil || e.Error() != "OFFLINE_SCOPE_BACKUP_PRIVATE_DIRECTORY_REQUIRED" {
				t.Fatal("unsafe backup directory", e)
			}
			after, e := os.ReadFile(filepath.Join(saved, journalName))
			if e != nil || !bytes.Equal(journal, after) {
				t.Fatal("unsafe entrance changed recovery journal", e)
			}
			if fault == "regular" {
				raw, e := os.ReadFile(c.ScopeRoot)
				if e != nil || string(raw) != "synthetic-preserve" {
					t.Fatal("unsafe object changed", e)
				}
			}
		})
	}
}

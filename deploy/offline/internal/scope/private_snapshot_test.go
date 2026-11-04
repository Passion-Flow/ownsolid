package scope

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

func privateSnapshotFixture(t *testing.T, nested bool) (*os.Root, string, string) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "private")
	if e := os.Mkdir(base, 0700); e != nil {
		t.Fatal(e)
	}
	name := "payload"
	if nested {
		name = "inputs/role/payload"
		if e := os.MkdirAll(filepath.Dir(filepath.Join(base, name)), 0700); e != nil {
			t.Fatal(e)
		}
	}
	put(t, filepath.Join(base, name), []byte("synthetic-private-payload"))
	root, e := os.OpenRoot(base + "/.")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { root.Close() })
	return root, name, base
}

func TestScopePrivateSnapshotLimitsAndReferences(t *testing.T) {
	for _, limit := range []int64{25, 26, int64(^uint64(0) >> 1)} {
		t.Run("valid-"+strconv.FormatInt(limit, 10), func(t *testing.T) {
			root, name, _ := privateSnapshotFixture(t, true)
			raw, e := regular(root, name, limit)
			if e != nil || string(raw) != "synthetic-private-payload" {
				t.Fatal("valid bounded nested read refused", e)
			}
		})
	}
	t.Run("caller-limit-above-one-mib", func(t *testing.T) {
		root, name, base := privateSnapshotFixture(t, false)
		data := bytes.Repeat([]byte{'x'}, (1<<20)+3)
		put(t, filepath.Join(base, name), data)
		raw, e := regular(root, name, int64(len(data)))
		if e != nil || !bytes.Equal(raw, data) {
			t.Fatal("caller limit was replaced by a smaller fixed limit", e)
		}
	})
	for _, fault := range []string{"zero-limit", "negative-limit", "over-limit", "empty", "missing", "public-mode", "hardlink", "symlink", "fifo", "directory", "absolute", "escape", "unclean", "dot", "parent-mode", "parent-link", "nil-root"} {
		t.Run(fault, func(t *testing.T) {
			root, name, base := privateSnapshotFixture(t, true)
			path, maximum := filepath.Join(base, name), int64(1024)
			switch fault {
			case "zero-limit":
				maximum = 0
			case "negative-limit":
				maximum = -1
			case "over-limit":
				maximum = 24
			case "empty":
				put(t, path, nil)
			case "missing":
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
			case "public-mode":
				if e := os.Chmod(path, 0644); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(path, filepath.Join(base, "alias")); e != nil {
					t.Fatal(e)
				}
			case "symlink", "fifo", "directory":
				if e := os.Rename(path, path+"-saved"); e != nil {
					t.Fatal(e)
				}
				var e error
				if fault == "symlink" {
					e = os.Symlink("payload-saved", path)
				}
				if fault == "fifo" {
					e = syscall.Mkfifo(path, 0600)
				}
				if fault == "directory" {
					e = os.Mkdir(path, 0700)
				}
				if e != nil {
					t.Fatal(e)
				}
			case "absolute":
				name = path
			case "escape":
				name = "../private/" + name
			case "unclean":
				name = "inputs/../" + name
			case "dot":
				name = "."
			case "parent-mode":
				if e := os.Chmod(filepath.Dir(path), 0755); e != nil {
					t.Fatal(e)
				}
			case "parent-link":
				if e := os.Rename(filepath.Dir(path), filepath.Dir(path)+"-saved"); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink("role-saved", filepath.Dir(path)); e != nil {
					t.Fatal(e)
				}
			case "nil-root":
				root = nil
			}
			if raw, e := regular(root, name, maximum); e == nil || raw != nil {
				t.Fatal("unsafe private reference returned bytes")
			}
		})
	}
}

func mutatePrivateLeaf(t *testing.T, path, fault string, before os.FileInfo) {
	t.Helper()
	// Change time has kernel-tick granularity: step past it so the mutation is observable.
	time.Sleep(20 * time.Millisecond)
	var e error
	switch fault {
	case "replacement":
		e = os.Rename(path, path+"-saved")
		if e == nil {
			e = os.WriteFile(path, []byte("synthetic-private-payload"), 0600)
		}
	case "restored-mtime":
		e = os.WriteFile(path, []byte("changed---private-payload"), 0600)
		if e == nil {
			e = os.Chtimes(path, before.ModTime(), before.ModTime())
		}
	case "mode":
		e = os.Chmod(path, 0644)
	case "hardlink":
		e = os.Link(path, path+"-alias")
	case "symlink", "fifo":
		e = os.Rename(path, path+"-saved")
		if e == nil && fault == "symlink" {
			e = os.Symlink("payload-saved", path)
		}
		if e == nil && fault == "fifo" {
			e = syscall.Mkfifo(path, 0600)
		}
	case "truncate":
		e = os.Truncate(path, 3)
	}
	if e != nil {
		t.Fatal(e)
	}
}

func TestScopePrivateSnapshotRejectsLeafMutation(t *testing.T) {
	for _, stage := range []string{"before-open", "before-read"} {
		for _, fault := range []string{"replacement", "restored-mtime", "mode", "hardlink", "symlink", "fifo", "truncate", "closed-descriptor", "short-read"} {
			if stage == "before-open" && (fault == "closed-descriptor" || fault == "short-read") {
				continue
			}
			t.Run(stage+"/"+fault, func(t *testing.T) {
				root, name, base := privateSnapshotFixture(t, false)
				parent, parents, e := pinPrivateParent(root, name)
				if e != nil {
					t.Fatal(e)
				}
				defer parent.Close()
				before, e := parent.Lstat(name)
				if e != nil {
					t.Fatal(e)
				}
				if stage == "before-open" {
					mutatePrivateLeaf(t, filepath.Join(base, name), fault, before)
					f, e := openPrivateSnapshot(root, parent, parents, name, before, 1024)
					if f != nil {
						f.Close()
					}
					if e == nil || f != nil {
						t.Fatal("changed leaf opened")
					}
					return
				}
				f, e := openPrivateSnapshot(root, parent, parents, name, before, 1024)
				if e != nil {
					t.Fatal(e)
				}
				if fault == "closed-descriptor" {
					if e := f.Close(); e != nil {
						t.Fatal(e)
					}
				} else if fault == "short-read" {
					if _, e := f.Seek(1, 0); e != nil {
						t.Fatal(e)
					}
				} else {
					mutatePrivateLeaf(t, filepath.Join(base, name), fault, before)
				}
				raw, e := readPrivateSnapshot(root, parent, parents, name, before, f, 1024)
				if e == nil || raw != nil {
					t.Fatal("changed read returned partial/private bytes")
				}
			})
		}
	}
}

func TestScopePrivateSnapshotRejectsParentMutation(t *testing.T) {
	for _, level := range []string{"root", "intermediate", "leaf-parent"} {
		for _, fault := range []string{"replacement", "mode", "symlink", "fifo"} {
			t.Run(level+"/"+fault, func(t *testing.T) {
				root, name, base := privateSnapshotFixture(t, true)
				parent, parents, e := pinPrivateParent(root, name)
				if e != nil {
					t.Fatal(e)
				}
				defer parent.Close()
				leaf := filepath.Base(name)
				before, e := parent.Lstat(leaf)
				if e != nil {
					t.Fatal(e)
				}
				f, e := openPrivateSnapshot(root, parent, parents, leaf, before, 1024)
				if e != nil {
					t.Fatal(e)
				}
				path := base
				if level == "intermediate" {
					path = filepath.Join(base, "inputs")
				}
				if level == "leaf-parent" {
					path = filepath.Dir(filepath.Join(base, name))
				}
				if fault == "mode" {
					e = os.Chmod(path, 0755)
				} else {
					e = os.Rename(path, path+"-saved")
					if e == nil {
						switch fault {
						case "replacement":
							e = os.Mkdir(path, 0700)
						case "symlink":
							e = os.Symlink(filepath.Base(path)+"-saved", path)
						case "fifo":
							e = syscall.Mkfifo(path, 0600)
						}
					}
				}
				if e != nil {
					f.Close()
					t.Fatal(e)
				}
				raw, e := readPrivateSnapshot(root, parent, parents, leaf, before, f, 1024)
				if e == nil || raw != nil {
					t.Fatal("changed parent accepted")
				}
				if privateParentsUnchanged(root, parent, parents) {
					t.Fatal("changed parent snapshot accepted")
				}
				if f, e := openPrivateSnapshot(root, parent, parents, leaf, before, 1024); e == nil || f != nil {
					if f != nil {
						f.Close()
					}
					t.Fatal("changed parent reopened")
				}
			})
		}
	}
}

func TestScopeProtectedInputsShareSnapshotBoundary(t *testing.T) {
	for _, fault := range []string{"valid", "public-parent", "symlink", "outside-root", "public-file", "hardlink"} {
		t.Run(fault, func(t *testing.T) {
			root, name, base := privateSnapshotFixture(t, false)
			_ = root
			path := filepath.Join(base, name)
			c := configuration.Defaults()
			c.Verification.MountRoot = filepath.Dir(base)
			c.Verification.MaximumMetadataBytes = 1024
			c.Compose = &configuration.Compose{ModelFile: path}
			var e error
			switch fault {
			case "public-parent":
				e = os.Chmod(base, 0755)
			case "symlink":
				e = os.Rename(path, path+"-saved")
				if e == nil {
					e = os.Symlink("payload-saved", path)
				}
			case "outside-root":
				c.Verification.MountRoot = filepath.Join(base, "unrelated")
			case "public-file":
				e = os.Chmod(path, 0644)
			case "hardlink":
				e = os.Link(path, path+"-alias")
			}
			if e != nil {
				t.Fatal(e)
			}
			input, ie := protectedInput(c, path, 1024)
			model, me := readModel(c)
			if fault == "valid" {
				if ie != nil || me != nil || !bytes.Equal(input, model) || string(input) != "synthetic-private-payload" {
					t.Fatal("private owners disagree", ie, me)
				}
				return
			}
			if ie == nil || me == nil || input != nil || model != nil {
				t.Fatal("unsafe input/model returned bytes")
			}
			if fault == "public-parent" || fault == "symlink" || fault == "outside-root" {
				if ie.Error() != "OFFLINE_SCOPE_PRIVATE_INPUT_REFERENCE_REQUIRED" || me.Error() != "OFFLINE_SCOPE_PRIVATE_MODEL_REFERENCE_REQUIRED" {
					t.Fatal("reference errors changed", ie, me)
				}
			}
		})
	}
}

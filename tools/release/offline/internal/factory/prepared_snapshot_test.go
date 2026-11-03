package factory

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func preparedDirectoryRefusal(t *testing.T, root *os.Root, err error) {
	t.Helper()
	if root != nil {
		root.Close()
		t.Fatal("changed directory exposed a descriptor")
	}
	if err == nil || !strings.HasPrefix(err.Error(), "RELEASE_PREPARED_DIRECTORY_") {
		t.Fatal("expected a fixed directory refusal", err)
	}
}

func TestPreparedDirectoryCheckedOpen(t *testing.T) {
	for _, kind := range []string{"stable", "replacement", "symlink", "mode", "entry_added", "missing_native_facts"} {
		t.Run(kind, func(t *testing.T) {
			name := releaseInputFile(t, 0600, 0700)
			directory := filepath.Dir(name)
			before, e := os.Lstat(directory)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "replacement", "symlink":
				if e = os.Rename(directory, directory+".original"); e != nil {
					t.Fatal(e)
				}
				if kind == "replacement" {
					e = os.Mkdir(directory, 0700)
				} else {
					e = os.Symlink(directory+".original", directory)
				}
			case "mode":
				e = os.Chmod(directory, 0755)
			case "entry_added":
				// Establish an actual directory timestamp change. Adding an entry
				// in the original filesystem clock tick can retain every metadata
				// fact; complete namespace refusal is tested separately below.
				deadline := time.Now().Add(2 * time.Second)
				for additions := 1; ; additions++ {
					if e = os.WriteFile(filepath.Join(directory, "unknown-"+strconv.Itoa(additions)), []byte("synthetic"), 0600); e != nil {
						break
					}
					after, err := os.Lstat(directory)
					if err != nil {
						t.Fatal(err)
					}
					if !sameRegularChangeTime(before, after) {
						t.Logf("observed directory change-time advance after %d additions", additions)
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("fixture could not observe an actual directory change-time tick")
					}
					time.Sleep(time.Millisecond)
				}
			case "missing_native_facts":
				before = releaseInputNativeFacts{FileInfo: before, native: nil}
			}
			if e != nil {
				t.Fatal(e)
			}
			root, e := openPreparedDirectory(directory, before)
			if kind != "stable" {
				preparedDirectoryRefusal(t, root, e)
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			releaseInputCloseRoot(t, root)
			visited := 0
			e = readPreparedDirectory(context.Background(), directory, before, func(root *os.Root, entry os.DirEntry) error {
				visited++
				if entry.Name() != filepath.Base(name) {
					t.Fatal("unexpected stable entry")
				}
				return nil
			})
			if e != nil || visited != 1 {
				t.Fatal("stable directory not read exactly once", visited, e)
			}
		})
	}
}

func TestPreparedPayloadCheckedOpen(t *testing.T) {
	for _, kind := range []string{"stable", "replacement", "symlink", "mode", "hardlink", "digest", "parent_replacement"} {
		t.Run(kind, func(t *testing.T) {
			name := releaseInputFile(t, 0600, 0700)
			maximum := int64(len(releaseInputTestBody))
			root, before, e := pinRegularInput(name, maximum, true, 1)
			if e != nil {
				t.Fatal(e)
			}
			defer releaseInputCloseRoot(t, root)
			digest := wire.Digest(releaseInputTestBody)
			switch kind {
			case "replacement", "symlink":
				if e = os.Rename(name, name+".original"); e != nil {
					t.Fatal(e)
				}
				if kind == "replacement" {
					e = os.WriteFile(name, releaseInputTestBody, 0600)
				} else {
					e = os.Symlink(filepath.Base(name)+".original", name)
				}
			case "mode":
				e = os.Chmod(name, 0644)
			case "hardlink":
				e = os.Link(name, name+".link")
			case "digest":
				digest = strings.Repeat("a", 64)
			case "parent_replacement":
				parent := filepath.Dir(name)
				if e = os.Rename(parent, parent+".original"); e != nil {
					t.Fatal(e)
				}
				e = os.Mkdir(parent, 0700)
			}
			if e != nil {
				t.Fatal(e)
			}
			e = readPreparedFile(context.Background(), root, filepath.Base(name), before, maximum, digest, wire.Asset{})
			if kind == "stable" {
				if e != nil {
					t.Fatal("stable prepared payload refused", e)
				}
			} else if e == nil || e.Error() != "RELEASE_PREPARED_FILE_CHANGED" {
				t.Fatal("changed payload not refused", e)
			}
		})
	}
}

func TestPreparedDirectoryReadDetectsMutationAndCancellation(t *testing.T) {
	for _, kind := range []string{"mutation", "cancellation", "visitor_error"} {
		t.Run(kind, func(t *testing.T) {
			name := releaseInputFile(t, 0600, 0700)
			directory := filepath.Dir(name)
			before, e := os.Lstat(directory)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var observed *os.Root
			e = readPreparedDirectory(ctx, directory, before, func(root *os.Root, entry os.DirEntry) error {
				if observed != nil {
					return nil
				}
				observed = root
				switch kind {
				case "mutation":
					put(t, filepath.Join(directory, "new-after-enumeration"), []byte("synthetic"))
				case "cancellation":
					cancel()
				case "visitor_error":
					return fail("RELEASE_PREPARED_FILE_CHANGED")
				}
				return nil
			})
			if e == nil || observed == nil {
				t.Fatal("changed, interrupted or failed read was accepted", e)
			}
			if _, e = observed.Stat("."); e == nil {
				t.Fatal("failed directory read retained its descriptor")
			}
			if _, e = os.Lstat(directory); e != nil {
				t.Fatal("failed directory read removed its input", e)
			}
		})
	}
}

func TestPreparedSnapshotRejectsPostreadChanges(t *testing.T) {
	for _, kind := range []string{"stable", "rewrite_restored_mtime", "rewrite_restored_bytes", "mode", "hardlink", "nested_replacement", "root_replacement", "entry_added", "entry_removed", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			name := releaseInputFile(t, 0600, 0700)
			directory := filepath.Dir(name)
			nested := filepath.Join(directory, "nested")
			if e := os.Mkdir(nested, 0700); e != nil {
				t.Fatal(e)
			}
			payload := filepath.Join(nested, "payload")
			put(t, payload, releaseInputTestBody)
			rootBefore, e := os.Lstat(directory)
			if e != nil {
				t.Fatal(e)
			}
			root, e := openPreparedDirectory(directory, rootBefore)
			if e != nil {
				t.Fatal(e)
			}
			defer releaseInputCloseRoot(t, root)
			directories := map[string]os.FileInfo{".": rootBefore}
			directories["nested"], e = root.Lstat("nested")
			if e != nil {
				t.Fatal(e)
			}
			files := map[string]os.FileInfo{}
			for _, relative := range []string{filepath.Base(name), "nested/payload"} {
				files[relative], e = root.Lstat(relative)
				if e != nil {
					t.Fatal(e)
				}
				raw, e := readRegular(filepath.Join(directory, relative), int64(len(releaseInputTestBody)), true)
				if e != nil || !bytes.Equal(raw, releaseInputTestBody) {
					t.Fatal("initial actual payload read failed", e)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "rewrite_restored_mtime", "rewrite_restored_bytes":
				put(t, payload, bytes.Repeat([]byte("x"), len(releaseInputTestBody)))
				if kind == "rewrite_restored_bytes" {
					put(t, payload, releaseInputTestBody)
				}
				before := files["nested/payload"]
				if e = os.Chtimes(payload, before.ModTime(), before.ModTime()); e != nil {
					t.Fatal(e)
				}
				after, e := os.Lstat(payload)
				if e != nil || !sameVersion(before, after) {
					t.Fatal("control must satisfy the old inode/size/mtime predicate", e)
				}
			case "mode":
				e = os.Chmod(payload, 0644)
			case "hardlink":
				e = os.Link(payload, filepath.Join(filepath.Dir(directory), "additional-link"))
			case "nested_replacement", "root_replacement":
				selected := nested
				if kind == "root_replacement" {
					selected = directory
				}
				if e = os.Rename(selected, selected+".original"); e != nil {
					t.Fatal(e)
				}
				e = os.Mkdir(selected, 0700)
			case "entry_added":
				e = os.WriteFile(filepath.Join(nested, "unknown"), []byte("synthetic"), 0600)
			case "entry_removed":
				e = os.Remove(payload)
			case "cancelled":
				cancel()
			}
			if e != nil {
				t.Fatal(e)
			}
			e = verifyPreparedSnapshot(ctx, root, files, directories)
			if kind == "stable" {
				if e != nil {
					t.Fatal("stable complete snapshot refused", e)
				}
			} else if e == nil {
				t.Fatal("changed or interrupted snapshot accepted")
			}
			if _, e := os.Lstat(directory); e != nil {
				t.Fatal("verification removed the owned diagnostic directory", e)
			}
		})
	}
}

const preparedFIFOChildMarker = "PROJECT_ONE_TEST_PREPARED_FIFO_CHILD"

func TestPreparedCheckedOpenRefusesFIFO(t *testing.T) {
	for _, kind := range []string{"directory", "payload"} {
		t.Run(kind, func(t *testing.T) {
			if os.Getenv(preparedFIFOChildMarker) == kind {
				name := releaseInputFile(t, 0600, 0700)
				if kind == "directory" {
					directory := filepath.Dir(name)
					before, e := os.Lstat(directory)
					if e != nil {
						t.Fatal(e)
					}
					if e = os.Rename(directory, directory+".original"); e != nil {
						t.Fatal(e)
					}
					if e = syscall.Mkfifo(directory, 0600); e != nil {
						t.Fatal(e)
					}
					root, e := openPreparedDirectory(directory, before)
					preparedDirectoryRefusal(t, root, e)
				} else {
					maximum := int64(len(releaseInputTestBody))
					root, before, e := pinRegularInput(name, maximum, true, 1)
					if e != nil {
						t.Fatal(e)
					}
					defer releaseInputCloseRoot(t, root)
					if e = os.Remove(name); e != nil {
						t.Fatal(e)
					}
					if e = syscall.Mkfifo(name, 0600); e != nil {
						t.Fatal(e)
					}
					e = readPreparedFile(context.Background(), root, filepath.Base(name), before, maximum, wire.Digest(releaseInputTestBody), wire.Asset{})
					if e == nil || e.Error() != "RELEASE_PREPARED_FILE_CHANGED" {
						t.Fatal("postcheck FIFO not refused", e)
					}
				}
				t.Log("PREPARED_ACTUAL_FIFO_REFUSED")
				return
			}
			executable, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			scratch, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestPreparedCheckedOpenRefusesFIFO$/^"+kind+"$", "-test.count=1", "-test.timeout=3s", "-test.v")
			command.WaitDelay = time.Second
			for _, value := range os.Environ() {
				if !strings.HasPrefix(value, preparedFIFOChildMarker+"=") && !strings.HasPrefix(value, "TMPDIR=") {
					command.Env = append(command.Env, value)
				}
			}
			command.Env = append(command.Env, preparedFIFOChildMarker+"="+kind, "TMPDIR="+scratch)
			output, e := command.CombinedOutput()
			if e != nil || ctx.Err() != nil || command.ProcessState == nil || !command.ProcessState.Success() || bytes.Count(output, []byte("PREPARED_ACTUAL_FIFO_REFUSED")) != 1 {
				t.Fatalf("FIFO child failed or did not join: %v; %s", e, output)
			}
		})
	}
}

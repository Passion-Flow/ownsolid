package factory

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var releaseInputTestBody = []byte("synthetic release input; no credential or provider\n")

func releaseInputFile(t *testing.T, mode, parentMode os.FileMode) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "selected")
	if err = os.Mkdir(parent, parentMode); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(parent, parentMode); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(parent, "input.json")
	if err = os.WriteFile(name, releaseInputTestBody, mode); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(name, mode); err != nil {
		t.Fatal(err)
	}
	return name
}

func releaseInputRefusal(t *testing.T, file *os.File, err error, code string) {
	t.Helper()
	if file != nil {
		file.Close()
		t.Fatal("refusal exposed an input descriptor")
	}
	if err == nil || err.Error() != code {
		t.Fatalf("expected fixed refusal %s, got %v", code, err)
	}
}

func releaseInputCloseRoot(t *testing.T, root *os.Root) {
	t.Helper()
	if err := root.Close(); err != nil {
		t.Error("pinned directory close failed", err)
	}
	if _, err := root.Stat("."); err == nil {
		t.Error("closed directory remained usable")
	}
}

func TestReleasePinnedRegularInputAdmission(t *testing.T) {
	for _, value := range []struct {
		name       string
		mode       os.FileMode
		parentMode os.FileMode
		private    bool
	}{
		{"private_0600", 0600, 0700, true},
		{"private_read_only_0400", 0400, 0700, true},
		{"public_0644_parent_0755", 0644, 0755, false},
		{"public_read_only_0444", 0444, 0755, false},
		{"public_executable_0755", 0755, 0755, false},
	} {
		t.Run(value.name, func(t *testing.T) {
			name := releaseInputFile(t, value.mode, value.parentMode)
			raw, err := readRegular(name, int64(len(releaseInputTestBody)), value.private)
			if err != nil || !bytes.Equal(raw, releaseInputTestBody) {
				t.Fatal("stable input refused", err)
			}
			parent, err := os.Stat(filepath.Dir(name))
			if err != nil || parent.Mode().Perm() != value.parentMode {
				t.Fatal("input read changed the existing directory policy", err)
			}
		})
	}
	t.Run("explicit_archive_two_links", func(t *testing.T) {
		name := releaseInputFile(t, 0600, 0700)
		if err := os.Link(name, name+".incomplete"); err != nil {
			t.Fatal(err)
		}
		file, err := openRegularLinks(name, int64(len(releaseInputTestBody)), true, 2)
		if err != nil {
			t.Fatal("explicit archive publication link count refused", err)
		}
		raw, readError := io.ReadAll(file)
		closeError := file.Close()
		if readError != nil || closeError != nil || !bytes.Equal(raw, releaseInputTestBody) {
			t.Fatal("explicit archive input changed", readError, closeError)
		}
		file, err = openRegular(name, int64(len(releaseInputTestBody)), true)
		releaseInputRefusal(t, file, err, "RELEASE_HARDLINK_REFUSED")
	})
	for _, kind := range []string{
		"relative", "unclean", "missing", "directory", "fifo", "empty", "oversized", "zero_maximum", "negative_maximum",
		"symlink_file", "symlink_parent", "group_writable", "world_writable",
		"private_group_readable", "private_world_readable", "unknown_three_links", "invalid_link_budget",
	} {
		t.Run(kind, func(t *testing.T) {
			name := releaseInputFile(t, 0600, 0700)
			maximum, links := int64(len(releaseInputTestBody)), uint64(1)
			private := true
			code := "RELEASE_REGULAR_FILE_REQUIRED"
			switch kind {
			case "relative":
				name = filepath.Base(name)
			case "unclean":
				name = filepath.Dir(name) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(name)
			case "missing":
				name += ".missing"
			case "directory", "fifo":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				var err error
				if kind == "directory" {
					err = os.Mkdir(name, 0700)
				} else {
					err = syscall.Mkfifo(name, 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "empty":
				if err := os.Truncate(name, 0); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				maximum--
			case "zero_maximum":
				maximum = 0
			case "negative_maximum":
				maximum = -1
			case "symlink_file":
				if err := os.Symlink(filepath.Base(name), name+".link"); err != nil {
					t.Fatal(err)
				}
				name += ".link"
			case "symlink_parent":
				parent := filepath.Dir(name)
				if err := os.Symlink(parent, parent+".link"); err != nil {
					t.Fatal(err)
				}
				name = filepath.Join(parent+".link", filepath.Base(name))
			case "group_writable", "world_writable", "private_group_readable", "private_world_readable":
				mode := map[string]os.FileMode{"group_writable": 0620, "world_writable": 0602, "private_group_readable": 0640, "private_world_readable": 0604}[kind]
				private = kind != "group_writable" && kind != "world_writable"
				if err := os.Chmod(name, mode); err != nil {
					t.Fatal(err)
				}
			case "unknown_three_links":
				for _, suffix := range []string{".one", ".two"} {
					if err := os.Link(name, name+suffix); err != nil {
						t.Fatal(err)
					}
				}
				links, code = 2, "RELEASE_HARDLINK_REFUSED"
			case "invalid_link_budget":
				links, code = 0, "RELEASE_HARDLINK_REFUSED"
			}
			file, err := openRegularLinks(name, maximum, private, links)
			releaseInputRefusal(t, file, err, code)
			if kind == "unknown_three_links" {
				file, err = openRegularLinks(name, maximum, private, 3)
				releaseInputRefusal(t, file, err, "RELEASE_HARDLINK_REFUSED")
			}
		})
	}
}

const releaseFIFOChildMarker = "PROJECT_ONE_TEST_RELEASE_PINNED_FIFO_CHILD"

func releasePostcheckFIFO(t *testing.T) {
	t.Helper()
	name := releaseInputFile(t, 0600, 0700)
	maximum := int64(len(releaseInputTestBody))
	root, before, err := pinRegularInput(name, maximum, true, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseInputCloseRoot(t, root)
	if err = os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Mkfifo(name, 0600); err != nil {
		t.Fatal(err)
	}
	// This invokes the real production open after the actual precheck. An
	// ordinary blocking open would hang here and fail the joined parent control.
	file, err := openPinnedRegular(root, filepath.Base(name), before, maximum, true, 1)
	releaseInputRefusal(t, file, err, "RELEASE_INPUT_CHANGED")
}

func TestReleasePinnedOpenRefusesActualPostcheckReplacements(t *testing.T) {
	t.Run("fifo", func(t *testing.T) {
		if os.Getenv(releaseFIFOChildMarker) == "1" {
			releasePostcheckFIFO(t)
			t.Log("RELEASE_ACTUAL_POSTCHECK_FIFO_REFUSED_AND_CLOSED")
			return
		}
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		childScratch, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(childScratch, 0700); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(ctx, executable, "-test.run=^TestReleasePinnedOpenRefusesActualPostcheckReplacements$/^fifo$", "-test.count=1", "-test.timeout=3s", "-test.v")
		command.WaitDelay = time.Second
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, releaseFIFOChildMarker+"=") && !strings.HasPrefix(value, "TMPDIR=") {
				command.Env = append(command.Env, value)
			}
		}
		// The parent owns this exact child scratch and cleans it only after
		// CombinedOutput joins the child, including a failed/forced-close run.
		command.Env = append(command.Env, releaseFIFOChildMarker+"=1", "TMPDIR="+childScratch)
		output, err := command.CombinedOutput()
		// CombinedOutput waits for child exit, including forced cleanup on
		// deadline. A timeout, signal or test failure is never a successful case.
		if err != nil || ctx.Err() != nil || command.ProcessState == nil || !command.ProcessState.Success() || bytes.Count(output, []byte("RELEASE_ACTUAL_POSTCHECK_FIFO_REFUSED_AND_CLOSED")) != 1 {
			t.Fatalf("actual postcheck FIFO child failed or did not join: %v; %s", err, output)
		}
	})
	for _, kind := range []string{"regular", "symlink", "mode", "link", "parent"} {
		t.Run(kind, func(t *testing.T) {
			name := releaseInputFile(t, 0600, 0755)
			maximum := int64(len(releaseInputTestBody))
			root, before, err := pinRegularInput(name, maximum, true, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer releaseInputCloseRoot(t, root)
			switch kind {
			case "regular", "symlink":
				if err = os.Rename(name, name+".original"); err != nil {
					t.Fatal(err)
				}
				if kind == "regular" {
					err = os.WriteFile(name, bytes.Repeat([]byte("x"), len(releaseInputTestBody)), 0600)
				} else {
					// Relative and within the pinned root: Go Root may resolve
					// this internally. The final named Lstat must still refuse it.
					err = os.Symlink(filepath.Base(name)+".original", name)
				}
			case "mode":
				err = os.Chmod(name, 0644)
			case "link":
				err = os.Link(name, name+".unapproved")
			case "parent":
				parent := filepath.Dir(name)
				if err = os.Rename(parent, parent+".original"); err != nil {
					t.Fatal(err)
				}
				if err = os.Mkdir(parent, 0755); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(name, bytes.Repeat([]byte("x"), len(releaseInputTestBody)), 0600); err != nil {
					t.Fatal(err)
				}
				pinned, statError := root.Lstat(filepath.Base(name))
				if statError != nil || !os.SameFile(before, pinned) {
					t.Fatal("pinned directory switched to replacement", statError)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			file, err := openPinnedRegular(root, filepath.Base(name), before, maximum, true, 1)
			releaseInputRefusal(t, file, err, "RELEASE_INPUT_CHANGED")
		})
	}
	t.Run("stable_descriptor_survives_root_close", func(t *testing.T) {
		name := releaseInputFile(t, 0644, 0755)
		file, err := openRegular(name, int64(len(releaseInputTestBody)), false)
		if err != nil {
			t.Fatal(err)
		}
		raw, readError := io.ReadAll(file)
		closeError := file.Close()
		if readError != nil || closeError != nil || !bytes.Equal(raw, releaseInputTestBody) {
			t.Fatal("returned descriptor invalid after directory close", readError, closeError)
		}
		if _, err := file.Stat(); err == nil {
			t.Fatal("closed input descriptor remained usable")
		}
	})
}

type releaseInputNativeFacts struct {
	os.FileInfo
	native any
}

func (value releaseInputNativeFacts) Sys() any { return value.native }

func TestReleaseReadSnapshotRejectsRestoredMtimeRewrite(t *testing.T) {
	t.Run("actual_equal_size_same_inode_restored_mtime", func(t *testing.T) {
		name := releaseInputFile(t, 0600, 0700)
		maximum := int64(len(releaseInputTestBody))
		root, before, err := pinRegularInput(name, maximum, true, 1)
		if err != nil {
			t.Fatal(err)
		}
		defer releaseInputCloseRoot(t, root)
		changed := bytes.Repeat([]byte("z"), len(releaseInputTestBody))
		// Some Linux filesystems reuse a timestamp within the same clock tick.
		// Establish the actual changed-ctime premise rather than assuming each
		// immediate write must advance it. Production snapshot guards stay strict.
		deadline := time.Now().Add(2 * time.Second)
		var after os.FileInfo
		writes := 0
		for {
			writer, err := os.OpenFile(name, os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0)
			if err != nil {
				t.Fatal(err)
			}
			written, writeError := writer.Write(changed)
			syncError := writer.Sync()
			closeError := writer.Close()
			if writeError != nil || syncError != nil || closeError != nil || written != len(changed) {
				t.Fatal("controlled actual rewrite failed", writeError, syncError, closeError)
			}
			if err = os.Chtimes(name, before.ModTime(), before.ModTime()); err != nil {
				t.Fatal(err)
			}
			writes++
			after, err = root.Lstat(filepath.Base(name))
			if err != nil || !sameVersion(before, after) {
				t.Fatal("rewrite did not reproduce old same-inode/size/mtime predicate", err)
			}
			if !sameRegularChangeTime(before, after) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("fixture could not observe an actual change-time tick")
			}
			time.Sleep(time.Millisecond)
		}
		t.Logf("observed actual change-time advance after %d controlled writes", writes)
		if sameRegularChangeTime(before, after) || sameRegularSnapshot(before, after, maximum, true, 1) {
			t.Fatal("actual rewrite with restored mtime escaped strict change-time guard")
		}
		file, err := openPinnedRegular(root, filepath.Base(name), before, maximum, true, 1)
		releaseInputRefusal(t, file, err, "RELEASE_INPUT_CHANGED")
		fresh, err := readRegular(name, maximum, true)
		if err != nil || !bytes.Equal(fresh, changed) {
			t.Fatal("fresh stable snapshot did not recover after explicit retry", err)
		}
	})
	t.Run("missing_native_facts_refuse", func(t *testing.T) {
		name := releaseInputFile(t, 0600, 0700)
		before, err := os.Lstat(name)
		if err != nil {
			t.Fatal(err)
		}
		maximum := int64(len(releaseInputTestBody))
		for _, unavailable := range []os.FileInfo{nil, releaseInputNativeFacts{before, nil}, releaseInputNativeFacts{before, (*syscall.Stat_t)(nil)}} {
			if sameRegularChangeTime(before, unavailable) || sameRegularChangeTime(unavailable, before) || regularInput(unavailable, maximum, true, 1) || sameRegularSnapshot(before, unavailable, maximum, true, 1) {
				t.Fatal("missing native facts accepted")
			}
		}
	})
	t.Run("synthetic_foreign_owner_refuses", func(t *testing.T) {
		name := releaseInputFile(t, 0600, 0700)
		before, err := os.Lstat(name)
		if err != nil {
			t.Fatal(err)
		}
		facts, ok := before.Sys().(*syscall.Stat_t)
		if !ok || facts == nil {
			t.Fatal("native stat facts unavailable")
		}
		// This is an explicitly synthetic native-fact test, not a real chown or
		// another account. No system account or file ownership is modified.
		foreign := *facts
		foreign.Uid++
		if regularInput(releaseInputNativeFacts{before, &foreign}, int64(len(releaseInputTestBody)), true, 1) {
			t.Fatal("foreign input owner accepted")
		}
	})
}

func TestManifestHashRejectsEqualLengthInPlaceRewriteIndependentlyOfChangeTime(t *testing.T) {
	c, manifest, _ := setup(t)
	var name string
	for _, asset := range manifest.Assets {
		if asset.ID == "fixture" {
			name = filepath.Join(c.ArtifactRoot, asset.Path)
		}
	}
	before, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Repeat([]byte("z"), int(before.Size()))
	writer, err := os.OpenFile(name, os.O_WRONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	n, we := writer.Write(changed)
	se, ce := writer.Sync(), writer.Close()
	if we != nil || se != nil || ce != nil || n != len(changed) {
		t.Fatal("controlled equal-length rewrite failed", we, se, ce)
	}
	if err = os.Chtimes(name, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(name)
	if err != nil || !sameVersion(before, after) {
		t.Fatal("same inode/size/mtime fixture required", err)
	}
	// Do not require any ctime difference. The signed expected hash is an
	// independent refusal boundary even when metadata facts cannot distinguish it.
	result, err := Validate(t.Context(), c)
	if err == nil || len(result.Issues) != 1 || result.Issues[0].AssetID != "fixture" || result.Issues[0].Code != "RELEASE_ASSET_HASH_INVALID" {
		t.Fatal("changed bytes escaped expected manifest hash", result.Issues, err)
	}
}

package scope

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mvpNativeRestoreCustodyRoot(t *testing.T) *os.Root {
	t.Helper()
	name := t.TempDir()
	if e := os.Chmod(name, 0700); e != nil {
		t.Fatal(e)
	}
	root, e := os.OpenRoot(name)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

func TestMVPNativeRestoreObservationCustodyRetainsExactOriginalAndReadonlyReopen(t *testing.T) {
	// Real private-file/fsync/reopen checks, with synthetic native output. This
	// does not establish native SQL, a signed source or scope/Engine integration.
	root := mvpNativeRestoreCustodyRoot(t)
	source, value := mvpNativeRestoreFixture()
	operationID := strings.Repeat("a", 32)
	raw := append([]byte(" \n"), encode(value)...)
	file, first, e := retainMVPNativeRestoreObservation(context.Background(), root, operationID, raw, 1<<20, value.RestoreID, value.NativePostgreSQL.RequestSHA256, source, nil)
	if e != nil || !bytes.Equal(first.Original, raw) {
		t.Fatal("original output custody failed", e)
	}
	before, e := root.Stat(file.Name)
	if e != nil || before.Mode() != 0600 {
		t.Fatal("private original unavailable", e)
	}
	reopened, e := os.OpenRoot(root.Name())
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	got, e := readMVPNativeRestoreObservation(context.Background(), reopened, operationID, file, 1<<20, value.RestoreID, value.NativePostgreSQL.RequestSHA256, source, first.NativePostgreSQL)
	after, statError := root.Stat(file.Name)
	if e != nil || statError != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() || !bytes.Equal(got.Original, raw) {
		t.Fatal("readonly reopen changed original custody", e, statError)
	}
	_, fresh := mvpNativeRestoreFixture()
	fresh.ObservedAt = "2026-11-03T00:01:00Z"
	fresh.NativePostgreSQL.ObservationSHA256 = strings.Repeat("e", 64)
	secondFile, second, e := retainMVPNativeRestoreObservation(context.Background(), root, strings.Repeat("b", 32), encode(fresh), 1<<20, value.RestoreID, value.NativePostgreSQL.RequestSHA256, source, first.NativePostgreSQL)
	if e != nil || secondFile.Name == file.Name || secondFile.SHA256 == file.SHA256 || !sameMVPNativeRestore(first.NativePostgreSQL, second.NativePostgreSQL) {
		t.Fatal("fresh observation did not retain separate original bytes", e)
	}
	original, e := os.ReadFile(filepath.Join(root.Name(), file.Name))
	if e != nil || !bytes.Equal(original, raw) {
		t.Fatal("fresh observation replaced original output", e)
	}
}

func TestMVPNativeRestoreObservationCustodyRejectsChangedRecordsWithoutRepair(t *testing.T) {
	for _, mutation := range []string{"digest", "size", "path", "raw", "mode", "hardlink", "symlink", "missing", "root-mode", "root-replacement"} {
		t.Run(mutation, func(t *testing.T) {
			root := mvpNativeRestoreCustodyRoot(t)
			source, value := mvpNativeRestoreFixture()
			id := strings.Repeat("a", 32)
			file, _, e := retainMVPNativeRestoreObservation(context.Background(), root, id, encode(value), 1<<20, value.RestoreID, value.NativePostgreSQL.RequestSHA256, source, nil)
			if e != nil {
				t.Fatal(e)
			}
			name := filepath.Join(root.Name(), file.Name)
			switch mutation {
			case "digest":
				file.SHA256 = strings.Repeat("0", 64)
			case "size":
				file.Bytes++
			case "path":
				file.Name = "../" + file.Name
			case "raw":
				e = os.WriteFile(name, []byte("changed original stdout\n"), 0600)
			case "mode":
				e = os.Chmod(name, 0644)
			case "hardlink":
				e = os.Link(name, filepath.Join(root.Name(), "retained-extra-link"))
			case "symlink":
				e = os.Rename(name, name+".retained")
				if e == nil {
					e = os.Symlink(name+".retained", name)
				}
			case "missing":
				e = os.Rename(name, name+".retained")
			case "root-mode":
				e = os.Chmod(root.Name(), 0755)
			case "root-replacement":
				retained := root.Name() + ".retained"
				e = os.Rename(root.Name(), retained)
				if e == nil {
					t.Cleanup(func() { os.RemoveAll(retained) })
					e = os.Mkdir(root.Name(), 0700)
				}
				if e == nil {
					e = os.WriteFile(name, encode(value), 0600)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			before, e := os.ReadDir(root.Name())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = readMVPNativeRestoreObservation(context.Background(), root, id, file, 1<<20, value.RestoreID, value.NativePostgreSQL.RequestSHA256, source, nil); e == nil {
				t.Fatal("changed original custody accepted")
			}
			after, e := os.ReadDir(root.Name())
			if e != nil || len(before) != len(after) {
				t.Fatal("refusal changed retained files", e)
			}
		})
	}
}

func TestMVPNativeRestoreObservationCustodyNeverOverwritesOrCompletesMissingProof(t *testing.T) {
	root := mvpNativeRestoreCustodyRoot(t)
	source, value := mvpNativeRestoreFixture()
	id := strings.Repeat("a", 32)
	raw := encode(value)
	file, _, e := retainMVPNativeRestoreObservation(context.Background(), root, id, raw, 1<<20, value.RestoreID, value.NativePostgreSQL.RequestSHA256, source, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = retainMVPNativeRestoreObservation(context.Background(), root, id, raw, 1<<20, value.RestoreID, value.NativePostgreSQL.RequestSHA256, source, nil); e == nil {
		t.Fatal("second publication overwrote original custody")
	}
	retained, e := os.ReadFile(filepath.Join(root.Name(), file.Name))
	if e != nil || !bytes.Equal(retained, raw) {
		t.Fatal("duplicate publication changed original", e)
	}
	value.NativePostgreSQL, value.OriginalCompletionPresent = nil, false
	_, partial, e := retainMVPNativeRestoreObservation(context.Background(), root, strings.Repeat("b", 32), encode(value), 1<<20, value.RestoreID, strings.Repeat("4", 64), source, nil)
	if e != nil || partial.NativePostgreSQL != nil || partial.OriginalCompletionPresent {
		t.Fatal("missing original completion was manufactured", e)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e = retainMVPNativeRestoreObservation(cancelled, root, strings.Repeat("c", 32), raw, 1<<20, value.RestoreID, strings.Repeat("4", 64), source, nil); e == nil {
		t.Fatal("cancelled output publication accepted")
	}
	if _, _, e = retainMVPNativeRestoreObservation(context.Background(), root, strings.Repeat("d", 32), []byte("{}"), 1<<20, value.RestoreID, strings.Repeat("4", 64), source, nil); e == nil {
		t.Fatal("invalid original output published")
	}
	entries, e := os.ReadDir(root.Name())
	if e != nil || len(entries) != 2 {
		t.Fatal("refused publication changed retained inventory", e)
	}
}

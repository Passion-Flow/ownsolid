package factory

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"strings"
	"testing"
)

func preparedFixture(t *testing.T) (Config, Result, string) {
	t.Helper()
	c, _, _ := setup(t)
	r, e := Build(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	return c, r, filepath.Join(filepath.Dir(c.OutputFile), "prepared")
}
func TestActualUnpackDurableFilesAndUnknownScopePreservation(t *testing.T) {
	c, r, destination := preparedFixture(t)
	result, e := Unpack(context.Background(), c, c.OutputFile, destination, r.ArchiveSHA256, r.ManifestSHA256)
	if e != nil || result.Status != "prepared_verified_inventory_only_not_installed" {
		t.Fatal(result, e)
	}
	for _, a := range r.Manifest.Assets {
		raw, e := os.ReadFile(filepath.Join(destination, a.Path))
		if e != nil || wire.Digest(raw) != a.SHA256 {
			t.Fatal("payload mismatch", e)
		}
		st, e := os.Stat(filepath.Join(destination, a.Path))
		if e != nil || st.Mode().Perm() != 0600 {
			t.Fatal("unsafe mode", e)
		}
	}
	marker, e := os.ReadFile(filepath.Join(destination, PreparedMarker))
	if e != nil || !bytes.Contains(marker, []byte(r.ArchiveSHA256)) {
		t.Fatal("marker missing", e)
	}
	journal, e := os.ReadFile(destination + ".journal.jsonl")
	if e != nil || !bytes.Contains(journal, []byte(`"status":"prepared_verified_inventory_only"`)) {
		t.Fatal("durable completion missing", e)
	}
	if _, e = Unpack(context.Background(), c, c.OutputFile, destination, r.ArchiveSHA256, r.ManifestSHA256); e == nil {
		t.Fatal("occupied destination reused")
	}
	again, _ := os.ReadFile(filepath.Join(destination, PreparedMarker))
	if !bytes.Equal(marker, again) {
		t.Fatal("occupied state modified")
	}
}
func TestPreparationIndependentDigestsAndOccupiedTargets(t *testing.T) {
	for _, fault := range []string{"archive-digest", "manifest-digest", "empty-digest", "fixture-disabled", "empty-existing", "symlink-existing", "journal-existing", "unmounted"} {
		t.Run(fault, func(t *testing.T) {
			c, r, destination := preparedFixture(t)
			archiveSHA, manifestSHA := r.ArchiveSHA256, r.ManifestSHA256
			switch fault {
			case "archive-digest":
				archiveSHA = strings.Repeat("a", 64)
			case "manifest-digest":
				manifestSHA = strings.Repeat("a", 64)
			case "empty-digest":
				archiveSHA = ""
			case "fixture-disabled":
				c.AllowTestFixture = false
			case "empty-existing":
				os.Mkdir(destination, 0700)
			case "symlink-existing":
				os.Symlink(filepath.Dir(destination), destination)
			case "journal-existing":
				put(t, destination+".journal.jsonl", []byte("unknown retained journal"))
			case "unmounted":
				c.MountRoot = filepath.Dir(c.OutputFile)
			}
			if _, e := Unpack(context.Background(), c, c.OutputFile, destination, archiveSHA, manifestSHA); e == nil {
				t.Fatal("unsafe unpack accepted")
			}
			if _, e := os.Stat(filepath.Join(destination, PreparedMarker)); !os.IsNotExist(e) {
				t.Fatal("unexpected completion marker")
			}
		})
	}
}
func TestPreparationFailureHasNoCompletionAndRetainsDiagnosis(t *testing.T) {
	for _, fault := range []string{"directory-sync", "bundle-mutated", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			c, r, destination := preparedFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ops := unpackIO{syncDirectory: func(f *os.File) error {
				if fault == "directory-sync" {
					return errors.New("explicit synthetic fsync failure")
				}
				return f.Sync()
			}, afterInspection: func() {
				if fault == "bundle-mutated" {
					raw, _ := os.ReadFile(c.OutputFile)
					raw[0] ^= 1
					put(t, c.OutputFile, raw)
				}
				if fault == "cancelled" {
					cancel()
				}
			}}
			if _, e := unpack(ctx, c, c.OutputFile, destination, r.ArchiveSHA256, r.ManifestSHA256, ops); e == nil {
				t.Fatal("failure passed")
			}
			if _, e := os.Stat(filepath.Join(destination, PreparedMarker)); !os.IsNotExist(e) {
				t.Fatal("failed preparation marked complete")
			}
			journal, e := os.ReadFile(destination + ".journal.jsonl")
			if e != nil || !bytes.Contains(journal, []byte(`"status":"failed_preserve_partial_or_uncertain_preparation"`)) {
				t.Fatal("failed journal missing", e)
			}
		})
	}
}
func TestConcurrentPreparationSingleWinner(t *testing.T) {
	c, r, destination := preparedFixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, e := Unpack(context.Background(), c, c.OutputFile, destination, r.ArchiveSHA256, r.ManifestSHA256)
			results <- e
		}()
	}
	close(start)
	passed := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			passed++
		}
	}
	if passed != 1 {
		t.Fatal("concurrent scope not exclusive", passed)
	}
}

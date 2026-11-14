package postgresbackup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPostgresBackupImmutableDerivedDirectoryCopyRetainsModesAndHashes(t *testing.T) {
	root := privateTestRoot(t)
	source := filepath.Join(root, "source")
	nested := filepath.Join(source, "derived", "accepted")
	if e := os.MkdirAll(nested, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(nested, "model.safetensors"), []byte("synthetic immutable tensor fixture"), 0444); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(nested, 0555); e != nil {
		t.Fatal(e)
	}
	// Cleanup belongs to this exact synthetic fixture, after all assertions.
	t.Cleanup(func() {
		_ = os.Chmod(nested, 0700)
		_ = os.Chmod(filepath.Join(root, "copy", "derived", "accepted"), 0700)
	})
	before, e := TreeDigest(context.Background(), source, 1<<20, 100, 16384)
	if e != nil {
		t.Fatal("existing owner0555 accepted below private root", e)
	}
	copied, e := copyTree(context.Background(), source, filepath.Join(root, "copy"), 1<<20, 100, 16384, 1<<20)
	if e != nil || copied != before {
		t.Fatal("immutable directory copy/readback", e)
	}
	for _, base := range []string{source, filepath.Join(root, "copy")} {
		for _, item := range []struct {
			path string
			mode os.FileMode
		}{{".", 0700}, {"derived", 0700}, {"derived/accepted", 0555}, {"derived/accepted/model.safetensors", 0444}} {
			info, e := os.Lstat(filepath.Join(base, item.path))
			if e != nil || info.Mode().Perm() != item.mode {
				t.Fatal("owner modes changed", base, item.path, e)
			}
		}
	}
	after, e := TreeDigest(context.Background(), source, 1<<20, 100, 16384)
	if e != nil || after != before {
		t.Fatal("source mutated", e)
	}
	for _, mode := range []os.FileMode{0755, 0777, 0500} {
		if e = os.Chmod(nested, mode); e != nil {
			t.Fatal(e)
		}
		if _, e = TreeDigest(context.Background(), source, 1<<20, 100, 16384); e == nil {
			t.Fatalf("unsupported descendant mode accepted: %o", mode)
		}
	}
	if e = os.Chmod(nested, 0555); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(source, 0555); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.Chmod(source, 0700) })
	if _, e = TreeDigest(context.Background(), source, 1<<20, 100, 16384); e == nil {
		t.Fatal("nonprivate top-level root accepted")
	}
}

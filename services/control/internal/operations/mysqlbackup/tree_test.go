package mysqlbackup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTreeReadbackUsesRealBytesPathsModesAndTotalLimits(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(root, "nested"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "nested", "value"), []byte("synthetic \u4e16\u754c bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	first, e := TreeDigest(t.Context(), root, 1024, 10, 4096)
	if e != nil || first.Files != 1 || first.Bytes != int64(len([]byte("synthetic \u4e16\u754c bytes"))) || len(first.SHA256) != 64 {
		t.Fatal("real tree readback", first, e)
	}
	second, e := TreeDigest(t.Context(), root, 1024, 10, 4096)
	if e != nil || first != second {
		t.Fatal("unchanged tree differs", e)
	}
	if e = os.WriteFile(filepath.Join(root, "nested", "value"), []byte("changed actual bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	second, e = TreeDigest(t.Context(), root, 1024, 10, 4096)
	if e != nil || first.SHA256 == second.SHA256 {
		t.Fatal("changed file adopted", e)
	}
	if proof, e := TreeDigest(t.Context(), root, 1, 10, 4096); e == nil || proof != (TreeReadback{}) {
		t.Fatal("byte limit projected success")
	}
	if e = os.WriteFile(filepath.Join(root, "second"), []byte("retained"), 0600); e != nil {
		t.Fatal(e)
	}
	if proof, e := TreeDigest(t.Context(), root, 1024, 1, 4096); e == nil || proof != (TreeReadback{}) {
		t.Fatal("file count limit projected success")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if proof, e := TreeDigest(ctx, root, 1024, 10, 4096); e == nil || proof != (TreeReadback{}) {
		t.Fatal("cancelled tree projected success")
	}
}

func TestTreeReadbackRejectsLinksAndForeignRootWithoutRemovingInputs(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "public_root"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if e := os.Chmod(root, 0700); e != nil {
				t.Fatal(e)
			}
			file := filepath.Join(root, "original")
			content := []byte("PRESERVE_SYNTHETIC_BYTES")
			if e := os.WriteFile(file, content, 0600); e != nil {
				t.Fatal(e)
			}
			var e error
			switch kind {
			case "symlink":
				e = os.Symlink(file, filepath.Join(root, "alias"))
			case "hardlink":
				e = os.Link(file, filepath.Join(root, "alias"))
			case "public_root":
				e = os.Chmod(root, 0755)
			}
			if e != nil {
				t.Fatal(e)
			}
			if proof, e := TreeDigest(t.Context(), root, 1024, 10, 4096); e == nil || proof != (TreeReadback{}) {
				t.Fatal("unsafe path accepted")
			}
			actual, e := os.ReadFile(file)
			if e != nil || string(actual) != string(content) {
				t.Fatal("refused data changed")
			}
		})
	}
}

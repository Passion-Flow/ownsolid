package components

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManagementPrerequisitesFailWithoutEffects(t *testing.T) {
	root := t.TempDir()
	journal := t.TempDir()
	_, e := New(Config{RootDirectory: root, ServiceUser: "root"}, journal)
	if e == nil {
		t.Fatal("unsupported host or root service identity accepted")
	}
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 0 {
		t.Fatal("preflight modified component files", e)
	}
}
func TestArtifactMustMatchCompiledRecipeBeforeExtraction(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "corrupt.tar.gz")
	if e := os.WriteFile(archive, []byte("synthetic invalid asset"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := extractArchive(archive, root); e == nil || e.Error() != "COMPONENT_ASSET_INVALID" {
		t.Fatal("corrupt asset accepted", e)
	}
	if e := DownloadTo(filepath.Join(root, "truncated"), &emptyReader{}); e == nil {
		t.Fatal("truncated download accepted")
	}
}

type emptyReader struct{}

func (*emptyReader) Read([]byte) (int, error) { return 0, errors.New("synthetic interrupted download") }

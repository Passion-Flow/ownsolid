package teststorage

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitFixtureStorageRecordsRealAncestorAndRejectsEscapes(t *testing.T) {
	if mode := os.Getenv("PROJECT_ONE_STORAGE_BOUNDARY_CHILD"); mode != "" {
		root := t.TempDir()
		if e := os.Chmod(root, 0700); e != nil {
			t.Fatal(e)
		}
		t.Setenv("PROJECT_ONE_TEST_STORAGE_ROOT", root)
		path := filepath.Join(root, "new-store")
		if mode == "escape" {
			path = filepath.Dir(root)
		}
		if mode == "link" {
			path = filepath.Join(root, "linked")
			if e := os.Symlink(filepath.Dir(root), path); e != nil {
				t.Fatal(e)
			}
		}
		Observe(t, "synthetic", path)
		return
	}
	for _, mode := range []string{"valid", "escape", "link"} {
		t.Run(mode, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestExplicitFixtureStorageRecordsRealAncestorAndRejectsEscapes$", "-test.v")
			command.Env = append(os.Environ(), "PROJECT_ONE_STORAGE_BOUNDARY_CHILD="+mode)
			output, e := command.CombinedOutput()
			if mode == "valid" {
				if e != nil || !strings.Contains(string(output), "TEMPORARY_STORAGE_MAPPING") {
					t.Fatal("actual mapping", e, string(output))
				}
			} else if e == nil || !strings.Contains(string(output), "TEMPORARY_TEST_STORE_") {
				t.Fatal("escape/link accepted", e, string(output))
			}
		})
	}
}

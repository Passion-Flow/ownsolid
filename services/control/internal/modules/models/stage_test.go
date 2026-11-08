package models

import (
	"os"
	"path/filepath"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

const testStage = "imports/stage-under-test"

// stageRoot opens a private store holding an empty stage directory.
func stageRoot(t *testing.T) (*os.Root, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "store")
	if e := os.MkdirAll(filepath.Join(path, testStage), 0700); e != nil {
		t.Fatal(e)
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { root.Close() })
	return root, filepath.Join(path, testStage)
}

// finishFile leaves one approved file as extraction does: full size and read-only.
// The size comes from a sparse file, because classifyStage reads no content.
func finishFile(t *testing.T, directory string, file wire.ModelFile) {
	t.Helper()
	path := filepath.Join(directory, file.Path)
	if e := os.WriteFile(path, nil, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Truncate(path, file.SizeBytes); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(path, 0444); e != nil {
		t.Fatal(e)
	}
}

func TestClassifyStage(t *testing.T) {
	files := wire.ApprovedModel().Files
	cases := []struct {
		name    string
		prepare func(t *testing.T, directory string)
		want    stageCondition
	}{
		{"an empty directory", func(*testing.T, string) {}, stageEmpty},
		{"a missing directory", func(t *testing.T, directory string) {
			if e := os.Remove(directory); e != nil {
				t.Fatal(e)
			}
		}, stageEmpty},
		{"every file finished", func(t *testing.T, directory string) {
			for _, file := range files {
				finishFile(t, directory, file)
			}
		}, stageFinished},
		{"a file still being written", func(t *testing.T, directory string) {
			if e := os.WriteFile(filepath.Join(directory, files[0].Path), []byte("part"), 0600); e != nil {
				t.Fatal(e)
			}
		}, stagePartial},
		{"finished files with one still to come", func(t *testing.T, directory string) {
			for _, file := range files[1:] {
				finishFile(t, directory, file)
			}
		}, stagePartial},
		{"a full-size file not yet read-only", func(t *testing.T, directory string) {
			for _, file := range files {
				finishFile(t, directory, file)
			}
			if e := os.Chmod(filepath.Join(directory, files[0].Path), 0600); e != nil {
				t.Fatal(e)
			}
		}, stagePartial},
		{"a file extraction never writes", func(t *testing.T, directory string) {
			if e := os.WriteFile(filepath.Join(directory, "unknown-sentinel"), []byte("keep"), 0600); e != nil {
				t.Fatal(e)
			}
		}, stageForeign},
		{"a link in place of an approved file", func(t *testing.T, directory string) {
			if e := os.Symlink("/etc/passwd", filepath.Join(directory, files[0].Path)); e != nil {
				t.Fatal(e)
			}
		}, stageForeign},
		{"an executable approved file", func(t *testing.T, directory string) {
			if e := os.WriteFile(filepath.Join(directory, files[0].Path), []byte("part"), 0700); e != nil {
				t.Fatal(e)
			}
		}, stageForeign},
		{"a second link to an approved file", func(t *testing.T, directory string) {
			path := filepath.Join(directory, files[0].Path)
			if e := os.WriteFile(path, []byte("part"), 0600); e != nil {
				t.Fatal(e)
			}
			if e := os.Link(path, filepath.Join(filepath.Dir(directory), "outside")); e != nil {
				t.Fatal(e)
			}
		}, stageForeign},
		{"a file in place of the directory", func(t *testing.T, directory string) {
			if e := os.Remove(directory); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(directory, nil, 0600); e != nil {
				t.Fatal(e)
			}
		}, stageForeign},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, directory := stageRoot(t)
			c.prepare(t, directory)
			got, e := classifyStage(root, testStage)
			if e != nil || got != c.want {
				t.Fatalf("condition %d, error %v; want %d", got, e, c.want)
			}
		})
	}
}

func TestRemoveStageDeletesApprovedFilesOnly(t *testing.T) {
	files := wire.ApprovedModel().Files
	t.Run("a part-written stage goes, read-only files included", func(t *testing.T) {
		root, directory := stageRoot(t)
		finishFile(t, directory, files[1])
		if e := os.WriteFile(filepath.Join(directory, files[0].Path), []byte("part"), 0600); e != nil {
			t.Fatal(e)
		}
		if e := removeStage(root, testStage); e != nil {
			t.Fatal(e)
		}
		if _, e := os.Lstat(directory); !os.IsNotExist(e) {
			t.Fatal("stage survived", e)
		}
	})
	t.Run("a stage with anything else stays whole", func(t *testing.T) {
		root, directory := stageRoot(t)
		kept := filepath.Join(directory, files[0].Path)
		if e := os.WriteFile(kept, []byte("part"), 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(directory, "unknown-sentinel"), []byte("keep"), 0600); e != nil {
			t.Fatal(e)
		}
		if e := removeStage(root, testStage); e == nil || e.Error() != "MODEL_CLEANUP_CONFLICT" {
			t.Fatal(e)
		}
		if _, e := os.Lstat(kept); e != nil {
			t.Fatal("an approved file went before the refusal", e)
		}
	})
}

func TestIsStageName(t *testing.T) {
	for name, want := range map[string]bool{"stage-1": true, "stage-2": true, "stage-3": true, "stage-0": false, "stage-4": false, "stage-": false, "stage-1x": false, "chunk-000000000000": false} {
		if isStageName(name) != want {
			t.Errorf("%s: want %t", name, want)
		}
	}
}

func TestDiscardUploadRefusesSymlinkedImportsDirectory(t *testing.T) {
	path := t.TempDir()
	id := "a87bb20c-438b-42fd-a640-73eb2cb7a375"
	other := filepath.Join(path, "other")
	upload := filepath.Join(other, id)
	if e := os.MkdirAll(upload, 0700); e != nil {
		t.Fatal(e)
	}
	chunk := filepath.Join(upload, "chunk-000000000000")
	if e := os.WriteFile(chunk, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("other", filepath.Join(path, "imports")); e != nil {
		t.Fatal(e)
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e = discardUpload(root, id, ChunkBytes); e == nil || e.Error() != "MODEL_PATH_INVALID" {
		t.Fatalf("symlinked imports directory: %v", e)
	}
	if got, e := os.ReadFile(chunk); e != nil || string(got) != "keep" {
		t.Fatalf("cleanup changed data outside imports: %q, %v", got, e)
	}
}

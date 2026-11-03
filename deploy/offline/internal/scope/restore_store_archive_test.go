package scope

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestRestoreStoreArchivePinnedDotRootAndEngineGuards(t *testing.T) {
	for _, role := range []string{"models", "datasets", "training", "preparations"} {
		t.Run(role, func(t *testing.T) {
			directory := t.TempDir()
			if e := os.Chmod(directory, 0700); e != nil {
				t.Fatal(e)
			}
			var raw bytes.Buffer
			w := tar.NewWriter(&raw)
			manifest := "snapshot.json"
			if role == "models" {
				manifest = "model-files.json"
			}
			for _, h := range []*tar.Header{
				{Name: "snapshot/", Mode: 0700, Typeflag: tar.TypeDir, Uid: 65532, Gid: 65532},
				{Name: "snapshot/" + manifest, Mode: 0600, Typeflag: tar.TypeReg, Uid: 65532, Gid: 65532, Size: 2},
			} {
				if e := w.WriteHeader(h); e != nil {
					t.Fatal(e)
				}
				if h.Size != 0 {
					if _, e := w.Write([]byte("{}")); e != nil {
						t.Fatal(e)
					}
				}
			}
			if e := w.Close(); e != nil {
				t.Fatal(e)
			}
			name := filepath.Join(directory, role+".tar")
			if e := os.WriteFile(name, raw.Bytes(), 0600); e != nil {
				t.Fatal(e)
			}
			root, e := openBackupDirectory(directory)
			if e != nil {
				t.Fatal(e)
			}
			defer root.Close()
			artifact := engine.StoreRestoreArtifact{Role: role, UID: 65532, GID: 65532, Bytes: int64(raw.Len()), SHA256: wire.Digest(raw.Bytes()), MaximumEntries: 100}
			dotted, e := root.Open(role + ".tar")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = engine.DescribeStoreRestoreArchive(context.Background(), dotted, artifact); e == nil || e.Error() != "OFFLINE_ENGINE_RESTORE_PRIVATE_ARCHIVE_REQUIRED" {
				t.Fatal("original noncanonical descriptor must remain refused", e)
			}
			dotted.Close()
			f, e := openRestoreStoreArchive(root, role)
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			if f.Name() != name {
				t.Fatal("canonical archive name required", f.Name())
			}
			if _, e = engine.DescribeStoreRestoreArchive(context.Background(), f, artifact); e != nil {
				t.Fatal("real Engine archive validation", e)
			}
			artifact.SHA256 = wire.Digest([]byte("changed"))
			if _, e = engine.DescribeStoreRestoreArchive(context.Background(), f, artifact); e == nil {
				t.Fatal("changed digest accepted")
			}
			artifact.SHA256 = wire.Digest(raw.Bytes())
			if e = os.Chmod(name, 0644); e != nil {
				t.Fatal(e)
			}
			if _, e = engine.DescribeStoreRestoreArchive(context.Background(), f, artifact); e == nil {
				t.Fatal("public archive accepted")
			}
			if e = os.Chmod(name, 0600); e != nil {
				t.Fatal(e)
			}
			if e = os.Link(name, name+"-linked"); e != nil {
				t.Fatal(e)
			}
			if _, e = engine.DescribeStoreRestoreArchive(context.Background(), f, artifact); e == nil {
				t.Fatal("multiply linked archive accepted")
			}
		})
	}
}

func TestRestoreStoreArchiveRejectsReplacedDirectoryAndSymlink(t *testing.T) {
	for _, fault := range []string{"directory", "symlink", "role"} {
		t.Run(fault, func(t *testing.T) {
			parent := t.TempDir()
			directory := filepath.Join(parent, "backup")
			if os.Mkdir(directory, 0700) != nil || os.WriteFile(filepath.Join(directory, "models.tar"), []byte("old"), 0600) != nil {
				t.Fatal("fixture")
			}
			root, e := openBackupDirectory(directory)
			if e != nil {
				t.Fatal(e)
			}
			defer root.Close()
			role := "models"
			switch fault {
			case "directory":
				if os.Rename(directory, directory+"-old") != nil || os.Mkdir(directory, 0700) != nil || os.WriteFile(filepath.Join(directory, "models.tar"), []byte("new"), 0600) != nil {
					t.Fatal("replacement fixture")
				}
			case "symlink":
				if os.Rename(filepath.Join(directory, "models.tar"), filepath.Join(directory, "other.tar")) != nil || os.Symlink("other.tar", filepath.Join(directory, "models.tar")) != nil {
					t.Fatal("symlink fixture")
				}
			case "role":
				role = "../models"
			}
			if f, e := openRestoreStoreArchive(root, role); e == nil {
				f.Close()
				t.Fatal("changed or unselected input accepted")
			}
		})
	}
}

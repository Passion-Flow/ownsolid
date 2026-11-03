package recovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestObjectSnapshotOperatorRefusesLinksAndPublicFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot")
	if e := os.WriteFile(path, []byte("synthetic only"), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := openArtifactSnapshot(path)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	link := filepath.Join(dir, "link")
	if e = os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if f, e = openArtifactSnapshot(link); e == nil || f != nil {
		t.Fatal("symlink admitted")
	}
	if e = os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if f, e = openArtifactSnapshot(path); e == nil || f != nil {
		t.Fatal("public file admitted")
	}
	if f, e = openArtifactSnapshot("relative/snapshot"); e == nil || f != nil {
		t.Fatal("relative file admitted")
	}
}
func TestObjectOperatorMissingAuthorityCannotProjectSuccess(t *testing.T) {
	if e := VerifyArtifactSnapshot(context.Background(), nil, "", "", "", "reason"); e == nil {
		t.Fatal("missing selected authority accepted")
	}
	if e := InitializeObjectStage(context.Background(), nil, "reason"); e == nil {
		t.Fatal("missing selected authority initialized files")
	}
}

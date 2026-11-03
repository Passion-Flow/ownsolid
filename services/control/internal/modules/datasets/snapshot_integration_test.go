//go:build dataset_processor_integration

package datasets

import (
	"context"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
)

func TestActualDatasetFileSnapshotExactReferencesPrivateRestoreAndCorruption(t *testing.T) {
	s, _, a := workerScenario(t)
	ctx := context.Background()
	data, d := importFixture(t, s, a, row("a", "one", "first")+row("b", "two", "second"))
	process(t, s)
	d, _ = s.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, d.ID)
	job, e := s.Publish(ctx, a, a.Me.Workspace.ID, data.ID, d.ID, PublicationRequest{ExpectedRevision: d.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Snapshot real immutable version"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	process(t, s)
	if _, e = s.RetireVersion(ctx, a, a.Me.Workspace.ID, data.ID, *job.VersionID, "Retain retired backup data", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	// These actual module calls run in a new isolated fixture with no serving process or background worker.
	destination := filepath.Join(privateSnapshotDirectory(t), "new-snapshot")
	if e = s.ExportFiles(ctx, destination); e != nil {
		t.Fatal(e)
	}
	if e = s.AuditFileOperation(ctx, "export", "Synthetic isolated snapshot export"); e != nil {
		t.Fatal(e)
	}
	snapshot, e := s.readSnapshot(ctx, destination)
	if e != nil || len(snapshot.References.Versions) != 1 || len(snapshot.Files) != 11 {
		t.Fatalf("snapshot inventory %d %v", len(snapshot.Files), e)
	}
	if e = s.ExportFiles(ctx, destination); e == nil || e.Error() != "DATASET_SNAPSHOT_DESTINATION_EXISTS" {
		t.Fatalf("overwrite %v", e)
	}
	fresh := &Service{DB: s.DB, Identity: s.Identity, Workspaces: s.Workspaces}
	target := privateSnapshotDirectory(t)
	if e = fresh.ImportFiles(ctx, destination, target); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(target, a.Me.Workspace.TenantID, 4096)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	if restored.Namespace() != s.Store.Namespace() {
		t.Fatal("store namespace lost")
	}
	fresh.Store = restored
	if e = fresh.VerifyStored(ctx); e != nil {
		t.Fatal(e)
	}
	if e = fresh.ImportFiles(ctx, destination, privateSnapshotDirectory(t)); e == nil || e.Error() != "DATASET_STORE_IN_USE" {
		t.Fatalf("live owner import %v", e)
	}
	fresh.Store = nil
	populated := privateSnapshotDirectory(t)
	os.WriteFile(filepath.Join(populated, "preserve"), []byte("unrelated"), 0600)
	if e = fresh.ImportFiles(ctx, destination, populated); e == nil || e.Error() != "DATASET_SNAPSHOT_DESTINATION_EXISTS" {
		t.Fatalf("populated restore %v", e)
	}
	raw, _ := os.ReadFile(filepath.Join(populated, "preserve"))
	if string(raw) != "unrelated" {
		t.Fatal("unrelated data modified")
	}
	file := snapshot.Files[len(snapshot.Files)-1]
	path := filepath.Join(destination, filepath.FromSlash(file.Path))
	original, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("corruption"), 0600); e != nil {
		t.Fatal(e)
	}
	empty := privateSnapshotDirectory(t)
	if e = fresh.ImportFiles(ctx, destination, empty); e == nil {
		t.Fatal("corrupt snapshot imported")
	}
	entries, _ := os.ReadDir(empty)
	if len(entries) != 0 {
		t.Fatal("unverified snapshot changed destination")
	}
	os.WriteFile(path, original, 0600)
	unknown := filepath.Join(destination, "unknown")
	os.WriteFile(unknown, []byte("preserve"), 0600)
	if _, e = s.readSnapshot(ctx, destination); e == nil {
		t.Fatal("unknown snapshot file accepted")
	}
	raw, _ = os.ReadFile(unknown)
	if string(raw) != "preserve" {
		t.Fatal("unknown file removed")
	}
}

func privateSnapshotDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if e := os.Chmod(directory, 0700); e != nil {
		t.Fatal(e)
	}
	return directory
}

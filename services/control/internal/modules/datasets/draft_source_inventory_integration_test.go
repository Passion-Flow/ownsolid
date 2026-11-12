//go:build dataset_processor_integration

package datasets

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/google/uuid"
)

func TestActualCSVDraftFileSnapshotAndRestore(t *testing.T) {
	service, _, actor := workerScenario(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	workspace := actor.Me.Workspace.ID
	data, err := service.Create(ctx, actor, CreateRequest{WorkspaceID: workspace,
		Name: "CSV snapshot fixture", Origin: "Synthetic fixture", License: "Synthetic CC0",
		RightsAsserted: true, Reason: "Verify original and normalized CSV custody"}, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := NewDraftSourceCandidate(service, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("source_id,prompt,completion\ncsv-row,question,answer\n")
	imported, err := candidate.Import(ctx, actor, workspace, data.ID, SourceCSV,
		DraftSourceRequest{Name: "source.csv", SHA256: originalInventoryDigest(raw), Bytes: int64(len(raw))},
		uuid.NewString(), bytes.NewReader(raw), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	process(t, service)
	before, err := candidate.Read(ctx, actor, workspace, data.ID, imported.Draft.ID)
	if err != nil || before.Draft.State != "editing" {
		t.Fatal("CSV source was not inspected", err)
	}
	backup := filepath.Join(privateSnapshotDirectory(t), "csv-files")
	if err = service.ExportFiles(ctx, backup); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.readSnapshot(ctx, backup)
	if err != nil || snapshot.SchemaVersion != originalSourceSnapshotSchema || len(snapshot.Files) != 3 {
		t.Fatal("CSV original was not inventoried", err)
	}
	if err = service.AuditFileOperation(ctx, "export", "Synthetic CSV source snapshot"); err != nil {
		t.Fatal(err)
	}
	restoredRoot := privateSnapshotDirectory(t)
	account := &identity.Service{DB: service.DB, Config: service.Identity.Config,
		Passwords: service.Identity.Passwords, HMACKey: service.Identity.HMACKey}
	account.Config.Datasets = service.Processor.configuration
	account.Config.Datasets.ArtifactRoot = restoredRoot
	account.BusinessAdmission, err = licensing.FromConfig(account)
	if err != nil {
		t.Fatal(err)
	}
	fresh := &Service{DB: service.DB, Identity: account, Workspaces: &workspaces.Service{DB: service.DB, Identity: account}}
	if err = fresh.ImportFiles(ctx, backup, restoredRoot); err != nil {
		t.Fatal(err)
	}
	if err = fresh.OpenConfiguredStore(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Store.Close() })
	reader, err := NewDraftSourceCandidate(fresh, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.Read(ctx, actor, workspace, data.ID, before.Draft.ID)
	if err != nil || !reflect.DeepEqual(got, before) {
		t.Fatal("CSV report changed after file restore", err)
	}
	var original string
	for _, file := range snapshot.Files {
		if filepath.Base(file.Path) == "source.csv" {
			original = filepath.Join(restoredRoot, filepath.FromSlash(file.Path))
		}
	}
	if original == "" {
		t.Fatal("CSV original role missing")
	}
	restored, err := os.ReadFile(original)
	if err != nil || !bytes.Equal(restored, raw) {
		t.Fatal("CSV original bytes changed", err)
	}
	if err = os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if err = fresh.VerifyStored(ctx); err == nil || err.Error() != "DATASET_FILE_INVALID" {
		t.Fatal("CSV readiness ignored a missing original", err)
	}
	if out, err := reader.Read(ctx, actor, workspace, data.ID, before.Draft.ID); err == nil || !reflect.DeepEqual(out, DraftSourceResult{}) {
		t.Fatal("missing CSV original returned a report", err)
	}
	if err = writeNew(original, raw); err != nil {
		t.Fatal(err)
	}
	if err = fresh.VerifyStored(ctx); err != nil {
		t.Fatal("CSV exact repair did not recover readiness", err)
	}
	if out, err := reader.Read(ctx, actor, workspace, data.ID, before.Draft.ID); err != nil || !reflect.DeepEqual(out, before) {
		t.Fatal("CSV exact repair did not recover the original report", err)
	}
}

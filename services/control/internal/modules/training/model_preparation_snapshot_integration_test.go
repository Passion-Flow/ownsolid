//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func TestActualCpuModelPreparationSnapshotExcludesUnacceptedUploadsAndPreservesNamespace(t *testing.T) {
	g, _, l, metadata := preparationOutputFixture(t)
	ctx := context.Background()
	if _, e := g.service.BeginUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, metadata); e != nil {
		t.Fatal(e)
	}
	parent := t.TempDir()
	os.Chmod(parent, 0700)
	snapshot := filepath.Join(parent, "accepted-only")
	if e := g.service.ExportFiles(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(snapshot, "snapshot.json"))
	if e != nil {
		t.Fatal(e)
	}
	var m struct {
		Files      []modelpreparations.SnapshotFile `json:"files"`
		References struct {
			Outputs []modelpreparations.UploadDescriptor `json:"outputs"`
		} `json:"references"`
	}
	if json.Unmarshal(raw, &m) != nil || len(m.Files) != 1 || m.Files[0].Path != "namespace.json" || len(m.References.Outputs) != 0 {
		t.Fatal("unfinished upload entered accepted snapshot")
	}
	destination := t.TempDir()
	os.Chmod(destination, 0700)
	copyService := *g.service
	copyService.Store = nil
	if e = copyService.ImportFiles(ctx, snapshot, destination); e != nil {
		t.Fatal(e)
	}
	store, e := modelpreparations.OpenStore(destination, g.f.admin.Me.Workspace.TenantID, 4096, wire.MaximumModelBytes, wire.MaximumModelFiles)
	if e != nil {
		t.Fatal(e)
	}
	copyService.Store = store
	if e = copyService.VerifyStored(ctx); e != nil {
		t.Fatal(e)
	}
	if store.Namespace() != g.store.Namespace() {
		t.Fatal("import replaced namespace identity")
	}
	store.Close()
	copyService.Store = nil
	if e = copyService.ImportFiles(ctx, snapshot, destination); e == nil {
		t.Fatal("populated destination adopted")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_artifacts") != 0 || count(t, g.f, "SELECT count(*) FROM model_preparation_uploads") != 1 || count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='held'", l.Preparation.Scope.PreparationID) != 1 {
		t.Fatal("file snapshot fabricated acceptance or physical stop")
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	refused := filepath.Join(parent, "cancelled")
	if e = g.service.ExportFiles(cancelled, refused); e == nil {
		t.Fatal("cancelled export passed")
	}
	if _, e = os.Lstat(refused); !os.IsNotExist(e) {
		t.Fatal("cancelled export changed destination")
	}
}

func TestActualCpuModelPreparationSnapshotRefusesChangedForeignUnknownAndLinkedSources(t *testing.T) {
	g := newPreparationScenario(t)
	ctx := context.Background()
	parent := t.TempDir()
	os.Chmod(parent, 0700)
	snapshot := filepath.Join(parent, "backup")
	if e := g.service.ExportFiles(ctx, snapshot); e != nil {
		t.Fatal(e)
	}
	metadata := filepath.Join(snapshot, "snapshot.json")
	raw, e := os.ReadFile(metadata)
	if e != nil {
		t.Fatal(e)
	}
	assertRefused := func() {
		t.Helper()
		target := t.TempDir()
		os.Chmod(target, 0700)
		probe := *g.service
		probe.Store = nil
		if probe.ImportFiles(ctx, snapshot, target) == nil {
			t.Fatal("invalid snapshot accepted")
		}
		entries, e := os.ReadDir(target)
		if e != nil || len(entries) != 0 {
			t.Fatal("invalid source mutated target", e)
		}
	}
	var original map[string]json.RawMessage
	if json.Unmarshal(raw, &original) != nil {
		t.Fatal("metadata")
	}
	original["unknown"] = json.RawMessage(`true`)
	changed, _ := json.Marshal(original)
	os.WriteFile(metadata, changed, 0600)
	assertRefused()
	os.WriteFile(metadata, raw, 0600)
	var data map[string]any
	json.Unmarshal(raw, &data)
	namespace := data["namespace"].(map[string]any)
	namespace["tenant_id"] = uuid.NewString()
	changed, _ = json.Marshal(data)
	os.WriteFile(metadata, changed, 0600)
	assertRefused()
	os.WriteFile(metadata, raw, 0600)
	unknown := filepath.Join(snapshot, "unknown")
	os.WriteFile(unknown, []byte("synthetic"), 0600)
	assertRefused()
	os.Remove(unknown)
	if e = os.Link(filepath.Join(snapshot, "namespace.json"), unknown); e != nil {
		t.Fatal(e)
	}
	assertRefused()
	os.Remove(unknown)
	if e = os.Symlink(filepath.Join(snapshot, "namespace.json"), unknown); e != nil {
		t.Fatal(e)
	}
	assertRefused()
	os.Remove(unknown)
	duplicate := append([]byte(`{"schema_version":1,`), raw[1:]...)
	os.WriteFile(metadata, duplicate, 0600)
	assertRefused()
}

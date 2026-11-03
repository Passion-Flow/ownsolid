//go:build model_lifecycle_output_storage_integration

package modelpreparations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// This is actual native CPU-produced merged-model storage, not business acceptance or NVIDIA execution.
func TestActualCpuPreparedModelPrivateChunkAssemblyPublicationReplayAndRestart(t *testing.T) {
	store, root, tenant := privatePreparationStore(t)
	ctx := context.Background()
	output := os.Getenv("PROJECT_ONE_ACTUAL_PREPARATION_OUTPUT")
	if output == "" {
		t.Fatal("actual passed native preparation output required")
	}
	metadata, e := os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil {
		t.Fatal(e)
	}
	var report wire.PreparedResult
	if json.Unmarshal(metadata, &report) != nil || report.Status != "prepared" || report.Device != trainingwire.VerificationProfile || report.ComputationDtype != "float32" || report.RowCount != 3 || report.EventCount != 9 {
		t.Fatal("actual declared native CPU report required")
	}
	d := modelpreparations.UploadDescriptor{SchemaVersion: 1, Scope: wire.Scope{TenantID: tenant, WorkspaceID: uuid.NewString(), PreparationID: uuid.NewString()}, UploadID: uuid.NewString(), AttemptID: uuid.NewString(), ManifestSHA256: preparationStorageDigest(metadata), Files: report.ModelFiles}
	if e = store.PrepareUpload(ctx, d, metadata); e != nil {
		t.Fatal(e)
	}
	if _, e = store.PreparePublishedFiles(ctx, d); e == nil {
		t.Fatal("incomplete model was published")
	}
	var chunks int
	var total int64
	for _, asset := range d.Files {
		source, e := os.Open(filepath.Join(output, "model", asset.Path))
		if e != nil {
			t.Fatal(e)
		}
		for offset := int64(0); offset < asset.SizeBytes; offset += wire.TransferChunkBytes {
			length := min(wire.TransferChunkBytes, asset.SizeBytes-offset)
			body, e := io.ReadAll(io.NewSectionReader(source, offset, length))
			if e != nil || int64(len(body)) != length {
				source.Close()
				t.Fatal("actual producer range", e)
			}
			if _, e = store.WriteChunk(ctx, d, asset.Path, offset, length, preparationStorageDigest(body), bytes.NewReader(body)); e != nil {
				source.Close()
				t.Fatal("real bounded private chunk", asset.Path, offset, e)
			}
			chunks++
			total += length
		}
		source.Close()
	}
	if total != 1208042441 || chunks != 152 {
		t.Fatal("declared actual workload changed", total, chunks)
	}
	location, e := store.PreparePublishedFiles(ctx, d)
	if e != nil || location != filepath.Join("artifacts", d.Scope.PreparationID, d.AttemptID, d.UploadID) {
		t.Fatal("actual complete private publication", e)
	}
	if e = store.Close(); e != nil {
		t.Fatal(e)
	}
	store, e = modelpreparations.OpenStore(root, tenant, 4096, wire.MaximumModelBytes, wire.MaximumModelFiles)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	file, asset, e := store.OpenPublishedAsset(ctx, d, "model.safetensors")
	if e != nil {
		t.Fatal("actual published model after restart", e)
	}
	info, e := file.Stat()
	file.Close()
	if e != nil || info.Size() != asset.SizeBytes {
		t.Fatal("published immutable model size", e)
	}
	if again, e := store.PreparePublishedFiles(ctx, d); e != nil || again != location {
		t.Fatal("exact publication replay", e)
	}
	publishedReport, e := os.ReadFile(filepath.Join(root, location, "result.json"))
	if e != nil || !bytes.Equal(publishedReport, metadata) {
		t.Fatal("original report provenance changed", e)
	}
	t.Logf("actual native model files=%d bytes=%d complete_chunks=%d; no business acceptance or GPU qualification", len(d.Files), total, chunks)
}

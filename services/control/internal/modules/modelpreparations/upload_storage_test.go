package modelpreparations_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func preparationStorageDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

// Only private storage is under test. The tiny tensor/report payloads are explicit synthetic fixtures,
// never prepared reports or independently accepted/servable model artifacts.
func preparationUploadFixture(tenant string) (modelpreparations.UploadDescriptor, []byte, []byte) {
	body := bytes.Repeat([]byte("synthetic private chunk bytes"), 8)
	metadata := []byte("{\n  \"synthetic_storage_fixture\": true\n}\n")
	files := wire.ApprovedBaseFiles()
	for index := range files {
		if files[index].Path == "model.safetensors" {
			files[index].SizeBytes = int64(len(body))
			files[index].SHA256 = preparationStorageDigest(body)
		}
	}
	return modelpreparations.UploadDescriptor{SchemaVersion: 1, Scope: wire.Scope{TenantID: tenant, WorkspaceID: uuid.NewString(), PreparationID: uuid.NewString()}, UploadID: uuid.NewString(), AttemptID: uuid.NewString(), ManifestSHA256: preparationStorageDigest(metadata), Files: files}, metadata, body
}

func TestPreparationUploadPrivateReportExactReplayChunkPersistenceAndBodyVerification(t *testing.T) {
	store, root, tenant := privatePreparationStore(t)
	d, metadata, body := preparationUploadFixture(tenant)
	ctx := context.Background()
	if e := store.PrepareUpload(ctx, d, metadata); e != nil {
		t.Fatal(e)
	}
	if e := store.PrepareUpload(ctx, d, metadata); e != nil {
		t.Fatal("exact stage replay", e)
	}
	raw, e := os.ReadFile(filepath.Join(root, "uploads", d.UploadID, "result.json"))
	if e != nil || !bytes.Equal(raw, metadata) {
		t.Fatal("report bytes reserialized", e)
	}
	sha := preparationStorageDigest(body)
	receipt, e := store.WriteChunk(ctx, d, "model.safetensors", 0, int64(len(body)), sha, bytes.NewReader(body))
	if e != nil || receipt.Duplicate {
		t.Fatal("actual private fsynced chunk", e)
	}
	if e = store.VerifyChunk(ctx, d, receipt); e != nil {
		t.Fatal("retained chunk", e)
	}
	duplicate, e := store.WriteChunk(ctx, d, "model.safetensors", 0, int64(len(body)), sha, bytes.NewReader(body))
	if e != nil || !duplicate.Duplicate {
		t.Fatal("exact chunk replay", e)
	}
	for _, invalid := range [][]byte{body[:len(body)-1], append(append([]byte{}, body...), 1), bytes.Repeat([]byte("x"), len(body))} {
		if _, e = store.WriteChunk(ctx, d, "model.safetensors", 0, int64(len(body)), sha, bytes.NewReader(invalid)); e == nil {
			t.Fatal("replay did not verify exact new body")
		}
	}
	if e = store.Close(); e != nil {
		t.Fatal(e)
	}
	store, e = modelpreparations.OpenStore(root, tenant, 4096, wire.MaximumModelBytes, wire.MaximumModelFiles)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	if e = store.VerifyChunk(ctx, d, receipt); e != nil {
		t.Fatal("private chunk failed restart", e)
	}
}

func TestPreparationUploadRejectsForeignIncompleteChangedReportAndUnapprovedFiles(t *testing.T) {
	store, root, tenant := privatePreparationStore(t)
	d, metadata, _ := preparationUploadFixture(tenant)
	ctx := context.Background()
	bad := d
	bad.Scope.TenantID = uuid.NewString()
	if e := store.PrepareUpload(ctx, bad, metadata); e == nil {
		t.Fatal("foreign tenant store stage")
	}
	bad = d
	bad.Files = append([]datawire.Asset{}, d.Files...)
	bad.Files[0].Path = "unsafe.py"
	if e := store.PrepareUpload(ctx, bad, metadata); e == nil {
		t.Fatal("unapproved file allowed")
	}
	bad = d
	bad.Files = d.Files[:len(d.Files)-1]
	if e := store.PrepareUpload(ctx, bad, metadata); e == nil {
		t.Fatal("incomplete full-model inventory")
	}
	bad = d
	bad.Files = append([]datawire.Asset{}, d.Files...)
	bad.Files[0].SizeBytes++
	if e := store.PrepareUpload(ctx, bad, metadata); e == nil {
		t.Fatal("changed non-target original asset")
	}

	if e := store.PrepareUpload(ctx, d, []byte(`{"changed":true}`)); e == nil {
		t.Fatal("unbound report stage")
	}
	if e := store.PrepareUpload(ctx, d, metadata); e != nil {
		t.Fatal(e)
	}
	report := filepath.Join(root, "uploads", d.UploadID, "result.json")
	if e := os.WriteFile(report, []byte(`{"tampered":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := store.PrepareUpload(ctx, d, metadata); e == nil {
		t.Fatal("changed report accepted on replay")
	}
	if e := os.WriteFile(report, metadata, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "uploads", d.UploadID, "unknown"), []byte("retained"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := store.PrepareUpload(ctx, d, metadata); e == nil {
		t.Fatal("unknown populated upload adopted")
	}
}

func TestPreparationUploadRefusesCancelledStreamsLinkedChunksAndTampering(t *testing.T) {
	store, root, tenant := privatePreparationStore(t)
	d, metadata, body := preparationUploadFixture(tenant)
	ctx := context.Background()
	if e := store.PrepareUpload(ctx, d, metadata); e != nil {
		t.Fatal(e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	sha := preparationStorageDigest(body)
	if _, e := store.WriteChunk(cancelled, d, "model.safetensors", 0, int64(len(body)), sha, bytes.NewReader(body)); e == nil {
		t.Fatal("cancelled stream wrote a chunk")
	}
	receipt, e := store.WriteChunk(ctx, d, "model.safetensors", 0, int64(len(body)), sha, bytes.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "uploads", d.UploadID, "chunks", "model.safetensors", "0000000000000000")
	alias := filepath.Join(t.TempDir(), "owned-test-alias")
	if e = os.Link(path, alias); e != nil {
		t.Fatal(e)
	}
	if e = store.VerifyChunk(ctx, d, receipt); e == nil {
		t.Fatal("linked chunk accepted")
	}
	if e = os.Remove(alias); e != nil {
		t.Fatal(e)
	}
	if e = store.VerifyChunk(ctx, d, receipt); e != nil {
		t.Fatal("owned test link removal repair", e)
	}
	if e = os.WriteFile(path, bytes.Repeat([]byte("x"), len(body)), 0600); e != nil {
		t.Fatal(e)
	}
	if e = store.VerifyChunk(ctx, d, receipt); e == nil {
		t.Fatal("corrupt canonical chunk accepted")
	}
	if _, e = store.WriteChunk(ctx, d, "model.safetensors", 0, int64(len(body)), sha, bytes.NewReader(body)); e == nil {
		t.Fatal("replay overwrote a corrupt canonical chunk")
	}
}

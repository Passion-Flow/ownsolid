package training_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func privateStore(t *testing.T) (*training.Store, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "store")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	tenant := uuid.NewString()
	if e := training.InitializeStore(root, tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := training.OpenStore(root, tenant, 4096, 67108864, 64)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	return store, root, tenant
}
func descriptor(tenant string) (training.UploadDescriptor, map[string][]byte) {
	// Explicit synthetic byte buffers exercise private transport only, not valid or accepted model artifacts.
	values := map[string][]byte{"adapter_model.safetensors": bytes.Repeat([]byte("x"), int(wire.TransferChunkBytes)+17), "adapter_config.json": []byte("{}"), "README.md": []byte("Synthetic private upload bytes")}
	d := training.UploadDescriptor{SchemaVersion: 1, Scope: wire.Scope{TenantID: tenant, WorkspaceID: uuid.NewString(), JobID: uuid.NewString()}, UploadID: uuid.NewString(), AttemptID: uuid.NewString(), Kind: "artifact", GlobalStep: 1, ManifestSHA256: digest([]byte("Synthetic immutable manifest"))}
	for _, name := range []string{"adapter_model.safetensors", "adapter_config.json", "README.md"} {
		raw := values[name]
		d.Files = append(d.Files, datawire.Asset{Path: name, SizeBytes: int64(len(raw)), SHA256: digest(raw)})
	}
	return d, values
}
func TestTrainingPrivateStoreNamespaceLockAndNonadoption(t *testing.T) {
	s, root, tenant := privateStore(t)
	namespace := s.Namespace()
	if _, e := training.OpenStore(root, tenant, 4096, 67108864, 64); e == nil || e.Error() != "TRAINING_STORE_IN_USE" {
		t.Fatal("exclusive store ownership", e)
	}
	if _, e := training.OpenStore(root, uuid.NewString(), 4096, 67108864, 64); e == nil {
		t.Fatal("foreign tenant store opened")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := training.OpenStore(root, tenant, 4096, 67108864, 64)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if reopened.Namespace() != namespace {
		t.Fatal("namespace changed on restart")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if e = training.InitializeStore(missing, tenant, os.Geteuid(), os.Getegid()); e == nil {
		t.Fatal("missing mount path created")
	}
	if _, e = os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("missing path substituted")
	}
	populated := t.TempDir()
	sentinel := filepath.Join(populated, "preserve")
	os.WriteFile(sentinel, []byte("PRESERVE_UNKNOWN_DATA"), 0600)
	if e = training.InitializeStore(populated, tenant, os.Geteuid(), os.Getegid()); e == nil {
		t.Fatal("populated directory adopted")
	}
	if raw, e := os.ReadFile(sentinel); e != nil || string(raw) != "PRESERVE_UNKNOWN_DATA" {
		t.Fatal("unknown data changed")
	}
}
func TestTrainingPrivateChunkExactBytesReplayBoundsAndTamperDetection(t *testing.T) {
	s, root, tenant := privateStore(t)
	d, values := descriptor(tenant)
	ctx := context.Background()
	if e := s.PrepareUpload(d); e != nil {
		t.Fatal(e)
	}
	if e := s.PrepareUpload(d); e != nil {
		t.Fatal("stage replay", e)
	}
	raw := values["adapter_model.safetensors"][:wire.TransferChunkBytes]
	sum := digest(raw)
	first, e := s.WriteChunk(ctx, d, "adapter_model.safetensors", 0, int64(len(raw)), sum, bytes.NewReader(raw))
	if e != nil || first.Duplicate {
		t.Fatal(e)
	}
	if e = s.VerifyChunk(ctx, d, first); e != nil {
		t.Fatal(e)
	}
	replay, e := s.WriteChunk(ctx, d, first.Asset, 0, first.SizeBytes, sum, bytes.NewReader(raw))
	if e != nil || !replay.Duplicate {
		t.Fatal("identical chunk replay", e)
	}
	_, e = s.WriteChunk(ctx, d, first.Asset, 0, first.SizeBytes, sum, bytes.NewReader([]byte("different body")))
	expected(t, e, "TRAINING_UPLOAD_CHECKSUM_MISMATCH")
	_, e = s.WriteChunk(ctx, d, first.Asset, 1, first.SizeBytes, sum, bytes.NewReader(raw))
	expected(t, e, "TRAINING_UPLOAD_INVALID")
	_, e = s.WriteChunk(ctx, d, first.Asset, 0, first.SizeBytes, strings.Repeat("a", 64), bytes.NewReader(raw))
	expected(t, e, "TRAINING_UPLOAD_CONFLICT")
	last := values[first.Asset][wire.TransferChunkBytes:]
	lastSum := digest(last)
	_, e = s.WriteChunk(ctx, d, first.Asset, wire.TransferChunkBytes, int64(len(last)), lastSum, bytes.NewReader(append(append([]byte{}, last...), 'x')))
	expected(t, e, "TRAINING_UPLOAD_CHECKSUM_MISMATCH")
	lastReceipt, e := s.WriteChunk(ctx, d, first.Asset, wire.TransferChunkBytes, int64(len(last)), lastSum, bytes.NewReader(last))
	if e != nil {
		t.Fatal("bounded correct retry", e)
	}
	if e = s.VerifyChunk(ctx, d, lastReceipt); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "uploads", d.UploadID, "chunks", first.Asset, "0000000000000000")
	file, e := os.OpenFile(path, os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	file.WriteAt([]byte("changed"), 0)
	file.Close()
	if e = s.VerifyChunk(ctx, d, first); e == nil {
		t.Fatal("tampered chunk accepted")
	}
	changed := d
	changed.ManifestSHA256 = strings.Repeat("b", 64)
	if e = s.PrepareUpload(changed); e == nil {
		t.Fatal("different manifest adopted existing stage")
	}
}
func TestTrainingPrivateChunkConcurrentReplayAndNamespaceMutationRefusal(t *testing.T) {
	s, root, tenant := privateStore(t)
	d, values := descriptor(tenant)
	if e := s.PrepareUpload(d); e != nil {
		t.Fatal(e)
	}
	raw := values["README.md"]
	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan training.ChunkReceipt, 2)
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			r, e := s.WriteChunk(context.Background(), d, "README.md", 0, int64(len(raw)), digest(raw), bytes.NewReader(raw))
			results <- r
			errors <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	duplicates := 0
	for result := range results {
		if result.Duplicate {
			duplicates++
		}
	}
	if duplicates != 1 {
		t.Fatal("concurrent replay created two authoritative chunks")
	}
	marker := filepath.Join(root, "namespace.json")
	if e := os.WriteFile(marker, []byte(`{"schema_version":1,"kind":"training","store_id":"`+uuid.NewString()+`","tenant_id":"`+tenant+`"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := s.PrepareUpload(d); e == nil {
		t.Fatal("swapped namespace accepted")
	}
}
func TestTrainingPrivateStoreRejectsSymlinkAndHardlinkStages(t *testing.T) {
	for _, mode := range []string{"symlink", "hardlink"} {
		t.Run(mode, func(t *testing.T) {
			s, root, tenant := privateStore(t)
			d, values := descriptor(tenant)
			if e := s.PrepareUpload(d); e != nil {
				t.Fatal(e)
			}
			raw := values["README.md"]
			if _, e := s.WriteChunk(context.Background(), d, "README.md", 0, int64(len(raw)), digest(raw), bytes.NewReader(raw)); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(root, "uploads", d.UploadID, "chunks", "README.md", "0000000000000000")
			outside := filepath.Join(t.TempDir(), "synthetic-preserved")
			if mode == "hardlink" {
				if e := os.Link(path, outside); e != nil {
					t.Fatal(e)
				}
			} else {
				if e := os.WriteFile(outside, []byte("PRESERVE_OUTSIDE"), 0600); e != nil {
					t.Fatal(e)
				}
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(outside, path); e != nil {
					t.Fatal(e)
				}
			}
			_, e := s.WriteChunk(context.Background(), d, "README.md", 0, int64(len(raw)), digest(raw), bytes.NewReader(raw))
			if e == nil {
				t.Fatal("unsafe linked stage accepted")
			}
			if _, e = os.Stat(outside); e != nil {
				t.Fatal("outside data touched", e)
			}
		})
	}
}

// This bounded workload verifies filesystem durability/reopen/replay using small
// synthetic bytes. It does not represent an accepted training checkpoint.
func TestTrainingPrivateCheckpointTransportRepeatedDurableReopenAndReplay(t *testing.T) {
	s, root, tenant := privateStore(t)
	ctx := context.Background()
	names := []string{"adapter_model.safetensors", "adapter_config.json", "optimizer.pt", "scheduler.pt", "trainer_state.json", "rng_state.pth", "projectone-checkpoint.json"}
	type retainedUpload struct {
		descriptor training.UploadDescriptor
		receipts   []training.ChunkReceipt
	}
	retained := make([]retainedUpload, 0, 16)
	payload := bytes.Repeat([]byte("synthetic-checkpoint-transport\n"), 2048)
	for attempt := 0; attempt < 16; attempt++ {
		d := training.UploadDescriptor{SchemaVersion: 1, Scope: wire.Scope{TenantID: tenant, WorkspaceID: uuid.NewString(), JobID: uuid.NewString()}, UploadID: uuid.NewString(), AttemptID: uuid.NewString(), Kind: "checkpoint", GlobalStep: 1, ManifestSHA256: digest([]byte("synthetic-transport-only"))}
		for _, name := range names {
			d.Files = append(d.Files, datawire.Asset{Path: name, SizeBytes: int64(len(payload)), SHA256: digest(payload)})
		}
		if e := s.PrepareUpload(d); e != nil {
			t.Fatalf("prepare attempt=%d code=%s diagnostic=%+v", attempt, e.Error(), training.StorageDiagnostic(e))
		}
		item := retainedUpload{descriptor: d}
		for _, name := range names {
			r, e := s.WriteChunk(ctx, d, name, 0, int64(len(payload)), digest(payload), bytes.NewReader(payload))
			if e != nil {
				t.Fatalf("write attempt=%d asset=%s code=%s diagnostic=%+v", attempt, name, e.Error(), training.StorageDiagnostic(e))
			}
			item.receipts = append(item.receipts, r)
		}
		retained = append(retained, item)
	}
	if e := s.Close(); e != nil {
		t.Fatalf("close code=%s diagnostic=%+v", e.Error(), training.StorageDiagnostic(e))
	}
	reopened, e := training.OpenStore(root, tenant, 4096, 67108864, 64)
	if e != nil {
		t.Fatalf("reopen code=%s diagnostic=%+v", e.Error(), training.StorageDiagnostic(e))
	}
	defer reopened.Close()
	for _, item := range retained {
		for _, r := range item.receipts {
			if e = reopened.VerifyChunk(ctx, item.descriptor, r); e != nil {
				t.Fatalf("verify asset=%s code=%s diagnostic=%+v", r.Asset, e.Error(), training.StorageDiagnostic(e))
			}
			replay, e := reopened.WriteChunk(ctx, item.descriptor, r.Asset, r.Offset, r.SizeBytes, r.SHA256, bytes.NewReader(payload))
			if e != nil || !replay.Duplicate {
				t.Fatalf("replay asset=%s duplicate=%v error=%v diagnostic=%+v", r.Asset, replay.Duplicate, e, training.StorageDiagnostic(e))
			}
		}
	}
}

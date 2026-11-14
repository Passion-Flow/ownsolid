package training_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func chunks(t *testing.T, s *training.Store, d training.UploadDescriptor, values map[string][]byte) {
	t.Helper()
	for _, asset := range d.Files {
		raw := values[asset.Path]
		for offset := int64(0); offset < int64(len(raw)); offset += wire.TransferChunkBytes {
			length := min(wire.TransferChunkBytes, int64(len(raw))-offset)
			block := raw[offset : offset+length]
			if _, e := s.WriteChunk(context.Background(), d, asset.Path, offset, length, digest(block), bytes.NewReader(block)); e != nil {
				t.Fatal(e)
			}
		}
	}
}
func TestTrainingPrivatePublishedBytesRestartReplayAndIntegrity(t *testing.T) {
	s, root, tenant := privateStore(t)
	d, values := descriptor(tenant)
	ctx := context.Background()
	if e := s.PrepareUpload(d); e != nil {
		t.Fatal(e)
	}
	chunks(t, s, d, values)
	key, e := s.PreparePublishedFiles(ctx, d)
	if e != nil {
		t.Fatal(e)
	}
	if key != filepath.Join("artifacts", d.Scope.JobID, d.AttemptID, d.UploadID) {
		t.Fatal("publication namespace")
	}
	replayed, e := s.PreparePublishedFiles(ctx, d)
	if e != nil || replayed != key {
		t.Fatal("private publication replay", e)
	}
	file, asset, e := s.OpenPublishedAsset(ctx, d, "adapter_model.safetensors")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(file)
	file.Close()
	if e != nil || digest(raw) != asset.SHA256 || !bytes.Equal(raw, values[asset.Path]) {
		t.Fatal("actual assembled bytes/digest", e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := training.OpenStore(root, tenant, 4096, 67108864, 64)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if e = reopened.VerifyPublished(ctx, d); e != nil {
		t.Fatal("publication lost after restart", e)
	}
	path := filepath.Join(root, key, "adapter_config.json")
	if e = os.WriteFile(path, []byte("CORRUPT"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = reopened.VerifyPublished(ctx, d); e == nil {
		t.Fatal("corrupt referenced file accepted")
	}
	if _, _, e = reopened.OpenPublishedAsset(ctx, d, "README.md"); e == nil {
		t.Fatal("partial corrupt publication exported")
	}
}
func TestTrainingPrivatePublicationMissingChangedAndUnknownDataFailClosed(t *testing.T) {
	for _, mode := range []string{"missing_chunk", "changed_chunk", "unknown_stage", "unknown_publication"} {
		t.Run(mode, func(t *testing.T) {
			s, root, tenant := privateStore(t)
			d, values := descriptor(tenant)
			ctx := context.Background()
			if e := s.PrepareUpload(d); e != nil {
				t.Fatal(e)
			}
			if mode != "missing_chunk" {
				chunks(t, s, d, values)
			}
			var sentinel string
			switch mode {
			case "changed_chunk":
				path := filepath.Join(root, "uploads", d.UploadID, "chunks", "README.md", "0000000000000000")
				if e := os.WriteFile(path, bytes.Repeat([]byte("z"), len(values["README.md"])), 0600); e != nil {
					t.Fatal(e)
				}
			case "unknown_stage":
				sentinel = filepath.Join(root, "uploads", d.UploadID, "chunks", "preserve-unknown")
				if e := os.WriteFile(sentinel, []byte("PRESERVE_UNKNOWN"), 0600); e != nil {
					t.Fatal(e)
				}
			case "unknown_publication":
				key, e := s.PreparePublishedFiles(ctx, d)
				if e != nil {
					t.Fatal(e)
				}
				sentinel = filepath.Join(root, key, "preserve-unknown")
				if e = os.WriteFile(sentinel, []byte("PRESERVE_UNKNOWN"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := s.PreparePublishedFiles(ctx, d); e == nil {
				t.Fatal("incomplete/conflicting private output reported prepared")
			}
			if sentinel != "" {
				if raw, e := os.ReadFile(sentinel); e != nil || string(raw) != "PRESERVE_UNKNOWN" {
					t.Fatal("unknown data removed", e)
				}
			}
			if mode == "missing_chunk" {
				if _, e := os.Stat(filepath.Join(root, "artifacts", d.Scope.JobID)); !os.IsNotExist(e) {
					t.Fatal("incomplete input created publication")
				}
			}
		})
	}
}

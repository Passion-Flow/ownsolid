package training_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/google/uuid"
)

func readerPublication(t *testing.T, kind string) (*training.Store, *training.PublishedReader, training.UploadDescriptor, map[string][]byte, string, string) {
	t.Helper()
	store, root, tenant := privateStore(t)
	d, values := descriptor(tenant)
	if kind == "checkpoint" {
		d.Kind, d.Files = kind, nil
		values = map[string][]byte{}
		for _, name := range []string{"adapter_model.safetensors", "adapter_config.json", "optimizer.pt", "scheduler.pt", "trainer_state.json", "rng_state.pth", "projectone-checkpoint.json"} {
			// These bytes exercise the file owner, not a valid training seal or GPU computation.
			raw := []byte("generated retained checkpoint transport\n")
			values[name] = raw
			d.Files = append(d.Files, datawire.Asset{Path: name, SizeBytes: int64(len(raw)), SHA256: digest(raw)})
		}
	}
	if err := store.PrepareUpload(d); err != nil {
		t.Fatal(err)
	}
	chunks(t, store, d, values)
	key, err := store.PreparePublishedFiles(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := training.OpenPublishedReader(root, tenant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	})
	return store, reader, d, values, root, key
}

func TestTrainingPublishedReaderRetainsWriterAndVerifiesCompletePublication(t *testing.T) {
	for _, kind := range []string{"artifact", "checkpoint"} {
		t.Run(kind, func(t *testing.T) {
			_, reader, d, values, root, _ := readerPublication(t, kind)
			if err := reader.VerifyPublished(t.Context(), d); err != nil {
				t.Fatal(err)
			}
			name := d.Files[0].Path
			file, asset, err := reader.OpenPublishedAsset(t.Context(), d, name)
			if err != nil || asset != d.Files[0] {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err := file.WriteAt([]byte("changed"), 0); err == nil {
				t.Fatal("reader returned a writable file")
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(file)
			if err != nil || !bytes.Equal(raw, values[name]) {
				t.Fatal("returned descriptor was not pinned", err)
			}
			if err := reader.VerifyPublished(t.Context(), d); err == nil {
				t.Fatal("closed reader accepted verification")
			}
			if owner, err := training.OpenStore(root, d.Scope.TenantID, 4096, wire.MaximumCheckpointBytes, wire.MaximumCheckpointFiles); err == nil {
				owner.Close()
				t.Fatal("reader released writer ownership")
			} else if err.Error() != "TRAINING_STORE_IN_USE" {
				t.Fatal(err)
			}
		})
	}
}

func TestTrainingPublishedReaderServiceRetainsAcceptedAdapterFileChecks(t *testing.T) {
	writer, reader, d, values, root, key := readerPublication(t, "artifact")
	service := &training.Service{PublishedReader: reader}
	source := training.AcceptedAdapter{Descriptor: d}
	if err := service.VerifyAcceptedAdapter(t.Context(), source); err != nil {
		t.Fatal("verify retained bytes with Store=nil", err)
	}
	name := "adapter_model.safetensors"
	file, asset, err := service.OpenAcceptedAdapterAsset(t.Context(), source, name)
	if err != nil || asset.Path != name {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(file)
	file.Close()
	if err != nil || !bytes.Equal(raw, values[name]) {
		t.Fatal("service did not read the actual retained adapter", err)
	}
	// A corrupt neighboring file invalidates the entire publication, even when
	// the requested adapter bytes remain unchanged.
	config := filepath.Join(root, key, "adapter_config.json")
	if err := os.WriteFile(config, []byte("xx"), 0600); err != nil {
		t.Fatal(err)
	}
	if file, _, err := service.OpenAcceptedAdapterAsset(t.Context(), source, name); err == nil || file != nil {
		t.Fatal("partial corrupt publication accepted")
	}
	if err := os.WriteFile(config, values["adapter_config.json"], 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyAcceptedAdapter(t.Context(), source); err != nil {
		t.Fatal("exact repair refused", err)
	}
	service.Store = writer
	if err := service.VerifyAcceptedAdapter(t.Context(), source); err == nil || err.Error() != "TRAINING_STORAGE_MISMATCH" {
		t.Fatal("ambiguous writer/reader accepted", err)
	}
	service.Store = nil
	foreign := source
	foreign.Descriptor.Scope.TenantID = uuid.NewString()
	if err := service.VerifyAcceptedAdapter(t.Context(), foreign); err == nil || err.Error() != "TRAINING_STORAGE_MISMATCH" {
		t.Fatal("foreign namespace accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := reader.VerifyPublished(ctx, d); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled verification accepted", err)
	}
	reader.Close()
	if err := service.VerifyAcceptedAdapter(t.Context(), source); err == nil || err.Error() != "TRAINING_STORAGE_UNAVAILABLE" {
		t.Fatal("closed file owner accepted", err)
	}
	service.PublishedReader = &training.PublishedReader{}
	if err := service.VerifyAcceptedAdapter(t.Context(), source); err == nil || err.Error() != "TRAINING_STORAGE_UNAVAILABLE" {
		t.Fatal("zero file owner accepted", err)
	}
}

func TestTrainingPublishedReaderRefusesChangedReceiptAndConcurrentClosure(t *testing.T) {
	_, reader, d, _, root, key := readerPublication(t, "artifact")
	receipt := filepath.Join(root, key, "publication.json")
	raw, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := reader.VerifyPublished(t.Context(), d); err == nil {
		t.Fatal("changed receipt accepted")
	}
	if err := os.WriteFile(receipt, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := reader.VerifyPublished(t.Context(), d); err != nil {
		t.Fatal("restored receipt refused", err)
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Go(func() {
			<-start
			file, _, err := reader.OpenPublishedAsset(t.Context(), d, d.Files[0].Path)
			if err == nil {
				file.Close()
			} else if err.Error() != "TRAINING_STORAGE_UNAVAILABLE" {
				t.Error(err)
			}
		})
	}
	workers.Go(func() { <-start; reader.Close() })
	close(start)
	workers.Wait()
}

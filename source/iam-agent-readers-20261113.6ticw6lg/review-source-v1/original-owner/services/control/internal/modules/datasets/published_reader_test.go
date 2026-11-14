package datasets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/google/uuid"
)

func publishedReaderFixture(t *testing.T) (*Store, *PublishedReader, TrainingInputs, string, string, []byte) {
	t.Helper()
	store, path, tenant := initializedStore(t)
	version, attempt := uuid.NewString(), uuid.NewString()
	directory := filepath.Join(path, versionDirectory(version, attempt))
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte("{\"prompt\":\"generated\",\"completion\":\"fixture\"}\n")
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	assets := []wire.Asset{{Path: "train.jsonl", SizeBytes: int64(len(raw)), SHA256: digest}, {Path: "validation.jsonl", SizeBytes: int64(len(raw)), SHA256: digest}}
	for _, asset := range assets {
		if err := os.WriteFile(filepath.Join(directory, asset.Path), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := OpenPublishedReader(path, tenant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	})
	input, err := trainingInputsFromVersion(Version{ID: version, DatasetID: uuid.NewString(), WorkspaceID: uuid.NewString(), AttemptID: attempt, Manifest: wire.Manifest{Files: assets}})
	if err != nil {
		t.Fatal(err)
	}
	return store, reader, input, path, tenant, raw
}

func readPublishedFixture(ctx context.Context, reader *PublishedReader, input TrainingInputs) (*os.File, error) {
	return reader.OpenVersionAssetContext(ctx, input.Version.ID, input.Version.AttemptID, input.Train.Path, input.Train.SizeBytes, input.Train.SHA256)
}

func TestDatasetPublishedReaderRetainsWriterAndPinnedReadonlyFile(t *testing.T) {
	_, reader, input, path, tenant, raw := publishedReaderFixture(t)
	second, err := OpenPublishedReader(path, tenant)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, current := range []*PublishedReader{reader, second} {
		file, err := readPublishedFixture(t.Context(), current, input)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(file)
		if err != nil || !bytes.Equal(data, raw) {
			t.Fatal("read exact retained bytes", err)
		}
		if _, err := file.WriteAt([]byte("changed"), 0); err == nil {
			t.Fatal("reader returned a writable descriptor")
		}
		file.Close()
	}
	file, err := readPublishedFixture(t.Context(), reader, input)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil || !bytes.Equal(data, raw) {
		t.Fatal("reader closure invalidated a returned descriptor", err)
	}
	if _, err := readPublishedFixture(t.Context(), reader, input); err == nil || err.Error() != "DATASET_STORAGE_UNAVAILABLE" {
		t.Fatal("closed reader accepted an open", err)
	}
	if owner, err := OpenStore(path, tenant, 4096); err == nil {
		owner.Close()
		t.Fatal("reader close released the existing writer")
	} else if err.Error() != "DATASET_STORE_IN_USE" {
		t.Fatal(err)
	}
	if _, err := OpenStoreForVerification(path, tenant, 4096); err == nil || err.Error() != "DATASET_STORE_IN_USE" {
		t.Fatal("exclusive maintenance ownership changed", err)
	}
}

func TestDatasetPublishedReaderServiceUsesActualPartitionsWithoutWriterStore(t *testing.T) {
	store, reader, input, _, tenant, raw := publishedReaderFixture(t)
	service := &Service{Identity: &identity.Service{}, PublishedReader: reader}
	if err := service.VerifyTrainingInputSnapshot(t.Context(), input); err != nil {
		t.Fatal("verify real bytes with Store=nil", err)
	}
	file, asset, err := service.OpenTrainingAssetContext(t.Context(), input, "train.jsonl")
	if err != nil || asset != input.Train {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	file.Close()
	if err != nil || !bytes.Equal(data, raw) {
		t.Fatal("service did not return the actual retained file", err)
	}
	if _, err := service.publishedFileAccess(uuid.NewString()); err == nil || err.Error() != "DATASET_STORAGE_MISMATCH" {
		t.Fatal("foreign tenant reader accepted", err)
	}
	if _, err := service.publishedFileAccess(tenant); err != nil {
		t.Fatal(err)
	}
	changed := input
	changed.Train.SHA256 = hex.EncodeToString(make([]byte, sha256.Size))
	if err := service.VerifyTrainingInputSnapshot(t.Context(), changed); err == nil || err.Error() != "DATASET_DATA_INVALID" {
		t.Fatal("forged snapshot accepted", err)
	}
	if file, _, err := service.OpenTrainingAssetContext(t.Context(), input, "../namespace.json"); err == nil || file != nil {
		t.Fatal("arbitrary asset accepted")
	}
	service.Store = store
	if err := service.VerifyTrainingInputSnapshot(t.Context(), input); err == nil || err.Error() != "DATASET_STORAGE_MISMATCH" {
		t.Fatal("ambiguous reader/writer configuration accepted", err)
	}
	service.Store = nil
	reader.Close()
	if err := service.VerifyTrainingInputSnapshot(t.Context(), input); err == nil || err.Error() != "DATASET_STORAGE_UNAVAILABLE" {
		t.Fatal("closed file owner accepted", err)
	}
	service.PublishedReader = &PublishedReader{}
	if err := service.VerifyTrainingInputSnapshot(t.Context(), input); err == nil || err.Error() != "DATASET_STORAGE_UNAVAILABLE" {
		t.Fatal("zero reader accepted", err)
	}
}

func TestDatasetPublishedReaderRefusesChangedFilesAndAllowsExactRepair(t *testing.T) {
	_, reader, input, path, tenant, raw := publishedReaderFixture(t)
	asset := filepath.Join(path, versionDirectory(input.Version.ID, input.Version.AttemptID), input.Train.Path)
	for _, changed := range [][]byte{bytes.Repeat([]byte("x"), len(raw)), raw[:len(raw)-1], append(append([]byte{}, raw...), 'x')} {
		if err := os.WriteFile(asset, changed, 0600); err != nil {
			t.Fatal(err)
		}
		if file, err := readPublishedFixture(t.Context(), reader, input); err == nil || file != nil {
			t.Fatal("changed bytes accepted")
		}
		if err := os.WriteFile(asset, raw, 0600); err != nil {
			t.Fatal(err)
		}
		file, err := readPublishedFixture(t.Context(), reader, input)
		if err != nil {
			t.Fatal("exact repair refused", err)
		}
		file.Close()
	}
	if _, err := OpenPublishedReader(path, uuid.NewString()); err == nil {
		t.Fatal("foreign namespace accepted")
	}
	if _, err := OpenPublishedReader(filepath.Join(path, "missing"), tenant); err == nil {
		t.Fatal("missing namespace created")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if file, err := readPublishedFixture(ctx, reader, input); !errors.Is(err, context.Canceled) || file != nil {
		t.Fatal("cancelled read accepted", err)
	}
	retained := asset + ".owned-backup"
	if err := os.Rename(asset, retained); err != nil {
		t.Fatal(err)
	}
	if file, err := readPublishedFixture(t.Context(), reader, input); err == nil || file != nil {
		t.Fatal("missing asset accepted")
	}
	if err := os.Symlink(retained, asset); err != nil {
		t.Fatal(err)
	}
	if file, err := readPublishedFixture(t.Context(), reader, input); err == nil || file != nil {
		t.Fatal("symlink asset accepted")
	}
	if err := os.Remove(asset); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(retained, asset); err != nil {
		t.Fatal(err)
	}
}

func TestDatasetPublishedReaderRefusesRootMarkerAndCarrierReplacement(t *testing.T) {
	_, reader, input, path, _, _ := publishedReaderFixture(t)
	marker, err := os.ReadFile(filepath.Join(path, markerFile))
	if err != nil {
		t.Fatal(err)
	}
	changed := reader.namespace
	changed.StoreID = uuid.NewString()
	encoded, err := json.Marshal(changed)
	if err != nil || os.WriteFile(filepath.Join(path, markerFile), encoded, 0600) != nil {
		t.Fatal("change owned namespace")
	}
	if file, err := readPublishedFixture(t.Context(), reader, input); err == nil || file != nil {
		t.Fatal("changed namespace accepted")
	}
	if err := os.WriteFile(filepath.Join(path, markerFile), marker, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{lockFile, "."} {
		target := filepath.Join(path, name)
		backup := target + ".owned-backup"
		if err := os.Rename(target, backup); err != nil {
			t.Fatal(err)
		}
		if name == "." {
			err = os.Mkdir(target, 0700)
		} else {
			err = os.WriteFile(target, nil, 0600)
		}
		if err != nil {
			t.Fatal(err)
		}
		if file, err := readPublishedFixture(t.Context(), reader, input); err == nil || file != nil {
			t.Fatal("replaced root or ownership carrier accepted")
		}
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(backup, target); err != nil {
			t.Fatal(err)
		}
		file, err := readPublishedFixture(t.Context(), reader, input)
		if err != nil {
			t.Fatal("repaired namespace refused", err)
		}
		file.Close()
	}
}

func TestDatasetPublishedReaderConcurrentOpenAndClose(t *testing.T) {
	_, reader, input, _, _, _ := publishedReaderFixture(t)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Go(func() {
			<-start
			file, err := readPublishedFixture(t.Context(), reader, input)
			if err != nil {
				if err.Error() != "DATASET_STORAGE_UNAVAILABLE" {
					t.Error(err)
				}
				return
			}
			file.Close()
		})
	}
	workers.Go(func() { <-start; reader.Close() })
	close(start)
	workers.Wait()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}

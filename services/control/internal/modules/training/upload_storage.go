package training

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type UploadDescriptor struct {
	SchemaVersion  int              `json:"schema_version"`
	Scope          wire.Scope       `json:"scope"`
	UploadID       string           `json:"upload_id"`
	AttemptID      string           `json:"attempt_id"`
	Kind           string           `json:"kind"`
	GlobalStep     int64            `json:"global_step"`
	ManifestSHA256 string           `json:"manifest_sha256"`
	Files          []datawire.Asset `json:"files"`
}

func (d UploadDescriptor) Validate(maximumBytes, maximumFiles int64) error {
	if d.SchemaVersion != 1 || d.Scope.Validate() != nil || !datawire.ValidID(d.UploadID) || !datawire.ValidID(d.AttemptID) || !datawire.ValidDigest(d.ManifestSHA256) || d.Kind != "artifact" && d.Kind != "checkpoint" || d.GlobalStep < 1 || d.GlobalStep > wire.MaximumStepsCeiling || len(d.Files) < 1 || int64(len(d.Files)) > maximumFiles {
		return fail("TRAINING_UPLOAD_INVALID")
	}
	seen := map[string]bool{}
	var total int64
	for _, asset := range d.Files {
		allowed := wire.AdapterAssetAllowed(asset.Path)
		if d.Kind == "checkpoint" {
			allowed = wire.CheckpointAssetAllowed(asset.Path)
		}
		if !allowed || seen[asset.Path] || asset.SizeBytes < 1 || asset.SizeBytes > maximumBytes || !datawire.ValidDigest(asset.SHA256) {
			return fail("TRAINING_UPLOAD_INVALID")
		}
		seen[asset.Path] = true
		total += asset.SizeBytes
	}
	required := []string{"adapter_model.safetensors", "adapter_config.json", "README.md"}
	if d.Kind == "checkpoint" {
		required = []string{"adapter_model.safetensors", "adapter_config.json", "optimizer.pt", "scheduler.pt", "trainer_state.json", "rng_state.pth", "projectone-checkpoint.json"}
	}
	for _, name := range required {
		if !seen[name] {
			return fail("TRAINING_UPLOAD_INVALID")
		}
	}
	if total > maximumBytes {
		return fail("TRAINING_UPLOAD_INVALID")
	}
	return nil
}
func uploadDirectory(id string) string { return "uploads/" + id }
func (s *Store) descriptor(d UploadDescriptor) error {
	if e := d.Validate(s.maximumBytes, s.maximumFiles); e != nil {
		return e
	}
	if d.Scope.TenantID != s.namespace.TenantID {
		return fail("TRAINING_UPLOAD_INVALID")
	}
	file, e := s.checkedFile(uploadDirectory(d.UploadID)+"/"+uploadIndex, wire.MaximumCheckpointMetadataBytes)
	if e != nil {
		return e
	}
	defer file.Close()
	raw, e := io.ReadAll(io.LimitReader(file, wire.MaximumCheckpointMetadataBytes+1))
	if e != nil || len(raw) > wire.MaximumCheckpointMetadataBytes {
		return fail("TRAINING_STORAGE_DATA_INVALID")
	}
	var stored UploadDescriptor
	if wire.DecodeStrict(raw, &stored) != nil {
		return fail("TRAINING_STORAGE_DATA_INVALID")
	}
	expected, _ := json.Marshal(d)
	actual, _ := json.Marshal(stored)
	if string(actual) != string(expected) {
		return fail("TRAINING_UPLOAD_CONFLICT")
	}
	return nil
}

// PrepareUpload creates an exact private stage for an already-authorized durable upload intent.
// A partial/unknown existing stage is retained and refused; no filesystem scan invents a business intent.
func (s *Store) PrepareUpload(d UploadDescriptor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkBase(); e != nil {
		return e
	}
	if e := d.Validate(s.maximumBytes, s.maximumFiles); e != nil {
		return e
	}
	if d.Scope.TenantID != s.namespace.TenantID {
		return fail("TRAINING_UPLOAD_INVALID")
	}
	directory := uploadDirectory(d.UploadID)
	if _, e := s.root.Lstat(directory); e == nil {
		return s.descriptor(d)
	} else if !os.IsNotExist(e) {
		return storageUnavailable("upload_directory_stat", e)
	}
	if e := s.reserve(maximumNamespaceBytes); e != nil {
		return e
	}
	if e := s.directory(directory); e != nil {
		return e
	}
	if e := s.directory(directory + "/chunks"); e != nil {
		return e
	}
	if e := s.newJSON(directory+"/"+uploadIndex, d); e != nil {
		return e
	}
	return s.sync(directory)
}

type ChunkReceipt struct {
	Asset     string `json:"asset"`
	Offset    int64  `json:"offset"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	Duplicate bool   `json:"duplicate"`
}

// WriteChunk bounds and fsyncs exact bytes without holding a business transaction or overwriting a file.
func (s *Store) WriteChunk(ctx context.Context, d UploadDescriptor, asset string, offset, length int64, digest string, body io.Reader) (ChunkReceipt, error) {
	var out ChunkReceipt
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkBase(); e != nil {
		return out, e
	}
	if e := s.descriptor(d); e != nil {
		return out, e
	}
	var selected datawire.Asset
	for _, item := range d.Files {
		if item.Path == asset {
			selected = item
			break
		}
	}
	if body == nil || selected.Path == "" || !datawire.ValidDigest(digest) || offset < 0 || offset%wire.TransferChunkBytes != 0 || offset >= selected.SizeBytes || length != min(wire.TransferChunkBytes, selected.SizeBytes-offset) {
		return out, fail("TRAINING_UPLOAD_INVALID")
	}
	directory := filepath.Join(uploadDirectory(d.UploadID), "chunks", asset)
	if e := s.directory(directory); e != nil {
		return out, e
	}
	path := filepath.Join(directory, chunkName(offset))
	out = ChunkReceipt{Asset: asset, Offset: offset, SizeBytes: length, SHA256: digest}
	if _, e := s.root.Lstat(path); e == nil {
		file, e := s.checkedFile(path, length)
		if e != nil {
			return ChunkReceipt{}, e
		}
		sum, n, e := fileDigest(ctx, file, length)
		file.Close()
		if e != nil || n != length || sum != digest {
			return ChunkReceipt{}, fail("TRAINING_UPLOAD_CONFLICT")
		}
		replayHash := sha256.New()
		replayed, e := io.Copy(replayHash, io.LimitReader(contextReader{ctx: ctx, reader: body}, length+1))
		if e != nil {
			return ChunkReceipt{}, e
		}
		if replayed != length || hex.EncodeToString(replayHash.Sum(nil)) != digest {
			return ChunkReceipt{}, fail("TRAINING_UPLOAD_CHECKSUM_MISMATCH")
		}
		out.Duplicate = true
		return out, nil
	} else if !os.IsNotExist(e) {
		return ChunkReceipt{}, storageUnavailable("chunk_stat", e)
	}
	if e := s.reserve(length); e != nil {
		return ChunkReceipt{}, e
	}
	temporary := filepath.Join(directory, ".pending-"+uuid.NewString())
	file, e := s.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return ChunkReceipt{}, storageUnavailable("chunk_open", e)
	}
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(file, hash), io.LimitReader(contextReader{ctx: ctx, reader: body}, length+1))
	if e == nil && n == length && hex.EncodeToString(hash.Sum(nil)) == digest {
		if syncError := file.Sync(); syncError != nil {
			e = storageUnavailable("chunk_sync", syncError)
		}
	} else if e == nil {
		e = fail("TRAINING_UPLOAD_CHECKSUM_MISMATCH")
	}
	closeError := file.Close()
	if e != nil {
		return ChunkReceipt{}, e
	}
	if closeError != nil {
		return ChunkReceipt{}, storageUnavailable("chunk_close", closeError)
	}
	// The complete private chunk is linked without replacement, then only its own temporary name is removed.
	if e = s.root.Link(temporary, path); e != nil {
		return ChunkReceipt{}, fail("TRAINING_UPLOAD_CONFLICT")
	}
	if e = s.root.Remove(temporary); e != nil {
		return ChunkReceipt{}, storageUnavailable("chunk_pending_remove", e)
	}
	if e = s.sync(directory); e != nil {
		return ChunkReceipt{}, e
	}
	return out, nil
}

func (s *Store) VerifyChunk(ctx context.Context, d UploadDescriptor, expected ChunkReceipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkBase(); e != nil {
		return e
	}
	if e := s.descriptor(d); e != nil {
		return e
	}
	var selected datawire.Asset
	for _, asset := range d.Files {
		if asset.Path == expected.Asset {
			selected = asset
			break
		}
	}
	if selected.Path == "" || expected.Offset < 0 || expected.Offset%wire.TransferChunkBytes != 0 || expected.Offset >= selected.SizeBytes || expected.SizeBytes != min(wire.TransferChunkBytes, selected.SizeBytes-expected.Offset) || !datawire.ValidDigest(expected.SHA256) {
		return fail("TRAINING_UPLOAD_INVALID")
	}
	path := filepath.Join(uploadDirectory(d.UploadID), "chunks", expected.Asset, chunkName(expected.Offset))
	file, e := s.checkedFile(path, expected.SizeBytes)
	if e != nil {
		return e
	}
	defer file.Close()
	digest, n, e := fileDigest(ctx, file, expected.SizeBytes)
	if e != nil || n != expected.SizeBytes || digest != expected.SHA256 {
		return fail("TRAINING_STORAGE_DATA_INVALID")
	}
	return nil
}

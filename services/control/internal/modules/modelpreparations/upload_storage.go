package modelpreparations

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
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

const reportFile = "result.json"

type UploadDescriptor struct {
	SchemaVersion  int              `json:"schema_version"`
	Scope          wire.Scope       `json:"scope"`
	UploadID       string           `json:"upload_id"`
	AttemptID      string           `json:"attempt_id"`
	ManifestSHA256 string           `json:"manifest_sha256"`
	Files          []datawire.Asset `json:"files"`
}

func (d UploadDescriptor) Validate(maximumBytes, maximumFiles int64) error {
	base := wire.ApprovedBaseFiles()
	if d.SchemaVersion != 1 || d.Scope.Validate() != nil || !datawire.ValidID(d.UploadID) || !datawire.ValidID(d.AttemptID) || !datawire.ValidDigest(d.ManifestSHA256) || maximumBytes < 1 || maximumBytes > wire.MaximumModelBytes || maximumFiles < 1 || maximumFiles > wire.MaximumModelFiles || len(d.Files) != len(base) || int64(len(d.Files)) > maximumFiles {
		return fail("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	approved := map[string]datawire.Asset{}
	for _, asset := range base {
		approved[asset.Path] = asset
	}
	seen := map[string]bool{}
	var total int64
	for _, asset := range d.Files {
		original, allowed := approved[asset.Path]
		if !allowed || seen[asset.Path] || asset.SizeBytes < 1 || asset.SizeBytes > maximumBytes || !datawire.ValidDigest(asset.SHA256) {
			return fail("MODEL_PREPARATION_UPLOAD_INVALID")
		}
		if asset.Path != "model.safetensors" && asset != original || asset.Path == "model.safetensors" && asset.SHA256 == original.SHA256 {
			return fail("MODEL_PREPARATION_UPLOAD_INVALID")
		}
		seen[asset.Path] = true
		total += asset.SizeBytes
	}
	if total > maximumBytes {
		return fail("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	return nil
}
func uploadDirectory(id string) string { return "uploads/" + id }
func (s *Store) descriptor(ctx context.Context, d UploadDescriptor) error {
	if e := d.Validate(s.maximumBytes, s.maximumFiles); e != nil {
		return e
	}
	if d.Scope.TenantID != s.namespace.TenantID {
		return fail("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	file, e := s.checkedFile(uploadDirectory(d.UploadID)+"/"+uploadIndex, wire.MaximumUploadDescriptorBytes)
	if e != nil {
		return e
	}
	defer file.Close()
	raw, e := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: file}, wire.MaximumUploadDescriptorBytes+1))
	if e := ctx.Err(); e != nil {
		return e
	}
	if e != nil || int64(len(raw)) > wire.MaximumUploadDescriptorBytes {
		return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	var stored UploadDescriptor
	if wire.DecodeStrict(raw, &stored) != nil {
		return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	expected, _ := json.Marshal(d)
	actual, _ := json.Marshal(stored)
	if string(actual) != string(expected) {
		return fail("MODEL_PREPARATION_UPLOAD_CONFLICT")
	}
	return s.verifyUploadReport(ctx, d, nil)
}

// PrepareUpload creates an exact private stage for an already-authorized durable upload intent.
// A partial/unknown existing stage is retained and refused; no filesystem scan invents a business intent.
func (s *Store) PrepareUpload(ctx context.Context, d UploadDescriptor, metadata []byte) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if len(metadata) < 2 || int64(len(metadata)) > wire.MaximumResultBytes || hash(metadata) != d.ManifestSHA256 {
		return fail("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkBase(); e != nil {
		return e
	}
	if e := d.Validate(s.maximumBytes, s.maximumFiles); e != nil {
		return e
	}
	if d.Scope.TenantID != s.namespace.TenantID {
		return fail("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	directory := uploadDirectory(d.UploadID)
	if _, e := s.root.Lstat(directory); e == nil {
		if e = s.descriptor(ctx, d); e != nil {
			return e
		}
		return s.verifyUploadReport(ctx, d, metadata)
	} else if !os.IsNotExist(e) {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	if e := s.reserve(maximumNamespaceBytes + int64(len(metadata))); e != nil {
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
	if e := s.newRawReport(filepath.Join(directory, reportFile), metadata); e != nil {
		return e
	}
	return s.sync(directory)
}

// newRawReport preserves original report bytes instead of reserializing JSON into a different digest.
func (s *Store) newRawReport(relative string, raw []byte) error {
	for parent := filepath.Dir(relative); parent != "."; parent = filepath.Dir(parent) {
		if _, e := ownedPath(filepath.Join(s.path, parent), true, 0); e != nil {
			return e
		}
	}
	file, e := s.root.OpenFile(relative, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return fail("MODEL_PREPARATION_STORAGE_CONFLICT")
	}
	_, e = file.Write(raw)
	if e == nil {
		e = file.Sync()
	}
	closeError := file.Close()
	if e != nil || closeError != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	return s.sync(filepath.Dir(relative))
}

func (s *Store) verifyUploadReport(ctx context.Context, d UploadDescriptor, expected []byte) error {
	entries, e := s.readDirectory(uploadDirectory(d.UploadID), 3)
	if e != nil || len(entries) != 3 {
		return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	allowed := map[string]bool{uploadIndex: true, reportFile: true, "chunks": true}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
		}
	}
	file, e := s.checkedFile(filepath.Join(uploadDirectory(d.UploadID), reportFile), wire.MaximumResultBytes)
	if e != nil {
		return e
	}
	defer file.Close()
	raw, e := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: file}, wire.MaximumResultBytes+1))
	if e := ctx.Err(); e != nil {
		return e
	}
	if e != nil || int64(len(raw)) > wire.MaximumResultBytes || hash(raw) != d.ManifestSHA256 || expected != nil && string(raw) != string(expected) {
		return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	return nil
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
	if e := s.descriptor(ctx, d); e != nil {
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
		return out, fail("MODEL_PREPARATION_UPLOAD_INVALID")
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
			return ChunkReceipt{}, fail("MODEL_PREPARATION_UPLOAD_CONFLICT")
		}
		replayHash := sha256.New()
		replayed, e := io.Copy(replayHash, io.LimitReader(contextReader{ctx: ctx, reader: body}, length+1))
		if e != nil {
			return ChunkReceipt{}, e
		}
		if replayed != length || hex.EncodeToString(replayHash.Sum(nil)) != digest {
			return ChunkReceipt{}, fail("MODEL_PREPARATION_UPLOAD_CHECKSUM_MISMATCH")
		}
		out.Duplicate = true
		return out, nil
	} else if !os.IsNotExist(e) {
		return ChunkReceipt{}, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	entries, e := s.readDirectory(directory, wire.MaximumChunkDirectoryEntries)
	if e != nil || len(entries) >= wire.MaximumChunkDirectoryEntries {
		return ChunkReceipt{}, fail("MODEL_PREPARATION_UPLOAD_RECOVERY_REQUIRED")
	}
	if e := s.reserve(length); e != nil {
		return ChunkReceipt{}, e
	}
	temporary := filepath.Join(directory, ".pending-"+uuid.NewString())
	file, e := s.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return ChunkReceipt{}, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(file, hash), io.LimitReader(contextReader{ctx: ctx, reader: body}, length+1))
	if e == nil && n == length && hex.EncodeToString(hash.Sum(nil)) == digest {
		e = file.Sync()
	} else if e == nil {
		e = fail("MODEL_PREPARATION_UPLOAD_CHECKSUM_MISMATCH")
	}
	closeError := file.Close()
	if e != nil {
		return ChunkReceipt{}, e
	}
	if closeError != nil {
		return ChunkReceipt{}, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	// The complete private chunk is linked without replacement, then only its own temporary name is removed.
	if e = s.root.Link(temporary, path); e != nil {
		return ChunkReceipt{}, fail("MODEL_PREPARATION_UPLOAD_CONFLICT")
	}
	if e = s.root.Remove(temporary); e != nil {
		return ChunkReceipt{}, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
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
	if e := s.descriptor(ctx, d); e != nil {
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
		return fail("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	path := filepath.Join(uploadDirectory(d.UploadID), "chunks", expected.Asset, chunkName(expected.Offset))
	file, e := s.checkedFile(path, expected.SizeBytes)
	if e != nil {
		return e
	}
	defer file.Close()
	digest, n, e := fileDigest(ctx, file, expected.SizeBytes)
	if e != nil || n != expected.SizeBytes || digest != expected.SHA256 {
		return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	return nil
}

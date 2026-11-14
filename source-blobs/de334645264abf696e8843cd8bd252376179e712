package training

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

const publicationIndex = "publication.json"

type publicationReceipt struct {
	SchemaVersion int              `json:"schema_version"`
	StoreID       string           `json:"store_id"`
	Descriptor    UploadDescriptor `json:"descriptor"`
}

func publishedDirectory(d UploadDescriptor) string {
	kind := "checkpoints"
	if d.Kind == "artifact" {
		kind = "artifacts"
	}
	return filepath.Join(kind, d.Scope.JobID, d.AttemptID, d.UploadID)
}

func (s *Store) verifyPublished(ctx context.Context, d UploadDescriptor) error {
	if e := d.Validate(s.maximumBytes, s.maximumFiles); e != nil {
		return e
	}
	if d.Scope.TenantID != s.namespace.TenantID {
		return fail("TRAINING_UPLOAD_INVALID")
	}
	directory := publishedDirectory(d)
	if _, e := ownedPath(filepath.Join(s.path, directory), true, 0); e != nil {
		return e
	}
	entries, e := s.readDirectory(directory, len(d.Files)+1)
	if e != nil || len(entries) != len(d.Files)+1 {
		return fail("TRAINING_STORAGE_DATA_INVALID")
	}
	allowed := map[string]bool{publicationIndex: true}
	for _, asset := range d.Files {
		allowed[asset.Path] = true
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return fail("TRAINING_STORAGE_DATA_INVALID")
		}
	}
	file, e := s.checkedFile(filepath.Join(directory, publicationIndex), wire.MaximumCheckpointMetadataBytes)
	if e != nil {
		return e
	}
	raw, e := io.ReadAll(io.LimitReader(file, wire.MaximumCheckpointMetadataBytes+1))
	file.Close()
	if e != nil || len(raw) > wire.MaximumCheckpointMetadataBytes {
		return fail("TRAINING_STORAGE_DATA_INVALID")
	}
	var receipt publicationReceipt
	expected, _ := json.Marshal(publicationReceipt{SchemaVersion: 1, StoreID: s.namespace.StoreID, Descriptor: d})
	if wire.DecodeStrict(raw, &receipt) != nil {
		return fail("TRAINING_STORAGE_DATA_INVALID")
	}
	actual, _ := json.Marshal(receipt)
	if string(expected) != string(actual) {
		return fail("TRAINING_STORAGE_DATA_INVALID")
	}
	for _, asset := range d.Files {
		file, e := s.checkedFile(filepath.Join(directory, asset.Path), asset.SizeBytes)
		if e != nil {
			return e
		}
		sum, n, e := fileDigest(ctx, file, asset.SizeBytes)
		file.Close()
		if e != nil || n != asset.SizeBytes || sum != asset.SHA256 {
			return fail("TRAINING_STORAGE_DATA_INVALID")
		}
	}
	return nil
}
func (s *Store) VerifyPublished(ctx context.Context, d UploadDescriptor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkBase(); e != nil {
		return e
	}
	return s.verifyPublished(ctx, d)
}

// PreparePublishedFiles assembles exact immutable bytes. Only a separate authorized database/audit commit accepts a checkpoint/artifact.
func (s *Store) PreparePublishedFiles(ctx context.Context, d UploadDescriptor) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkBase(); e != nil {
		return "", e
	}
	if e := s.descriptor(d); e != nil {
		return "", e
	}
	directory := publishedDirectory(d)
	if _, e := s.root.Lstat(directory); e == nil {
		if e = s.verifyPublished(ctx, d); e != nil {
			return "", e
		}
		return directory, nil
	} else if !os.IsNotExist(e) {
		return "", fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	// Every canonical chunk must exist before creating an output directory; owned failed temporary chunks stay private and unreferenced.
	chunkEntries, e := s.readDirectory(filepath.Join(uploadDirectory(d.UploadID), "chunks"), len(d.Files))
	if e != nil {
		return "", fail("TRAINING_UPLOAD_INCOMPLETE")
	}
	allowedAssets := map[string]bool{}
	for _, asset := range d.Files {
		allowedAssets[asset.Path] = true
	}
	for _, entry := range chunkEntries {
		if !allowedAssets[entry.Name()] {
			return "", fail("TRAINING_STORAGE_CONFLICT")
		}
	}
	for _, asset := range d.Files {
		chunkDirectory := filepath.Join(uploadDirectory(d.UploadID), "chunks", asset.Path)
		entries, e := s.readDirectory(chunkDirectory, int(s.maximumFiles))
		if e != nil {
			return "", fail("TRAINING_UPLOAD_INCOMPLETE")
		}
		expected := map[string]bool{}
		for offset := int64(0); offset < asset.SizeBytes; offset += wire.TransferChunkBytes {
			expected[chunkName(offset)] = true
		}
		for _, entry := range entries {
			name := entry.Name()
			if expected[name] {
				delete(expected, name)
				continue
			}
			if !strings.HasPrefix(name, ".pending-") || !datawire.ValidID(strings.TrimPrefix(name, ".pending-")) {
				return "", fail("TRAINING_STORAGE_CONFLICT")
			}
			pending, e := s.checkedFile(filepath.Join(chunkDirectory, name), wire.TransferChunkBytes+1)
			if e != nil {
				return "", e
			}
			pending.Close()
		}
		if len(expected) != 0 {
			return "", fail("TRAINING_UPLOAD_INCOMPLETE")
		}
	}
	var bytes int64
	for _, asset := range d.Files {
		bytes += asset.SizeBytes
	}
	if e := s.reserve(bytes); e != nil {
		return "", e
	}
	parts := strings.Split(directory, string(filepath.Separator))
	for i := 1; i <= len(parts); i++ {
		if e := s.directory(filepath.Join(parts[:i]...)); e != nil {
			return "", e
		}
	}
	for _, asset := range d.Files {
		if e := s.assemble(ctx, d, directory, asset); e != nil {
			return "", e
		}
	}
	if e := s.newJSON(filepath.Join(directory, publicationIndex), publicationReceipt{SchemaVersion: 1, StoreID: s.namespace.StoreID, Descriptor: d}); e != nil {
		return "", e
	}
	if e := s.sync(directory); e != nil {
		return "", e
	}
	if e := s.sync(filepath.Dir(directory)); e != nil {
		return "", e
	}
	if e := s.verifyPublished(ctx, d); e != nil {
		return "", e
	}
	return directory, nil
}

func (s *Store) assemble(ctx context.Context, d UploadDescriptor, directory string, asset datawire.Asset) error {
	temporary := filepath.Join(directory, ".pending-"+uuid.NewString())
	target, e := s.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	hash := sha256.New()
	var total int64
	writer := io.MultiWriter(target, hash)
	for offset := int64(0); offset < asset.SizeBytes; offset += wire.TransferChunkBytes {
		length := min(wire.TransferChunkBytes, asset.SizeBytes-offset)
		source, e := s.checkedFile(filepath.Join(uploadDirectory(d.UploadID), "chunks", asset.Path, chunkName(offset)), length)
		if e != nil {
			target.Close()
			return e
		}
		n, e := io.Copy(writer, contextReader{ctx: ctx, reader: io.NewSectionReader(source, 0, length+1)})
		source.Close()
		total += n
		if e != nil || n != length {
			target.Close()
			return fail("TRAINING_STORAGE_DATA_INVALID")
		}
	}
	if total != asset.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		target.Close()
		return fail("TRAINING_UPLOAD_CHECKSUM_MISMATCH")
	}
	e = target.Sync()
	closeError := target.Close()
	if e != nil || closeError != nil {
		return fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	final := filepath.Join(directory, asset.Path)
	if s.root.Link(temporary, final) != nil {
		return fail("TRAINING_STORAGE_CONFLICT")
	}
	if s.root.Remove(temporary) != nil {
		return fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	return s.sync(directory)
}

// OpenPublishedAsset only opens an independently verified descriptor already authorized by the caller.
func (s *Store) OpenPublishedAsset(ctx context.Context, d UploadDescriptor, name string) (*os.File, datawire.Asset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var asset datawire.Asset
	if e := s.checkBase(); e != nil {
		return nil, asset, e
	}
	for _, item := range d.Files {
		if item.Path == name {
			asset = item
			break
		}
	}
	if asset.Path == "" {
		return nil, asset, fail("TRAINING_NOT_FOUND")
	}
	if e := s.verifyPublished(ctx, d); e != nil {
		return nil, asset, e
	}
	file, e := s.checkedFile(filepath.Join(publishedDirectory(d), name), asset.SizeBytes)
	return file, asset, e
}

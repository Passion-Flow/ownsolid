package modelpreparations

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
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

const publicationIndex = "publication.json"

type publicationReceipt struct {
	SchemaVersion int              `json:"schema_version"`
	StoreID       string           `json:"store_id"`
	Descriptor    UploadDescriptor `json:"descriptor"`
}

func publishedDirectory(d UploadDescriptor) string {
	return filepath.Join("artifacts", d.Scope.PreparationID, d.AttemptID, d.UploadID)
}

func (s *Store) verifyPublished(ctx context.Context, d UploadDescriptor) error {
	if e := d.Validate(s.maximumBytes, s.maximumFiles); e != nil {
		return e
	}
	if d.Scope.TenantID != s.namespace.TenantID {
		return fail("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	directory := publishedDirectory(d)
	if _, e := ownedPath(filepath.Join(s.path, directory), true, 0); e != nil {
		return e
	}
	entries, e := s.readDirectory(directory, len(d.Files)+2)
	if e != nil || len(entries) != len(d.Files)+2 {
		return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	allowed := map[string]bool{publicationIndex: true, reportFile: true}
	for _, asset := range d.Files {
		allowed[asset.Path] = true
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
		}
	}
	file, e := s.checkedFile(filepath.Join(directory, publicationIndex), wire.MaximumUploadDescriptorBytes)
	if e != nil {
		return e
	}
	raw, e := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: file}, wire.MaximumUploadDescriptorBytes+1))
	file.Close()
	if e != nil || int64(len(raw)) > wire.MaximumUploadDescriptorBytes {
		return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	var receipt publicationReceipt
	expected, _ := json.Marshal(publicationReceipt{SchemaVersion: 1, StoreID: s.namespace.StoreID, Descriptor: d})
	if wire.DecodeStrict(raw, &receipt) != nil {
		return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	actual, _ := json.Marshal(receipt)
	if string(expected) != string(actual) {
		return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	if _, e := s.readReport(ctx, filepath.Join(directory, reportFile), d.ManifestSHA256); e != nil {
		return e
	}
	for _, asset := range d.Files {
		file, e := s.checkedFile(filepath.Join(directory, asset.Path), asset.SizeBytes)
		if e != nil {
			return e
		}
		sum, n, e := fileDigest(ctx, file, asset.SizeBytes)
		file.Close()
		if e != nil || n != asset.SizeBytes || sum != asset.SHA256 {
			return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
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
	if e := s.descriptor(ctx, d); e != nil {
		return "", e
	}
	directory := publishedDirectory(d)
	if _, e := s.root.Lstat(directory); e == nil {
		if e = s.verifyPublished(ctx, d); e != nil {
			return "", e
		}
		return directory, nil
	} else if !os.IsNotExist(e) {
		return "", fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	// Every canonical chunk must exist before creating an output directory; owned failed temporary chunks stay private and unreferenced.
	chunkEntries, e := s.readDirectory(filepath.Join(uploadDirectory(d.UploadID), "chunks"), len(d.Files))
	if e != nil {
		return "", fail("MODEL_PREPARATION_UPLOAD_INCOMPLETE")
	}
	allowedAssets := map[string]bool{}
	for _, asset := range d.Files {
		allowedAssets[asset.Path] = true
	}
	for _, entry := range chunkEntries {
		if !allowedAssets[entry.Name()] {
			return "", fail("MODEL_PREPARATION_STORAGE_CONFLICT")
		}
	}
	for _, asset := range d.Files {
		chunkDirectory := filepath.Join(uploadDirectory(d.UploadID), "chunks", asset.Path)
		entries, e := s.readDirectory(chunkDirectory, wire.MaximumChunkDirectoryEntries)
		if e != nil {
			return "", fail("MODEL_PREPARATION_UPLOAD_INCOMPLETE")
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
				return "", fail("MODEL_PREPARATION_STORAGE_CONFLICT")
			}
			pending, e := s.checkedFile(filepath.Join(chunkDirectory, name), wire.TransferChunkBytes+1)
			if e != nil {
				return "", e
			}
			pending.Close()
		}
		if len(expected) != 0 {
			return "", fail("MODEL_PREPARATION_UPLOAD_INCOMPLETE")
		}
	}
	var bytes int64
	for _, asset := range d.Files {
		bytes += asset.SizeBytes
	}
	if e := s.reserve(bytes + wire.MaximumResultBytes + wire.MaximumUploadDescriptorBytes); e != nil {
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
	report, e := s.readReport(ctx, filepath.Join(uploadDirectory(d.UploadID), reportFile), d.ManifestSHA256)
	if e != nil {
		return "", e
	}
	if e = s.newRawReport(filepath.Join(directory, reportFile), report); e != nil {
		return "", e
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
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
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
			if e := ctx.Err(); e != nil {
				return e
			}
			return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
		}
	}
	if total != asset.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		target.Close()
		return fail("MODEL_PREPARATION_UPLOAD_CHECKSUM_MISMATCH")
	}
	e = target.Sync()
	closeError := target.Close()
	if e != nil || closeError != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	final := filepath.Join(directory, asset.Path)
	if s.root.Link(temporary, final) != nil {
		return fail("MODEL_PREPARATION_STORAGE_CONFLICT")
	}
	if s.root.Remove(temporary) != nil {
		return fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
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
		return nil, asset, fail("MODEL_PREPARATION_NOT_FOUND")
	}
	if e := s.verifyPublished(ctx, d); e != nil {
		return nil, asset, e
	}
	file, e := s.checkedFile(filepath.Join(publishedDirectory(d), name), asset.SizeBytes)
	return file, asset, e
}

func (s *Store) readReport(ctx context.Context, relative, expected string) ([]byte, error) {
	file, e := s.checkedFile(relative, wire.MaximumResultBytes)
	if e != nil {
		return nil, e
	}
	defer file.Close()
	raw, e := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: file}, wire.MaximumResultBytes+1))
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if e != nil || int64(len(raw)) > wire.MaximumResultBytes || hash(raw) != expected {
		return nil, fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	return raw, nil
}

// OpenPublishedFiles verifies one complete accepted publication and pins its ordinary files.
// The caller owns all returned FDs; no path or write authority crosses the module boundary.
func (s *Store) OpenPublishedFiles(ctx context.Context, d UploadDescriptor) (map[string]*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.checkBase(); e != nil {
		return nil, e
	}
	if e := s.verifyPublished(ctx, d); e != nil {
		return nil, e
	}
	files := map[string]*os.File{}
	for _, asset := range d.Files {
		file, e := s.checkedFile(filepath.Join(publishedDirectory(d), asset.Path), asset.SizeBytes)
		if e != nil {
			for _, opened := range files {
				opened.Close()
			}
			return nil, e
		}
		files[asset.Path] = file
	}
	return files, nil
}

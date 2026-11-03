package datasets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type publicationReceipt struct {
	SchemaVersion   int    `json:"schema_version"`
	StoreID         string `json:"store_id"`
	TenantID        string `json:"tenant_id"`
	VersionID       string `json:"version_id"`
	DraftID         string `json:"draft_id"`
	JobID           string `json:"job_id"`
	AttemptID       string `json:"attempt_id"`
	EffectiveSHA256 string `json:"effective_sha256"`
	MappingSHA256   string `json:"mapping_sha256"`
	ManifestSHA256  string `json:"manifest_sha256"`
}

func versionDirectory(version, attempt string) string { return "versions/" + version + "/" + attempt }

// PrepareVersionFiles durably retains an immutable attempt-specific publication. Only a later fenced database commit makes it visible.
func (s *Store) PrepareVersionFiles(request wire.ProcessorRequest, result wire.ProcessorResult, version, draft string, mapping []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	expectedScope := filepath.Join(s.path, "attempts", request.JobID, request.AttemptID)
	if s.closed || !wire.ValidID(version) || !wire.ValidID(draft) || request.Validate() != nil || request.ScopeDirectory != expectedScope || request.InputFile != filepath.Join(expectedScope, "input.jsonl") || request.OutputDirectory != filepath.Join(expectedScope, "output") || result.Status != "prepared" || result.Manifest == nil || result.Manifest.Validate(request) != nil || result.JobID != request.JobID || result.AttemptID != request.AttemptID || int64(len(mapping)) > request.Resources.MaximumResultBytes || len(mapping) == 0 {
		return "", failure("DATASET_FILE_INVALID")
	}
	if verifyPrepared(request, result) != nil {
		return "", failure("DATASET_FILE_INVALID")
	}
	digest := sha256.Sum256(mapping)
	mappingSHA := hex.EncodeToString(digest[:])
	receipt := publicationReceipt{storeSchema, s.namespace.StoreID, s.namespace.TenantID, version, draft, request.JobID, request.AttemptID, request.InputSHA256, mappingSHA, result.Manifest.ManifestFile.SHA256}
	if e := s.reserve(int64(len(mapping)) + 4096); e != nil {
		return "", e
	}
	if e := s.ensureDirectory("versions/" + version); e != nil {
		return "", e
	}
	relative := versionDirectory(version, request.AttemptID)
	if e := s.root.Mkdir(relative, 0700); e != nil {
		// Never replace a partial or uncertain publication. A retry uses a fresh attempt ID.
		return "", failure("DATASET_OUTPUT_CONFLICT")
	}
	for _, asset := range append(append([]wire.Asset{}, result.Manifest.Files...), result.Manifest.ManifestFile) {
		source := filepath.Join("attempts", request.JobID, request.AttemptID, "output", "artifacts", asset.Path)
		if s.root.Link(source, relative+"/"+asset.Path) != nil {
			return "", failure("DATASET_STORAGE_UNAVAILABLE")
		}
	}
	if s.root.Link(filepath.Join("attempts", request.JobID, request.AttemptID, "input.jsonl"), relative+"/effective.jsonl") != nil {
		return "", failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if e := writeNew(filepath.Join(s.path, relative, "mapping.jsonl"), mapping); e != nil {
		return "", e
	}
	encoded, _ := json.Marshal(receipt)
	if e := writeNew(filepath.Join(s.path, relative, "publication.json"), append(encoded, '\n')); e != nil {
		return "", e
	}
	if e := s.sync(relative); e != nil {
		return "", e
	}
	if e := s.sync("versions/" + version); e != nil {
		return "", e
	}
	return mappingSHA, nil
}

func (s *Store) VerifyVersion(version, draft, job, attempt, effective, mappingSHA string, manifest wire.Manifest) error {
	return s.VerifyVersionContext(context.Background(), version, draft, job, attempt, effective, mappingSHA, manifest)
}
func (s *Store) VerifyVersionContext(ctx context.Context, version, draft, job, attempt, effective, mappingSHA string, manifest wire.Manifest) error {
	validation := wire.ProcessorRequest{InputSHA256: effective, Limits: wire.Limits{MaximumRows: wire.MaximumRowsCeiling}, Resources: wire.Resources{MaximumResultBytes: wire.MaximumResultBytesCeiling}, Seed: &manifest.Seed, ValidationBasisPoints: &manifest.ValidationBasisPoints}
	if wire.ValidateSplit(manifest.Seed, manifest.ValidationBasisPoints) != nil || manifest.Validate(validation) != nil {
		return failure("DATASET_FILE_INVALID")
	}
	if !wire.ValidID(version) || !wire.ValidID(draft) || !wire.ValidID(job) || !wire.ValidID(attempt) || !wire.ValidDigest(effective) || !wire.ValidDigest(mappingSHA) {
		return failure("DATASET_FILE_INVALID")
	}
	directory := filepath.Join(s.path, versionDirectory(version, attempt))
	if _, e := privatePath(directory, s.path, true, 0); e != nil {
		return failure("DATASET_FILE_INVALID")
	}
	entries, e := os.ReadDir(directory)
	if e != nil || len(entries) != 9 {
		return failure("DATASET_FILE_INVALID")
	}
	receiptPath := filepath.Join(directory, "publication.json")
	if _, e = privatePath(receiptPath, directory, false, 4096); e != nil {
		return failure("DATASET_FILE_INVALID")
	}
	raw, e := os.ReadFile(receiptPath)
	var receipt publicationReceipt
	if e != nil || wire.DecodeStrict(raw, &receipt) != nil || receipt != (publicationReceipt{storeSchema, s.namespace.StoreID, s.namespace.TenantID, version, draft, job, attempt, effective, mappingSHA, manifest.ManifestFile.SHA256}) {
		return failure("DATASET_FILE_INVALID")
	}
	files := append(append([]wire.Asset{}, manifest.Files...), manifest.ManifestFile)
	for _, asset := range files {
		if !validAsset(asset.Path) {
			return failure("DATASET_FILE_INVALID")
		}
		path := filepath.Join(directory, asset.Path)
		if _, e = privatePath(path, directory, false, asset.SizeBytes); e != nil {
			return failure("DATASET_FILE_INVALID")
		}
		hash, n, e := contextFileDigest(ctx, path, asset.SizeBytes)
		if e != nil || n != asset.SizeBytes || hash != asset.SHA256 {
			return failure("DATASET_FILE_INVALID")
		}
	}
	for name, expected := range map[string]string{"effective.jsonl": effective, "mapping.jsonl": mappingSHA} {
		path := filepath.Join(directory, name)
		if _, e = privatePath(path, directory, false, wire.MaximumResultBytesCeiling); e != nil {
			return failure("DATASET_FILE_INVALID")
		}
		hash, _, e := contextFileDigest(ctx, path, wire.MaximumResultBytesCeiling)
		if e != nil || hash != expected {
			return failure("DATASET_FILE_INVALID")
		}
	}
	return nil
}
func validAsset(name string) bool {
	switch name {
	case "samples.jsonl", "train.jsonl", "validation.jsonl", "assignments.jsonl", "duplicates.jsonl", "manifest.json":
		return true
	}
	return false
}
func (s *Store) OpenVersionAsset(version, attempt, asset string, size int64, digest string) (*os.File, error) {
	return s.OpenVersionAssetContext(context.Background(), version, attempt, asset, size, digest)
}

// OpenVersionAssetContext verifies and returns one descriptor. It never reopens
// a separately replaceable pathname after hashing and closes every failure path.
func (s *Store) OpenVersionAssetContext(ctx context.Context, version, attempt, asset string, size int64, digest string) (*os.File, error) {
	if s == nil || ctx == nil || !wire.ValidID(version) || !wire.ValidID(attempt) || !validAsset(asset) || size < 0 || size > wire.MaximumResultBytesCeiling || !wire.ValidDigest(digest) {
		return nil, failure("DATASET_FILE_INVALID")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	path := filepath.Join(s.path, versionDirectory(version, attempt), asset)
	info, e := privatePath(path, s.path, false, size)
	if e != nil {
		return nil, failure("DATASET_FILE_INVALID")
	}
	file, e := os.Open(path)
	if e != nil {
		return nil, failure("DATASET_FILE_INVALID")
	}
	verified := false
	defer func() {
		if !verified {
			file.Close()
		}
	}()
	before, e := file.Stat()
	if e != nil || !os.SameFile(info, before) || !before.Mode().IsRegular() || before.Size() != size {
		return nil, failure("DATASET_FILE_INVALID")
	}
	h := sha256.New()
	n, e := io.Copy(h, boundedContextReader{ctx, io.LimitReader(file, size+1)})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if e != nil || n != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return nil, failure("DATASET_FILE_INVALID")
	}
	after, e := file.Stat()
	if e != nil || !os.SameFile(before, after) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() {
		return nil, failure("DATASET_FILE_INVALID")
	}
	position, e := file.Seek(0, io.SeekStart)
	if e != nil || position != 0 {
		return nil, failure("DATASET_FILE_INVALID")
	}
	verified = true
	return file, nil
}

// OpenEffective is an internal currently-authorized clone source, not an arbitrary file export.
func (s *Store) OpenEffective(ctx context.Context, version, attempt, digest string, maximum int64) (*os.File, int64, error) {
	if !wire.ValidID(version) || !wire.ValidID(attempt) || !wire.ValidDigest(digest) || maximum < 1 || maximum > wire.MaximumBytesCeiling {
		return nil, 0, failure("DATASET_FILE_INVALID")
	}
	path := filepath.Join(s.path, versionDirectory(version, attempt), "effective.jsonl")
	info, e := privatePath(path, s.path, false, maximum)
	if e != nil || info.Size() < 1 {
		return nil, 0, failure("DATASET_FILE_INVALID")
	}
	file, e := os.Open(path)
	if e != nil {
		return nil, 0, failure("DATASET_FILE_INVALID")
	}
	current, e := file.Stat()
	if e != nil || !os.SameFile(info, current) {
		file.Close()
		return nil, 0, failure("DATASET_FILE_INVALID")
	}
	hash := sha256.New()
	n, e := io.Copy(hash, boundedContextReader{ctx, io.LimitReader(file, maximum+1)})
	if e != nil || n != info.Size() || hex.EncodeToString(hash.Sum(nil)) != digest {
		file.Close()
		return nil, 0, failure("DATASET_FILE_INVALID")
	}
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		file.Close()
		return nil, 0, failure("DATASET_FILE_INVALID")
	}
	return file, n, nil
}

// EffectiveSize permits an internal authorized caller to reject its own analysis budget before hashing source bytes.
func (s *Store) EffectiveSize(version, attempt string) (int64, error) {
	if !wire.ValidID(version) || !wire.ValidID(attempt) {
		return 0, failure("DATASET_FILE_INVALID")
	}
	path := filepath.Join(s.path, versionDirectory(version, attempt), "effective.jsonl")
	info, e := privatePath(path, s.path, false, wire.MaximumBytesCeiling)
	if e != nil || info.Size() < 1 {
		return 0, failure("DATASET_FILE_INVALID")
	}
	return info.Size(), nil
}

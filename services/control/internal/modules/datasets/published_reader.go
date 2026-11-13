package datasets

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// PublishedReader opens only immutable published assets. It grants no client
// authority and cannot initialize storage, mutate files or release a writer lock.
type PublishedReader struct {
	mu        sync.RWMutex
	root      *os.Root
	carrier   *os.File
	path      string
	namespace storeNamespace
	closed    bool
}

// OpenPublishedReader opens an existing local namespace for concurrent reads.
// The installation owns any read-only mount and must provide the exact namespace.
// Unlike maintenance verification, this reader never acquires writer ownership.
func OpenPublishedReader(path, tenant string) (*PublishedReader, error) {
	namespace, err := readStoreNamespace(path, tenant)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	carrier, err := root.Open(lockFile)
	if err != nil {
		root.Close()
		return nil, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if err = checkPublishedBase(root, carrier, path, namespace); err != nil {
		carrier.Close()
		root.Close()
		return nil, err
	}
	return &PublishedReader{root: root, carrier: carrier, path: path, namespace: namespace}, nil
}

// Close waits for current opens and closes only this reader's descriptors.
// Files already returned to an authorized caller remain pinned and caller-owned.
func (r *PublishedReader) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	var rootError, carrierError error
	if r.root != nil {
		rootError = r.root.Close()
	}
	if r.carrier != nil {
		carrierError = r.carrier.Close()
	}
	if rootError != nil || carrierError != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	return nil
}

// OpenVersionAssetContext verifies the full immutable asset and returns the same
// read-only descriptor. The caller must hold and repeat current SQL authority.
func (r *PublishedReader) OpenVersionAssetContext(ctx context.Context, version, attempt, asset string, size int64, digest string) (*os.File, error) {
	if r == nil {
		return nil, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed || r.root == nil || r.carrier == nil {
		return nil, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	return openPublishedVersionAsset(ctx, r.root, r.carrier, r.path, r.namespace, version, attempt, asset, size, digest)
}

func checkPublishedBase(root *os.Root, carrier *os.File, path string, namespace storeNamespace) error {
	if root == nil || carrier == nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	info, err := privatePath(path, "", true, 0)
	if err != nil {
		return failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	info, err = privatePath(filepath.Join(path, lockFile), path, false, 0)
	if err != nil {
		return failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	held, err := carrier.Stat()
	if err != nil || !os.SameFile(info, held) {
		return failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	info, err = privatePath(filepath.Join(path, markerFile), path, false, maximumNamespaceBytes)
	if err != nil {
		return failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	marker, err := root.Open(markerFile)
	if err != nil {
		return failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	defer marker.Close()
	opened, err = marker.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	raw, err := io.ReadAll(io.LimitReader(marker, maximumNamespaceBytes+1))
	var current storeNamespace
	if err != nil || wire.DecodeStrict(raw, &current) != nil || current != namespace {
		return failure("DATASET_STORE_NAMESPACE_INVALID")
	}
	return nil
}

type versionAssetOpener interface {
	OpenVersionAssetContext(context.Context, string, string, string, int64, string) (*os.File, error)
}

func (s *Service) publishedFileAccess(tenant string) (versionAssetOpener, error) {
	if s == nil || s.Store != nil && s.PublishedReader != nil {
		return nil, failure("DATASET_STORAGE_MISMATCH")
	}
	if s.Store != nil {
		s.Store.mu.Lock()
		defer s.Store.mu.Unlock()
		if s.Store.closed || s.Store.root == nil || s.Store.lock == nil {
			return nil, failure("DATASET_STORAGE_UNAVAILABLE")
		}
		if tenant != "" && s.Store.namespace.TenantID != tenant {
			return nil, failure("DATASET_STORAGE_MISMATCH")
		}
		return s.Store, nil
	}
	if s.PublishedReader != nil {
		r := s.PublishedReader
		r.mu.RLock()
		defer r.mu.RUnlock()
		if r.closed || r.root == nil || r.carrier == nil {
			return nil, failure("DATASET_STORAGE_UNAVAILABLE")
		}
		if tenant != "" && r.namespace.TenantID != tenant {
			return nil, failure("DATASET_STORAGE_MISMATCH")
		}
		return r, nil
	}
	return nil, failure("DATASET_STORAGE_UNAVAILABLE")
}

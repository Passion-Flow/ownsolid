package training

import (
	"context"
	"os"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// PublishedReader verifies immutable training publications without owning a
// writer lock. Its private store cannot be used for uploads or mutations.
type PublishedReader struct{ store *Store }

// OpenPublishedReader opens an existing retained namespace and creates no state.
// The installation provides the exact local/read-only mount; this is no remote
// artifact transport and does not authorize a caller or start a training worker.
func OpenPublishedReader(path, tenant string) (*PublishedReader, error) {
	namespace, err := readStoreNamespace(path, tenant)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	carrier, err := root.Open(ownerLock)
	if err != nil {
		root.Close()
		return nil, fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	// Retained bytes use versioned physical ceilings, independently of new-job
	// policy. The concrete reader exposes no writer, reserve or staging methods.
	store := &Store{root: root, lock: carrier, path: path, namespace: namespace, maximumBytes: wire.MaximumCheckpointBytes, maximumFiles: wire.MaximumCheckpointFiles}
	if err := store.checkBase(); err != nil {
		store.Close()
		return nil, err
	}
	return &PublishedReader{store: store}, nil
}

// Close closes only the independent reader descriptors and is idempotent.
// An already returned file remains pinned and must be closed by its caller.
func (r *PublishedReader) Close() error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.Close()
}

// VerifyPublished checks the complete receipt and every declared file without SQL.
func (r *PublishedReader) VerifyPublished(ctx context.Context, descriptor UploadDescriptor) error {
	if r == nil || r.store == nil || ctx == nil {
		return fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := r.store.VerifyPublished(ctx, descriptor)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// OpenPublishedAsset verifies a complete publication and returns a read-only
// descriptor for one exact asset. The caller retains current SQL authorization.
func (r *PublishedReader) OpenPublishedAsset(ctx context.Context, descriptor UploadDescriptor, name string) (*os.File, datawire.Asset, error) {
	if r == nil || r.store == nil || ctx == nil {
		return nil, datawire.Asset{}, fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	if err := ctx.Err(); err != nil {
		return nil, datawire.Asset{}, err
	}
	file, asset, err := r.store.OpenPublishedAsset(ctx, descriptor, name)
	if ctx.Err() != nil {
		if file != nil {
			file.Close()
		}
		return nil, datawire.Asset{}, ctx.Err()
	}
	return file, asset, err
}

type publishedFileAccess interface {
	VerifyPublished(context.Context, UploadDescriptor) error
	OpenPublishedAsset(context.Context, UploadDescriptor, string) (*os.File, datawire.Asset, error)
}

func (s *Service) publishedFiles(tenant string) (publishedFileAccess, error) {
	if s == nil || s.Store != nil && s.PublishedReader != nil {
		return nil, fail("TRAINING_STORAGE_MISMATCH")
	}
	var store *Store
	if s.Store != nil {
		store = s.Store
	} else if s.PublishedReader != nil {
		store = s.PublishedReader.store
	}
	if store == nil {
		return nil, fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.root == nil || store.lock == nil {
		return nil, fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	if tenant != "" && store.namespace.TenantID != tenant {
		return nil, fail("TRAINING_STORAGE_MISMATCH")
	}
	if s.Store != nil {
		return s.Store, nil
	}
	return s.PublishedReader, nil
}

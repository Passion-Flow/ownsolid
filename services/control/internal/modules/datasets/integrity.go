package datasets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"io"
	"os"
	"path/filepath"
)

func (s *Service) OpenConfiguredStore(ctx context.Context) error {
	repo, e := s.repository()
	if e != nil {
		return e
	}

	configuration := s.Identity.Config.Datasets
	if configuration.ArtifactRoot == "" {
		return s.VerifyStored(ctx)
	}
	var tenant string
	if e := repo.QueryRow(ctx, datasetSQL(repo, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete", `SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete`, nil, nil, transaction.SQL(transaction.Dameng, dmIntegrityTenant))).Scan(&tenant); e != nil {
		return failure("DATASET_INSTALLATION_REQUIRED")
	}
	store, e := OpenStore(configuration.ArtifactRoot, tenant, s.policy().MinimumFreeBytes)
	if e != nil {
		return e
	}
	processor, e := NewProcessor(configuration, s.policy())
	if e != nil {
		store.Close()
		return e
	}
	s.Store = store
	s.Processor = processor
	if e = s.VerifyStored(ctx); e != nil {
		store.Close()
		s.Store = nil
		s.Processor = nil
		return e
	}
	return nil
}
func contextFileDigest(ctx context.Context, path string, maximum int64) (string, int64, error) {
	file, e := os.Open(path)
	if e != nil {
		return "", 0, failure("DATASET_FILE_INVALID")
	}
	defer file.Close()
	hash := sha256.New()
	n, e := io.Copy(hash, boundedContextReader{ctx, io.LimitReader(file, maximum+1)})
	if e != nil || n > maximum {
		return "", 0, failure("DATASET_FILE_INVALID")
	}
	return hex.EncodeToString(hash.Sum(nil)), n, nil
}

// VerifyStored checks currently referenced original/version files, including retired history. It never adopts, repairs or removes unknown files.
func (s *Service) VerifyStored(ctx context.Context) error {
	repo, e := s.repository()
	if e != nil {
		return e
	}

	if s.Store == nil {
		var required bool
		e := repo.QueryRow(ctx, datasetSQL(repo, "SELECT EXISTS(SELECT 1 FROM dataset_drafts d JOIN installation_state i ON i.tenant_id=d.tenant_id WHERE i.id=1 AND d.source_received_at IS NOT NULL) OR EXISTS(SELECT 1 FROM mongodb_dataset_imports m JOIN installation_state i ON i.tenant_id=m.tenant_id WHERE i.id=1 AND m.source_sha256 IS NOT NULL) OR EXISTS(SELECT 1 FROM dataset_versions v JOIN installation_state i ON i.tenant_id=v.tenant_id WHERE i.id=1) OR EXISTS(SELECT 1 FROM dataset_draft_import_candidates c JOIN installation_state i ON i.tenant_id=c.tenant_id WHERE i.id=1)", `SELECT EXISTS(SELECT 1 FROM dataset_drafts d JOIN installation_state i ON i.tenant_id=d.tenant_id WHERE i.id=1 AND d.source_received_at IS NOT NULL) OR EXISTS(SELECT 1 FROM dataset_versions v JOIN installation_state i ON i.tenant_id=v.tenant_id WHERE i.id=1)`, nil, nil, transaction.SQL(transaction.Dameng, dmIntegrityReferencesExist))).Scan(&required)
		if e != nil {
			return e
		}
		if required {
			return failure("DATASET_STORAGE_REQUIRED")
		}
		return nil
	}
	select {
	case s.Store.verification <- struct{}{}:
		defer func() { <-s.Store.verification }()
	case <-ctx.Done():
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	rows, e := repo.Query(ctx, datasetSQL(repo, "SELECT d.source_object_id::text,d.source_sha256,d.expected_bytes FROM dataset_drafts d JOIN installation_state i ON i.tenant_id=d.tenant_id WHERE i.id=1 AND d.source_received_at IS NOT NULL UNION ALL SELECT m.object_id::text,m.source_sha256,m.source_bytes FROM mongodb_dataset_imports m JOIN installation_state i ON i.tenant_id=m.tenant_id WHERE i.id=1 AND m.source_sha256 IS NOT NULL ORDER BY 1", `SELECT d.source_object_id,d.source_sha256,d.expected_bytes FROM dataset_drafts d JOIN installation_state i ON i.tenant_id=d.tenant_id WHERE i.id=1 AND d.source_received_at IS NOT NULL ORDER BY d.id`, nil, nil, transaction.SQL(transaction.Dameng, dmIntegritySources)))
	if e != nil {
		return e
	}
	for rows.Next() {
		var source SnapshotSource
		if e = rows.Scan(&source.ObjectID, &source.SHA256, &source.SizeBytes); e != nil {
			rows.Close()
			return e
		}
		if e = s.verifySourceReference(ctx, source); e != nil {
			rows.Close()
			return e
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return rows.Err()
	}
	if e = walkOriginalSourceReferences(ctx, repo, s.Store.namespace.TenantID, func(source SnapshotSource) error {
		return s.verifySourceReference(ctx, source)
	}); e != nil {
		return e
	}
	// A scoped subquery keeps the public field selector unambiguous and includes no credentials.
	rows, e = repo.Query(ctx, datasetSQL(repo, "SELECT "+versionFields+" FROM dataset_versions WHERE tenant_id=(SELECT tenant_id FROM installation_state WHERE id=1) ORDER BY id", `SELECT id,dataset_id,workspace_id,draft_id,job_id,attempt_id,ordinal,parent_version_id,published_by,published_at,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256,retired_at,retire_reason FROM dataset_versions WHERE tenant_id=(SELECT tenant_id FROM installation_state WHERE id=1) ORDER BY id`, nil, nil, transaction.SQL(transaction.Dameng, dmIntegrityVersions)))
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		if ctx.Err() != nil {
			return failure("DATASET_STORAGE_UNAVAILABLE")
		}
		v, e := scanVersion(rows)
		if e != nil {
			return e
		}
		if e = s.Store.VerifyVersionContext(ctx, v.ID, v.DraftID, v.JobID, v.AttemptID, v.EffectiveSourceSHA256, v.RowMappingSHA256, v.Manifest); e != nil {
			return e
		}
	}
	return rows.Err()
}

func (s *Service) verifySourceReference(ctx context.Context, source SnapshotSource) error {
	relative, err := snapshotSourcePath(source)
	if err != nil {
		return failure("DATASET_DATA_INVALID")
	}
	file := filepath.Join(s.Store.path, relative)
	if _, err = privatePath(file, s.Store.path, false, source.SizeBytes); err != nil {
		return failure("DATASET_FILE_INVALID")
	}
	actual, n, err := contextFileDigest(ctx, file, source.SizeBytes)
	if err != nil || actual != source.SHA256 || n != source.SizeBytes {
		return failure("DATASET_FILE_INVALID")
	}
	return nil
}

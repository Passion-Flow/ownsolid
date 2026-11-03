package datasets

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const versionFields = `id::text,dataset_id::text,workspace_id::text,draft_id::text,job_id::text,attempt_id::text,ordinal,parent_version_id::text,published_by::text,published_at,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256,retired_at,retire_reason`

func scanVersion(row transaction.Row) (Version, error) {
	var v Version
	var raw []byte
	e := row.Scan(&v.ID, &v.DatasetID, &v.WorkspaceID, &v.DraftID, &v.JobID, &v.AttemptID, &v.Ordinal, &v.ParentVersionID, &v.PublishedBy, &v.PublishedAt, &v.RawSourceSHA256, &v.EffectiveSourceSHA256, &v.ManifestSHA256, &raw, &v.RowMappingSHA256, &v.RetiredAt, &v.RetireReason)
	if errors.Is(e, pgx.ErrNoRows) {
		return Version{}, failure("DATASET_NOT_FOUND")
	}
	if e != nil {
		return committed(v, e)
	}
	if wire.DecodeStrict(raw, &v.Manifest) != nil || v.ManifestSHA256 != v.Manifest.ManifestFile.SHA256 || v.EffectiveSourceSHA256 != v.Manifest.SourceSHA256 {
		return Version{}, failure("DATASET_DATA_INVALID")
	}
	if e != nil {
		return Version{}, e
	}
	v.PublishedAt = v.PublishedAt.UTC()
	if v.RetiredAt != nil {
		stamp := v.RetiredAt.UTC()
		v.RetiredAt = &stamp
	}

	return v, nil
}
func lockedVersion(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, dataset, id string) (Version, error) {
	if !wire.ValidID(id) {
		return Version{}, failure("DATASET_NOT_FOUND")
	}
	return scanVersion(tx.QueryRow(ctx, datasetSQL(tx, "SELECT "+versionFields+" FROM dataset_versions WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND id=$4 FOR UPDATE", `SELECT id,dataset_id,workspace_id,draft_id,job_id,attempt_id,ordinal,parent_version_id,published_by,published_at,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256,retired_at,retire_reason FROM dataset_versions WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=? FOR UPDATE`, []any{w.TenantID, w.ID, dataset, id}, []any{w.TenantID, w.ID, dataset, id}, transaction.SQL(transaction.Dameng, dmVersionLock, w.TenantID, w.ID, dataset, id))))
}
func (s *Service) GetVersion(ctx context.Context, a identity.Auth, workspace, dataset, id string) (Version, error) {
	var out Version
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedDataset(ctx, tx, w, dataset); e != nil {
			return e
		}
		var e error
		out, e = lockedVersion(ctx, tx, w, dataset, id)
		return e
	})
	return committed(out, e)
}
func (s *Service) VersionFile(ctx context.Context, a identity.Auth, workspace, dataset, id, asset string) (*os.File, wire.Asset, error) {
	var file *os.File
	var metadata wire.Asset
	if !validAsset(asset) {
		return nil, metadata, failure("DATASET_NOT_FOUND")
	}
	if s.Store == nil {
		return nil, metadata, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	e := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedDataset(ctx, tx, w, dataset); e != nil {
			return e
		}
		version, e := lockedVersion(ctx, tx, w, dataset, id)
		if e != nil {
			return e
		}
		for _, candidate := range append(append([]wire.Asset{}, version.Manifest.Files...), version.Manifest.ManifestFile) {
			if candidate.Path == asset {
				metadata = candidate
				break
			}
		}
		if metadata.Path == "" {
			return failure("DATASET_FILE_INVALID")
		}
		file, e = s.Store.OpenVersionAssetContext(ctx, version.ID, version.AttemptID, asset, metadata.SizeBytes, metadata.SHA256)
		return e
	})
	if e != nil {
		if file != nil {
			file.Close()
			file = nil
		}
		metadata = wire.Asset{}
	}
	return file, metadata, e
}
func datasetReadable(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, id string) error {
	if canWrite(a, w) {
		return nil
	}
	var published bool
	if e := tx.QueryRow(ctx, datasetSQL(tx, "SELECT EXISTS(SELECT 1 FROM dataset_versions WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3)", `SELECT EXISTS(SELECT 1 FROM dataset_versions WHERE tenant_id=? AND workspace_id=? AND dataset_id=?)`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id}, transaction.SQL(transaction.Dameng, dmDatasetHasPublishedVersion, w.TenantID, w.ID, id))).Scan(&published); e != nil {
		return e
	}
	if !published {
		return failure("DATASET_NOT_FOUND")
	}
	return nil
}

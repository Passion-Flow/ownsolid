package datasets

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const mysqlPublishedDatasetFields = `id,workspace_id,name,origin,license,rights_asserted,created_by,revision,created_at,updated_at,retired_at,retire_reason`
const mysqlPublishedVersionFields = `id,dataset_id,workspace_id,draft_id,job_id,attempt_id,ordinal,parent_version_id,published_by,published_at,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256,retired_at,retire_reason`

func publishedSourceSQL(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	return datasetSQL(q, pg, my, args, args, transaction.SQL(transaction.Dameng, dm, args...))
}

func publishedSourceError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, context.Canceled) {
		return failure(string(transaction.Cancelled))
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return failure(string(transaction.Timeout))
	}
	switch e.Error() {
	case "DATASET_NOT_FOUND", "DATASET_DATA_INVALID", "DATASET_RETIRED", "DATASET_STORAGE_UNAVAILABLE",
		"DATASET_STORAGE_MISMATCH", "DATASET_IDENTITY_REQUIRED", string(transaction.Cancelled),
		string(transaction.Timeout), string(transaction.LockTimeout), string(transaction.Deadlock):
		return failure(e.Error())
	default:
		return failure("STORAGE_UNAVAILABLE")
	}
}

// PublishedSourceWithin borrows the owner's already scoped transaction. It
// neither authenticates a client nor opens files or commits a transaction.
func (s *Service) PublishedSourceWithin(ctx context.Context, q transaction.Executor, tenant, workspace, dataset, version string, retained bool) (Version, error) {
	if s == nil || ctx == nil || q == nil {
		return Version{}, failure("STORAGE_UNAVAILABLE")
	}
	if e := ctx.Err(); e != nil {
		return Version{}, publishedSourceError(e)
	}
	for _, id := range []string{tenant, workspace, dataset, version} {
		if !wire.ValidID(id) {
			return Version{}, failure("DATASET_NOT_FOUND")
		}
	}
	if s.Identity == nil || s.Workspaces == nil || s.Workspaces.Identity != s.Identity {
		return Version{}, failure("DATASET_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return Version{}, failure("DATASET_STORAGE_MISMATCH")
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return Version{}, publishedSourceError(e)
	}
	if store.Dialect() != q.Dialect() || q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL && q.Dialect() != transaction.Dameng {
		return Version{}, failure("DATASET_STORAGE_MISMATCH")
	}
	if s.Store == nil {
		return Version{}, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	parent, e := scanDataset(q.QueryRow(ctx, publishedSourceSQL(q,
		"SELECT "+datasetFields+" FROM datasets WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE",
		"SELECT "+mysqlPublishedDatasetFields+" FROM datasets WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE",
		dmPublishedDataset,
		tenant, workspace, dataset)))
	if e != nil {
		return Version{}, publishedSourceError(e)
	}
	value, e := scanVersion(q.QueryRow(ctx, publishedSourceSQL(q,
		"SELECT "+versionFields+" FROM dataset_versions WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND id=$4 FOR UPDATE",
		"SELECT "+mysqlPublishedVersionFields+" FROM dataset_versions WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=? FOR UPDATE",
		dmPublishedVersion,
		tenant, workspace, dataset, version)))
	if e != nil {
		return Version{}, publishedSourceError(e)
	}
	if !retained && (parent.RetiredAt != nil || value.RetiredAt != nil) {
		return Version{}, failure("DATASET_RETIRED")
	}
	value.PublishedAt = value.PublishedAt.UTC()
	if value.RetiredAt != nil {
		stamp := value.RetiredAt.UTC()
		value.RetiredAt = &stamp
	}
	return value, nil
}

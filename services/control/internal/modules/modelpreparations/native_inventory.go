package modelpreparations

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// Read one immutable output at a time. Report bytes are fetched only for the
// physical integrity walk; snapshot inventories need the descriptor alone.
func walkNativePreparationOutputs(ctx context.Context, q transaction.Executor, tenant string, withReport bool, visit func(UploadDescriptor, []byte) error) error {
	after := ""
	for {
		query := `SELECT "ID","WORKSPACE_ID","PREPARATION_ID","ATTEMPT_ID","MANIFEST_SHA256","DESCRIPTOR","STORAGE_PATH"`
		if withReport {
			query += `,"REPORT_BYTES"`
		}
		query += ` FROM "MODEL_PREPARATION_ARTIFACTS" WHERE "TENANT_ID"=?`
		args := []any{tenant}
		if after != "" {
			query += ` AND "ID">?`
			args = append(args, after)
		}
		query += ` ORDER BY "ID" LIMIT 1`
		var id, workspace, preparation, attempt, sha, storage string
		var raw, report []byte
		dest := []any{&id, &workspace, &preparation, &attempt, &sha, &raw, &storage}
		if withReport {
			dest = append(dest, &report)
		}
		e := preparationQueryRow(ctx, q, transaction.SQL(transaction.Dameng, query, args...)).Scan(dest...)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		var d UploadDescriptor
		if wire.DecodeStrict(raw, &d) != nil || d.Validate(wire.MaximumModelBytes, wire.MaximumModelFiles) != nil || d.Scope != (wire.Scope{TenantID: tenant, WorkspaceID: workspace, PreparationID: preparation}) || d.AttemptID != attempt || d.ManifestSHA256 != sha || d.UploadID != id || storage != publishedDirectory(d) || id <= after {
			return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
		}
		if e = visit(d, report); e != nil {
			return e
		}
		after = id
	}
}

// Each count has a one-row result. Uniqueness of (upload,asset,offset), exact
// offset alignment, range and final size together prove complete coverage.
func completeNativePreparationUpload(ctx context.Context, q transaction.Executor, d UploadDescriptor) error {
	var total, observed int64
	if e := preparationQueryRow(ctx, q, transaction.SQL(transaction.Dameng, `SELECT COUNT(*) FROM "MODEL_PREPARATION_UPLOAD_CHUNKS" WHERE "UPLOAD_ID"=?`, d.UploadID)).Scan(&total); e != nil {
		return e
	}
	for _, asset := range d.Files {
		var count, valid int64
		e := preparationQueryRow(ctx, q, transaction.SQL(transaction.Dameng, `SELECT COUNT(*),COUNT(CASE WHEN "BYTE_OFFSET">=0 AND MOD("BYTE_OFFSET",?)=0 AND "BYTE_OFFSET"<? AND "SIZE_BYTES"=CASE WHEN ?-"BYTE_OFFSET"<? THEN ?-"BYTE_OFFSET" ELSE ? END THEN 1 END) FROM "MODEL_PREPARATION_UPLOAD_CHUNKS" WHERE "UPLOAD_ID"=? AND "ASSET"=?`, wire.TransferChunkBytes, asset.SizeBytes, asset.SizeBytes, wire.TransferChunkBytes, asset.SizeBytes, wire.TransferChunkBytes, d.UploadID, asset.Path)).Scan(&count, &valid)
		if e != nil {
			return e
		}
		if valid != count {
			return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
		}
		if count != (asset.SizeBytes+wire.TransferChunkBytes-1)/wire.TransferChunkBytes {
			return fail("MODEL_PREPARATION_UPLOAD_INCOMPLETE")
		}
		observed += count
	}
	if total != observed {
		return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	return nil
}

func (s *Service) nativeTrainingReferences(ctx context.Context, q transaction.Executor, tenant, workspace string) ([]trainingReference, error) {
	values := []trainingReference{}
	var after *trainingReference
	for {
		// Reuse the existing collection transport ceiling; the analysis policy owns
		// the total number of versions, including a single overflow sentinel.
		limit := min(pagination.MaximumLimit, s.policy().ScanMaximumVersions+1-len(values))
		query := `SELECT DISTINCT "DATASET_ID","DATASET_VERSION_ID","DATASET_MANIFEST_SHA256" FROM "TRAINING_JOBS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=?`
		args := []any{tenant, workspace}
		if after != nil {
			query += ` AND ("DATASET_ID">? OR ("DATASET_ID"=? AND "DATASET_VERSION_ID">?) OR ("DATASET_ID"=? AND "DATASET_VERSION_ID"=? AND "DATASET_MANIFEST_SHA256">?))`
			args = append(args, after.DatasetID, after.DatasetID, after.VersionID, after.DatasetID, after.VersionID, after.ManifestSHA256)
		}
		query += ` ORDER BY "DATASET_ID","DATASET_VERSION_ID","DATASET_MANIFEST_SHA256"`
		args = append(args, limit)
		text, bound, ok := preparationDMPage(query, pagination.MaximumLimit).bind(args)
		if !ok {
			return nil, fail("MODEL_HOLDOUT_DATA_INVALID")
		}
		rows, e := preparationQuery(ctx, q, transaction.SQL(transaction.Dameng, text, bound...))
		if e != nil {
			return nil, e
		}
		page, e := s.scanTrainingReferences(rows)
		rows.Close()
		if e != nil {
			return nil, e
		}
		if len(page) > limit {
			return nil, fail("MODEL_HOLDOUT_DATA_INVALID")
		}
		for _, value := range page {
			if after != nil && !preparationReferenceAfter(value, *after) {
				return nil, fail("MODEL_HOLDOUT_DATA_INVALID")
			}
			values = append(values, value)
			last := value
			after = &last
		}
		if len(values) > s.policy().ScanMaximumVersions {
			return nil, fail("MODEL_HOLDOUT_ANALYSIS_LIMIT")
		}
		if len(page) < limit {
			return values, nil
		}
	}
}
func preparationReferenceAfter(value, after trainingReference) bool {
	if value.DatasetID != after.DatasetID {
		return value.DatasetID > after.DatasetID
	}
	if value.VersionID != after.VersionID {
		return value.VersionID > after.VersionID
	}
	return value.ManifestSHA256 > after.ManifestSHA256
}

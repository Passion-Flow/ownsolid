package training

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// One retained descriptor per fetch is a streaming invariant, not a new
// customer limit. No materialized driver result grows with the installation.
func walkNativeTrainingOutputs(ctx context.Context, q transaction.Executor, tenant string, visit func(UploadDescriptor) error) error {
	for _, source := range []struct{ kind, query string }{
		{"checkpoint", `SELECT "ID","WORKSPACE_ID","JOB_ID","ATTEMPT_ID","RECEIPT_SHA256","FILE_INVENTORY","STORAGE_KEY" FROM "TRAINING_CHECKPOINTS" WHERE "TENANT_ID"=?`},
		{"artifact", `SELECT "ID","WORKSPACE_ID","JOB_ID","ATTEMPT_ID","MANIFEST_SHA256","FILE_INVENTORY","STORAGE_KEY" FROM "TRAINING_ARTIFACTS" WHERE "TENANT_ID"=?`},
	} {
		after := ""
		for {
			query, args := source.query, []any{tenant}
			if after != "" {
				query += ` AND "ID">?`
				args = append(args, after)
			}
			query += ` ORDER BY "ID" LIMIT 1`
			var id, workspace, job, attempt, sha, storage string
			var raw []byte
			e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, query, args...)).Scan(&id, &workspace, &job, &attempt, &sha, &raw, &storage)
			if errors.Is(e, sql.ErrNoRows) {
				break
			}
			if e != nil {
				return e
			}
			var d UploadDescriptor
			if wire.DecodeStrict(raw, &d) != nil || d.Validate(wire.MaximumCheckpointBytes, wire.MaximumCheckpointFiles) != nil || d.Scope != (wire.Scope{TenantID: tenant, WorkspaceID: workspace, JobID: job}) || d.AttemptID != attempt || d.ManifestSHA256 != sha || d.Kind != source.kind || storage != publishedDirectory(d) || d.UploadID != id || id <= after {
				return fail("TRAINING_STORAGE_DATA_INVALID")
			}
			if e = visit(d); e != nil {
				return e
			}
			after = id
		}
	}
	return nil
}

// The (upload,asset,offset) primary key makes the valid-offset count exact:
// every positive expected chunk must occur once, with its exact final size.
// A separate total rejects rows for assets absent from the retained manifest.
func completeNativeTrainingUpload(ctx context.Context, q transaction.Executor, d UploadDescriptor) error {
	var total, observed int64
	if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT COUNT(*) FROM "TRAINING_UPLOAD_CHUNKS" WHERE "UPLOAD_ID"=?`, d.UploadID)).Scan(&total); e != nil {
		return e
	}
	for _, asset := range d.Files {
		var count, valid int64
		e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT COUNT(*),COUNT(CASE WHEN "BYTE_OFFSET">=0 AND MOD("BYTE_OFFSET",?)=0 AND "BYTE_OFFSET"<? AND "SIZE_BYTES"=CASE WHEN ?-"BYTE_OFFSET"<? THEN ?-"BYTE_OFFSET" ELSE ? END THEN 1 END) FROM "TRAINING_UPLOAD_CHUNKS" WHERE "UPLOAD_ID"=? AND "ASSET"=?`, wire.TransferChunkBytes, asset.SizeBytes, asset.SizeBytes, wire.TransferChunkBytes, asset.SizeBytes, wire.TransferChunkBytes, d.UploadID, asset.Path)).Scan(&count, &valid)
		if e != nil {
			return e
		}
		if valid != count {
			return fail("TRAINING_STORAGE_DATA_INVALID")
		}
		if count != (asset.SizeBytes+wire.TransferChunkBytes-1)/wire.TransferChunkBytes {
			return fail("TRAINING_UPLOAD_INCOMPLETE")
		}
		observed += count
	}
	if total != observed {
		return fail("TRAINING_STORAGE_DATA_INVALID")
	}
	return nil
}

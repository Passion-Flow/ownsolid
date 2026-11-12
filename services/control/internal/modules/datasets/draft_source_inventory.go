package datasets

import (
	"context"
	"encoding/json"
	"path"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const (
	normalizedSourceSnapshotSchema = 1
	originalSourceSnapshotSchema   = 2
)

// This structural page bound closes the SQL result before hashing a file and
// keeps receipt metadata memory independent of the number or size of sources.
const sourceInventoryPageSize = 64

func snapshotSchemaVersion(references snapshotReferences) int {
	for _, source := range references.Sources {
		if source.OriginalFormat != "" {
			return originalSourceSnapshotSchema
		}
	}
	return normalizedSourceSnapshotSchema
}

func snapshotSourcePath(source SnapshotSource) (string, error) {
	if !wire.ValidID(source.ObjectID) || !wire.ValidDigest(source.SHA256) ||
		source.SizeBytes < 1 || source.SizeBytes > wire.MaximumBytesCeiling {
		return "", failure("DATASET_SNAPSHOT_INVALID")
	}
	if source.OriginalFormat == "" {
		return objectPath(source.ObjectID), nil
	}
	name := draftOriginalFile(source.OriginalFormat)
	if name == "" {
		return "", failure("DATASET_SNAPSHOT_INVALID")
	}
	return path.Join("objects", source.ObjectID, name), nil
}

func decodeDraftSourceReport(format SourceFormat, raw []byte, normalizedSHA256 string, normalizedBytes int64) (SourceImportReport, error) {
	var report SourceImportReport
	if draftOriginalFile(format) == "" || !wire.ValidDigest(normalizedSHA256) ||
		normalizedBytes < 1 || normalizedBytes > wire.MaximumBytesCeiling ||
		json.Unmarshal(raw, &report) != nil || report.SourceBytes < 1 ||
		report.SourceBytes > wire.MaximumBytesCeiling || !wire.ValidDigest(report.SourceSHA256) ||
		report.OutputBytes != normalizedBytes || report.OutputSHA256 != normalizedSHA256 {
		return SourceImportReport{}, failure("DATASET_DATA_INVALID")
	}
	return report, nil
}

// The receipt exists only in the registered PostgreSQL lineage. The operator
// inventories the entire installed tenant, including retained history, using
// the same scope as the normalized sources and paired logical database dump.
func walkOriginalSourceReferences(ctx context.Context, q transaction.Executor, tenant string, visit func(SnapshotSource) error) error {
	if q.Dialect() != transaction.PostgreSQL {
		return nil
	}
	var after *string
	for {
		rows, err := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `
            SELECT d.id::text,d.source_object_id::text,c.source_format,c.report,
                   d.source_sha256,d.expected_bytes,d.source_received_at IS NOT NULL
            FROM dataset_drafts d JOIN dataset_draft_import_candidates c
              ON (c.tenant_id,c.workspace_id,c.dataset_id,c.draft_id,c.site_id,c.cell_id)
               = (d.tenant_id,d.workspace_id,d.dataset_id,d.id,d.site_id,d.cell_id)
            WHERE d.tenant_id=$1 AND ($2::uuid IS NULL OR d.id>$2::uuid)
            ORDER BY d.id LIMIT $3`, tenant, after, sourceInventoryPageSize))
		if err != nil {
			return err
		}
		batch := make([]SnapshotSource, 0, sourceInventoryPageSize)
		for rows.Next() {
			var id, format, normalizedSHA256 string
			var raw []byte
			var normalizedBytes int64
			var received bool
			var source SnapshotSource
			if err = rows.Scan(&id, &source.ObjectID, &format, &raw, &normalizedSHA256, &normalizedBytes, &received); err != nil {
				break
			}
			if !wire.ValidID(id) || !wire.ValidID(source.ObjectID) || !received {
				err = failure("DATASET_DATA_INVALID")
				break
			}
			source.OriginalFormat = SourceFormat(format)
			var report SourceImportReport
			report, err = decodeDraftSourceReport(source.OriginalFormat, raw, normalizedSHA256, normalizedBytes)
			if err != nil {
				break
			}
			source.SHA256, source.SizeBytes = report.SourceSHA256, report.SourceBytes
			batch = append(batch, source)
			after = &id
		}
		rows.Close()
		if err != nil {
			return err
		}
		if err = rows.Err(); err != nil {
			return err
		}
		for _, source := range batch {
			if err = visit(source); err != nil {
				return err
			}
		}
		if len(batch) < sourceInventoryPageSize {
			return nil
		}
	}
}

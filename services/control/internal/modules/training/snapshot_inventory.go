package training

import (
	"context"
	"encoding/json"
	"path"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"sort"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

const snapshotFile = "snapshot.json"

type snapshotReferences struct {
	TenantID string             `json:"tenant_id"`
	Outputs  []UploadDescriptor `json:"outputs"`
}
type SnapshotFile struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}
type FileSnapshot struct {
	SchemaVersion int                `json:"schema_version"`
	Namespace     storeNamespace     `json:"namespace"`
	References    snapshotReferences `json:"references"`
	Files         []SnapshotFile     `json:"files"`
}

func (s *Service) snapshotReferences(ctx context.Context) (snapshotReferences, error) {
	return s.storedReferences(ctx, true)
}
func (s *Service) storedReferences(ctx context.Context, bounded bool) (snapshotReferences, error) {
	out := snapshotReferences{Outputs: []UploadDescriptor{}}
	e := s.trainingStorageTransaction(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		if e := q.QueryRow(ctx, acceptedAdapterSQL(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1")).Scan(&out.TenantID); e != nil {
			return fail("TRAINING_INSTALLATION_REQUIRED")
		}
		rows, e := q.Query(ctx, transferStatement(q, `SELECT id::text,tenant_id::text,workspace_id::text,job_id::text,attempt_id::text,receipt_sha256,file_inventory,storage_key,'checkpoint' FROM training_checkpoints WHERE tenant_id=$1 UNION ALL SELECT id::text,tenant_id::text,workspace_id::text,job_id::text,attempt_id::text,manifest_sha256,file_inventory,storage_key,'artifact' FROM training_artifacts WHERE tenant_id=$1 ORDER BY 1`, `SELECT id,tenant_id,workspace_id,job_id,attempt_id,receipt_sha256,file_inventory,storage_key,'checkpoint' FROM training_checkpoints WHERE tenant_id=? UNION ALL SELECT id,tenant_id,workspace_id,job_id,attempt_id,manifest_sha256,file_inventory,storage_key,'artifact' FROM training_artifacts WHERE tenant_id=? ORDER BY 1`, []any{out.TenantID}, []any{out.TenantID, out.TenantID}))
		if e != nil {
			return e
		}
		defer rows.Close()
		fileCount := 1
		for rows.Next() {
			var id, tenant, workspace, job, attempt, sha, storage, kind string
			var raw []byte
			if e = rows.Scan(&id, &tenant, &workspace, &job, &attempt, &sha, &raw, &storage, &kind); e != nil {
				return e
			}
			var descriptor UploadDescriptor
			if wire.DecodeStrict(raw, &descriptor) != nil || descriptor.Validate(wire.MaximumCheckpointBytes, wire.MaximumCheckpointFiles) != nil || descriptor.Scope != (wire.Scope{TenantID: tenant, WorkspaceID: workspace, JobID: job}) || descriptor.AttemptID != attempt || descriptor.ManifestSHA256 != sha || descriptor.Kind != kind || storage != publishedDirectory(descriptor) || descriptor.Scope.TenantID != out.TenantID || descriptor.UploadID != id {
				if bounded {
					return fail("TRAINING_SNAPSHOT_INVALID")
				}
				return fail("TRAINING_STORAGE_DATA_INVALID")
			}
			fileCount += len(descriptor.Files) + 1
			if bounded && fileCount > s.policy().SnapshotMaximumFiles {
				return fail("TRAINING_SNAPSHOT_LIMIT_REACHED")
			}
			out.Outputs = append(out.Outputs, descriptor)
		}
		return rows.Err()
	})
	if e != nil {
		return snapshotReferences{}, e
	}
	return out, nil
}

func referencesEqual(a, b snapshotReferences) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
func snapshotFileSet(references snapshotReferences) (map[string]SnapshotFile, error) {
	if !datawire.ValidID(references.TenantID) {
		return nil, fail("TRAINING_SNAPSHOT_INVALID")
	}
	out := map[string]SnapshotFile{namespaceFile: {Path: namespaceFile, SizeBytes: -1}}
	ids := map[string]bool{}
	for _, descriptor := range references.Outputs {
		if descriptor.Validate(wire.MaximumCheckpointBytes, wire.MaximumCheckpointFiles) != nil || descriptor.Scope.TenantID != references.TenantID || ids[descriptor.UploadID] {
			return nil, fail("TRAINING_SNAPSHOT_INVALID")
		}
		ids[descriptor.UploadID] = true
		prefix := publishedDirectory(descriptor)
		for _, asset := range descriptor.Files {
			relative := path.Join(prefix, asset.Path)
			if _, exists := out[relative]; exists {
				return nil, fail("TRAINING_SNAPSHOT_INVALID")
			}
			out[relative] = SnapshotFile{Path: relative, SizeBytes: asset.SizeBytes, SHA256: asset.SHA256}
		}
		relative := path.Join(prefix, publicationIndex)
		out[relative] = SnapshotFile{Path: relative, SizeBytes: -1}
	}
	return out, nil
}
func sortedSnapshotFiles(files map[string]SnapshotFile) []SnapshotFile {
	out := make([]SnapshotFile, 0, len(files))
	for _, file := range files {
		out = append(out, file)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

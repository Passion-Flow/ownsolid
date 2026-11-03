package modelpreparations

import (
	"context"
	"encoding/json"
	"path"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"sort"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
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
	out := snapshotReferences{Outputs: []UploadDescriptor{}}
	store, repositoryError := s.runtimeRepository()
	if repositoryError != nil {
		return out, repositoryError
	}
	if e := preparationIDRow(preparationQueryRow(ctx, store, preparationSelectedSQL(store, preparationDM(`SELECT "TENANT_ID" FROM "INSTALLATION_STATE" WHERE "ID"=1 AND "BOOTSTRAP_COMPLETE"=1`), "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete", `SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete`)), 0).Scan(&out.TenantID); e != nil {
		return out, preparationInstallationFailure(store, e)
	}
	if store.Dialect() == transaction.Dameng {
		fileCount := 1
		e := walkNativePreparationOutputs(ctx, store, out.TenantID, false, func(d UploadDescriptor, _ []byte) error {
			fileCount += len(d.Files) + 2
			if fileCount > s.policy().SnapshotMaximumFiles {
				return fail("MODEL_PREPARATION_SNAPSHOT_LIMIT_REACHED")
			}
			out.Outputs = append(out.Outputs, d)
			return nil
		})
		if e != nil {
			return snapshotReferences{}, e
		}
		return out, nil
	}
	rows, e := preparationQuery(ctx, store, preparationSQL(store, `SELECT id::text,workspace_id::text,preparation_id::text,attempt_id::text,manifest_sha256,descriptor,storage_path FROM model_preparation_artifacts WHERE tenant_id=$1 ORDER BY id`, `SELECT id,workspace_id,preparation_id,attempt_id,manifest_sha256,descriptor,storage_path FROM model_preparation_artifacts WHERE tenant_id=? ORDER BY id`, out.TenantID))
	if e != nil {
		return out, e
	}
	defer rows.Close()
	fileCount := 1
	for rows.Next() {
		var id, workspace, preparation, attempt, sha, storage string
		var raw []byte
		if e = rows.Scan(&id, &workspace, &preparation, &attempt, &sha, &raw, &storage); e != nil {
			return out, e
		}
		var d UploadDescriptor
		if wire.DecodeStrict(raw, &d) != nil || d.Validate(wire.MaximumModelBytes, wire.MaximumModelFiles) != nil || d.Scope != (wire.Scope{TenantID: out.TenantID, WorkspaceID: workspace, PreparationID: preparation}) || d.AttemptID != attempt || d.ManifestSHA256 != sha || d.UploadID != id || storage != publishedDirectory(d) {
			return out, fail("MODEL_PREPARATION_SNAPSHOT_INVALID")
		}
		fileCount += len(d.Files) + 2
		if fileCount > s.policy().SnapshotMaximumFiles {
			return out, fail("MODEL_PREPARATION_SNAPSHOT_LIMIT_REACHED")
		}
		out.Outputs = append(out.Outputs, d)
	}
	return out, rows.Err()
}
func referencesEqual(a, b snapshotReferences) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
func snapshotFileSet(references snapshotReferences) (map[string]SnapshotFile, error) {
	if !datawire.ValidID(references.TenantID) {
		return nil, fail("MODEL_PREPARATION_SNAPSHOT_INVALID")
	}
	out := map[string]SnapshotFile{namespaceFile: {Path: namespaceFile, SizeBytes: -1}}
	ids := map[string]bool{}
	for _, d := range references.Outputs {
		if d.Validate(wire.MaximumModelBytes, wire.MaximumModelFiles) != nil || d.Scope.TenantID != references.TenantID || ids[d.UploadID] {
			return nil, fail("MODEL_PREPARATION_SNAPSHOT_INVALID")
		}
		ids[d.UploadID] = true
		prefix := publishedDirectory(d)
		for _, asset := range d.Files {
			relative := path.Join(prefix, asset.Path)
			if _, exists := out[relative]; exists {
				return nil, fail("MODEL_PREPARATION_SNAPSHOT_INVALID")
			}
			out[relative] = SnapshotFile{Path: relative, SizeBytes: asset.SizeBytes, SHA256: asset.SHA256}
		}
		for _, name := range []string{publicationIndex, reportFile} {
			relative := path.Join(prefix, name)
			file := SnapshotFile{Path: relative, SizeBytes: -1}
			if name == reportFile {
				file.SHA256 = d.ManifestSHA256
			}
			out[relative] = file
		}
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

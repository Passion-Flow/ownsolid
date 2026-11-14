package datasets

import (
	"context"
	"encoding/json"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"path"
	"sort"
)

// SnapshotSource binds one normalized file or a receipt-bound CSV/Parquet original.
// An empty OriginalFormat retains the legacy normalized-JSONL role.
type SnapshotSource struct {
	ObjectID       string       `json:"object_id"`
	SHA256         string       `json:"sha256"`
	SizeBytes      int64        `json:"size_bytes"`
	OriginalFormat SourceFormat `json:"original_format,omitempty"`
}

// SnapshotVersion binds a published version to its immutable publication files.
type SnapshotVersion struct {
	ID              string        `json:"id"`
	DraftID         string        `json:"draft_id"`
	JobID           string        `json:"job_id"`
	AttemptID       string        `json:"attempt_id"`
	EffectiveSHA256 string        `json:"effective_sha256"`
	MappingSHA256   string        `json:"mapping_sha256"`
	Manifest        wire.Manifest `json:"manifest"`
}
type snapshotReferences struct {
	TenantID string            `json:"tenant_id"`
	Sources  []SnapshotSource  `json:"sources"`
	Versions []SnapshotVersion `json:"versions"`
}

// SnapshotFile pins one private relative path, exact byte count and SHA-256.
type SnapshotFile struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

// FileSnapshot is the operator transfer manifest. Its version is independent
// of the store namespace format and includes every authoritative file role.
type FileSnapshot struct {
	SchemaVersion int                `json:"schema_version"`
	Namespace     storeNamespace     `json:"namespace"`
	References    snapshotReferences `json:"references"`
	Files         []SnapshotFile     `json:"files"`
}

const snapshotName = "snapshot.json"

func (s *Service) snapshotReferences(ctx context.Context) (snapshotReferences, error) {
	repo, e := s.repository()
	if e != nil {
		return snapshotReferences{}, e
	}

	out := snapshotReferences{Sources: []SnapshotSource{}, Versions: []SnapshotVersion{}}
	if e := repo.QueryRow(ctx, datasetSQL(repo, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete", `SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete`, nil, nil, transaction.SQL(transaction.Dameng, dmSnapshotTenant))).Scan(&out.TenantID); e != nil {
		return out, failure("DATASET_INSTALLATION_REQUIRED")
	}
	rows, e := repo.Query(ctx, datasetSQL(repo, "SELECT source_object_id::text,source_sha256,expected_bytes FROM dataset_drafts WHERE tenant_id=$1 AND source_received_at IS NOT NULL UNION ALL SELECT object_id::text,source_sha256,source_bytes FROM mongodb_dataset_imports WHERE tenant_id=$1 AND source_sha256 IS NOT NULL ORDER BY 1", `SELECT source_object_id,source_sha256,expected_bytes FROM dataset_drafts WHERE tenant_id=? AND source_received_at IS NOT NULL ORDER BY source_object_id`, []any{out.TenantID}, []any{out.TenantID}, transaction.SQL(transaction.Dameng, dmSnapshotSources, out.TenantID)))
	if e != nil {
		return committed(out, e)
	}
	for rows.Next() {
		var source SnapshotSource
		if e = rows.Scan(&source.ObjectID, &source.SHA256, &source.SizeBytes); e != nil {
			rows.Close()
			return committed(out, e)
		}
		out.Sources = append(out.Sources, source)
		if len(out.Sources) > s.policy().SnapshotMaximumFiles {
			rows.Close()
			return out, failure("DATASET_SNAPSHOT_LIMIT_REACHED")
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return committed(out, rows.Err())
	}
	if e = walkOriginalSourceReferences(ctx, repo, out.TenantID, func(source SnapshotSource) error {
		out.Sources = append(out.Sources, source)
		if len(out.Sources) > s.policy().SnapshotMaximumFiles {
			return failure("DATASET_SNAPSHOT_LIMIT_REACHED")
		}
		return nil
	}); e != nil {
		return committed(out, e)
	}
	if snapshotSchemaVersion(out) == originalSourceSnapshotSchema {
		sort.Slice(out.Sources, func(i, j int) bool {
			if out.Sources[i].ObjectID != out.Sources[j].ObjectID {
				return out.Sources[i].ObjectID < out.Sources[j].ObjectID
			}
			return out.Sources[i].OriginalFormat < out.Sources[j].OriginalFormat
		})
	}
	rows, e = repo.Query(ctx, datasetSQL(repo, "SELECT "+versionFields+" FROM dataset_versions WHERE tenant_id=$1 ORDER BY id", `SELECT id,dataset_id,workspace_id,draft_id,job_id,attempt_id,ordinal,parent_version_id,published_by,published_at,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256,retired_at,retire_reason FROM dataset_versions WHERE tenant_id=? ORDER BY id`, []any{out.TenantID}, []any{out.TenantID}, transaction.SQL(transaction.Dameng, dmSnapshotVersions, out.TenantID)))
	if e != nil {
		return committed(out, e)
	}
	defer rows.Close()
	fileCount := len(out.Sources)
	for rows.Next() {
		v, e := scanVersion(rows)
		if e != nil {
			return committed(out, e)
		}
		out.Versions = append(out.Versions, SnapshotVersion{v.ID, v.DraftID, v.JobID, v.AttemptID, v.EffectiveSourceSHA256, v.RowMappingSHA256, v.Manifest})
		fileCount += versionFileCount(v.Manifest)
		if fileCount > s.policy().SnapshotMaximumFiles {
			return out, failure("DATASET_SNAPSHOT_LIMIT_REACHED")
		}
	}
	return committed(out, rows.Err())
}
func referenceFiles(references snapshotReferences) (map[string]SnapshotFile, error) {
	if !wire.ValidID(references.TenantID) {
		return nil, failure("DATASET_SNAPSHOT_INVALID")
	}
	out := map[string]SnapshotFile{}
	normalized, originals := map[string]bool{}, map[string]bool{}
	for _, source := range references.Sources {
		name, e := snapshotSourcePath(source)
		if e != nil {
			return nil, e
		}
		if _, exists := out[name]; exists {
			return nil, failure("DATASET_SNAPSHOT_INVALID")
		}
		if source.OriginalFormat == "" {
			normalized[source.ObjectID] = true
		} else {
			if originals[source.ObjectID] {
				return nil, failure("DATASET_SNAPSHOT_INVALID")
			}
			originals[source.ObjectID] = true
		}
		out[name] = SnapshotFile{name, source.SHA256, source.SizeBytes}
	}
	for object := range originals {
		if !normalized[object] {
			return nil, failure("DATASET_SNAPSHOT_INVALID")
		}
	}
	versions := map[string]bool{}
	for _, v := range references.Versions {
		if !wire.ValidID(v.ID) || !wire.ValidID(v.DraftID) || !wire.ValidID(v.JobID) || !wire.ValidID(v.AttemptID) || !wire.ValidDigest(v.EffectiveSHA256) || !wire.ValidDigest(v.MappingSHA256) || versions[v.ID] {
			return nil, failure("DATASET_SNAPSHOT_INVALID")
		}
		versions[v.ID] = true
		if v.Manifest.ValidateStored(v.EffectiveSHA256, wire.MaximumRowsCeiling, wire.MaximumResultBytesCeiling) != nil {
			return nil, failure("DATASET_SNAPSHOT_INVALID")
		}
		prefix := versionDirectory(v.ID, v.AttemptID)
		for _, asset := range append(append([]wire.Asset{}, v.Manifest.Files...), v.Manifest.ManifestFile) {
			name := path.Join(prefix, asset.Path)
			out[name] = SnapshotFile{name, asset.SHA256, asset.SizeBytes}
		}
		for name, digest := range map[string]string{"effective.jsonl": v.EffectiveSHA256, "mapping.jsonl": v.MappingSHA256, "publication.json": ""} {
			relative := path.Join(prefix, name)
			out[relative] = SnapshotFile{relative, digest, -1}
		}
	}
	return out, nil
}
func referencesEqual(a, b snapshotReferences) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
func sortedFiles(files map[string]SnapshotFile) []SnapshotFile {
	out := make([]SnapshotFile, 0, len(files))
	for _, file := range files {
		out = append(out, file)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

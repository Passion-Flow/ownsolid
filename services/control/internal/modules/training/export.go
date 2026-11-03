package training

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"database/sql"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type AdapterProvenance struct {
	SchemaVersion         int                    `json:"schema_version"`
	Kind                  string                 `json:"kind"`
	JobID                 string                 `json:"job_id"`
	ArtifactID            string                 `json:"artifact_id"`
	ModelID               string                 `json:"model_id"`
	BaseRepository        string                 `json:"base_repository"`
	BaseRevision          string                 `json:"base_revision"`
	ModelCatalogSHA256    string                 `json:"model_catalog_sha256"`
	DatasetID             string                 `json:"dataset_id"`
	DatasetVersionID      string                 `json:"dataset_version_id"`
	DatasetManifestSHA256 string                 `json:"dataset_manifest_sha256"`
	Configuration         wire.Configuration     `json:"configuration"`
	ConfigurationSHA256   string                 `json:"configuration_sha256"`
	ExecutionIdentity     wire.ExecutionIdentity `json:"execution_identity"`
	Packages              wire.Packages          `json:"packages"`
	GlobalStep            int64                  `json:"global_step"`
	TrainingLoss          float64                `json:"training_loss"`
	CreatedAt             time.Time              `json:"created_at"`
	Files                 []datawire.Asset       `json:"files"`
	TargetQualification   string                 `json:"target_qualification"`
}

type AdapterExport struct {
	File     *httpserver.Download
	Manifest *httpserver.MemoryDownload
}

// Export returns only a currently authorized successful immutable adapter or manufacturer provenance, never private checkpoint serialization.
func (s *Service) Export(ctx context.Context, a identity.Auth, workspace, id, asset string, requestID string) (AdapterExport, error) {
	var out AdapterExport
	if asset != "manifest.json" && !wire.AdapterAssetAllowed(asset) {
		return out, fail("TRAINING_NOT_FOUND")
	}
	if s.Store == nil {
		return out, fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	var descriptor UploadDescriptor
	var provenance AdapterProvenance
	admit := func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		j, e := lockedJobWithin(ctx, tx, w, id)
		if e != nil {
			return e
		}
		if j.State != "succeeded" || j.AcceptedArtifactID == nil || j.CurrentAttemptID == nil {
			return fail("TRAINING_RESULT_UNAVAILABLE")
		}
		var raw, metadata []byte
		var sha, storage string
		var created time.Time
		e = tx.QueryRow(ctx, transferStatement(tx, `SELECT r.file_inventory,u.manifest_bytes,r.manifest_sha256,r.storage_key,r.created_at FROM training_artifacts r JOIN training_uploads u ON u.id=r.id AND u.state='accepted' WHERE r.tenant_id=$1 AND r.workspace_id=$2 AND r.job_id=$3 AND r.id=$4 AND r.attempt_id=$5`, `SELECT r.file_inventory,u.manifest_bytes,r.manifest_sha256,r.storage_key,r.created_at FROM training_artifacts r JOIN training_uploads u ON u.id=r.id AND u.state='accepted' WHERE r.tenant_id=? AND r.workspace_id=? AND r.job_id=? AND r.id=? AND r.attempt_id=?`, []any{w.TenantID, w.ID, j.ID, *j.AcceptedArtifactID, *j.CurrentAttemptID}, []any{w.TenantID, w.ID, j.ID, *j.AcceptedArtifactID, *j.CurrentAttemptID})).Scan(&raw, &metadata, &sha, &storage, &created)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("TRAINING_DATA_INVALID")
		}
		if e != nil {
			return e
		}
		var d UploadDescriptor
		if wire.DecodeStrict(raw, &d) != nil || d.Kind != "artifact" || d.UploadID != *j.AcceptedArtifactID || d.Scope != (wire.Scope{TenantID: w.TenantID, WorkspaceID: w.ID, JobID: j.ID}) || d.AttemptID != *j.CurrentAttemptID || d.ManifestSHA256 != sha || rawDigest(metadata) != sha || storage != publishedDirectory(d) {
			return fail("TRAINING_DATA_INVALID")
		}
		if descriptor.UploadID != "" && (descriptor.UploadID != d.UploadID || descriptor.ManifestSHA256 != d.ManifestSHA256) {
			return fail("TRAINING_RESULT_CONFLICT")
		}
		var result wire.Result
		if wire.DecodeStrict(metadata, &result) != nil || result.Status != "trained" || result.TrainingLoss == nil || result.Bindings == nil || result.Bindings.Scope != d.Scope || result.Bindings.ConfigurationSHA256 != j.ConfigurationSHA256 {
			return fail("TRAINING_DATA_INVALID")
		}
		descriptor = d
		catalog := agentwire.ApprovedModel()
		provenance = AdapterProvenance{SchemaVersion: 1, Kind: "projectone-lora-adapter", JobID: j.ID, ArtifactID: d.UploadID, ModelID: j.ModelID, BaseRepository: catalog.Repository, BaseRevision: catalog.Revision, ModelCatalogSHA256: j.ModelCatalogSHA256, DatasetID: j.DatasetID, DatasetVersionID: j.DatasetVersionID, DatasetManifestSHA256: j.DatasetManifestSHA256, Configuration: j.Configuration, ConfigurationSHA256: j.ConfigurationSHA256, ExecutionIdentity: j.Recipe.Identity, Packages: result.Bindings.Packages, GlobalStep: result.GlobalStep, TrainingLoss: *result.TrainingLoss, CreatedAt: created, Files: append([]datawire.Asset{}, d.Files...), TargetQualification: "not_established_by_training_output"}
		return nil
	}
	if e := s.trainingActorTransaction(ctx, a, workspace, false, admit); e != nil {
		return out, e
	}
	name := "training-" + id + "-" + asset
	if asset == "manifest.json" {
		if e := s.Store.VerifyPublished(ctx, descriptor); e != nil {
			return out, e
		}
		raw, e := json.Marshal(provenance)
		if e != nil {
			return out, e
		}
		out.Manifest = &httpserver.MemoryDownload{Bytes: raw, SHA256: rawDigest(raw), FileName: name, ContentType: "application/json"}
	} else {
		file, selected, e := s.Store.OpenPublishedAsset(ctx, descriptor, asset)
		if e != nil {
			return out, e
		}
		media := "application/octet-stream"
		if asset == "adapter_config.json" {
			media = "application/json"
		} else if asset == "README.md" {
			media = "text/markdown"
		}
		out.File = &httpserver.Download{File: file, Size: selected.SizeBytes, SHA256: selected.SHA256, FileName: name, ContentType: media}
	}
	e := s.trainingActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if e := admit(ctx, tx, w); e != nil {
			return e
		}
		return transferAuditWithin(ctx, tx, a, w, "training.adapter_exported", id, "Current authorized accepted adapter/provenance export", requestID)
	})
	if e != nil {
		if out.File != nil {
			out.File.File.Close()
		}
		return AdapterExport{}, e
	}
	return out, nil
}

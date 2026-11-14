package training

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

// AdapterProvenance describes an exported adapter and the immutable inputs that produced it.
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

// AdapterExport carries one authorized provenance document or adapter file.
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
	name := "training-" + id + "-" + asset
	err := s.withVerifiedAdapterExport(ctx, a, workspace, id, requestID, func(record acceptedAdapterRecord) error {
		descriptor := record.Descriptor
		if asset != "manifest.json" {
			file, selected, e := s.Store.OpenPublishedAsset(ctx, descriptor, asset)
			if e != nil {
				return e
			}
			media := "application/octet-stream"
			if asset == "adapter_config.json" {
				media = "application/json"
			} else if asset == "README.md" {
				media = "text/markdown"
			}
			out.File = &httpserver.Download{File: file, Size: selected.SizeBytes, SHA256: selected.SHA256, FileName: name, ContentType: media}
			return nil
		}
		if e := s.VerifyAcceptedAdapter(ctx, record.AcceptedAdapter); e != nil {
			return e
		}
		command, result := record.Command, record.result
		catalog := agentwire.ApprovedModel()
		provenance := AdapterProvenance{
			SchemaVersion: 1, Kind: "projectone-lora-adapter", JobID: record.Job.ID, ArtifactID: descriptor.UploadID,
			ModelID: command.ModelID, BaseRepository: catalog.Repository, BaseRevision: catalog.Revision,
			ModelCatalogSHA256: command.ModelCatalogSHA256, DatasetID: command.DatasetID,
			DatasetVersionID: command.DatasetVersionID, DatasetManifestSHA256: command.DatasetManifestSHA256,
			Configuration: command.Configuration, ConfigurationSHA256: command.ConfigurationSHA256,
			ExecutionIdentity: command.Recipe.Identity, Packages: result.Bindings.Packages,
			GlobalStep: result.GlobalStep, TrainingLoss: *result.TrainingLoss, CreatedAt: record.createdAt,
			Files: descriptor.Files, TargetQualification: "not_established_by_training_output",
		}
		raw, e := json.Marshal(provenance)
		if e != nil {
			return e
		}
		out.Manifest = &httpserver.MemoryDownload{Bytes: raw, SHA256: rawDigest(raw), FileName: name, ContentType: "application/json"}
		return nil
	})
	if err != nil {
		if out.File != nil {
			out.File.File.Close()
		}
		return AdapterExport{}, err
	}
	return out, nil
}

// withVerifiedAdapterExport keeps file work outside SQL locks and commits the
// current authority and immutable-result recheck before the caller can expose it.
func (s *Service) withVerifiedAdapterExport(ctx context.Context, a identity.Auth, workspace, id, requestID string, verify func(acceptedAdapterRecord) error) error {
	if s.Store == nil {
		return fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	read := func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) (acceptedAdapterRecord, error) {
		j, e := lockedJobWithin(ctx, tx, w, id)
		if e != nil {
			return acceptedAdapterRecord{}, e
		}
		record, e := readAcceptedAdapterWithin(ctx, tx, w.TenantID, j)
		if errors.Is(e, sql.ErrNoRows) {
			return acceptedAdapterRecord{}, fail("TRAINING_DATA_INVALID")
		}
		return record, e
	}
	var record acceptedAdapterRecord
	if e := s.trainingActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		var e error
		record, e = read(ctx, tx, w)
		return e
	}); e != nil {
		return e
	}
	if e := verify(record); e != nil {
		return e
	}
	return s.trainingActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		current, e := read(ctx, tx, w)
		if e != nil {
			return e
		}
		if current.IdentitySHA256() != record.IdentitySHA256() || current.stopSHA256 != record.stopSHA256 {
			return fail("TRAINING_RESULT_CONFLICT")
		}
		return transferAuditWithin(ctx, tx, a, w, "training.adapter_exported", id, "Current authorized accepted adapter/provenance export", requestID)
	})
}

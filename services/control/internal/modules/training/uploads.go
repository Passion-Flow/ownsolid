package training

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"database/sql"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

type Upload = wire.Upload

func (s *Service) outputDescriptor(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Job, command wire.Command, kind string, raw []byte) (UploadDescriptor, error) {
	var out UploadDescriptor
	if len(raw) > wire.MaximumCheckpointMetadataBytes {
		return out, fail("TRAINING_UPLOAD_INVALID")
	}
	request, e := command.ContainerRequest()
	if e != nil {
		return out, e
	}
	out = UploadDescriptor{SchemaVersion: 1, Scope: command.Scope, AttemptID: command.AttemptID, Kind: kind, ManifestSHA256: rawDigest(raw)}
	var lastSequence, lastStep int64
	if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT last_event_sequence,last_global_step FROM training_attempts WHERE id=$1 AND job_id=$2 FOR UPDATE", "SELECT last_event_sequence,last_global_step FROM training_attempts WHERE id=? AND job_id=? FOR UPDATE", []any{command.AttemptID, j.ID}, []any{command.AttemptID, j.ID})).Scan(&lastSequence, &lastStep); e != nil {
		return out, e
	}
	switch kind {
	case "checkpoint":
		receipt, e := wire.DecodeCheckpoint(raw, request, agentwire.ApprovedModel().Repository)
		if e != nil {
			return out, fail("TRAINING_CHECKPOINT_INVALID")
		}
		out.GlobalStep = receipt.GlobalStep
		if receipt.GlobalStep > lastStep || command.Resume != nil && receipt.GlobalStep <= command.Resume.GlobalStep {
			return out, fail("TRAINING_UPLOAD_INVALID")
		}
		var announced bool
		if e = tx.QueryRow(ctx, transferStatement(tx, `SELECT EXISTS(SELECT 1 FROM training_events WHERE attempt_id=$1 AND kind='checkpoint' AND global_step=$2 AND payload->>'receipt_sha256'=$3)`, `SELECT EXISTS(SELECT 1 FROM training_events WHERE attempt_id=? AND kind='checkpoint' AND global_step=? AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.receipt_sha256'))=?)`, []any{command.AttemptID, receipt.GlobalStep, out.ManifestSHA256}, []any{command.AttemptID, receipt.GlobalStep, out.ManifestSHA256})).Scan(&announced); e != nil {
			return out, e
		}
		if !announced {
			return out, fail("TRAINING_CHECKPOINT_NOT_ANNOUNCED")
		}
		out.Files = append(append([]datawire.Asset{}, receipt.Files...), datawire.Asset{Path: "projectone-checkpoint.json", SizeBytes: int64(len(raw)), SHA256: out.ManifestSHA256})
	case "artifact":
		result, e := wire.DecodeResult(raw, request, agentwire.ApprovedModel().Repository)
		if e != nil || result.Status != "trained" {
			return out, fail("TRAINING_RESULT_INVALID")
		}
		if result.EventCount != lastSequence || result.GlobalStep != lastStep {
			return out, fail("TRAINING_EVENT_ORDER_CONFLICT")
		}
		inputs, e := s.Datasets.TrainingInputSnapshotWithin(ctx, tx, w.TenantID, w.ID, j.DatasetID, j.DatasetVersionID, true)
		if e != nil {
			return out, e
		}
		if inputs.Version.ManifestSHA256 != j.DatasetManifestSHA256 || inputs.Version.Manifest.TrainRows != result.TrainRows || inputs.Version.Manifest.ValidationRows != result.ValidationRows {
			return out, fail("TRAINING_RESULT_INVALID")
		}
		out.GlobalStep = result.GlobalStep
		for _, asset := range result.Files {
			if strings.HasPrefix(asset.Path, "adapter/") {
				asset.Path = strings.TrimPrefix(asset.Path, "adapter/")
				out.Files = append(out.Files, asset)
			}
		}
	default:
		return out, fail("TRAINING_UPLOAD_INVALID")
	}
	return out, nil
}
func rawDigest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

const uploadFields = `u.id::text,u.job_id::text,u.attempt_id::text,u.kind,u.global_step,u.manifest_sha256,u.state,COALESCE((SELECT sum(size_bytes) FROM training_upload_chunks c WHERE c.upload_id=u.id),0)::bigint,u.created_at,u.manifest_bytes`

const mysqlTransferUploadFields = `u.id,u.job_id,u.attempt_id,u.kind,u.global_step,u.manifest_sha256,u.state,COALESCE((SELECT SUM(size_bytes) FROM training_upload_chunks c WHERE c.upload_id=u.id),0),u.created_at,u.manifest_bytes`

func scanUpload(row transaction.Row) (Upload, []byte, error) {
	var out Upload
	var raw []byte
	e := row.Scan(&out.ID, &out.JobID, &out.AttemptID, &out.Kind, &out.GlobalStep, &out.ManifestSHA256, &out.State, &out.ReceivedBytes, &out.CreatedAt, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return out, raw, fail("TRAINING_UPLOAD_NOT_FOUND")
	}
	if e != nil {
		return Upload{}, nil, e
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return out, raw, nil
}

// BeginUpload commits an authenticated immutable metadata intent before preparing its exact private file stage.
func (s *Service) BeginUpload(ctx context.Context, node, credential, task, token, kind string, metadata []byte) (Upload, error) {
	var out Upload
	var descriptor UploadDescriptor
	if s.Store == nil {
		return out, fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	if kind == "artifact" {
		if e := s.verifyMachineInputFiles(ctx, node, credential, task, token); e != nil {
			return Upload{}, e
		}
	}
	e := s.withMachineLeaseActor(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, actor identity.Auth, w workspaces.Workspace, j Job, command wire.Command) error {
		var e error
		descriptor, e = s.outputDescriptor(ctx, tx, w, j, command, kind, metadata)
		if e != nil {
			return e
		}
		existing, raw, e := scanUpload(tx.QueryRow(ctx, transferStatement(tx, "SELECT "+uploadFields+" FROM training_uploads u WHERE u.attempt_id=$1 AND u.kind=$2 AND u.global_step=$3 FOR UPDATE OF u", "SELECT "+mysqlTransferUploadFields+" FROM training_uploads u WHERE u.attempt_id=? AND u.kind=? AND u.global_step=? FOR UPDATE", []any{command.AttemptID, kind, descriptor.GlobalStep}, []any{command.AttemptID, kind, descriptor.GlobalStep})))
		if e == nil {
			if existing.ManifestSHA256 != descriptor.ManifestSHA256 || rawDigest(raw) != existing.ManifestSHA256 || string(raw) != string(metadata) {
				return fail("TRAINING_UPLOAD_CONFLICT")
			}
			if existing.State == "failed" {
				return fail("TRAINING_UPLOAD_RECOVERY_REQUIRED")
			}
			out = existing
			descriptor.UploadID = out.ID
		} else {
			if e.Error() != "TRAINING_UPLOAD_NOT_FOUND" {
				return e
			}
			descriptor.UploadID = uuid.NewString()
			if e = descriptor.Validate(j.Recipe.Resources.MaximumOutputBytes, j.Recipe.Resources.MaximumOutputFiles); e != nil {
				return e
			}
			_, e = tx.Exec(ctx, transferStatement(tx, `INSERT INTO training_uploads(id,tenant_id,workspace_id,job_id,attempt_id,kind,global_step,manifest_sha256,manifest,manifest_bytes,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'uploading')`, `INSERT INTO training_uploads(id,tenant_id,workspace_id,job_id,attempt_id,kind,global_step,manifest_sha256,manifest,manifest_bytes,state) VALUES(?,?,?,?,?,?,?,?,CAST(? AS JSON),?,'uploading')`, []any{descriptor.UploadID, w.TenantID, w.ID, j.ID, command.AttemptID, kind, descriptor.GlobalStep, descriptor.ManifestSHA256, metadata, metadata}, []any{descriptor.UploadID, w.TenantID, w.ID, j.ID, command.AttemptID, kind, descriptor.GlobalStep, descriptor.ManifestSHA256, metadata, metadata}))
			if e != nil {
				return e
			}
			out, _, e = scanUpload(tx.QueryRow(ctx, transferStatement(tx, "SELECT "+uploadFields+" FROM training_uploads u WHERE u.id=$1", "SELECT "+mysqlTransferUploadFields+" FROM training_uploads u WHERE u.id=?", []any{descriptor.UploadID}, []any{descriptor.UploadID})))
			if e != nil {
				return e
			}
			if e = transferAuditWithin(ctx, tx, actor, w, "training.upload_started", j.ID, "Current-authority immutable output upload intent", uuid.NewString()); e != nil {
				return e
			}
		}
		out.Files = append([]datawire.Asset{}, descriptor.Files...)
		out.ChunkBytes = wire.TransferChunkBytes
		return nil
	})
	if e != nil {
		return Upload{}, e
	}
	if e = s.Store.PrepareUpload(descriptor); e != nil {
		return Upload{}, e
	}
	// No filesystem work holds a business lock; losing authority during preparation cannot grant an upload response.
	e = s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Job, command wire.Command) error {
		return nil
	})
	if e != nil {
		return Upload{}, e
	}
	return out, nil
}

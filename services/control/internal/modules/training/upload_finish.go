package training

import (
	"context"
	"encoding/json"
	"errors"

	"database/sql"
	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

const finalizationAsset = ".finalize"

type OutputAcceptance = wire.OutputAcceptance

func completeUpload(ctx context.Context, tx transaction.Executor, d UploadDescriptor) error {
	expected := map[string]map[int64]int64{}
	for _, asset := range d.Files {
		expected[asset.Path] = map[int64]int64{}
		for offset := int64(0); offset < asset.SizeBytes; offset += wire.TransferChunkBytes {
			expected[asset.Path][offset] = min(wire.TransferChunkBytes, asset.SizeBytes-offset)
		}
	}
	rows, e := tx.Query(ctx, transferStatement(tx, "SELECT asset,byte_offset,size_bytes FROM training_upload_chunks WHERE upload_id=$1", "SELECT asset,byte_offset,size_bytes FROM training_upload_chunks WHERE upload_id=?", []any{d.UploadID}, []any{d.UploadID}))
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var asset string
		var offset, size int64
		if e = rows.Scan(&asset, &offset, &size); e != nil {
			return e
		}
		if expected[asset][offset] != size || size < 1 {
			return fail("TRAINING_STORAGE_DATA_INVALID")
		}
		delete(expected[asset], offset)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	for _, parts := range expected {
		if len(parts) != 0 {
			return fail("TRAINING_UPLOAD_INCOMPLETE")
		}
	}
	return nil
}

func (s *Service) clearUploadTicket(ctx context.Context, node, credential, task, token, id, ticket string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(s.policy().TransactionSeconds))
	defer cancel()
	_ = s.withMachineLease(cleanup, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Job, command wire.Command) error {
		_, e := tx.Exec(ctx, transferStatement(tx, "UPDATE training_uploads SET active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL WHERE id=$1 AND active_chunk_id=$2", "UPDATE training_uploads SET active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL WHERE id=? AND active_chunk_id=?", []any{id, ticket}, []any{id, ticket}))
		return e
	})
}

// FinishUpload prepares and independently verifies physical bytes before a second current-authority acceptance transaction.
// Accepting an adapter does not make a job successful or release a GPU; exact positive stop evidence is separately required.
func (s *Service) FinishUpload(ctx context.Context, node, credential, task, token, id string) (OutputAcceptance, error) {
	var out OutputAcceptance
	var descriptor UploadDescriptor
	var shape BaseShape
	var configuration wire.Configuration
	ticket := uuid.NewString()
	if s.Store == nil {
		return out, fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	e := s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Job, command wire.Command) error {
		u, d, e := s.lockedUpload(ctx, tx, w, j, command, id)
		if e != nil {
			return e
		}
		if u.State != "uploading" && u.State != "accepted" {
			return fail("TRAINING_UPLOAD_STATE_CONFLICT")
		}
		if e = completeUpload(ctx, tx, d); e != nil {
			return e
		}
		var active bool
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT COALESCE(active_expires_at>clock_timestamp(),false) FROM training_uploads WHERE id=$1", "SELECT COALESCE(active_expires_at>UTC_TIMESTAMP(6),false) FROM training_uploads WHERE id=?", []any{id}, []any{id})).Scan(&active); e != nil {
			return e
		}
		if active {
			return fail("TRAINING_UPLOAD_BUSY")
		}
		raw, e := s.Models.ConfigurationWithin(ctx, tx, w.TenantID, w.ID, j.ModelID)
		if e != nil {
			return e
		}
		if json.Unmarshal(raw, &shape) != nil || shape.Validate() != nil {
			return fail("TRAINING_BASE_SHAPE_INVALID")
		}
		descriptor = d
		configuration = j.Configuration
		_, e = tx.Exec(ctx, transferStatement(tx, "UPDATE training_uploads SET active_chunk_id=$2,active_asset=$3,active_offset=0,active_expires_at=clock_timestamp()+make_interval(secs=>$4),updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_uploads SET active_chunk_id=?,active_asset=?,active_offset=0,active_expires_at=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{id, ticket, finalizationAsset, s.policy().UploadSeconds}, []any{ticket, finalizationAsset, s.policy().UploadSeconds, id}))
		return e
	})
	if e != nil {
		return out, e
	}
	defer s.clearUploadTicket(ctx, node, credential, task, token, id, ticket)
	if e = s.verifyMachineInputFiles(ctx, node, credential, task, token); e != nil {
		return out, e
	}
	verification, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().UploadSeconds))
	defer cancel()
	storage, e := s.Store.PreparePublishedFiles(verification, descriptor)
	if e != nil {
		return out, e
	}
	if e = s.Store.VerifyPublishedOutput(verification, descriptor, shape, configuration, agentwire.ApprovedModel().Repository); e != nil {
		return out, e
	}
	e = s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Job, command wire.Command) error {
		u, d, e := s.lockedUpload(ctx, tx, w, j, command, id)
		if e != nil {
			return e
		}
		var valid bool
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT COALESCE(active_chunk_id=$2 AND active_asset=$3 AND active_expires_at>clock_timestamp(),false) FROM training_uploads WHERE id=$1", "SELECT COALESCE(active_chunk_id=? AND active_asset=? AND active_expires_at>UTC_TIMESTAMP(6),false) FROM training_uploads WHERE id=?", []any{id, ticket, finalizationAsset}, []any{ticket, finalizationAsset, id})).Scan(&valid); e != nil {
			return e
		}
		if !valid || d.ManifestSHA256 != descriptor.ManifestSHA256 {
			return fail("TRAINING_UPLOAD_TICKET_STALE")
		}
		if u.State != "uploading" && u.State != "accepted" {
			return fail("TRAINING_UPLOAD_STATE_CONFLICT")
		}
		inventory, e := json.Marshal(d)
		if e != nil {
			return e
		}
		var raw []byte
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT manifest_bytes FROM training_uploads WHERE id=$1", "SELECT manifest_bytes FROM training_uploads WHERE id=?", []any{id}, []any{id})).Scan(&raw); e != nil {
			return e
		}
		var existingID, existingSHA, existingStorage string
		query := acceptedAdapterSQL(tx, "SELECT id::text,receipt_sha256,storage_key FROM training_checkpoints WHERE attempt_id=$1 AND global_step=$2", "SELECT id,receipt_sha256,storage_key FROM training_checkpoints WHERE attempt_id=? AND global_step=?", command.AttemptID, d.GlobalStep)
		if d.Kind == "artifact" {
			query = acceptedAdapterSQL(tx, "SELECT id::text,manifest_sha256,storage_key FROM training_artifacts WHERE attempt_id=$1", "SELECT id,manifest_sha256,storage_key FROM training_artifacts WHERE attempt_id=?", command.AttemptID)
		}
		e = tx.QueryRow(ctx, query).Scan(&existingID, &existingSHA, &existingStorage)
		duplicate := e == nil
		if duplicate {
			if u.State != "accepted" || existingID != id || existingSHA != d.ManifestSHA256 || existingStorage != storage {
				return fail("TRAINING_UPLOAD_CONFLICT")
			}
		} else {
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if u.State != "uploading" {
				return fail("TRAINING_STORAGE_DATA_INVALID")
			}
			if d.Kind == "checkpoint" {
				_, e = tx.Exec(ctx, transferStatement(tx, "INSERT INTO training_checkpoints(id,tenant_id,workspace_id,job_id,attempt_id,global_step,receipt_sha256,receipt,file_inventory,storage_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)", "INSERT INTO training_checkpoints(id,tenant_id,workspace_id,job_id,attempt_id,global_step,receipt_sha256,receipt,file_inventory,storage_key) VALUES(?,?,?,?,?,?,?,CAST(? AS JSON),CAST(? AS JSON),?)", []any{id, w.TenantID, w.ID, j.ID, command.AttemptID, d.GlobalStep, d.ManifestSHA256, raw, inventory, storage}, []any{id, w.TenantID, w.ID, j.ID, command.AttemptID, d.GlobalStep, d.ManifestSHA256, raw, inventory, storage}))
			} else {
				_, e = tx.Exec(ctx, transferStatement(tx, "INSERT INTO training_artifacts(id,tenant_id,workspace_id,job_id,attempt_id,manifest_sha256,manifest,file_inventory,storage_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)", "INSERT INTO training_artifacts(id,tenant_id,workspace_id,job_id,attempt_id,manifest_sha256,manifest,file_inventory,storage_key) VALUES(?,?,?,?,?,?,CAST(? AS JSON),CAST(? AS JSON),?)", []any{id, w.TenantID, w.ID, j.ID, command.AttemptID, d.ManifestSHA256, raw, inventory, storage}, []any{id, w.TenantID, w.ID, j.ID, command.AttemptID, d.ManifestSHA256, raw, inventory, storage}))
				if e == nil {
					tag, updateError := tx.Exec(ctx, transferStatement(tx, "UPDATE training_attempts SET outcome=$2,outcome_sha256=$3,updated_at=clock_timestamp() WHERE id=$1 AND outcome IS NULL", "UPDATE training_attempts SET outcome=CAST(? AS JSON),outcome_sha256=?,updated_at=UTC_TIMESTAMP(6) WHERE id=? AND outcome IS NULL", []any{command.AttemptID, raw, d.ManifestSHA256}, []any{raw, d.ManifestSHA256, command.AttemptID}))
					e = updateError
					if e == nil && tag != 1 {
						return fail("TRAINING_RESULT_CONFLICT")
					}
				}
			}
			if e != nil {
				return e
			}
			var a identity.Auth
			if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT requested_by::text,requested_audience FROM training_attempts WHERE id=$1", "SELECT requested_by,requested_audience FROM training_attempts WHERE id=?", []any{command.AttemptID}, []any{command.AttemptID})).Scan(&a.Me.User.ID, &a.Me.Audience); e != nil {
				return e
			}
			if e = transferAuditWithin(ctx, tx, a, w, "training."+d.Kind+"_accepted", j.ID, "Independently verified immutable training output", uuid.NewString()); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE training_uploads SET state='accepted',active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL,updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_uploads SET state='accepted',active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL,updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{id}, []any{id})); e != nil {
			return e
		}
		out = OutputAcceptance{UploadID: id, ReferenceID: id, Kind: d.Kind, GlobalStep: d.GlobalStep, Duplicate: duplicate}
		return nil
	})
	if e != nil {
		return OutputAcceptance{}, e
	}
	return out, nil
}

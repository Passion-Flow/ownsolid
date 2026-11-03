package training

import (
	"context"
	"errors"
	"io"
	"net"

	"database/sql"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type ChunkRequest = wire.ChunkRequest
type ChunkAcceptance = wire.ChunkAcceptance

func (s *Service) lockedUpload(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Job, command wire.Command, id string) (Upload, UploadDescriptor, error) {
	var descriptor UploadDescriptor
	if !datawire.ValidID(id) {
		return Upload{}, descriptor, fail("TRAINING_UPLOAD_NOT_FOUND")
	}
	u, metadata, e := scanUpload(tx.QueryRow(ctx, transferStatement(tx, "SELECT "+uploadFields+" FROM training_uploads u WHERE u.tenant_id=$1 AND u.workspace_id=$2 AND u.job_id=$3 AND u.attempt_id=$4 AND u.id=$5 FOR UPDATE OF u", "SELECT "+mysqlTransferUploadFields+" FROM training_uploads u WHERE u.tenant_id=? AND u.workspace_id=? AND u.job_id=? AND u.attempt_id=? AND u.id=? FOR UPDATE", []any{w.TenantID, w.ID, j.ID, command.AttemptID, id}, []any{w.TenantID, w.ID, j.ID, command.AttemptID, id})))
	if e != nil {
		return u, descriptor, e
	}
	if rawDigest(metadata) != u.ManifestSHA256 {
		return Upload{}, descriptor, fail("TRAINING_STORAGE_DATA_INVALID")
	}
	descriptor, e = s.outputDescriptor(ctx, tx, w, j, command, u.Kind, metadata)
	if e != nil {
		return Upload{}, descriptor, e
	}
	descriptor.UploadID = u.ID
	if descriptor.GlobalStep != u.GlobalStep || descriptor.ManifestSHA256 != u.ManifestSHA256 {
		return Upload{}, descriptor, fail("TRAINING_STORAGE_DATA_INVALID")
	}
	return u, descriptor, nil
}
func validateChunk(d UploadDescriptor, r ChunkRequest) error {
	if !datawire.ValidDigest(r.SHA256) {
		return fail("TRAINING_UPLOAD_INVALID")
	}
	for _, asset := range d.Files {
		if asset.Path == r.Asset && r.Offset >= 0 && r.Offset%wire.TransferChunkBytes == 0 && r.Offset < asset.SizeBytes && r.Length == min(wire.TransferChunkBytes, asset.SizeBytes-r.Offset) {
			return nil
		}
	}
	return fail("TRAINING_UPLOAD_INVALID")
}

// UploadChunk streams outside business locks and accounts bytes only after a fresh current-authority/fence/ticket check.
func (s *Service) UploadChunk(ctx context.Context, node, credential, task, token, id string, input ChunkRequest, body io.Reader) (ChunkAcceptance, error) {
	var out ChunkAcceptance
	var descriptor UploadDescriptor
	ticket := uuid.NewString()
	if s.Store == nil {
		return out, fail("TRAINING_STORAGE_UNAVAILABLE")
	}
	if body == nil {
		return out, fail("TRAINING_UPLOAD_INVALID")
	}
	e := s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Job, command wire.Command) error {
		u, d, e := s.lockedUpload(ctx, tx, w, j, command, id)
		if e != nil {
			return e
		}
		if u.State != "uploading" {
			return fail("TRAINING_UPLOAD_STATE_CONFLICT")
		}
		if e = validateChunk(d, input); e != nil {
			return e
		}
		descriptor = d
		var active bool
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT COALESCE(active_expires_at>clock_timestamp(),false) FROM training_uploads WHERE id=$1", "SELECT COALESCE(active_expires_at>UTC_TIMESTAMP(6),false) FROM training_uploads WHERE id=?", []any{id}, []any{id})).Scan(&active); e != nil {
			return e
		}
		if active {
			return fail("TRAINING_UPLOAD_BUSY")
		}
		var previousSHA string
		var previousSize int64
		e = tx.QueryRow(ctx, transferStatement(tx, "SELECT sha256,size_bytes FROM training_upload_chunks WHERE upload_id=$1 AND asset=$2 AND byte_offset=$3", "SELECT sha256,size_bytes FROM training_upload_chunks WHERE upload_id=? AND asset=? AND byte_offset=?", []any{id, input.Asset, input.Offset}, []any{id, input.Asset, input.Offset})).Scan(&previousSHA, &previousSize)
		if e == nil && (previousSHA != input.SHA256 || previousSize != input.Length) {
			return fail("TRAINING_UPLOAD_CONFLICT")
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		_, e = tx.Exec(ctx, transferStatement(tx, "UPDATE training_uploads SET active_chunk_id=$2,active_asset=$3,active_offset=$4,active_expires_at=clock_timestamp()+make_interval(secs=>$5),updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_uploads SET active_chunk_id=?,active_asset=?,active_offset=?,active_expires_at=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{id, ticket, input.Asset, input.Offset, s.policy().UploadSeconds}, []any{ticket, input.Asset, input.Offset, s.policy().UploadSeconds, id}))
		return e
	})
	if e != nil {
		return out, e
	}
	// Cleanup can clear only this exact ticket and only while current authority remains valid; otherwise it expires naturally.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(s.policy().TransactionSeconds))
		defer cancel()
		_ = s.withMachineLease(cleanup, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Job, command wire.Command) error {
			_, e := tx.Exec(ctx, transferStatement(tx, "UPDATE training_uploads SET active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL WHERE id=$1 AND active_chunk_id=$2", "UPDATE training_uploads SET active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL WHERE id=? AND active_chunk_id=?", []any{id, ticket}, []any{id, ticket}))
			return e
		})
	}()
	stream, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().UploadSeconds))
	_, e = s.Store.WriteChunk(stream, descriptor, input.Asset, input.Offset, input.Length, input.SHA256, body)
	cancel()
	if e != nil {
		var timeout net.Error
		if errors.Is(e, context.DeadlineExceeded) || errors.As(e, &timeout) && timeout.Timeout() {
			return out, fail("TRAINING_UPLOAD_TIMEOUT")
		}
		if errors.Is(e, io.ErrUnexpectedEOF) {
			return out, fail("TRAINING_UPLOAD_CHECKSUM_MISMATCH")
		}
		return out, e
	}
	e = s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Job, command wire.Command) error {
		u, d, e := s.lockedUpload(ctx, tx, w, j, command, id)
		if e != nil {
			return e
		}
		if u.State != "uploading" || d.ManifestSHA256 != descriptor.ManifestSHA256 {
			return fail("TRAINING_UPLOAD_STATE_CONFLICT")
		}
		var valid bool
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT COALESCE(active_chunk_id=$2 AND active_asset=$3 AND active_offset=$4 AND active_expires_at>clock_timestamp(),false) FROM training_uploads WHERE id=$1", "SELECT COALESCE(active_chunk_id=? AND active_asset=? AND active_offset=? AND active_expires_at>UTC_TIMESTAMP(6),false) FROM training_uploads WHERE id=?", []any{id, ticket, input.Asset, input.Offset}, []any{ticket, input.Asset, input.Offset, id})).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return fail("TRAINING_UPLOAD_TICKET_STALE")
		}
		var previousSHA string
		var previousSize int64
		e = tx.QueryRow(ctx, transferStatement(tx, "SELECT sha256,size_bytes FROM training_upload_chunks WHERE upload_id=$1 AND asset=$2 AND byte_offset=$3", "SELECT sha256,size_bytes FROM training_upload_chunks WHERE upload_id=? AND asset=? AND byte_offset=?", []any{id, input.Asset, input.Offset}, []any{id, input.Asset, input.Offset})).Scan(&previousSHA, &previousSize)
		duplicate := e == nil
		if duplicate {
			if previousSHA != input.SHA256 || previousSize != input.Length {
				return fail("TRAINING_UPLOAD_CONFLICT")
			}
		} else {
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if _, e = tx.Exec(ctx, transferStatement(tx, "INSERT INTO training_upload_chunks(upload_id,asset,byte_offset,size_bytes,sha256) VALUES($1,$2,$3,$4,$5)", "INSERT INTO training_upload_chunks(upload_id,asset,byte_offset,size_bytes,sha256) VALUES(?,?,?,?,?)", []any{id, input.Asset, input.Offset, input.Length, input.SHA256}, []any{id, input.Asset, input.Offset, input.Length, input.SHA256})); e != nil {
				return e
			}
			var a identity.Auth
			if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT requested_by::text,requested_audience FROM training_attempts WHERE id=$1", "SELECT requested_by,requested_audience FROM training_attempts WHERE id=?", []any{command.AttemptID}, []any{command.AttemptID})).Scan(&a.Me.User.ID, &a.Me.Audience); e != nil {
				return e
			}
			if e = transferAuditWithin(ctx, tx, a, w, "training.upload_chunk_accepted", j.ID, "Accepted exact current-authority output chunk", uuid.NewString()); e != nil {
				return e
			}
		}
		var received int64
		if e = tx.QueryRow(ctx, transferStatement(tx, "SELECT COALESCE(sum(size_bytes),0)::bigint FROM training_upload_chunks WHERE upload_id=$1", "SELECT COALESCE(sum(size_bytes),0) FROM training_upload_chunks WHERE upload_id=?", []any{id}, []any{id})).Scan(&received); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, transferStatement(tx, "UPDATE training_uploads SET active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL,updated_at=clock_timestamp() WHERE id=$1", "UPDATE training_uploads SET active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL,updated_at=UTC_TIMESTAMP(6) WHERE id=?", []any{id}, []any{id})); e != nil {
			return e
		}
		out = ChunkAcceptance{UploadID: id, Asset: input.Asset, Offset: input.Offset, SizeBytes: input.Length, SHA256: input.SHA256, Duplicate: duplicate, ReceivedBytes: received}
		return nil
	})
	if e != nil {
		return ChunkAcceptance{}, e
	}
	return out, nil
}

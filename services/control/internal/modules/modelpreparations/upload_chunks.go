package modelpreparations

import (
	"database/sql"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"context"
	"errors"
	"io"
	"net"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type ChunkRequest = wire.ChunkRequest
type ChunkAcceptance = wire.ChunkAcceptance

func validateChunk(d UploadDescriptor, r ChunkRequest) error {
	if !datawire.ValidDigest(r.SHA256) {
		return fail("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	for _, asset := range d.Files {
		if asset.Path == r.Asset && r.Offset >= 0 && r.Offset%wire.TransferChunkBytes == 0 && r.Offset < asset.SizeBytes && r.Length == min(wire.TransferChunkBytes, asset.SizeBytes-r.Offset) {
			return nil
		}
	}
	return fail("MODEL_PREPARATION_UPLOAD_INVALID")
}

// UploadChunk streams outside business locks and accounts bytes only after a fresh current-authority/fence/ticket check.
func (s *Service) UploadChunk(ctx context.Context, node, credential, task, token, id string, input ChunkRequest, body io.Reader) (ChunkAcceptance, error) {
	var out ChunkAcceptance
	var descriptor UploadDescriptor
	ticket := uuid.NewString()
	if s.Store == nil {
		return out, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	if body == nil {
		return out, fail("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	e := s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Preparation, command wire.Command) error {
		u, d, _, e := s.lockedUpload(ctx, tx, w, j, command, id)
		if e != nil {
			return e
		}
		if u.State != "uploading" {
			return fail("MODEL_PREPARATION_UPLOAD_STATE_CONFLICT")
		}
		if e = validateChunk(d, input); e != nil {
			return e
		}
		descriptor = d
		var active bool
		if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT CASE WHEN "ACTIVE_EXPIRES_AT">SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM "MODEL_PREPARATION_UPLOADS" WHERE "ID"=?`), "SELECT COALESCE(active_expires_at>clock_timestamp(),false) FROM model_preparation_uploads WHERE id=$1", `SELECT COALESCE(active_expires_at>UTC_TIMESTAMP(6),false) FROM model_preparation_uploads WHERE id=?`, id)).Scan(&active); e != nil {
			return e
		}
		if active {
			return fail("MODEL_PREPARATION_UPLOAD_BUSY")
		}
		var previousSHA string
		var previousSize int64
		e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "SHA256","SIZE_BYTES" FROM "MODEL_PREPARATION_UPLOAD_CHUNKS" WHERE "UPLOAD_ID"=? AND "ASSET"=? AND "BYTE_OFFSET"=?`), "SELECT sha256,size_bytes FROM model_preparation_upload_chunks WHERE upload_id=$1 AND asset=$2 AND byte_offset=$3", `SELECT sha256,size_bytes FROM model_preparation_upload_chunks WHERE upload_id=? AND asset=? AND byte_offset=?`, id, input.Asset, input.Offset)).Scan(&previousSHA, &previousSize)
		if e == nil && (previousSHA != input.SHA256 || previousSize != input.Length) {
			return fail("MODEL_PREPARATION_UPLOAD_CONFLICT")
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		_, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_UPLOADS" SET "ACTIVE_CHUNK_ID"=?,"ACTIVE_ASSET"=?,"ACTIVE_OFFSET"=?,"ACTIVE_EXPIRES_AT"=DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), "UPDATE model_preparation_uploads SET active_chunk_id=$2,active_asset=$3,active_offset=$4,active_expires_at=clock_timestamp()+make_interval(secs=>$5),updated_at=clock_timestamp() WHERE id=$1", `UPDATE model_preparation_uploads SET active_chunk_id=?,active_asset=?,active_offset=?,active_expires_at=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{id, ticket, input.Asset, input.Offset, s.policy().TransferSeconds}, []any{ticket, input.Asset, input.Offset, s.policy().TransferSeconds, id}))
		return e
	})
	if e != nil {
		return out, e
	}
	// Cleanup can clear only this exact ticket and only while current authority remains valid; otherwise it expires naturally.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(s.policy().TransactionSeconds))
		defer cancel()
		_ = s.withMachineLease(cleanup, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Preparation, command wire.Command) error {
			_, e := tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_UPLOADS" SET "ACTIVE_CHUNK_ID"=NULL,"ACTIVE_ASSET"=NULL,"ACTIVE_OFFSET"=NULL,"ACTIVE_EXPIRES_AT"=NULL WHERE "ID"=? AND "ACTIVE_CHUNK_ID"=?`), "UPDATE model_preparation_uploads SET active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL WHERE id=$1 AND active_chunk_id=$2", `UPDATE model_preparation_uploads SET active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL WHERE id=? AND active_chunk_id=?`, id, ticket))
			return e
		})
	}()
	stream, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().TransferSeconds))
	_, e = s.Store.WriteChunk(stream, descriptor, input.Asset, input.Offset, input.Length, input.SHA256, body)
	cancel()
	if e != nil {
		var timeout net.Error
		if errors.Is(e, context.DeadlineExceeded) || errors.As(e, &timeout) && timeout.Timeout() {
			return out, fail("MODEL_PREPARATION_UPLOAD_TIMEOUT")
		}
		if errors.Is(e, io.ErrUnexpectedEOF) {
			return out, fail("MODEL_PREPARATION_UPLOAD_CHECKSUM_MISMATCH")
		}
		return out, e
	}
	e = s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, j Preparation, command wire.Command) error {
		u, d, _, e := s.lockedUpload(ctx, tx, w, j, command, id)
		if e != nil {
			return e
		}
		if u.State != "uploading" || d.ManifestSHA256 != descriptor.ManifestSHA256 {
			return fail("MODEL_PREPARATION_UPLOAD_STATE_CONFLICT")
		}
		var valid bool
		if e = preparationQueryRow(ctx, tx, preparationSelectedRuntimeSQL(tx, preparationDM(`SELECT CASE WHEN "ACTIVE_CHUNK_ID"=? AND "ACTIVE_ASSET"=? AND "ACTIVE_OFFSET"=? AND "ACTIVE_EXPIRES_AT">SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM "MODEL_PREPARATION_UPLOADS" WHERE "ID"=?`), "SELECT COALESCE(active_chunk_id=$2 AND active_asset=$3 AND active_offset=$4 AND active_expires_at>clock_timestamp(),false) FROM model_preparation_uploads WHERE id=$1", `SELECT COALESCE(active_chunk_id=? AND active_asset=? AND active_offset=? AND active_expires_at>UTC_TIMESTAMP(6),false) FROM model_preparation_uploads WHERE id=?`, []any{id, ticket, input.Asset, input.Offset}, []any{ticket, input.Asset, input.Offset, id})).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return fail("MODEL_PREPARATION_UPLOAD_TICKET_STALE")
		}
		var previousSHA string
		var previousSize int64
		e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "SHA256","SIZE_BYTES" FROM "MODEL_PREPARATION_UPLOAD_CHUNKS" WHERE "UPLOAD_ID"=? AND "ASSET"=? AND "BYTE_OFFSET"=?`), "SELECT sha256,size_bytes FROM model_preparation_upload_chunks WHERE upload_id=$1 AND asset=$2 AND byte_offset=$3", `SELECT sha256,size_bytes FROM model_preparation_upload_chunks WHERE upload_id=? AND asset=? AND byte_offset=?`, id, input.Asset, input.Offset)).Scan(&previousSHA, &previousSize)
		duplicate := e == nil
		if duplicate {
			if previousSHA != input.SHA256 || previousSize != input.Length {
				return fail("MODEL_PREPARATION_UPLOAD_CONFLICT")
			}
		} else {
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if _, e = tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`INSERT INTO "MODEL_PREPARATION_UPLOAD_CHUNKS"("UPLOAD_ID","ASSET","BYTE_OFFSET","SIZE_BYTES","SHA256") VALUES(?,?,?,?,?)`), "INSERT INTO model_preparation_upload_chunks(upload_id,asset,byte_offset,size_bytes,sha256) VALUES($1,$2,$3,$4,$5)", `INSERT INTO model_preparation_upload_chunks(upload_id,asset,byte_offset,size_bytes,sha256) VALUES(?,?,?,?,?)`, id, input.Asset, input.Offset, input.Length, input.SHA256)); e != nil {
				return e
			}
			var a identity.Auth
			if e = preparationIDRow(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "REQUESTED_BY","REQUESTED_AUDIENCE" FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "ID"=?`), "SELECT requested_by::text,requested_audience FROM model_preparation_attempts WHERE id=$1", `SELECT requested_by,requested_audience FROM model_preparation_attempts WHERE id=?`, command.AttemptID)), 0).Scan(&a.Me.User.ID, &a.Me.Audience); e != nil {
				return e
			}
			if e = preparationAuditWithin(ctx, tx, a, w, "model.preparation_chunk_accepted", j.ID, "Accepted exact current-authority output chunk", uuid.NewString()); e != nil {
				return e
			}
		}
		var received int64
		if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT COALESCE(SUM("SIZE_BYTES"),0) FROM "MODEL_PREPARATION_UPLOAD_CHUNKS" WHERE "UPLOAD_ID"=?`), "SELECT COALESCE(sum(size_bytes),0)::bigint FROM model_preparation_upload_chunks WHERE upload_id=$1", `SELECT COALESCE(sum(size_bytes),0) FROM model_preparation_upload_chunks WHERE upload_id=?`, id)).Scan(&received); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_UPLOADS" SET "ACTIVE_CHUNK_ID"=NULL,"ACTIVE_ASSET"=NULL,"ACTIVE_OFFSET"=NULL,"ACTIVE_EXPIRES_AT"=NULL,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), "UPDATE model_preparation_uploads SET active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL,updated_at=clock_timestamp() WHERE id=$1", `UPDATE model_preparation_uploads SET active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, id)); e != nil {
			return e
		}
		out = ChunkAcceptance{UploadID: id, Asset: input.Asset, Offset: input.Offset, SizeBytes: input.Length, SHA256: input.SHA256, Duplicate: duplicate, ReceivedBytes: received}
		return nil
	})
	if e != nil {
		return ChunkAcceptance{}, e
	}
	return out, e
}

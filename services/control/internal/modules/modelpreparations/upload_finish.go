package modelpreparations

import (
	"database/sql"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"reflect"
)

const finalizationAsset = ".finalize"

type OutputAcceptance = wire.OutputAcceptance

func completeUpload(ctx context.Context, tx transaction.Executor, d UploadDescriptor) error {
	if tx.Dialect() == transaction.Dameng {
		return completeNativePreparationUpload(ctx, tx, d)
	}
	expected := map[string]map[int64]int64{}
	for _, asset := range d.Files {
		expected[asset.Path] = map[int64]int64{}
		for offset := int64(0); offset < asset.SizeBytes; offset += wire.TransferChunkBytes {
			expected[asset.Path][offset] = min(wire.TransferChunkBytes, asset.SizeBytes-offset)
		}
	}
	rows, e := preparationQuery(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "ASSET","BYTE_OFFSET","SIZE_BYTES" FROM "MODEL_PREPARATION_UPLOAD_CHUNKS" WHERE "UPLOAD_ID"=?`), "SELECT asset,byte_offset,size_bytes FROM model_preparation_upload_chunks WHERE upload_id=$1", `SELECT asset,byte_offset,size_bytes FROM model_preparation_upload_chunks WHERE upload_id=?`, d.UploadID))
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
			return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
		}
		delete(expected[asset], offset)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	for _, parts := range expected {
		if len(parts) != 0 {
			return fail("MODEL_PREPARATION_UPLOAD_INCOMPLETE")
		}
	}
	return nil
}

func (s *Service) clearUploadTicket(ctx context.Context, node, credential, task, token, id, ticket string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(s.policy().TransactionSeconds))
	defer cancel()
	_ = s.withMachineLease(cleanup, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, p Preparation, c wire.Command) error {
		_, e := tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_UPLOADS" SET "STATE"=CASE WHEN "STATE"='verifying' THEN 'uploading' ELSE "STATE" END,"ACTIVE_CHUNK_ID"=NULL,"ACTIVE_ASSET"=NULL,"ACTIVE_OFFSET"=NULL,"ACTIVE_EXPIRES_AT"=NULL,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=? AND "ACTIVE_CHUNK_ID"=?`), `UPDATE model_preparation_uploads SET state=CASE WHEN state='verifying' THEN 'uploading' ELSE state END,active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL,updated_at=clock_timestamp() WHERE id=$1 AND active_chunk_id=$2`, `UPDATE model_preparation_uploads SET state=CASE WHEN state='verifying' THEN 'uploading' ELSE state END,active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL,updated_at=UTC_TIMESTAMP(6) WHERE id=? AND active_chunk_id=?`, id, ticket))
		return e
	})
}

// FinishUpload verifies actual complete files, BF16 tensors and report semantics before final current-authority acceptance.
// It preserves the physical GPU owner and leaves reviewability to a separately accepted positive stop.
func (s *Service) FinishUpload(ctx context.Context, node, credential, task, token, id string) (OutputAcceptance, error) {
	var out OutputAcceptance
	var descriptor UploadDescriptor
	var metadata []byte
	if s.Store == nil {
		return out, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().OutputSeconds))
	defer cancel()
	ticket := uuid.NewString()
	e := s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, p Preparation, c wire.Command) error {
		u, d, raw, e := s.lockedUpload(ctx, tx, w, p, c, id)
		if e != nil {
			return e
		}
		if u.State != "uploading" && u.State != "verifying" && u.State != "accepted" {
			return fail("MODEL_PREPARATION_UPLOAD_STATE_CONFLICT")
		}
		if e = completeUpload(ctx, tx, d); e != nil {
			return e
		}
		var active bool
		if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT CASE WHEN "ACTIVE_EXPIRES_AT">SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM "MODEL_PREPARATION_UPLOADS" WHERE "ID"=?`), "SELECT COALESCE(active_expires_at>clock_timestamp(),false) FROM model_preparation_uploads WHERE id=$1", `SELECT COALESCE(active_expires_at>UTC_TIMESTAMP(6),false) FROM model_preparation_uploads WHERE id=?`, id)).Scan(&active); e != nil {
			return e
		}
		if active {
			return fail("MODEL_PREPARATION_UPLOAD_BUSY")
		}
		descriptor, metadata = d, raw
		_, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_UPLOADS" SET "STATE"=CASE WHEN "STATE"='accepted' THEN 'accepted' ELSE 'verifying' END,"ACTIVE_CHUNK_ID"=?,"ACTIVE_ASSET"=?,"ACTIVE_OFFSET"=0,"ACTIVE_EXPIRES_AT"=DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), `UPDATE model_preparation_uploads SET state=CASE WHEN state='accepted' THEN 'accepted' ELSE 'verifying' END,active_chunk_id=$2,active_asset=$3,active_offset=0,active_expires_at=clock_timestamp()+make_interval(secs=>$4),updated_at=clock_timestamp() WHERE id=$1`, `UPDATE model_preparation_uploads SET state=CASE WHEN state='accepted' THEN 'accepted' ELSE 'verifying' END,active_chunk_id=?,active_asset=?,active_offset=0,active_expires_at=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{id, ticket, finalizationAsset, s.policy().OutputSeconds}, []any{ticket, finalizationAsset, s.policy().OutputSeconds, id}))
		return e
	})
	if e != nil {
		return out, e
	}
	defer s.clearUploadTicket(ctx, node, credential, task, token, id, ticket)
	expected, e := s.prepareOutputExpectation(ctx, node, credential, task, token, metadata, true)
	if e != nil {
		return out, e
	}
	defer expected.BaseWeights.Close()
	if !reflect.DeepEqual(descriptor.Files, expected.Report.ModelFiles) || descriptor.Scope != expected.Command.Scope || descriptor.AttemptID != expected.Command.AttemptID {
		return out, fail("MODEL_PREPARATION_UPLOAD_CONFLICT")
	}
	release, e := s.acquireScan(ctx)
	if e != nil {
		return out, e
	}
	defer release()
	storage, e := s.Store.PreparePublishedFiles(ctx, descriptor)
	if e != nil {
		return out, e
	}
	candidate, asset, e := s.Store.OpenPublishedAsset(ctx, descriptor, "model.safetensors")
	if e != nil {
		return out, e
	}
	defer candidate.Close()
	for _, base := range expected.Expected.BaseFiles {
		if base.Path == "model.safetensors" {
			info, e := expected.BaseWeights.Stat()
			if e != nil {
				return out, fail("MODEL_PREPARATION_INPUT_CHANGED")
			}
			verified, e := wire.VerifyMergedWeights(ctx, expected.BaseWeights, info.Size(), base, candidate, asset.SizeBytes, true)
			if e != nil {
				return out, e
			}
			if verified.SHA256 != asset.SHA256 || verified.SizeBytes != asset.SizeBytes || !reflect.DeepEqual(verified.ChangedTargets, expected.Report.ChangedTargets) {
				return out, fail("MODEL_PREPARATION_REPORT_INVALID")
			}
		}
	}
	e = s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, p Preparation, c wire.Command) error {
		if e := s.bindOutputExpectation(ctx, tx, w, p, c, expected); e != nil {
			return e
		}
		u, d, raw, e := s.lockedUpload(ctx, tx, w, p, c, id)
		if e != nil {
			return e
		}
		if !bytes.Equal(raw, metadata) || !reflect.DeepEqual(d, descriptor) {
			return fail("MODEL_PREPARATION_UPLOAD_CONFLICT")
		}
		if u.State != "verifying" && u.State != "accepted" {
			return fail("MODEL_PREPARATION_UPLOAD_STATE_CONFLICT")
		}
		var valid bool
		if e = preparationQueryRow(ctx, tx, preparationSelectedRuntimeSQL(tx, preparationDM(`SELECT CASE WHEN "ACTIVE_CHUNK_ID"=? AND "ACTIVE_ASSET"=? AND "ACTIVE_EXPIRES_AT">SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM "MODEL_PREPARATION_UPLOADS" WHERE "ID"=?`), "SELECT COALESCE(active_chunk_id=$2 AND active_asset=$3 AND active_expires_at>clock_timestamp(),false) FROM model_preparation_uploads WHERE id=$1", `SELECT COALESCE(active_chunk_id=? AND active_asset=? AND active_expires_at>UTC_TIMESTAMP(6),false) FROM model_preparation_uploads WHERE id=?`, []any{id, ticket, finalizationAsset}, []any{ticket, finalizationAsset, id})).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return fail("MODEL_PREPARATION_UPLOAD_TICKET_STALE")
		}
		if e = completeUpload(ctx, tx, d); e != nil {
			return e
		}
		var existingID, existingSHA, existingStorage string
		var existingDescriptor, existingReport []byte
		e = preparationIDRow(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "ID","MANIFEST_SHA256","STORAGE_PATH","DESCRIPTOR","REPORT_BYTES" FROM "MODEL_PREPARATION_ARTIFACTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "PREPARATION_ID"=? AND "ATTEMPT_ID"=?`), `SELECT id::text,manifest_sha256,storage_path,descriptor,report_bytes FROM model_preparation_artifacts WHERE tenant_id=$1 AND workspace_id=$2 AND preparation_id=$3 AND attempt_id=$4`, `SELECT id,manifest_sha256,storage_path,descriptor,report_bytes FROM model_preparation_artifacts WHERE tenant_id=? AND workspace_id=? AND preparation_id=? AND attempt_id=?`, w.TenantID, w.ID, p.ID, c.AttemptID)), 0).Scan(&existingID, &existingSHA, &existingStorage, &existingDescriptor, &existingReport)
		duplicate := e == nil
		if duplicate {
			var stored UploadDescriptor
			if u.State != "accepted" || existingID != id || existingSHA != d.ManifestSHA256 || existingStorage != storage || !bytes.Equal(existingReport, raw) || wire.DecodeStrict(existingDescriptor, &stored) != nil || !reflect.DeepEqual(stored, d) {
				return fail("MODEL_PREPARATION_UPLOAD_CONFLICT")
			}
			var outcomeSHA *string
			var outcome []byte
			if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "OUTCOME_SHA256","OUTCOME_BYTES" FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "ID"=?`), "SELECT outcome_sha256,outcome_bytes FROM model_preparation_attempts WHERE id=$1", `SELECT outcome_sha256,outcome_bytes FROM model_preparation_attempts WHERE id=?`, c.AttemptID)).Scan(&outcomeSHA, &outcome); e != nil {
				return e
			}
			if outcomeSHA == nil || *outcomeSHA != d.ManifestSHA256 || !bytes.Equal(outcome, raw) {
				return fail("MODEL_PREPARATION_REPORT_CONFLICT")
			}
		} else {
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if u.State != "verifying" {
				return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
			}
			inventory, e := json.Marshal(d)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`INSERT INTO "MODEL_PREPARATION_ARTIFACTS"("ID","TENANT_ID","WORKSPACE_ID","PREPARATION_ID","ATTEMPT_ID","UPLOAD_ID","MANIFEST_SHA256","DESCRIPTOR","REPORT","REPORT_BYTES","STORAGE_PATH") VALUES(?,?,?,?,?,?,?,CAST(? AS CLOB),CAST(? AS CLOB),?,?)`, 7, 8), `INSERT INTO model_preparation_artifacts(id,tenant_id,workspace_id,preparation_id,attempt_id,upload_id,manifest_sha256,descriptor,report,report_bytes,storage_path) VALUES($1,$2,$3,$4,$5,$1,$6,$7,$8,$9,$10)`, `INSERT INTO model_preparation_artifacts(id,tenant_id,workspace_id,preparation_id,attempt_id,upload_id,manifest_sha256,descriptor,report,report_bytes,storage_path) VALUES(?,?,?,?,?,?,?,CAST(? AS JSON),CAST(? AS JSON),?,?)`, []any{id, w.TenantID, w.ID, p.ID, c.AttemptID, d.ManifestSHA256, inventory, raw, raw, storage}, []any{id, w.TenantID, w.ID, p.ID, c.AttemptID, id, d.ManifestSHA256, inventory, raw, raw, storage})); e != nil {
				return e
			}
			tag, e := tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_ATTEMPTS" SET "OUTCOME"=CAST(? AS CLOB),"OUTCOME_BYTES"=?,"OUTCOME_SHA256"=?,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=? AND "OUTCOME" IS NULL AND "PHYSICAL_STOPPED_AT" IS NULL`, 0), `UPDATE model_preparation_attempts SET outcome=$2,outcome_bytes=$3,outcome_sha256=$4,updated_at=clock_timestamp() WHERE id=$1 AND outcome IS NULL AND physical_stopped_at IS NULL`, `UPDATE model_preparation_attempts SET outcome=CAST(? AS JSON),outcome_bytes=?,outcome_sha256=?,updated_at=UTC_TIMESTAMP(6) WHERE id=? AND outcome IS NULL AND physical_stopped_at IS NULL`, []any{c.AttemptID, raw, raw, d.ManifestSHA256}, []any{raw, raw, d.ManifestSHA256, c.AttemptID}))
			if e != nil {
				return e
			}
			if tag != 1 {
				return fail("MODEL_PREPARATION_REPORT_CONFLICT")
			}
			var a identity.Auth
			if e = preparationIDRow(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "REQUESTED_BY","REQUESTED_AUDIENCE" FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "ID"=?`), "SELECT requested_by::text,requested_audience FROM model_preparation_attempts WHERE id=$1", `SELECT requested_by,requested_audience FROM model_preparation_attempts WHERE id=?`, c.AttemptID)), 0).Scan(&a.Me.User.ID, &a.Me.Audience); e != nil {
				return e
			}
			if e = preparationAuditWithin(ctx, tx, a, w, "model.preparation_artifact_accepted", p.ID, "Independent complete model tensors and frozen evaluation report verified", uuid.NewString()); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_UPLOADS" SET "STATE"='accepted',"ACCEPTED_AT"=COALESCE("ACCEPTED_AT",SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),"ACTIVE_CHUNK_ID"=NULL,"ACTIVE_ASSET"=NULL,"ACTIVE_OFFSET"=NULL,"ACTIVE_EXPIRES_AT"=NULL,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), `UPDATE model_preparation_uploads SET state='accepted',accepted_at=COALESCE(accepted_at,clock_timestamp()),active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE model_preparation_uploads SET state='accepted',accepted_at=COALESCE(accepted_at,UTC_TIMESTAMP(6)),active_chunk_id=NULL,active_asset=NULL,active_offset=NULL,active_expires_at=NULL,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, id)); e != nil {
			return e
		}
		out = OutputAcceptance{UploadID: id, PreparationID: p.ID, AttemptID: c.AttemptID, ManifestSHA256: d.ManifestSHA256, Duplicate: duplicate}
		return nil
	})
	if e != nil {
		return OutputAcceptance{}, e
	}
	return out, e
}

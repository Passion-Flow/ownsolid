package modelpreparations

import (
	"database/sql"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

const uploadFields = `u.id::text,u.preparation_id::text,u.attempt_id::text,u.manifest_sha256,u.state,COALESCE((SELECT sum(size_bytes) FROM model_preparation_upload_chunks c WHERE c.upload_id=u.id),0)::bigint,u.created_at,u.descriptor,u.manifest_bytes`

func scanUpload(row transaction.Row) (wire.Upload, UploadDescriptor, []byte, error) {
	row = preparationIDRow(row, 0, 1, 2)
	var out wire.Upload
	var descriptor UploadDescriptor
	var encoded, raw []byte
	e := row.Scan(&out.ID, &out.PreparationID, &out.AttemptID, &out.ManifestSHA256, &out.State, &out.ReceivedBytes, &out.CreatedAt, &encoded, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return out, descriptor, raw, fail("MODEL_PREPARATION_UPLOAD_NOT_FOUND")
	}
	if e != nil {
		return out, descriptor, raw, e
	}
	if wire.DecodeStrict(encoded, &descriptor) != nil || descriptor.Validate(wire.MaximumModelBytes, wire.MaximumModelFiles) != nil || descriptor.UploadID != out.ID || descriptor.Scope.PreparationID != out.PreparationID || descriptor.AttemptID != out.AttemptID || descriptor.ManifestSHA256 != out.ManifestSHA256 || hash(raw) != out.ManifestSHA256 || out.ReceivedBytes < 0 {
		return out, descriptor, raw, fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	var total int64
	for _, asset := range descriptor.Files {
		total += asset.SizeBytes
	}
	if out.ReceivedBytes > total {
		return out, descriptor, raw, fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	if out.State != "uploading" && out.State != "verifying" && out.State != "accepted" && out.State != "failed" {
		return out, descriptor, raw, fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.Files = append(out.Files, descriptor.Files...)
	out.ChunkBytes = wire.TransferChunkBytes
	return out, descriptor, raw, nil
}

func (s *Service) lockedUpload(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, p Preparation, c wire.Command, id string) (wire.Upload, UploadDescriptor, []byte, error) {
	var empty wire.Upload
	var d UploadDescriptor
	if !canonicalUploadID(id) {
		return empty, d, nil, fail("MODEL_PREPARATION_UPLOAD_NOT_FOUND")
	}
	out, d, raw, e := scanUpload(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "U"."ID","U"."PREPARATION_ID","U"."ATTEMPT_ID","U"."MANIFEST_SHA256","U"."STATE",COALESCE((SELECT SUM("SIZE_BYTES") FROM "MODEL_PREPARATION_UPLOAD_CHUNKS" "C" WHERE "C"."UPLOAD_ID"="U"."ID"),0),"U"."CREATED_AT","U"."DESCRIPTOR","U"."MANIFEST_BYTES" FROM "MODEL_PREPARATION_UPLOADS" "U" WHERE "U"."TENANT_ID"=? AND "U"."WORKSPACE_ID"=? AND "U"."PREPARATION_ID"=? AND "U"."ATTEMPT_ID"=? AND "U"."ID"=? FOR UPDATE`), "SELECT "+uploadFields+" FROM model_preparation_uploads u WHERE u.tenant_id=$1 AND u.workspace_id=$2 AND u.preparation_id=$3 AND u.attempt_id=$4 AND u.id=$5 FOR UPDATE OF u", `SELECT u.id,u.preparation_id,u.attempt_id,u.manifest_sha256,u.state,COALESCE((SELECT sum(size_bytes) FROM model_preparation_upload_chunks c WHERE c.upload_id=u.id),0),u.created_at,u.descriptor,u.manifest_bytes FROM model_preparation_uploads u WHERE u.tenant_id=? AND u.workspace_id=? AND u.preparation_id=? AND u.attempt_id=? AND u.id=? FOR UPDATE`, w.TenantID, w.ID, p.ID, c.AttemptID, id)))
	if e != nil {
		return out, d, raw, e
	}
	if d.Scope != c.Scope || d.AttemptID != c.AttemptID || d.Validate(c.Recipe.Resources.MaximumOutputBytes, c.Recipe.Resources.MaximumOutputFiles) != nil || int64(len(raw)) > c.Recipe.Resources.MaximumResultBytes {
		return out, d, raw, fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
	}
	return out, d, raw, nil
}

func canonicalUploadID(id string) bool {
	parsed, e := uuid.Parse(id)
	return e == nil && parsed != uuid.Nil && parsed.String() == id
}

// BeginUpload commits one source-checked immutable report intent before any private stage effect.
// A current-authority recheck follows staging; no upload or artifact becomes an approved model here.
func (s *Service) BeginUpload(ctx context.Context, node, credential, task, token string, metadata []byte) (wire.Upload, error) {
	var out wire.Upload
	if s.Store == nil {
		return out, fail("MODEL_PREPARATION_STORAGE_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().OutputSeconds))
	defer cancel()
	expected, e := s.prepareOutputExpectation(ctx, node, credential, task, token, metadata, false)
	if e != nil {
		return out, e
	}
	var descriptor UploadDescriptor
	e = s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, p Preparation, c wire.Command) error {
		if e := s.bindOutputExpectation(ctx, tx, w, p, c, expected); e != nil {
			return e
		}
		existing, stored, raw, e := scanUpload(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "U"."ID","U"."PREPARATION_ID","U"."ATTEMPT_ID","U"."MANIFEST_SHA256","U"."STATE",COALESCE((SELECT SUM("SIZE_BYTES") FROM "MODEL_PREPARATION_UPLOAD_CHUNKS" "C" WHERE "C"."UPLOAD_ID"="U"."ID"),0),"U"."CREATED_AT","U"."DESCRIPTOR","U"."MANIFEST_BYTES" FROM "MODEL_PREPARATION_UPLOADS" "U" WHERE "U"."ATTEMPT_ID"=? FOR UPDATE`), "SELECT "+uploadFields+" FROM model_preparation_uploads u WHERE u.attempt_id=$1 FOR UPDATE OF u", `SELECT u.id,u.preparation_id,u.attempt_id,u.manifest_sha256,u.state,COALESCE((SELECT sum(size_bytes) FROM model_preparation_upload_chunks c WHERE c.upload_id=u.id),0),u.created_at,u.descriptor,u.manifest_bytes FROM model_preparation_uploads u WHERE u.attempt_id=? FOR UPDATE`, c.AttemptID)))
		if e == nil {
			if stored.Scope != c.Scope || stored.AttemptID != c.AttemptID || existing.ManifestSHA256 != hash(metadata) || !bytes.Equal(raw, metadata) || !reflect.DeepEqual(stored.Files, expected.Report.ModelFiles) {
				return fail("MODEL_PREPARATION_UPLOAD_CONFLICT")
			}
			if existing.State == "failed" {
				return fail("MODEL_PREPARATION_UPLOAD_RECOVERY_REQUIRED")
			}
			out, descriptor = existing, stored
			return nil
		}
		if e.Error() != "MODEL_PREPARATION_UPLOAD_NOT_FOUND" {
			return e
		}
		descriptor = UploadDescriptor{SchemaVersion: 1, Scope: c.Scope, UploadID: uuid.NewString(), AttemptID: c.AttemptID, ManifestSHA256: hash(metadata), Files: expected.Report.ModelFiles}
		if e = descriptor.Validate(c.Recipe.Resources.MaximumOutputBytes, c.Recipe.Resources.MaximumOutputFiles); e != nil {
			return e
		}
		encoded, e := json.Marshal(descriptor)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`INSERT INTO "MODEL_PREPARATION_UPLOADS"("ID","TENANT_ID","WORKSPACE_ID","PREPARATION_ID","ATTEMPT_ID","MANIFEST_SHA256","MANIFEST","MANIFEST_BYTES","DESCRIPTOR") VALUES(?,?,?,?,?,?,CAST(? AS CLOB),?,CAST(? AS CLOB))`, 6, 8), `INSERT INTO model_preparation_uploads(id,tenant_id,workspace_id,preparation_id,attempt_id,manifest_sha256,manifest,manifest_bytes,descriptor) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, `INSERT INTO model_preparation_uploads(id,tenant_id,workspace_id,preparation_id,attempt_id,manifest_sha256,manifest,manifest_bytes,descriptor) VALUES(?,?,?,?,?,?,CAST(? AS JSON),?,CAST(? AS JSON))`, descriptor.UploadID, w.TenantID, w.ID, p.ID, c.AttemptID, descriptor.ManifestSHA256, metadata, metadata, encoded)); e != nil {
			return e
		}
		var a identity.Auth
		if e = preparationIDRow(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "REQUESTED_BY","REQUESTED_AUDIENCE" FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "ID"=?`), "SELECT requested_by::text,requested_audience FROM model_preparation_attempts WHERE id=$1", `SELECT requested_by,requested_audience FROM model_preparation_attempts WHERE id=?`, c.AttemptID)), 0).Scan(&a.Me.User.ID, &a.Me.Audience); e != nil {
			return e
		}
		if e = preparationAuditWithin(ctx, tx, a, w, "model.preparation_upload_started", p.ID, "Current-authority immutable prepared report upload intent", uuid.NewString()); e != nil {
			return e
		}
		out, _, _, e = scanUpload(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "U"."ID","U"."PREPARATION_ID","U"."ATTEMPT_ID","U"."MANIFEST_SHA256","U"."STATE",COALESCE((SELECT SUM("SIZE_BYTES") FROM "MODEL_PREPARATION_UPLOAD_CHUNKS" "C" WHERE "C"."UPLOAD_ID"="U"."ID"),0),"U"."CREATED_AT","U"."DESCRIPTOR","U"."MANIFEST_BYTES" FROM "MODEL_PREPARATION_UPLOADS" "U" WHERE "U"."ID"=?`), "SELECT "+uploadFields+" FROM model_preparation_uploads u WHERE u.id=$1", `SELECT u.id,u.preparation_id,u.attempt_id,u.manifest_sha256,u.state,COALESCE((SELECT sum(size_bytes) FROM model_preparation_upload_chunks c WHERE c.upload_id=u.id),0),u.created_at,u.descriptor,u.manifest_bytes FROM model_preparation_uploads u WHERE u.id=?`, descriptor.UploadID)))
		return e
	})
	if e != nil {
		return wire.Upload{}, e
	}
	if out.State == "accepted" {
		e = s.Store.VerifyPublished(ctx, descriptor)
	} else {
		e = s.Store.PrepareUpload(ctx, descriptor, metadata)
	}
	if e != nil {
		return wire.Upload{}, e
	}
	e = s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, p Preparation, c wire.Command) error {
		if e := s.bindOutputExpectation(ctx, tx, w, p, c, expected); e != nil {
			return e
		}
		current, stored, raw, e := s.lockedUpload(ctx, tx, w, p, c, out.ID)
		if e != nil {
			return e
		}
		if stored.ManifestSHA256 != descriptor.ManifestSHA256 || !bytes.Equal(raw, metadata) {
			return fail("MODEL_PREPARATION_UPLOAD_CONFLICT")
		}
		out = current
		return nil
	})
	if e != nil {
		return wire.Upload{}, e
	}
	return out, nil
}

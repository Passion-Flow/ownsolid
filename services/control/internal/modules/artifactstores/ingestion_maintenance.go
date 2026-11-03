package artifactstores

import (
	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const ingestionCancelled = "OBJECT_INGESTION_CANCELLED"

// Submitted metadata without the durable local-source proof remains a reference,
// including restored records from an older/incomplete schema deployment.
func ingestionReferencesWithin(ctx context.Context, q transaction.Executor, o storedObject) (bool, error) {
	var referenced bool
	e := q.QueryRow(ctx, statement(q, "SELECT EXISTS(SELECT 1 FROM artifact_object_ingestions WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND (state IN ('intent','running') OR (state='submitted' AND (source_verified_sha256 IS NULL OR source_verified_sha256<>$4 OR source_verified_at IS NULL OR receipt IS NULL OR (kind='model' AND model_import_id IS NULL) OR (kind='dataset' AND dataset_draft_id IS NULL)))))", "SELECT EXISTS(SELECT 1 FROM artifact_object_ingestions WHERE tenant_id=? AND workspace_id=? AND object_id=? AND (state IN ('intent','running') OR (state='submitted' AND (source_verified_sha256 IS NULL OR source_verified_sha256<>? OR source_verified_at IS NULL OR receipt IS NULL OR (kind='model' AND model_import_id IS NULL) OR (kind='dataset' AND dataset_draft_id IS NULL)))))", `SELECT CASE WHEN EXISTS(SELECT 1 FROM "ARTIFACT_OBJECT_INGESTIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND ("STATE" IN ('intent','running') OR ("STATE"='submitted' AND ("SOURCE_VERIFIED_SHA256" IS NULL OR "SOURCE_VERIFIED_SHA256"<>? OR "SOURCE_VERIFIED_AT" IS NULL OR "RECEIPT" IS NULL OR ("KIND"='model' AND "MODEL_IMPORT_ID" IS NULL) OR ("KIND"='dataset' AND "DATASET_DRAFT_ID" IS NULL))))) THEN 1 ELSE 0 END FROM DUAL`, o.Tenant, o.WorkspaceID, o.ID, o.SHA256)).Scan(&referenced)
	return referenced, e
}

// CancelIngestion stops only an unclaimed intent. It neither cancels a local
// artifact owner nor asserts that an uncertain process/target has stopped.
func (s *Service) CancelIngestion(ctx context.Context, a identity.Auth, workspace, object, ingestion, reason, request string) (Ingestion, error) {
	ctx, done, e := s.begin(ctx)
	if e != nil {
		return Ingestion{}, e
	}
	defer done()
	if !id(ingestion) || !id(request) || !workspaces.ValidReason(reason) {
		return Ingestion{}, fail("VALIDATION_FAILED")
	}
	var out Ingestion
	e = s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if !writeRole(a, w) {
			return fail("ACCESS_DENIED")
		}
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		row, e := scanIngestion(q.QueryRow(ctx, statement(q, "SELECT id::text,object_id::text,kind,COALESCE(model_import_id,dataset_draft_id)::text,state,error_code,created_by::text,input,target_dataset_id::text,source_verified_sha256 FROM artifact_object_ingestions WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4 FOR UPDATE", "SELECT id,object_id,kind,COALESCE(model_import_id,dataset_draft_id),state,error_code,created_by,`input`,target_dataset_id,source_verified_sha256 FROM artifact_object_ingestions WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=? FOR UPDATE", `SELECT "ID","OBJECT_ID","KIND",COALESCE("MODEL_IMPORT_ID","DATASET_DRAFT_ID"),"STATE","ERROR_CODE","CREATED_BY","INPUT","TARGET_DATASET_ID","SOURCE_VERIFIED_SHA256" FROM "ARTIFACT_OBJECT_INGESTIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=? FOR UPDATE`, w.TenantID, w.ID, o.ID, ingestion)))
		if e != nil {
			return e
		}
		if row.Creator != a.Me.User.ID && !admin(a) && (w.Role == nil || *w.Role != "owner") {
			return fail("RESOURCE_NOT_FOUND")
		}
		if row.State == "failed" && row.ErrorCode != nil && *row.ErrorCode == ingestionCancelled {
			out = row.Ingestion
			return nil
		}
		if row.State != "intent" || row.TargetID != nil || row.VerifiedDigest != nil || o.OperationID == nil || o.OperationKind == nil || *o.OperationKind != "ingest" || o.OperationPhase == nil || *o.OperationPhase != "prepared" {
			return fail("OBJECT_INGESTION_STOP_UNCONFIRMED")
		}
		actor, e := actorFrom(o.OperationAuthority)
		if e != nil || actor.Me.User.ID != row.Creator {
			return fail("OBJECT_INGESTION_STOP_UNCONFIRMED")
		}
		pending, e := s.ingestionWithin(ctx, q, o, actor)
		if e != nil || pending.ID != row.ID {
			return fail("OBJECT_INGESTION_STOP_UNCONFIRMED")
		}
		args := []any{w.TenantID, w.ID, o.ID, row.ID, ingestionCancelled}
		if positionalArguments(q) {
			args = []any{ingestionCancelled, w.TenantID, w.ID, o.ID, row.ID}
		}
		if _, e = q.Exec(ctx, statement(q, "UPDATE artifact_object_ingestions SET state='failed',error_code=$5,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4", "UPDATE artifact_object_ingestions SET state='failed',error_code=?,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=?", `UPDATE "ARTIFACT_OBJECT_INGESTIONS" SET "STATE"='failed',"ERROR_CODE"=?,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=?`, args...)); e != nil {
			return e
		}
		if e = clearOperation(ctx, q, o, "ready", o.UploadID, o.ETag); e != nil {
			return e
		}
		if e = audit(ctx, q, a, w, "object.ingestion_cancelled", o.ID, reason, request); e != nil {
			return e
		}
		code := ingestionCancelled
		row.State, row.ErrorCode = "failed", &code
		out = row.Ingestion
		return nil
	})
	if e != nil {
		return Ingestion{}, e
	}
	return out, nil
}

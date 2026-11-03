package artifactstores

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ReconcileIngestion lets the current job creator recover an accepted delegated
// ingestion without acquiring ownership of somebody else's source object.
func (s *Service) ReconcileIngestion(ctx context.Context, a identity.Auth, workspace, object, ingestion, grant, reason, request string) (Ingestion, error) {
	ctx, done, e := s.begin(ctx)
	if e != nil {
		return Ingestion{}, e
	}
	defer done()
	if !id(ingestion) || !id(request) || !workspaces.ValidReason(reason) {
		return Ingestion{}, fail("VALIDATION_FAILED")
	}
	issuer, e := s.grantIssuer(ctx, a, workspace, object, grant)
	if e != nil {
		return Ingestion{}, e
	}
	var out Ingestion
	e = s.within(ctx, a, workspace, issuer, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if !writeRole(a, w) {
			return fail("ACCESS_DENIED")
		}
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		row, e := s.ingestionWithin(ctx, q, o, a)
		if e != nil {
			return e
		}
		if row.ID != ingestion || row.Creator != a.Me.User.ID {
			return fail("RESOURCE_NOT_FOUND")
		}
		var input ingestionInput
		if json.Unmarshal(row.Input, &input) != nil || input.Grant != grant {
			return fail("OBJECT_INGESTION_INVALID")
		}
		if e = s.authorize(ctx, q, a, w, o, "ingest_"+row.Kind, grant); e != nil {
			return e
		}
		if e = liveWithin(ctx, q, o, false); e != nil {
			return e
		}
		if o.OperationID == nil || o.OperationKind == nil || *o.OperationKind != "ingest" {
			return fail("OBJECT_OPERATION_STALE")
		}
		var expired bool
		if e = q.QueryRow(ctx, statement(q, "SELECT operation_expires_at<=clock_timestamp() FROM artifact_objects WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "SELECT operation_expires_at<=UTC_TIMESTAMP(6) FROM artifact_objects WHERE tenant_id=? AND workspace_id=? AND id=?", `SELECT CASE WHEN "OPERATION_EXPIRES_AT"<=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) THEN 1 ELSE 0 END FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`, w.TenantID, w.ID, o.ID)).Scan(&expired); e != nil {
			return e
		}
		if !expired && o.ErrorCode == nil {
			return fail("OBJECT_BUSY")
		}
		operation := uuid.NewString()
		if _, e = q.Exec(ctx, statement(q, "UPDATE artifact_objects SET operation_id=$4,operation_phase='prepared',operation_authority=$5,operation_expires_at=clock_timestamp()+make_interval(secs=>$6),error_code=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "UPDATE artifact_objects SET operation_id=?,operation_phase='prepared',operation_authority=CAST(? AS JSON),operation_expires_at=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),error_code=NULL,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?", `UPDATE "ARTIFACT_OBJECTS" SET "OPERATION_ID"=?,"OPERATION_PHASE"='prepared',"OPERATION_AUTHORITY"=?,"OPERATION_EXPIRES_AT"=(SYS_EXTRACT_UTC(SYSTIMESTAMP(6))+NUMTODSINTERVAL(?,'SECOND')),"ERROR_CODE"=NULL,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`, reconcileArgs(q, o, operation, a, s.Configuration.TransferSeconds)...)); e != nil {
			return e
		}
		if e = audit(ctx, q, a, w, "object.ingestion_reconciled", o.ID, reason, request); e != nil {
			return e
		}
		out = row.Ingestion
		return nil
	})
	if e != nil {
		return Ingestion{}, e
	}
	return out, nil
}

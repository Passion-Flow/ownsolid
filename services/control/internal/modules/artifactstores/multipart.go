package artifactstores

import (
	"context"
	"io"
	"strconv"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

func (s *Service) operationWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, o storedObject, kind, state string) error {
	if o.OperationID != nil {
		return fail("OBJECT_BUSY")
	}
	_, e := q.Exec(ctx, statement(q, `UPDATE artifact_objects SET state=$4,operation_id=$5,operation_kind=$6,operation_phase='prepared',operation_authority=$7,operation_expires_at=clock_timestamp()+make_interval(secs=>$8),error_code=NULL,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3`, `UPDATE artifact_objects SET state=?,operation_id=?,operation_kind=?,operation_phase='prepared',operation_authority=CAST(? AS JSON),operation_expires_at=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),error_code=NULL,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`, `UPDATE "ARTIFACT_OBJECTS" SET "STATE"=?,"OPERATION_ID"=?,"OPERATION_KIND"=?,"OPERATION_PHASE"='prepared',"OPERATION_AUTHORITY"=?,"OPERATION_EXPIRES_AT"=(SYS_EXTRACT_UTC(SYSTIMESTAMP(6))+NUMTODSINTERVAL(?,'SECOND')),"ERROR_CODE"=NULL,"REVISION"="REVISION"+1,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`, operationArgs(q, a, w, o, kind, state, s.Configuration.TransferSeconds)...))
	return e
}
func operationArgs(q transaction.Executor, a identity.Auth, w workspaces.Workspace, o storedObject, kind, state string, seconds int) []any {
	op := uuid.NewString()
	if positionalArguments(q) {
		return []any{state, op, kind, jsonParameter(q, actorBytes(a)), seconds, w.TenantID, w.ID, o.ID}
	}
	return []any{w.TenantID, w.ID, o.ID, state, op, kind, jsonParameter(q, actorBytes(a)), seconds}
}
func (s *Service) partsWithin(ctx context.Context, q transaction.Executor, o storedObject, complete bool) ([]objectstore.Part, error) {
	rows, e := q.Query(ctx, statement(q, "SELECT number,bytes,sha256,etag,state FROM artifact_object_parts WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 ORDER BY number FOR UPDATE", "SELECT number,bytes,sha256,etag,state FROM artifact_object_parts WHERE tenant_id=? AND workspace_id=? AND object_id=? ORDER BY number FOR UPDATE", `SELECT "NUMBER","BYTES","SHA256","ETAG","STATE" FROM "ARTIFACT_OBJECT_PARTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? ORDER BY "NUMBER" FOR UPDATE`, o.Tenant, o.WorkspaceID, o.ID))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	parts := []objectstore.Part{}
	var total int64
	for rows.Next() {
		var p objectstore.Part
		var tag *string
		var state string
		if e = rows.Scan(&p.Number, &p.Bytes, &p.SHA256, &tag, &state); e != nil {
			return nil, e
		}
		if complete && (state != "uploaded" || tag == nil || p.Number != int32(len(parts)+1)) {
			return nil, fail("OBJECT_INCOMPLETE")
		}
		if tag != nil {
			p.ETag = *tag
		}
		parts = append(parts, p)
		total += p.Bytes
		if len(parts) > objectstore.MaximumMultipartParts {
			return nil, fail("OBJECT_PART_INVALID")
		}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if complete && (total != o.ExpectedBytes || len(parts) != int((o.ExpectedBytes+o.PartBytes-1)/o.PartBytes)) {
		return nil, fail("OBJECT_INCOMPLETE")
	}
	return parts, nil
}
func (s *Service) Parts(ctx context.Context, a identity.Auth, workspace, object string) ([]objectstore.Part, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return nil, admissionError
	}
	defer finish()
	var out []objectstore.Part
	e := s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if e = s.writeObject(ctx, q, a, w, o, true); e != nil {
			return e
		}
		out, e = s.partsWithin(ctx, q, o, false)
		return e
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
func (s *Service) PutPart(ctx context.Context, a identity.Auth, workspace, object string, part objectstore.Part, source io.Reader, request string) (Object, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return Object{}, admissionError
	}
	defer finish()
	if !id(request) || part.Number < 1 || part.Number > objectstore.MaximumMultipartParts || !objectstore.ValidDigest(part.SHA256) || source == nil {
		return Object{}, fail("OBJECT_PART_INVALID")
	}
	if e := s.admitNewBusiness(ctx, a, workspace); e != nil {
		return Object{}, e
	}
	var objectRow storedObject
	stageID := uuid.NewString()
	replay := false
	e := s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if e = s.writeObject(ctx, q, a, w, o, true); e != nil {
			return e
		}
		if e = liveWithin(ctx, q, o, true); e != nil {
			return e
		}
		if o.State != "uploading" || o.UploadID == nil {
			return fail("OBJECT_NOT_READY")
		}
		offset := int64(part.Number-1) * o.PartBytes
		expected := o.PartBytes
		if offset+expected > o.ExpectedBytes {
			expected = o.ExpectedBytes - offset
		}
		if offset >= o.ExpectedBytes || part.Bytes != expected {
			return fail("OBJECT_PART_INVALID")
		}
		all, e := s.partsWithin(ctx, q, o, false)
		if e != nil {
			return e
		}
		exists := false
		for _, p := range all {
			if p.Number == part.Number {
				if p.Bytes != part.Bytes || p.SHA256 != part.SHA256 {
					return fail("IDEMPOTENCY_CONFLICT")
				}
				if p.ETag != "" {
					objectRow = o
					replay = true
					return nil
				}
				exists = true
			}
		}
		if o.OperationID != nil {
			return fail("OBJECT_BUSY")
		}
		if exists {
			if e = q.QueryRow(ctx, statement(q, "SELECT stage_id::text FROM artifact_object_parts WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND number=$4", "SELECT stage_id FROM artifact_object_parts WHERE tenant_id=? AND workspace_id=? AND object_id=? AND number=?", `SELECT "STAGE_ID" FROM "ARTIFACT_OBJECT_PARTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "NUMBER"=?`, o.Tenant, o.WorkspaceID, o.ID, part.Number)).Scan(&stageID); e != nil {
				return e
			}
			return fail("OBJECT_RECONCILE_REQUIRED")
		}
		if _, e = q.Exec(ctx, statement(q, "INSERT INTO artifact_object_parts(tenant_id,workspace_id,object_id,number,bytes,sha256,state,stage_id) VALUES($1,$2,$3,$4,$5,$6,'intent',$7)", "INSERT INTO artifact_object_parts(tenant_id,workspace_id,object_id,number,bytes,sha256,state,stage_id) VALUES(?,?,?,?,?,?,'intent',?)", `INSERT INTO "ARTIFACT_OBJECT_PARTS"("TENANT_ID","WORKSPACE_ID","OBJECT_ID","NUMBER","BYTES","SHA256","STATE","STAGE_ID") VALUES(?,?,?,?,?,?,'intent',?)`, o.Tenant, o.WorkspaceID, o.ID, part.Number, part.Bytes, part.SHA256, stageID)); e != nil {
			return e
		}
		if e = s.operationWithin(ctx, q, a, w, o, "part", "uploading"); e != nil {
			return e
		}
		objectRow, e = s.objectWithin(ctx, q, w, o.ID)
		return e
	})
	if e != nil {
		return Object{}, e
	}
	if replay {
		return objectRow.Object, nil
	}
	staged, e := s.Stage.Write(ctx, stageID, part.Bytes, part.SHA256, source)
	if e != nil {
		s.reduceFailureBounded(objectRow, e)
		return Object{}, safe(e)
	}
	keep := true
	defer func() {
		if keep {
			staged.File.Close()
		}
	}()
	// A durable sent phase is committed before the remote call. Reconcile, never
	// automatic retry, handles a process crash or missing acknowledgment.
	e = s.sent(ctx, a, objectRow)
	if e != nil {
		return Object{}, e
	}
	received, e := s.Transport.PutPart(ctx, objectRow.ref(), *objectRow.UploadID, part, staged.File)
	if e != nil {
		s.reduceFailureBounded(objectRow, e)
		return Object{}, safe(e)
	}
	var out Object
	e = s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if e = s.sameOperation(ctx, q, a, w, current, objectRow, true); e != nil {
			return e
		}
		if e = s.Stage.Verify(staged); e != nil {
			return e
		}
		if _, e = q.Exec(ctx, statement(q, "UPDATE artifact_object_parts SET state='uploaded',etag=$5 WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND number=$4 AND state='intent'", "UPDATE artifact_object_parts SET state='uploaded',etag=? WHERE tenant_id=? AND workspace_id=? AND object_id=? AND number=? AND state='intent'", `UPDATE "ARTIFACT_OBJECT_PARTS" SET "STATE"='uploaded',"ETAG"=? WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "NUMBER"=? AND "STATE"='intent'`, partAckArgs(q, current, received)...)); e != nil {
			return e
		}
		if e = clearOperation(ctx, q, current, "uploading", current.UploadID, current.ETag); e != nil {
			return e
		}
		if e = audit(ctx, q, a, w, "object.part_received", object, "Receive exact accepted object part", request); e != nil {
			return e
		}
		value, e := s.objectWithin(ctx, q, w, object)
		out = value.Object
		return e
	})
	if e != nil {
		return Object{}, e
	}
	keep = false
	if e = s.Stage.Discard(staged); e != nil {
		return Object{}, safe(e)
	}
	return out, nil
}
func partAckArgs(q transaction.Executor, o storedObject, p objectstore.Part) []any {
	if positionalArguments(q) {
		return []any{p.ETag, o.Tenant, o.WorkspaceID, o.ID, p.Number}
	}
	return []any{o.Tenant, o.WorkspaceID, o.ID, p.Number, p.ETag}
}
func (s *Service) sameOperation(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, current, expected storedObject, upload bool) error {
	if e := s.writeObject(ctx, q, a, w, current, upload); e != nil {
		return e
	}
	if current.OperationID == nil || expected.OperationID == nil || *current.OperationID != *expected.OperationID || current.OperationKind == nil || expected.OperationKind == nil || *current.OperationKind != *expected.OperationKind {
		return fail("OBJECT_OPERATION_STALE")
	}
	if e := liveWithin(ctx, q, current, upload); e != nil {
		return e
	}
	var live bool
	e := q.QueryRow(ctx, statement(q, "SELECT operation_expires_at>clock_timestamp() FROM artifact_objects WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "SELECT operation_expires_at>UTC_TIMESTAMP(6) FROM artifact_objects WHERE tenant_id=? AND workspace_id=? AND id=?", `SELECT CASE WHEN "OPERATION_EXPIRES_AT">SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) THEN 1 ELSE 0 END FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`, current.Tenant, current.WorkspaceID, current.ID)).Scan(&live)
	if e != nil {
		return e
	}
	if !live {
		return fail("OBJECT_OPERATION_STALE")
	}
	return nil
}
func (s *Service) sent(ctx context.Context, a identity.Auth, o storedObject) error {
	return s.within(ctx, a, o.WorkspaceID, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := s.objectWithin(ctx, q, w, o.ID)
		if e != nil {
			return e
		}
		if e = s.sameOperation(ctx, q, a, w, current, o, true); e != nil {
			return e
		}
		if current.OperationPhase == nil || *current.OperationPhase != "prepared" {
			return fail("OBJECT_RECONCILE_REQUIRED")
		}
		_, e = q.Exec(ctx, statement(q, "UPDATE artifact_objects SET operation_phase='sent',updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND operation_id=$4", "UPDATE artifact_objects SET operation_phase='sent',updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=? AND operation_id=?", `UPDATE "ARTIFACT_OBJECTS" SET "OPERATION_PHASE"='sent',"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=? AND "OPERATION_ID"=?`, o.Tenant, o.WorkspaceID, o.ID, *o.OperationID))
		return e
	})
}
func clearOperation(ctx context.Context, q transaction.Executor, o storedObject, state string, upload, etag *string) error {
	n, e := q.Exec(ctx, statement(q, `UPDATE artifact_objects SET state=$5,remote_upload_id=$6,remote_etag=$7,operation_id=NULL,operation_kind=NULL,operation_phase=NULL,operation_authority=NULL,operation_expires_at=NULL,error_code=NULL,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND operation_id=$4`, `UPDATE artifact_objects SET state=?,remote_upload_id=?,remote_etag=?,operation_id=NULL,operation_kind=NULL,operation_phase=NULL,operation_authority=NULL,operation_expires_at=NULL,error_code=NULL,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=? AND operation_id=?`, `UPDATE "ARTIFACT_OBJECTS" SET "STATE"=?,"REMOTE_UPLOAD_ID"=?,"REMOTE_ETAG"=?,"OPERATION_ID"=NULL,"OPERATION_KIND"=NULL,"OPERATION_PHASE"=NULL,"OPERATION_AUTHORITY"=NULL,"OPERATION_EXPIRES_AT"=NULL,"ERROR_CODE"=NULL,"REVISION"="REVISION"+1,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=? AND "OPERATION_ID"=?`, clearArgs(q, o, state, upload, etag)...))
	if e != nil {
		return e
	}
	if n != 1 {
		return fail("OBJECT_OPERATION_STALE")
	}
	return nil
}
func clearArgs(q transaction.Executor, o storedObject, state string, upload, etag *string) []any {
	if positionalArguments(q) {
		return []any{state, upload, etag, o.Tenant, o.WorkspaceID, o.ID, *o.OperationID}
	}
	return []any{o.Tenant, o.WorkspaceID, o.ID, *o.OperationID, state, upload, etag}
}
func (s *Service) Complete(ctx context.Context, a identity.Auth, workspace, object, reason, request string) (Object, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return Object{}, admissionError
	}
	defer finish()
	if !id(request) || !workspaces.ValidReason(reason) {
		return Object{}, fail("VALIDATION_FAILED")
	}
	if e := s.admitNewBusiness(ctx, a, workspace); e != nil {
		return Object{}, e
	}
	var out Object
	e := s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if e = s.writeObject(ctx, q, a, w, o, true); e != nil {
			return e
		}
		if o.State == "ready" || o.State == "completing" {
			out = o.Object
			return nil
		}
		if e = liveWithin(ctx, q, o, true); e != nil {
			return e
		}
		if o.State != "uploading" || o.UploadID == nil {
			return fail("OBJECT_NOT_READY")
		}
		if _, e = s.partsWithin(ctx, q, o, true); e != nil {
			return e
		}
		if e = s.operationWithin(ctx, q, a, w, o, "complete", "completing"); e != nil {
			return e
		}
		if e = audit(ctx, q, a, w, "object.complete_requested", object, reason, request); e != nil {
			return e
		}
		value, e := s.objectWithin(ctx, q, w, object)
		out = value.Object
		return e
	})
	if e != nil {
		return Object{}, e
	}
	return out, nil
}

// Failure reduction cannot publish data or reset ownership. Only this exact
// persisted operation is annotated; expired credentials do not prevent reduction.
func (s *Service) reduceFailure(ctx context.Context, o storedObject, cause error) error {
	if o.OperationID == nil {
		return fail("OBJECT_OPERATION_STALE")
	}
	r, e := s.repository()
	if e != nil {
		return safe(e)
	}
	return safe(r.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		count, e := q.Exec(ctx, statement(q, "UPDATE artifact_objects SET error_code=$5,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND operation_id=$4", "UPDATE artifact_objects SET error_code=?,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=? AND operation_id=?", `UPDATE "ARTIFACT_OBJECTS" SET "ERROR_CODE"=?,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=? AND "OPERATION_ID"=?`, reduceArgs(q, o, safe(cause).Error())...))
		if e != nil {
			return e
		}
		if count != 1 {
			return fail("OBJECT_OPERATION_STALE")
		}
		return nil
	}))
}
func reduceArgs(q transaction.Executor, o storedObject, code string) []any {
	if positionalArguments(q) {
		return []any{code, o.Tenant, o.WorkspaceID, o.ID, *o.OperationID}
	}
	return []any{o.Tenant, o.WorkspaceID, o.ID, *o.OperationID, code}
}
func metadata(o storedObject) map[string]string {
	return map[string]string{"object-id": o.ID, "sha256": o.SHA256, "bytes": strconv.FormatInt(o.ExpectedBytes, 10)}
}
func infoMatches(o storedObject, i objectstore.Info) bool {
	return i.Bytes == o.ExpectedBytes && i.ETag != "" && i.Metadata["object-id"] == o.ID && i.Metadata["sha256"] == o.SHA256 && i.Metadata["bytes"] == strconv.FormatInt(o.ExpectedBytes, 10)
}

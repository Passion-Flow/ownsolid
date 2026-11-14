package artifactstores

import (
	"context"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

// Reconcile explicitly fences the previous operation before inspecting one exact
// remote upload/object. A timeout alone never certifies remote absence or stop.
func (s *Service) Reconcile(ctx context.Context, a identity.Auth, workspace, object, reason, request string) (Object, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return Object{}, admissionError
	}
	defer finish()
	if !workspaces.ValidReason(reason) || !id(request) {
		return Object{}, fail("VALIDATION_FAILED")
	}
	var expected storedObject
	reset := false
	e := s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if e = s.writeObject(ctx, q, a, w, o, false); e != nil {
			return e
		}
		if e = s.recoverExecutionWithin(ctx, q, a, w, o, reason, request); e != nil {
			return e
		}
		o, e = s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}

		if o.OperationID == nil {
			expected = o
			return nil
		}
		if o.OperationKind == nil || o.OperationPhase == nil {
			return fail("OBJECT_OPERATION_STALE")
		}
		if *o.OperationKind == "part" && *o.OperationPhase == "prepared" {
			// The remote call cannot occur until a sent commit for this operation. A
			// superseded local writer will fail that commit; preserve its private file.
			parts, e := s.partsWithin(ctx, q, o, false)
			if e != nil {
				return e
			}
			for _, p := range parts {
				if p.ETag == "" {
					if _, e = q.Exec(ctx, statement(q, "DELETE FROM artifact_object_parts WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND number=$4 AND state='intent'", "DELETE FROM artifact_object_parts WHERE tenant_id=? AND workspace_id=? AND object_id=? AND number=? AND state='intent'", `DELETE FROM "ARTIFACT_OBJECT_PARTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "NUMBER"=? AND "STATE"='intent'`, o.Tenant, o.WorkspaceID, o.ID, p.Number)); e != nil {
						return e
					}
				}
			}
			if e = clearOperation(ctx, q, o, "uploading", o.UploadID, o.ETag); e != nil {
				return e
			}
			if e = audit(ctx, q, a, w, "object.part_reset", object, reason, request); e != nil {
				return e
			}
			expected, e = s.objectWithin(ctx, q, w, object)
			reset = true
			return e
		}
		if *o.OperationPhase == "prepared" {
			var expired bool
			if e = q.QueryRow(ctx, statement(q, "SELECT operation_expires_at<=clock_timestamp() FROM artifact_objects WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "SELECT operation_expires_at<=UTC_TIMESTAMP(6) FROM artifact_objects WHERE tenant_id=? AND workspace_id=? AND id=?", `SELECT CASE WHEN "OPERATION_EXPIRES_AT"<=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) THEN 1 ELSE 0 END FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`, o.Tenant, o.WorkspaceID, o.ID)).Scan(&expired); e != nil {
				return e
			}
			if o.ErrorCode == nil && !expired {
				return fail("OBJECT_BUSY")
			}
			operation := uuid.NewString()
			if _, e = q.Exec(ctx, statement(q, "UPDATE artifact_objects SET operation_id=$4,operation_authority=$5,operation_expires_at=clock_timestamp()+make_interval(secs=>$6),error_code=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "UPDATE artifact_objects SET operation_id=?,operation_authority=CAST(? AS JSON),operation_expires_at=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),error_code=NULL,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?", `UPDATE "ARTIFACT_OBJECTS" SET "OPERATION_ID"=?,"OPERATION_AUTHORITY"=?,"OPERATION_EXPIRES_AT"=(SYS_EXTRACT_UTC(SYSTIMESTAMP(6))+NUMTODSINTERVAL(?,'SECOND')),"ERROR_CODE"=NULL,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`, reconcileArgs(q, o, operation, a, s.Configuration.TransferSeconds)...)); e != nil {
				return e
			}
			if e = audit(ctx, q, a, w, "object.prepared_recovered", object, reason, request); e != nil {
				return e
			}
			expected, e = s.objectWithin(ctx, q, w, object)
			reset = true
			return e
		}
		var expired bool
		if e = q.QueryRow(ctx, statement(q, "SELECT operation_expires_at<=clock_timestamp() FROM artifact_objects WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "SELECT operation_expires_at<=UTC_TIMESTAMP(6) FROM artifact_objects WHERE tenant_id=? AND workspace_id=? AND id=?", `SELECT CASE WHEN "OPERATION_EXPIRES_AT"<=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) THEN 1 ELSE 0 END FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`, o.Tenant, o.WorkspaceID, o.ID)).Scan(&expired); e != nil {
			return e
		}
		if !expired {
			return fail("OBJECT_BUSY")
		}
		operation := uuid.NewString()
		if _, e = q.Exec(ctx, statement(q, "UPDATE artifact_objects SET operation_id=$4,operation_authority=$5,operation_expires_at=clock_timestamp()+make_interval(secs=>$6),error_code=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "UPDATE artifact_objects SET operation_id=?,operation_authority=CAST(? AS JSON),operation_expires_at=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),error_code=NULL,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?", `UPDATE "ARTIFACT_OBJECTS" SET "OPERATION_ID"=?,"OPERATION_AUTHORITY"=?,"OPERATION_EXPIRES_AT"=(SYS_EXTRACT_UTC(SYSTIMESTAMP(6))+NUMTODSINTERVAL(?,'SECOND')),"ERROR_CODE"=NULL,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`, reconcileArgs(q, o, operation, a, s.Configuration.TransferSeconds)...)); e != nil {
			return e
		}
		expected, e = s.objectWithin(ctx, q, w, object)
		return e
	})
	if e != nil {
		return Object{}, e
	}
	if reset || expected.OperationID == nil {
		return expected.Object, nil
	}
	kind := *expected.OperationKind
	state := ""
	upload, etag := expected.UploadID, expected.ETag
	queue := false
	var received *objectstore.Part
	switch kind {
	case "ingest":
		if _, e = s.ingestionForResume(ctx, a, expected); e != nil {
			return Object{}, e
		}
		state = "ready"
		queue = true
	case "create":
		uploads, e := s.Transport.FindUploads(ctx, expected.ref())
		if e != nil {
			s.reduceFailureBounded(expected, e)
			return Object{}, safe(e)
		}
		if len(uploads) == 0 {
			return Object{}, fail("OBJECT_REMOTE_OUTCOME_UNKNOWN")
		}
		if len(uploads) != 1 {
			return Object{}, fail("OBJECT_AMBIGUOUS_REMOTE_UPLOAD")
		}
		upload = &uploads[0].ID
		state = "uploading"
	case "complete":
		info, e := s.verifyWhole(ctx, expected)
		if e == nil {
			state = "ready"
			etag = &info.ETag
		} else if e.Error() == "OBJECT_NOT_FOUND" {
			if expected.UploadID == nil {
				return Object{}, fail("OBJECT_UPLOAD_INVALID")
			}
			if _, e = s.Transport.Parts(ctx, expected.ref(), *expected.UploadID); e != nil {
				return Object{}, safe(e)
			}
			state = "completing"
			queue = true
		} else {
			s.reduceFailureBounded(expected, e)
			return Object{}, e
		}
	case "part":
		if expected.UploadID == nil {
			return Object{}, fail("OBJECT_UPLOAD_INVALID")
		}
		remote, e := s.Transport.Parts(ctx, expected.ref(), *expected.UploadID)
		if e != nil {
			return Object{}, safe(e)
		}
		var intent objectstore.Part
		var stage string
		e = s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
			o, e := s.objectWithin(ctx, q, w, object)
			if e != nil {
				return e
			}
			if e = s.sameOperation(ctx, q, a, w, o, expected, true); e != nil {
				return e
			}
			parts, e := s.partsWithin(ctx, q, o, false)
			if e != nil {
				return e
			}
			for _, p := range parts {
				if p.ETag == "" {
					if intent.Number != 0 {
						return fail("OBJECT_OPERATION_STALE")
					}
					intent = p
				}
			}
			if intent.Number == 0 {
				return fail("OBJECT_OPERATION_STALE")
			}
			return q.QueryRow(ctx, statement(q, "SELECT stage_id::text FROM artifact_object_parts WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND number=$4", "SELECT stage_id FROM artifact_object_parts WHERE tenant_id=? AND workspace_id=? AND object_id=? AND number=?", `SELECT "STAGE_ID" FROM "ARTIFACT_OBJECT_PARTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "NUMBER"=?`, o.Tenant, o.WorkspaceID, o.ID, intent.Number)).Scan(&stage)
		})
		if e != nil {
			return Object{}, e
		}
		for _, p := range remote {
			if p.Number == intent.Number && p.Bytes == intent.Bytes && p.SHA256 == intent.SHA256 && p.ETag != "" {
				copy := p
				received = &copy
			}
		}
		if received == nil {
			staged, e := s.Stage.OpenVerified(ctx, stage, intent.Bytes, intent.SHA256)
			if e != nil {
				return Object{}, safe(e)
			}
			part, e := s.Transport.PutPart(ctx, expected.ref(), *expected.UploadID, intent, staged.File)
			staged.File.Close()
			if e != nil {
				s.reduceFailureBounded(expected, e)
				return Object{}, safe(e)
			}
			received = &part
		}
		state = "uploading"
	case "delete":
		info, e := s.Transport.Head(ctx, expected.ref())
		if e != nil {
			if e.Error() != "OBJECT_NOT_FOUND" {
				return Object{}, safe(e)
			}
			state = "deleted"
		} else {
			if expected.ETag == nil || info.ETag != *expected.ETag || !infoMatches(expected, info) {
				return Object{}, fail("OBJECT_CHANGED")
			}
			state = "deleting"
			queue = true
		}
	case "abort":
		// A missing create acknowledgment with no known upload cannot be converted
		// into an absence assertion. Recover a unique actual owned ID first.
		if upload == nil {
			uploads, e := s.Transport.FindUploads(ctx, expected.ref())
			if e != nil {
				return Object{}, safe(e)
			}
			if len(uploads) == 0 {
				return Object{}, fail("OBJECT_REMOTE_OUTCOME_UNKNOWN")
			}
			if len(uploads) != 1 {
				return Object{}, fail("OBJECT_AMBIGUOUS_REMOTE_UPLOAD")
			}
			upload = &uploads[0].ID
		}
		if e = s.Transport.Abort(ctx, expected.ref(), *upload); e != nil {
			return Object{}, safe(e)
		}
		if _, e = s.Transport.Parts(ctx, expected.ref(), *upload); e == nil || e.Error() != "OBJECT_UPLOAD_NOT_FOUND" {
			return Object{}, fail("OBJECT_REMOTE_OUTCOME_UNKNOWN")
		}
		_, e := s.Transport.Head(ctx, expected.ref())
		if e == nil {
			verified, e := s.verifyWhole(ctx, expected)
			if e != nil {
				return Object{}, e
			}
			if e = s.Transport.Delete(ctx, expected.ref(), verified.ETag); e != nil {
				return Object{}, safe(e)
			}
			if _, e = s.Transport.Head(ctx, expected.ref()); e == nil || e.Error() != "OBJECT_NOT_FOUND" {
				return Object{}, fail("OBJECT_REMOTE_OUTCOME_UNKNOWN")
			}
		} else if e.Error() != "OBJECT_NOT_FOUND" {
			return Object{}, safe(e)
		}
		state = "aborted"
	default:
		return Object{}, fail("OBJECT_RECONCILE_REQUIRED")
	}
	var out Object
	e = s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if current.OperationID == nil || *current.OperationID != *expected.OperationID {
			return fail("OBJECT_OPERATION_STALE")
		}
		if e = s.writeObject(ctx, q, a, w, current, false); e != nil {
			return e
		}
		if kind == "create" || kind == "part" || kind == "complete" {
			if e = liveWithin(ctx, q, current, true); e != nil {
				return e
			}
		}
		if received != nil {
			if _, e = q.Exec(ctx, statement(q, "UPDATE artifact_object_parts SET state='uploaded',etag=$5 WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND number=$4 AND state='intent'", "UPDATE artifact_object_parts SET state='uploaded',etag=? WHERE tenant_id=? AND workspace_id=? AND object_id=? AND number=? AND state='intent'", `UPDATE "ARTIFACT_OBJECT_PARTS" SET "STATE"='uploaded',"ETAG"=? WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "NUMBER"=? AND "STATE"='intent'`, partAckArgs(q, current, *received)...)); e != nil {
				return e
			}
		}
		if queue {
			_, e = q.Exec(ctx, statement(q, "UPDATE artifact_objects SET operation_phase='prepared',operation_authority=$4,error_code=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "UPDATE artifact_objects SET operation_phase='prepared',operation_authority=CAST(? AS JSON),error_code=NULL,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?", `UPDATE "ARTIFACT_OBJECTS" SET "OPERATION_PHASE"='prepared',"OPERATION_AUTHORITY"=?,"ERROR_CODE"=NULL,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`, queueResumeArgs(q, current, a)...))
		} else {
			e = clearOperation(ctx, q, current, state, upload, etag)
		}
		if e != nil {
			return e
		}
		if e = audit(ctx, q, a, w, "object.reconciled", object, reason, request); e != nil {
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
func reconcileArgs(q transaction.Executor, o storedObject, op string, a identity.Auth, seconds int) []any {
	if positionalArguments(q) {
		return []any{op, jsonParameter(q, actorBytes(a)), seconds, o.Tenant, o.WorkspaceID, o.ID}
	}
	return []any{o.Tenant, o.WorkspaceID, o.ID, op, jsonParameter(q, actorBytes(a)), seconds}
}

func queueResumeArgs(q transaction.Executor, o storedObject, a identity.Auth) []any {
	if positionalArguments(q) {
		return []any{jsonParameter(q, actorBytes(a)), o.Tenant, o.WorkspaceID, o.ID}
	}
	return []any{o.Tenant, o.WorkspaceID, o.ID, jsonParameter(q, actorBytes(a))}
}

// Explicit recovery renews only execution authority; immutable creation epoch,
// membership, actor and fingerprint remain provenance of the original upload.
func (s *Service) recoverExecutionWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, o storedObject, reason, request string) error {
	if o.Creator != a.Me.User.ID || admin(a) {
		return nil
	}
	var membership string
	if e := q.QueryRow(ctx, statement(q, "SELECT authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", "SELECT authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", `SELECT "AUTHORITY_ID" FROM "WORKSPACE_MEMBERSHIPS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "USER_ID"=?`, w.TenantID, w.ID, a.Me.User.ID)).Scan(&membership); e != nil {
		return e
	}
	if o.ExecutionEpoch == a.Epoch && o.ExecutionMembership != nil && *o.ExecutionMembership == membership {
		return nil
	}
	_, e := q.Exec(ctx, statement(q, "UPDATE artifact_objects SET execution_epoch=$4,execution_membership=$5,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "UPDATE artifact_objects SET execution_epoch=?,execution_membership=?,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?", `UPDATE "ARTIFACT_OBJECTS" SET "EXECUTION_EPOCH"=?,"EXECUTION_MEMBERSHIP"=?,"REVISION"="REVISION"+1,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`, executionRecoveryArgs(q, o, a.Epoch, membership)...))
	if e != nil {
		return e
	}
	return audit(ctx, q, a, w, "object.execution_authority_recovered", o.ID, reason, request)
}
func executionRecoveryArgs(q transaction.Executor, o storedObject, epoch int64, membership string) []any {
	if positionalArguments(q) {
		return []any{epoch, membership, o.Tenant, o.WorkspaceID, o.ID}
	}
	return []any{o.Tenant, o.WorkspaceID, o.ID, epoch, membership}
}

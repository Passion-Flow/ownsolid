package artifactstores

import (
	"context"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

func (s *Service) verifiedSource(ctx context.Context, a identity.Auth, workspace, object, grant, action string) (objectstore.Staged, storedObject, error) {
	issuer, e := s.grantIssuer(ctx, a, workspace, object, grant)
	if e != nil {
		return objectstore.Staged{}, storedObject{}, e
	}
	var o storedObject
	e = s.within(ctx, a, workspace, issuer, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		var e error
		o, e = s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if e = s.authorize(ctx, q, a, w, o, action, grant); e != nil {
			return e
		}
		if e = liveWithin(ctx, q, o, false); e != nil {
			return e
		}
		if o.State != "ready" || o.ETag == nil {
			return fail("OBJECT_NOT_READY")
		}
		return nil
	})
	if e != nil {
		return objectstore.Staged{}, storedObject{}, e
	}
	info, e := s.Transport.Head(ctx, o.ref())
	if e != nil {
		return objectstore.Staged{}, storedObject{}, safe(e)
	}
	if !infoMatches(o, info) || info.ETag != *o.ETag {
		return objectstore.Staged{}, storedObject{}, fail("OBJECT_CHANGED")
	}
	download, e := s.Transport.Open(ctx, o.ref(), *o.ETag)
	if e != nil {
		return objectstore.Staged{}, storedObject{}, safe(e)
	}
	if !infoMatches(o, download.Info) || download.Info.ETag != *o.ETag {
		download.Body.Close()
		return objectstore.Staged{}, storedObject{}, fail("OBJECT_CHANGED")
	}
	staged, e := s.Stage.Write(ctx, uuid.NewString(), o.ExpectedBytes, o.SHA256, download.Body)
	download.Body.Close()
	if e != nil {
		return objectstore.Staged{}, storedObject{}, safe(e)
	}
	keep := false
	defer func() {
		if !keep {
			s.Stage.Discard(staged)
		}
	}()
	after, e := s.Transport.Head(ctx, o.ref())
	if e != nil {
		return objectstore.Staged{}, storedObject{}, safe(e)
	}
	if !infoMatches(o, after) || after.ETag != *o.ETag {
		return objectstore.Staged{}, storedObject{}, fail("OBJECT_CHANGED")
	}
	// Final current grant/session/Workspace/object checks run after all remote IO
	// and hashing. No S3 URL, prefix permission or stale metadata grants access.
	e = s.within(ctx, a, workspace, issuer, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if e = s.authorize(ctx, q, a, w, current, action, grant); e != nil {
			return e
		}
		if e = liveWithin(ctx, q, current, false); e != nil {
			return e
		}
		if current.State != "ready" || current.ETag == nil || *current.ETag != *o.ETag || current.SHA256 != o.SHA256 || current.ExpectedBytes != o.ExpectedBytes {
			return fail("OBJECT_CHANGED")
		}
		return s.Stage.Verify(staged)
	})
	if e != nil {
		return objectstore.Staged{}, storedObject{}, e
	}
	keep = true
	return staged, o, nil
}
func (s *Service) Download(ctx context.Context, a identity.Auth, workspace, object, grant string) (httpserver.Download, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return httpserver.Download{}, admissionError
	}
	defer finish()
	staged, o, e := s.verifiedSource(ctx, a, workspace, object, grant, "read")
	if e != nil {
		return httpserver.Download{}, e
	}
	if e = ctx.Err(); e != nil {
		s.Stage.Discard(staged)
		return httpserver.Download{}, safe(e)
	}
	file, e := s.Stage.Detach(staged)
	if e != nil {
		staged.File.Close()
		return httpserver.Download{}, safe(e)
	}
	return httpserver.Download{File: file, Size: o.ExpectedBytes, SHA256: o.SHA256, FileName: o.ID + ".bin", ContentType: "application/octet-stream"}, nil
}
func (s *Service) Delete(ctx context.Context, a identity.Auth, workspace, object, reason, request string) (Object, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return Object{}, admissionError
	}
	defer finish()
	return s.endObject(ctx, a, workspace, object, reason, request, false)
}
func (s *Service) Abort(ctx context.Context, a identity.Auth, workspace, object, reason, request string) (Object, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return Object{}, admissionError
	}
	defer finish()
	return s.endObject(ctx, a, workspace, object, reason, request, true)
}
func (s *Service) endObject(ctx context.Context, a identity.Auth, workspace, object, reason, request string, abort bool) (Object, error) {
	if !id(request) || !workspaces.ValidReason(reason) {
		return Object{}, fail("VALIDATION_FAILED")
	}
	var out Object
	e := s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if e = s.writeObject(ctx, q, a, w, o, false); e != nil {
			return e
		}
		if o.State == "deleted" || o.State == "aborted" {
			out = o.Object
			return nil
		}
		references, e := ingestionReferencesWithin(ctx, q, o)
		if e != nil {
			return e
		}
		if references {
			return fail("OBJECT_REFERENCED")
		}
		kind, state := "delete", "deleting"
		if abort {
			kind, state = "abort", "aborting"
			if o.State == "ready" {
				return fail("OBJECT_STATE_INVALID")
			}
		} else if o.State != "ready" && o.State != "deleting" {
			return fail("OBJECT_NOT_READY")
		}
		if o.State == state {
			out = o.Object
			return nil
		}
		if abort && o.OperationID != nil {
			if _, e = q.Exec(ctx, statement(q, "UPDATE artifact_objects SET operation_id=NULL,operation_kind=NULL,operation_phase=NULL,operation_authority=NULL,operation_expires_at=NULL WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "UPDATE artifact_objects SET operation_id=NULL,operation_kind=NULL,operation_phase=NULL,operation_authority=NULL,operation_expires_at=NULL WHERE tenant_id=? AND workspace_id=? AND id=?", `UPDATE "ARTIFACT_OBJECTS" SET "OPERATION_ID"=NULL,"OPERATION_KIND"=NULL,"OPERATION_PHASE"=NULL,"OPERATION_AUTHORITY"=NULL,"OPERATION_EXPIRES_AT"=NULL WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`, o.Tenant, o.WorkspaceID, o.ID)); e != nil {
				return e
			}
			o.OperationID = nil
		}
		if e = s.operationWithin(ctx, q, a, w, o, kind, state); e != nil {
			return e
		}
		if _, e = q.Exec(ctx, statement(q, "UPDATE artifact_object_grants SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3", "UPDATE artifact_object_grants SET revoked_at=COALESCE(revoked_at,UTC_TIMESTAMP(6)) WHERE tenant_id=? AND workspace_id=? AND object_id=?", `UPDATE "ARTIFACT_OBJECT_GRANTS" SET "REVOKED_AT"=COALESCE("REVOKED_AT",SYS_EXTRACT_UTC(SYSTIMESTAMP(6))) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=?`, o.Tenant, o.WorkspaceID, o.ID)); e != nil {
			return e
		}
		if e = audit(ctx, q, a, w, "object."+kind+"_requested", object, reason, request); e != nil {
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

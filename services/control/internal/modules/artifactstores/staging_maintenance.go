package artifactstores

import (
	"context"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

func (s *Service) StagingRecords(ctx context.Context, a identity.Auth, workspace string, limit int) ([]objectstore.StageRecord, error) {
	ctx, done, e := s.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	if limit < 1 || limit > MaximumObjectPageSize {
		return nil, fail("VALIDATION_FAILED")
	}
	e = s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if !admin(a) {
			return fail("ACCESS_DENIED")
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	out, e := s.Stage.Records(limit)
	if e != nil {
		return nil, safe(e)
	}
	// The immutable inode inventory is an administrator operation. Recheck current
	// authority after filesystem IO before returning even this private projection.
	e = s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if !admin(a) {
			return fail("ACCESS_DENIED")
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
func (s *Service) CleanupStaging(ctx context.Context, a identity.Auth, workspace string, record objectstore.StageRecord, reason, request string) error {
	ctx, done, e := s.begin(ctx)
	if e != nil {
		return e
	}
	defer done()
	if !id(record.ID) || record.Namespace != s.Stage.Namespace() || !id(request) || !workspaces.ValidReason(reason) {
		return fail("VALIDATION_FAILED")
	}
	e = s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if !admin(a) {
			return fail("ACCESS_DENIED")
		}
		var retained bool
		if e := q.QueryRow(ctx, statement(q, "SELECT EXISTS(SELECT 1 FROM artifact_object_parts p JOIN artifact_objects o ON o.tenant_id=p.tenant_id AND o.workspace_id=p.workspace_id AND o.id=p.object_id WHERE p.tenant_id=$1 AND o.store_id=$2 AND p.stage_id=$3 AND p.state='intent' AND o.state NOT IN ('deleted','aborted'))", "SELECT EXISTS(SELECT 1 FROM artifact_object_parts p JOIN artifact_objects o ON o.tenant_id=p.tenant_id AND o.workspace_id=p.workspace_id AND o.id=p.object_id WHERE p.tenant_id=? AND o.store_id=? AND p.stage_id=? AND p.state='intent' AND o.state NOT IN ('deleted','aborted'))", `SELECT CASE WHEN EXISTS(SELECT 1 FROM "ARTIFACT_OBJECT_PARTS" "P" JOIN "ARTIFACT_OBJECTS" "O" ON "O"."TENANT_ID"="P"."TENANT_ID" AND "O"."WORKSPACE_ID"="P"."WORKSPACE_ID" AND "O"."ID"="P"."OBJECT_ID" WHERE "P"."TENANT_ID"=? AND "O"."STORE_ID"=? AND "P"."STAGE_ID"=? AND "P"."STATE"='intent' AND "O"."STATE" NOT IN ('deleted','aborted')) THEN 1 ELSE 0 END FROM DUAL`, w.TenantID, s.Stage.Namespace(), record.ID)).Scan(&retained); e != nil {
			return e
		}
		if retained {
			return fail("OBJECT_STAGING_REFERENCED")
		}
		return stagingAudit(ctx, q, a, w, "object.staging_cleanup_requested", record.ID, reason, request)
	})
	if e != nil {
		return e
	}
	// A successful intent commit precedes any unlink. Unknown commit returns above.
	if e = s.Stage.RemoveAttempt(record); e != nil {
		return safe(e)
	}
	return s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if !admin(a) {
			return fail("ACCESS_DENIED")
		}
		return stagingAudit(ctx, q, a, w, "object.staging_removed", record.ID, reason, request)
	})
}
func stagingAudit(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, action, target, reason, request string) error {
	_, e := q.Exec(ctx, statement(q, "INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6,'success','artifact_staging',$7,$8,$9)", "INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,?,?,'success','artifact_staging',?,?,?)", `INSERT INTO "AUDIT_EVENTS"("ID","OCCURRED_AT","ACTOR_USER_ID","TENANT_ID","WORKSPACE_ID","AUDIENCE","ACTION","OUTCOME","TARGET_TYPE","TARGET_ID","REASON","REQUEST_ID") VALUES(?,SYS_EXTRACT_UTC(SYSTIMESTAMP(6)),?,?,?,?,?,'success','artifact_staging',?,?,?)`, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, target, reason, request))
	return e
}

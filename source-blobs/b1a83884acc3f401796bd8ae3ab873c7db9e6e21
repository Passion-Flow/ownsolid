package artifactstores

import (
	"context"
	"strconv"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type ExpiryResult struct {
	Queued   []string `json:"queued_object_ids"`
	Retained []string `json:"referenced_object_ids"`
}

// Expire creates a bounded, audited cleanup intent under a current administrator
// or Workspace owner. It never represents queued deletion as remote completion.
func (s *Service) Expire(ctx context.Context, a identity.Auth, workspace string, limit int, reason, request string) (ExpiryResult, error) {
	ctx, done, e := s.begin(ctx)
	if e != nil {
		return ExpiryResult{}, e
	}
	defer done()
	if !id(request) || !workspaces.ValidReason(reason) || limit < 1 || limit > MaximumObjectPageSize {
		return ExpiryResult{}, fail("VALIDATION_FAILED")
	}
	out := ExpiryResult{Queued: []string{}, Retained: []string{}}
	e = s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if !admin(a) && (w.Role == nil || *w.Role != "owner") {
			return fail("ACCESS_DENIED")
		}
		rows, e := q.Query(ctx, statement(q, "SELECT "+pgObjectFields+" FROM artifact_objects WHERE tenant_id=$1 AND workspace_id=$2 AND store_id=$3 AND operation_id IS NULL AND state IN ('ready','uploading','creating') AND (expires_at<=clock_timestamp() OR (state<>'ready' AND upload_expires_at<=clock_timestamp())) ORDER BY id LIMIT $4 FOR UPDATE", "SELECT "+myObjectFields+" FROM artifact_objects WHERE tenant_id=? AND workspace_id=? AND store_id=? AND operation_id IS NULL AND state IN ('ready','uploading','creating') AND (expires_at<=UTC_TIMESTAMP(6) OR (state<>'ready' AND upload_expires_at<=UTC_TIMESTAMP(6))) ORDER BY id LIMIT ? FOR UPDATE", `SELECT `+dmObjectFields+` FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "STORE_ID"=? AND "OPERATION_ID" IS NULL AND "STATE" IN ('ready','uploading','creating') AND ("EXPIRES_AT"<=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) OR ("STATE"<>'ready' AND "UPLOAD_EXPIRES_AT"<=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)))) ORDER BY "ID" LIMIT `+strconv.Itoa(limit)+` FOR UPDATE`, expiryArgs(q, w.TenantID, w.ID, s.Stage.Namespace(), limit)...))
		if e != nil {
			return e
		}
		objects := []storedObject{}
		for rows.Next() {
			o, e := scanObject(rows)
			if e != nil {
				rows.Close()
				return e
			}
			objects = append(objects, o)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, o := range objects {
			references, e := ingestionReferencesWithin(ctx, q, o)
			if e != nil {
				return e
			}
			if references {
				out.Retained = append(out.Retained, o.ID)
				continue
			}
			kind, state := "abort", "aborting"
			if o.State == "ready" {
				kind, state = "delete", "deleting"
			}
			if e = s.operationWithin(ctx, q, a, w, o, kind, state); e != nil {
				return e
			}
			if _, e = q.Exec(ctx, statement(q, "UPDATE artifact_object_grants SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3", "UPDATE artifact_object_grants SET revoked_at=COALESCE(revoked_at,UTC_TIMESTAMP(6)) WHERE tenant_id=? AND workspace_id=? AND object_id=?", `UPDATE "ARTIFACT_OBJECT_GRANTS" SET "REVOKED_AT"=COALESCE("REVOKED_AT",SYS_EXTRACT_UTC(SYSTIMESTAMP(6))) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=?`, w.TenantID, w.ID, o.ID)); e != nil {
				return e
			}
			if e = audit(ctx, q, a, w, "object.expired_cleanup_requested", o.ID, reason, request); e != nil {
				return e
			}
			out.Queued = append(out.Queued, o.ID)
		}
		return nil
	})
	if e != nil {
		return ExpiryResult{}, e
	}
	return out, nil
}

// FenceAfterRestoreWithin borrows the restore owner's transaction. It revokes
// external grants and prevents automatic external mutations from restored state.
// Remote identifiers and local ingestion receipts remain intact for reconciliation.
func FenceAfterRestoreWithin(ctx context.Context, q transaction.Executor, tenant string) error {
	if ctx == nil || q == nil || (q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL && q.Dialect() != transaction.Dameng) || !id(tenant) {
		return fail("VALIDATION_FAILED")
	}
	if _, e := q.Exec(ctx, statement(q, "UPDATE artifact_object_grants SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE tenant_id=$1", "UPDATE artifact_object_grants SET revoked_at=COALESCE(revoked_at,UTC_TIMESTAMP(6)) WHERE tenant_id=?", `UPDATE "ARTIFACT_OBJECT_GRANTS" SET "REVOKED_AT"=COALESCE("REVOKED_AT",SYS_EXTRACT_UTC(SYSTIMESTAMP(6))) WHERE "TENANT_ID"=?`, tenant)); e != nil {
		return e
	}
	if q.Dialect() == transaction.Dameng {
		return fenceDamengAfterRestore(ctx, q, tenant)
	}
	_, e := q.Exec(ctx, statement(q, "UPDATE artifact_objects SET operation_id=gen_random_uuid(),operation_phase='sent',error_code='OBJECT_RESTORE_RECONCILE_REQUIRED',operation_expires_at=clock_timestamp(),updated_at=clock_timestamp(),revision=revision+1 WHERE tenant_id=$1 AND operation_id IS NOT NULL", "UPDATE artifact_objects SET operation_id=UUID(),operation_phase='sent',error_code='OBJECT_RESTORE_RECONCILE_REQUIRED',operation_expires_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6),revision=revision+1 WHERE tenant_id=? AND operation_id IS NOT NULL", "", tenant))
	return e
}

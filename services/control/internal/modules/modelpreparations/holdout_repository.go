package modelpreparations

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func holdoutWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, id string) (Holdout, error) {
	if !datawire.ValidID(id) {
		return Holdout{}, fail("MODEL_HOLDOUT_NOT_FOUND")
	}
	out, e := scanHoldout(preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationDM("SELECT "+nativeHoldoutFields+` FROM "MODEL_HOLDOUTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=? FOR UPDATE`), "SELECT "+holdoutFields+" FROM model_holdouts WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR SHARE", "SELECT "+mysqlHoldoutFields+" FROM model_holdouts WHERE tenant_id=? AND workspace_id=? AND id=? FOR SHARE", w.TenantID, w.ID, id)))
	if e != nil {
		return Holdout{}, e
	}
	return out, nil
}
func (s *Service) replayHoldoutWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, key string, wanted []byte) (Holdout, bool, error) {
	var id string
	var previous []byte
	e := preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationDM(`SELECT "ID","CREATION_FINGERPRINT" FROM "MODEL_HOLDOUTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "CREATED_BY"=? AND "AUDIENCE"=? AND "CREATION_KEY"=? FOR UPDATE`), `SELECT id::text,creation_fingerprint FROM model_holdouts WHERE tenant_id=$1 AND workspace_id=$2 AND created_by=$3 AND audience=$4 AND creation_key=$5 FOR SHARE`, `SELECT id,creation_fingerprint FROM model_holdouts WHERE tenant_id=? AND workspace_id=? AND created_by=? AND audience=? AND creation_key=? FOR SHARE`, w.TenantID, w.ID, a.Me.User.ID, a.Me.Audience, key)).Scan(&id, &previous)
	if errors.Is(e, sql.ErrNoRows) {
		return Holdout{}, false, nil
	}
	if e != nil {
		return Holdout{}, false, e
	}
	if !same(previous, wanted) {
		return Holdout{}, false, fail("IDEMPOTENCY_CONFLICT")
	}
	out, e := holdoutWithin(ctx, q, w, id)
	if e != nil {
		return Holdout{}, false, e
	}
	return out, true, nil
}
func holdoutAuditWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, id, reason, request string) error {
	n, e := q.Exec(ctx, preparationSelectedSQL(q, preparationDM(`INSERT INTO "AUDIT_EVENTS"("ID","OCCURRED_AT","ACTOR_USER_ID","TENANT_ID","WORKSPACE_ID","AUDIENCE","ACTION","OUTCOME","TARGET_TYPE","TARGET_ID","REASON","REQUEST_ID") VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,?,?,?,'model.holdout_reserved','success','model_holdout',?,?,?)`), `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,'model.holdout_reserved','success','model_holdout',$6,$7,$8)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,?,'model.holdout_reserved','success','model_holdout',?,?,?)`, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, id, reason, request))
	if e != nil {
		return e
	}
	if n != 1 {
		return fail("STORAGE_UNAVAILABLE")
	}
	return nil
}

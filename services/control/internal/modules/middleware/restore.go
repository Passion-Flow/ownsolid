package middleware

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// InvalidateAuthoritiesWithin belongs to the coordinated fresh isolated restore.
// It fences execution authority; a cleared lease is not physical-stop evidence.
func InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor, reason string) error {
	if ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL {
		return fail("MIDDLEWARE_RESTORE_TRANSACTION_REQUIRED")
	}
	if _, pool := q.(transaction.Store); pool {
		return fail("MIDDLEWARE_RESTORE_TRANSACTION_REQUIRED")
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return fail("MIDDLEWARE_OPERATION_DEADLINE_REQUIRED")
	}
	if !workspaces.ValidReason(reason) {
		return fail("OPERATOR_REASON_REQUIRED")
	}
	var queued, running int64
	e := q.QueryRow(ctx, statement(q, `SELECT count(*) FILTER(WHERE state='queued'),count(*) FILTER(WHERE state='running') FROM middleware_operations WHERE state IN('queued','running')`, `SELECT COALESCE(SUM(state='queued'),0),COALESCE(SUM(state='running'),0) FROM middleware_operations WHERE state IN('queued','running')`)).Scan(&queued, &running)
	if e != nil {
		return e
	}
	if queued+running == 0 {
		return nil
	}
	n, e := q.Exec(ctx, statement(q, `UPDATE middleware_operations SET state=CASE WHEN state='queued' THEN 'failed' ELSE 'unknown' END,lease_expires_at=NULL,result_json=NULL,result_sha256=NULL,error_code='MIDDLEWARE_RESTORE_INVALIDATED',completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE state IN('queued','running')`, `UPDATE middleware_operations SET state=CASE WHEN state='queued' THEN 'failed' ELSE 'unknown' END,lease_expires_at=NULL,result_json=NULL,result_sha256=NULL,error_code='MIDDLEWARE_RESTORE_INVALIDATED',completed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE state IN('queued','running')`))
	if e != nil {
		return e
	}
	if n != queued+running {
		return fail("MIDDLEWARE_RESTORE_FENCE_CHANGED")
	}
	metadata, _ := json.Marshal(struct {
		Queued                int64 `json:"queued_failed"`
		Running               int64 `json:"running_unknown"`
		PhysicalStopConfirmed bool  `json:"physical_stop_confirmed"`
	}{Queued: queued, Running: running, PhysicalStopConfirmed: false})
	_, e = q.Exec(ctx, statement(q, `INSERT INTO audit_events(id,occurred_at,audience,action,outcome,target_type,request_id,reason,metadata) VALUES($1,clock_timestamp(),'operator','middleware.restore_invalidated','success','middleware_restore',$2,$3,$4::jsonb)`, `INSERT INTO audit_events(id,occurred_at,audience,action,outcome,target_type,request_id,reason,metadata) VALUES(?,UTC_TIMESTAMP(6),'operator','middleware.restore_invalidated','success','middleware_restore',?,?,CAST(? AS JSON))`, uuid.NewString(), uuid.NewString(), reason, string(metadata)))
	return e
}

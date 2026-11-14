package training

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
)

// FenceTrainingNode retains reservations until exact physical stop evidence is reconciled.
// The caller owns Workspace/node locks and the surrounding audit transaction.
func (s *Service) FenceTrainingNode(ctx context.Context, q transaction.Executor, n nodes.Node) error {
	return invalidateNodeWithin(ctx, q, n.TenantID, n.WorkspaceID, n.ID)
}
func InvalidateNode(ctx context.Context, tx pgx.Tx, tenant, workspace, node string) error {
	return invalidateNodeWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, node)
}

// InvalidateNodeWithin borrows the stopped-runtime operator or node mutation
// transaction; uncertain physical owners retain their reservation.
func InvalidateNodeWithin(ctx context.Context, q transaction.Executor, tenant, workspace, node string) error {
	return invalidateNodeWithin(ctx, q, tenant, workspace, node)
}
func invalidateNodeWithin(ctx context.Context, q transaction.Executor, tenant, workspace, node string) error {
	if ctx == nil || q == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	statements := []string{
		`UPDATE training_jobs SET state='recovery_required',error_code='TRAINING_NODE_UNAVAILABLE',wait_code=NULL,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND current_node_id=$3 AND state IN ('preparing','running','cancelling')`,
		`UPDATE training_attempts SET state='recovery_required',error_code='TRAINING_NODE_UNAVAILABLE',updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND physical_stopped_at IS NULL AND state IN ('pending','preparing','running','stopping')`,
		`UPDATE gpu_leases SET state='recovery_required',updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND training_job_id IS NOT NULL AND state='held'`,
	}
	if q.Dialect() == transaction.MySQL {
		statements = []string{
			`UPDATE training_jobs SET state='recovery_required',error_code='TRAINING_NODE_UNAVAILABLE',wait_code=NULL,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND current_node_id=? AND state IN ('preparing','running','cancelling')`,
			`UPDATE training_attempts SET state='recovery_required',error_code='TRAINING_NODE_UNAVAILABLE',updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND physical_stopped_at IS NULL AND state IN ('pending','preparing','running','stopping')`,
			`UPDATE gpu_leases SET state='recovery_required',updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND training_job_id IS NOT NULL AND state='held'`,
		}
	}
	for _, statement := range statements {
		if _, e := q.Exec(ctx, transaction.SQL(q.Dialect(), statement, tenant, workspace, node)); e != nil {
			return e
		}
	}
	return nil
}

// InvalidateAuthorities coordinates account/node/file restore in one operator transaction.
func InvalidateAuthorities(ctx context.Context, tx pgx.Tx) error {
	return InvalidateAuthoritiesWithin(ctx, transaction.PostgreSQLTransaction(tx))
}
func InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	for _, statement := range []transaction.Statement{
		acceptedAdapterSQL(q, `UPDATE training_jobs SET state=CASE WHEN state='queued' THEN 'cancelled' ELSE 'recovery_required' END,error_code='TRAINING_RESTORE_INVALIDATED',pending_recovery_id=NULL,revision=revision+1,finished_at=CASE WHEN state='queued' THEN clock_timestamp() ELSE NULL END,updated_at=clock_timestamp() WHERE state IN ('queued','preparing','running','cancelling')`, `UPDATE training_jobs SET finished_at=CASE WHEN state='queued' THEN UTC_TIMESTAMP(6) ELSE NULL END,state=CASE WHEN state='queued' THEN 'cancelled' ELSE 'recovery_required' END,error_code='TRAINING_RESTORE_INVALIDATED',pending_recovery_id=NULL,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE state IN ('queued','preparing','running','cancelling')`),
		acceptedAdapterSQL(q, `UPDATE training_attempts SET state='recovery_required',error_code='TRAINING_RESTORE_INVALIDATED',updated_at=clock_timestamp() WHERE physical_stopped_at IS NULL AND state IN ('pending','preparing','running','stopping')`, `UPDATE training_attempts SET state='recovery_required',error_code='TRAINING_RESTORE_INVALIDATED',updated_at=UTC_TIMESTAMP(6) WHERE physical_stopped_at IS NULL AND state IN ('pending','preparing','running','stopping')`),
		acceptedAdapterSQL(q, `UPDATE gpu_leases SET state='recovery_required',updated_at=clock_timestamp() WHERE training_job_id IS NOT NULL AND state='held'`, `UPDATE gpu_leases SET state='recovery_required',updated_at=UTC_TIMESTAMP(6) WHERE training_job_id IS NOT NULL AND state='held'`),
	} {
		if _, e := q.Exec(ctx, statement); e != nil {
			return e
		}
	}
	return nil
}

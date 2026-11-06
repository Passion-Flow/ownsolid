package modelpreparations

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// FencePreparationNode retains physical ownership in the caller's Workspace/node transaction.
func (s *Service) FencePreparationNode(ctx context.Context, q transaction.Executor, n nodes.Node) error {
	return invalidateNodeWithin(ctx, q, n.TenantID, n.WorkspaceID, n.ID)
}

func InvalidateNode(ctx context.Context, tx pgx.Tx, tenant, workspace, node string) error {
	return invalidateNodeWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, node)
}
func invalidateNodeWithin(ctx context.Context, q transaction.Executor, tenant, workspace, node string) error {
	if ctx == nil || q == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	statements := []string{
		`UPDATE model_preparations SET state='recovery_required',error_code='MODEL_PREPARATION_NODE_UNAVAILABLE',wait_code=NULL,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND current_node_id=$3 AND state IN ('preparing','running','cancelling')`,
		`UPDATE model_preparation_attempts SET state='recovery_required',error_code='MODEL_PREPARATION_NODE_UNAVAILABLE',updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND physical_stopped_at IS NULL AND state IN ('pending','preparing','running','stopping')`,
		`UPDATE gpu_leases SET state='recovery_required',updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND model_preparation_id IS NOT NULL AND state='held'`,
	}
	if q.Dialect() == transaction.MySQL {
		statements = []string{
			`UPDATE model_preparations SET state='recovery_required',error_code='MODEL_PREPARATION_NODE_UNAVAILABLE',wait_code=NULL,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND current_node_id=? AND state IN ('preparing','running','cancelling')`,
			`UPDATE model_preparation_attempts SET state='recovery_required',error_code='MODEL_PREPARATION_NODE_UNAVAILABLE',updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND physical_stopped_at IS NULL AND state IN ('pending','preparing','running','stopping')`,
			`UPDATE gpu_leases SET state='recovery_required',updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND model_preparation_id IS NOT NULL AND state='held'`,
		}
	}
	if q.Dialect() == transaction.Dameng {
		statements = []string{
			`UPDATE "MODEL_PREPARATIONS" SET "STATE"='recovery_required',"ERROR_CODE"='MODEL_PREPARATION_NODE_UNAVAILABLE',"WAIT_CODE"=NULL,"REVISION"="REVISION"+1,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "CURRENT_NODE_ID"=? AND "STATE" IN ('preparing','running','cancelling')`,
			`UPDATE "MODEL_PREPARATION_ATTEMPTS" SET "STATE"='recovery_required',"ERROR_CODE"='MODEL_PREPARATION_NODE_UNAVAILABLE',"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "PHYSICAL_STOPPED_AT" IS NULL AND "STATE" IN ('pending','preparing','running','stopping')`,
			`UPDATE "GPU_LEASES" SET "STATE"='recovery_required',"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "MODEL_PREPARATION_ID" IS NOT NULL AND "STATE"='held'`,
		}
	}
	for _, statement := range statements {
		if _, e := q.Exec(ctx, transaction.SQL(q.Dialect(), statement, tenant, workspace, node)); e != nil {
			return e
		}
	}
	return nil
}

// InvalidateAuthorities participates in the all-module database-aware restore transaction.
func InvalidateAuthorities(ctx context.Context, tx pgx.Tx) error {
	return InvalidateAuthoritiesWithin(ctx, transaction.PostgreSQLTransaction(tx))
}

// InvalidateNodeWithin borrows the stopped operator's exact selected transaction.
func InvalidateNodeWithin(ctx context.Context, q transaction.Executor, tenant, workspace, node string) error {
	return invalidateNodeWithin(ctx, q, tenant, workspace, node)
}

func InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	statements := []transaction.Statement{
		preparationSelectedSQL(q, preparationDM(`UPDATE "MODEL_PREPARATIONS" SET "FINISHED_AT"=CASE WHEN "STATE"='queued' THEN SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) ELSE NULL END,"STATE"=CASE WHEN "STATE"='queued' THEN 'cancelled' ELSE 'recovery_required' END,"ERROR_CODE"='MODEL_PREPARATION_RESTORE_INVALIDATED',"REVISION"="REVISION"+1,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "STATE" IN ('queued','preparing','running','cancelling')`), `UPDATE model_preparations SET state=CASE WHEN state='queued' THEN 'cancelled' ELSE 'recovery_required' END,error_code='MODEL_PREPARATION_RESTORE_INVALIDATED',revision=revision+1,finished_at=CASE WHEN state='queued' THEN clock_timestamp() ELSE NULL END,updated_at=clock_timestamp() WHERE state IN ('queued','preparing','running','cancelling')`, `UPDATE model_preparations SET finished_at=CASE WHEN state='queued' THEN UTC_TIMESTAMP(6) ELSE NULL END,state=CASE WHEN state='queued' THEN 'cancelled' ELSE 'recovery_required' END,error_code='MODEL_PREPARATION_RESTORE_INVALIDATED',revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE state IN ('queued','preparing','running','cancelling')`),
		preparationSelectedSQL(q, preparationDM(`UPDATE "MODEL_PREPARATION_ATTEMPTS" SET "STATE"='recovery_required',"ERROR_CODE"='MODEL_PREPARATION_RESTORE_INVALIDATED',"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "PHYSICAL_STOPPED_AT" IS NULL AND "STATE" IN ('pending','preparing','running','stopping')`), `UPDATE model_preparation_attempts SET state='recovery_required',error_code='MODEL_PREPARATION_RESTORE_INVALIDATED',updated_at=clock_timestamp() WHERE physical_stopped_at IS NULL AND state IN ('pending','preparing','running','stopping')`, `UPDATE model_preparation_attempts SET state='recovery_required',error_code='MODEL_PREPARATION_RESTORE_INVALIDATED',updated_at=UTC_TIMESTAMP(6) WHERE physical_stopped_at IS NULL AND state IN ('pending','preparing','running','stopping')`),
		preparationSelectedSQL(q, preparationDM(`UPDATE "GPU_LEASES" SET "STATE"='recovery_required',"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "MODEL_PREPARATION_ID" IS NOT NULL AND "STATE"='held'`), `UPDATE gpu_leases SET state='recovery_required',updated_at=clock_timestamp() WHERE model_preparation_id IS NOT NULL AND state='held'`, `UPDATE gpu_leases SET state='recovery_required',updated_at=UTC_TIMESTAMP(6) WHERE model_preparation_id IS NOT NULL AND state='held'`),
	}
	for _, statement := range statements {
		if _, e := q.Exec(ctx, statement); e != nil {
			return e
		}
	}
	return nil
}

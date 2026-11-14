package deployments

import (
	"context"

	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) ObserveInference(ctx context.Context, tx pgx.Tx, n nodes.Node, o wire.InferenceObservation) error {
	if tx == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return s.ObserveInferenceWithin(ctx, transaction.PostgreSQLTransaction(tx), n, o)
}
func (s *Service) RecordInferenceResult(ctx context.Context, tx pgx.Tx, n nodes.Node, t nodes.Task, result wire.Result) error {
	if tx == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return s.RecordInferenceResultWithin(ctx, transaction.PostgreSQLTransaction(tx), n, t, result)
}
func InvalidateAuthorities(ctx context.Context, tx pgx.Tx) error {
	if tx == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return InvalidateAuthoritiesWithin(ctx, transaction.PostgreSQLTransaction(tx))
}

// HasModelReferences exposes deployment-owned references under the shared Workspace transaction lock.
func (s *Service) HasModelReferences(ctx context.Context, q transaction.Executor, tenant, workspace, model string) (bool, error) {
	if s == nil || ctx == nil || q == nil {
		return false, fail("STORAGE_UNAVAILABLE")
	}
	if q.Dialect() == transaction.Dameng {
		if e := s.runtimeExecutor(ctx, q); e != nil {
			return false, e
		}
	}
	statement := "SELECT EXISTS(SELECT 1 FROM deployments WHERE tenant_id=$1 AND workspace_id=$2 AND model_id=$3 AND removed_at IS NULL)"
	if q.Dialect() == transaction.MySQL {
		statement = "SELECT EXISTS(SELECT 1 FROM deployments WHERE tenant_id=? AND workspace_id=? AND model_id=? AND removed_at IS NULL)"
	}
	if q.Dialect() == transaction.Dameng {
		statement = `SELECT CASE WHEN EXISTS(SELECT 1 FROM "DEPLOYMENTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "MODEL_ID"=? AND "REMOVED_AT" IS NULL) THEN 1 ELSE 0 END FROM DUAL`
	}
	var value bool
	e := q.QueryRow(ctx, transaction.SQL(q.Dialect(), statement, tenant, workspace, model)).Scan(&value)
	if e != nil {
		return false, e
	}
	return value, nil
}

// FenceInferenceNode retains external ownership after an audited administrator node removal.
func (s *Service) FenceInferenceNode(ctx context.Context, q transaction.Executor, n nodes.Node) error {
	if ctx == nil || q == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	if q.Dialect() == transaction.Dameng {
		if e := s.runtimeExecutor(ctx, q); e != nil {
			return e
		}
	}
	leaseSQL := "UPDATE gpu_leases SET state='recovery_required',updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND state<>'released'"
	deploymentSQL := "UPDATE deployments SET observed_state='unknown',observation=NULL,observed_at=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND removed_at IS NULL"
	if q.Dialect() == transaction.MySQL {
		leaseSQL = "UPDATE gpu_leases SET state='recovery_required',updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND state<>'released'"
		deploymentSQL = "UPDATE deployments SET observed_state='unknown',observation=NULL,observed_at=NULL,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND removed_at IS NULL"
	}
	if q.Dialect() == transaction.Dameng {
		leaseSQL = `UPDATE "GPU_LEASES" SET "STATE"='recovery_required',"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "STATE"<>'released'`
		deploymentSQL = `UPDATE "DEPLOYMENTS" SET "OBSERVED_STATE"='unknown',"OBSERVATION"=NULL,"OBSERVED_AT"=NULL,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "REMOVED_AT" IS NULL`
	}
	if _, e := q.Exec(ctx, transaction.SQL(q.Dialect(), leaseSQL, n.TenantID, n.WorkspaceID, n.ID)); e != nil {
		return e
	}
	_, e := q.Exec(ctx, transaction.SQL(q.Dialect(), deploymentSQL, n.TenantID, n.WorkspaceID, n.ID))
	return e
}

package recovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"time"
)

func recoverySQL(q transaction.Executor, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, args...)
	}
	return transaction.SQL(transaction.PostgreSQL, pg, args...)
}
func recoveryRuntimeSQL(q transaction.Executor, pg, my string, pgArgs, myArgs []any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, myArgs...)
	}
	return transaction.SQL(transaction.PostgreSQL, pg, pgArgs...)
}
func operatorAuditWithin(ctx context.Context, q transaction.Executor, tenant, workspace, action, target, id, reason string, metadata any) error {
	raw, e := json.Marshal(metadata)
	if e != nil {
		return errors.New("VALIDATION_FAILED")
	}
	affected, e := q.Exec(ctx, recoverySQL(q, `INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES($1,clock_timestamp(),$2,$3,'operator',$4,'success',$5,$6,$7,$8,$9)`, `INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,?,'operator',?,'success',?,?,?,?,CAST(? AS JSON))`, uuid.NewString(), tenant, workspace, action, target, id, reason, uuid.NewString(), raw))
	if e != nil {
		return e
	}
	if affected != 1 {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	return nil
}

func fenceInferenceNode(ctx context.Context, accounts *identity.Service, node, reason string) error {
	store, e := accounts.Repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(accounts.Config.Policy().ModelLifecycle.TransactionSeconds))
	defer cancel()
	parsed, e := uuid.Parse(node)
	if e != nil || parsed.String() != node || !operatorReason(reason) {
		return errors.New("VALIDATION_FAILED")
	}
	var tenant, workspace string
	e = store.QueryRow(ctx, recoverySQL(store, "SELECT n.tenant_id::text,n.workspace_id::text FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND n.id=$1 AND n.execution_profile IN ($2,$3,$4)", `SELECT n.tenant_id,n.workspace_id FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND n.id=? AND n.execution_profile IN (?,?,?)`, node, wire.DockerInferenceProfile, wire.DockerAIProfile, wire.DockerModelLifecycleProfile)).Scan(&tenant, &workspace)
	if errors.Is(e, sql.ErrNoRows) {
		return errors.New("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return e
	}
	return store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var lockedWorkspace string
		if e = tx.QueryRow(ctx, recoverySQL(tx, `SELECT w.id::text FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=$1 AND w.id=$2 FOR NO KEY UPDATE OF w`, `SELECT w.id FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.tenant_id=? AND w.id=? FOR UPDATE`, tenant, workspace)).Scan(&lockedWorkspace); e != nil {
			return e
		}
		var status string
		var fenced *time.Time
		if e = tx.QueryRow(ctx, recoverySQL(tx, "SELECT status,inference_fenced_at FROM managed_nodes WHERE id=$1 FOR UPDATE", `SELECT status,inference_fenced_at FROM managed_nodes WHERE id=? FOR UPDATE`, node)).Scan(&status, &fenced); e != nil {
			return e
		}
		if status == "removed" && fenced != nil {
			return nil
		}
		for _, statement := range []transaction.Statement{recoverySQL(tx, `UPDATE managed_nodes SET status='removed',credential_hash=NULL,inference_fenced_at=clock_timestamp(),version=version+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE managed_nodes SET status='removed',credential_hash=NULL,inference_fenced_at=UTC_TIMESTAMP(6),version=version+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, node),
			recoverySQL(tx, `UPDATE node_tasks SET state='cancelled',lease_token_hash=NULL,lease_expires_at=NULL,error_code='OPERATOR_FENCED',completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE node_id=$1 AND state IN ('pending','running')`, `UPDATE node_tasks SET state='cancelled',lease_token_hash=NULL,lease_expires_at=NULL,error_code='OPERATOR_FENCED',completed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE node_id=? AND state IN ('pending','running')`, node),
			recoverySQL(tx, `UPDATE gpu_leases SET state='recovery_required',updated_at=clock_timestamp() WHERE node_id=$1 AND state<>'released'`, `UPDATE gpu_leases SET state='recovery_required',updated_at=UTC_TIMESTAMP(6) WHERE node_id=? AND state<>'released'`, node),
			recoverySQL(tx, `UPDATE deployments SET observed_state='unknown',observation=NULL,observed_at=NULL,updated_at=clock_timestamp() WHERE node_id=$1 AND removed_at IS NULL`, `UPDATE deployments SET observed_state='unknown',observation=NULL,observed_at=NULL,updated_at=UTC_TIMESTAMP(6) WHERE node_id=? AND removed_at IS NULL`, node)} {
			if _, e = tx.Exec(ctx, statement); e != nil {
				return e
			}
		}
		{
			if e = modelpreparations.InvalidateNodeWithin(ctx, tx, tenant, workspace, node); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, recoverySQL(tx, "UPDATE model_preparations SET state='cancelled',error_code='MODEL_PREPARATION_NODE_UNAVAILABLE',revision=revision+1,finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND current_node_id=$3 AND state='queued'", `UPDATE model_preparations SET state='cancelled',error_code='MODEL_PREPARATION_NODE_UNAVAILABLE',revision=revision+1,finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND current_node_id=? AND state='queued'`, tenant, workspace, node)); e != nil {
				return e
			}
			if e = training.InvalidateNodeWithin(ctx, tx, tenant, workspace, node); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, recoverySQL(tx, "UPDATE training_jobs SET state='cancelled',error_code='TRAINING_NODE_UNAVAILABLE',revision=revision+1,finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND current_node_id=$3 AND state='queued'", `UPDATE training_jobs SET state='cancelled',error_code='TRAINING_NODE_UNAVAILABLE',revision=revision+1,finished_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND current_node_id=? AND state='queued'`, tenant, workspace, node)); e != nil {
				return e
			}
		}

		if e = operatorAuditWithin(ctx, tx, tenant, workspace, "inference.node_fenced", "node", node, reason, map[string]any{"external_process_stopped": false, "gpu_released": false}); e != nil {
			return e
		}
		return nil
	})
}

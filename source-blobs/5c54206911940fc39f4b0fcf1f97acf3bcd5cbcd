package modelpreparations

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// Each caller owns both vendor statements and positional argument lists. This
// selects immutable SQL only; it never translates or rewrites a statement.
func preparationRuntimeSQL(q transaction.Executor, pg, my string, postgresArgs, mysqlArgs []any) transaction.Statement {
	switch q.Dialect() {
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, mysqlArgs...)
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, postgresArgs...)
	}
	return transaction.SQL(q.Dialect(), "")
}

type preparationReconciliationTask struct {
	state     string
	errorCode *string
	attempt   int
	expired   bool
	// unreachable is a running task whose agent control has not heard from within the lease.
	unreachable bool
	command   wire.Command
}

// Control reconciliation borrows the already locked retained node and preparation.
// A changed execution profile must not hide old delivery uncertainty. This read
// grants no machine credential, lease, execution or physical-stop authority.
func retainedPreparationTask(ctx context.Context, q transaction.Executor, n nodes.Node, id string) (preparationReconciliationTask, error) {
	var out preparationReconciliationTask
	var raw []byte
	e := q.QueryRow(ctx, preparationSelectedRuntimeSQL(q,
		preparationDM(`SELECT "STATE","ERROR_CODE","ATTEMPT","PAYLOAD",CASE WHEN "DEADLINE_AT"<=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) OR ("STATE"='running' AND "LEASE_EXPIRES_AT" IS NULL) THEN 1 ELSE 0 END,CASE WHEN "STATE"='running' AND "LEASE_EXPIRES_AT"<=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM "NODE_TASKS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "ID"=? AND "OPERATION"=? FOR UPDATE`),
		`SELECT state,error_code,attempt,payload,deadline_at<=clock_timestamp() OR state='running' AND lease_expires_at IS NULL,state='running' AND COALESCE(lease_expires_at<=clock_timestamp(),false) FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 AND operation=$5 FOR UPDATE`,
		`SELECT state,error_code,attempt,payload,deadline_at<=UTC_TIMESTAMP(6) OR state='running' AND lease_expires_at IS NULL,state='running' AND COALESCE(lease_expires_at<=UTC_TIMESTAMP(6),false) FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND operation=? FOR UPDATE`,
		[]any{n.TenantID, n.WorkspaceID, n.ID, id, wire.RunOperation}, []any{n.TenantID, n.WorkspaceID, n.ID, id, wire.RunOperation})).Scan(&out.state, &out.errorCode, &out.attempt, &raw, &out.expired, &out.unreachable)
	if e != nil {
		return preparationReconciliationTask{}, e
	}
	out.command, e = wire.DecodeCommand(raw)
	if e != nil || out.command.Scope.TenantID != n.TenantID || out.command.Scope.WorkspaceID != n.WorkspaceID || out.attempt < 0 || out.attempt > 1 {
		return preparationReconciliationTask{}, fail("MODEL_PREPARATION_COMMAND_INVALID")
	}
	return out, nil
}

// Retained-file operators need the selected Identity repository even when no
// live Workspace service is attached. They never grant actor/execution authority.
func (s *Service) runtimeRepository() (transaction.Store, error) {
	if s == nil || s.Identity == nil {
		return nil, fail("MODEL_PREPARATION_IDENTITY_REQUIRED")
	}
	if s.DB != nil && s.DB != s.Identity.DB || s.Workspaces != nil && (s.Workspaces.Identity != s.Identity || s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB) {
		return nil, fail("MODEL_PREPARATION_STORAGE_MISMATCH")
	}
	return s.Identity.Repository()
}

func preparationSavepoint(ctx context.Context, q transaction.Executor, operation string) error {
	var statement string
	switch operation {
	case "begin":
		statement = "SAVEPOINT model_preparation_reservation"
	case "rollback":
		statement = "ROLLBACK TO SAVEPOINT model_preparation_reservation"
	case "release":
		statement = "RELEASE SAVEPOINT model_preparation_reservation"
	default:
		return fail("STORAGE_UNAVAILABLE")
	}
	_, e := q.Exec(ctx, transaction.SQL(q.Dialect(), statement))
	return e
}

// The model-preparation owner is looked up before insertion. An upsert must not
// react to a collision on another owner's physical GPU uniqueness index.
func reservePreparationGPU(ctx context.Context, q transaction.Executor, w workspaces.Workspace, n nodes.Node, p Preparation, revision int) (bool, error) {
	var state, gpu string
	e := q.QueryRow(ctx, preparationSelectedSQL(q, preparationDM(`SELECT "STATE","GPU_UUID" FROM "GPU_LEASES" WHERE "MODEL_PREPARATION_ID"=? FOR UPDATE`), `SELECT state,gpu_uuid FROM gpu_leases WHERE model_preparation_id=$1 FOR UPDATE`, `SELECT state,gpu_uuid FROM gpu_leases WHERE model_preparation_id=? FOR UPDATE`, p.ID)).Scan(&state, &gpu)
	if e == nil {
		if state != "released" || gpu != p.GPUUUID {
			return false, nil
		}
		affected, e := q.Exec(ctx, preparationSelectedRuntimeSQL(q, preparationDM(`UPDATE "GPU_LEASES" SET "NODE_ID"=?,"REVISION"=?,"STATE"='held',"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "MODEL_PREPARATION_ID"=? AND "STATE"='released' AND "GPU_UUID"=?`), `UPDATE gpu_leases SET node_id=$2,revision=$3,state='held',updated_at=clock_timestamp() WHERE model_preparation_id=$1 AND state='released' AND gpu_uuid=$4`, `UPDATE gpu_leases SET node_id=?,revision=?,state='held',updated_at=UTC_TIMESTAMP(6) WHERE model_preparation_id=? AND state='released' AND gpu_uuid=?`, []any{p.ID, n.ID, revision, p.GPUUUID}, []any{n.ID, revision, p.ID, p.GPUUUID}))
		return affected == 1, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return false, e
	}
	affected, e := q.Exec(ctx, preparationSelectedSQL(q, preparationDM(`INSERT INTO "GPU_LEASES"("ID","TENANT_ID","WORKSPACE_ID","NODE_ID","GPU_UUID","MODEL_PREPARATION_ID","REVISION","STATE") VALUES(?,?,?,?,?,?,?,'held')`), `INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,model_preparation_id,revision,state) VALUES($1,$2,$3,$4,$5,$6,$7,'held')`, `INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,model_preparation_id,revision,state) VALUES(?,?,?,?,?,?,?,'held')`, uuid.NewString(), w.TenantID, w.ID, n.ID, p.GPUUUID, p.ID, revision))
	return affected == 1, e
}

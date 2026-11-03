package workflows

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

var workflowRestoreTraces = workflowQuery{dameng: `UPDATE workflow_node_traces n SET state='interrupted',error_code='WORKFLOW_RUN_INTERRUPTED',finished_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE n.state='running' AND EXISTS(SELECT 1 FROM workflow_runs r WHERE r.id=n.run_id AND r.tenant_id=n.tenant_id AND r.workspace_id=n.workspace_id AND r.workflow_id=n.workflow_id AND r.state IN ('pending','running'))`,
	postgres: `UPDATE workflow_node_traces n SET state='interrupted',error_code='WORKFLOW_RUN_INTERRUPTED',finished_at=clock_timestamp() FROM workflow_runs r WHERE r.id=n.run_id AND r.state IN ('pending','running') AND n.state='running'`,
	mysql:    `UPDATE workflow_node_traces n JOIN workflow_runs r ON r.id=n.run_id AND r.tenant_id=n.tenant_id AND r.workspace_id=n.workspace_id AND r.workflow_id=n.workflow_id SET n.state='interrupted',n.error_code='WORKFLOW_RUN_INTERRUPTED',n.finished_at=UTC_TIMESTAMP(6) WHERE r.state IN('pending','running') AND n.state='running'`,
}
var workflowRestoreRuns = workflowQuery{dameng: `UPDATE workflow_runs SET state='interrupted',error_code='WORKFLOW_RUN_INTERRUPTED',answer=NULL,evidence=NULL,alias_snapshot=NULL,"USAGE"=NULL,tool_result=NULL,tool_proof=NULL,process_owner=NULL,lease_id=NULL,lease_expires_at=NULL,finished_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE state IN ('pending','running')`,
	postgres: `UPDATE workflow_runs SET state='interrupted',error_code='WORKFLOW_RUN_INTERRUPTED',answer=NULL,evidence=NULL,alias_snapshot=NULL,usage=NULL,tool_result=NULL,tool_proof=NULL,process_owner=NULL,lease_id=NULL,lease_expires_at=NULL,finished_at=clock_timestamp() WHERE state IN ('pending','running')`,
	mysql:    `UPDATE workflow_runs SET state='interrupted',error_code='WORKFLOW_RUN_INTERRUPTED',answer=NULL,evidence=NULL,alias_snapshot=NULL,` + "`usage`" + `=NULL,tool_result=NULL,tool_proof=NULL,process_owner=NULL,lease_id=NULL,lease_expires_at=NULL,finished_at=UTC_TIMESTAMP(6) WHERE state IN('pending','running')`,
}
var workflowRestoreNext = workflowQuery{dameng: `SELECT id FROM workflows WHERE (? IS NULL OR id>?) ORDER BY id LIMIT 1 FOR UPDATE`, damengOrder: []int{1, 1},
	postgres: `SELECT id::text FROM workflows WHERE ($1::uuid IS NULL OR id>$1::uuid) ORDER BY id LIMIT 1 FOR UPDATE`,
	mysql:    `SELECT id FROM workflows WHERE (? IS NULL OR id>?) ORDER BY id LIMIT 1 FOR UPDATE`, mysqlOrder: []int{1, 1},
}
var workflowRestoreAuthority = workflowQuery{dameng: `UPDATE workflows SET execution_authority=? WHERE id=?`, damengOrder: []int{2, 1}, postgres: `UPDATE workflows SET execution_authority=$2 WHERE id=$1`, mysql: `UPDATE workflows SET execution_authority=? WHERE id=?`, mysqlOrder: []int{2, 1}}

// InvalidateAuthoritiesWithin borrows the existing exclusive offline restore
// transaction. It cannot resume a restored run or commit independently.
func InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	if _, e := q.Exec(ctx, workflowStatement(q, workflowRestoreTraces)); e != nil {
		return e
	}
	if _, e := q.Exec(ctx, workflowStatement(q, workflowRestoreRuns)); e != nil {
		return e
	}
	// One scanned key and one freshly generated UUID per row keep memory bounded
	// and never reuse a single statement argument across all workflow authorities.
	var after any
	for {
		var id string
		e := q.QueryRow(ctx, workflowStatement(q, workflowRestoreNext, after)).Scan(&id)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		changed, e := q.Exec(ctx, workflowStatement(q, workflowRestoreAuthority, id, uuid.NewString()))
		if e != nil {
			return e
		}
		if changed != 1 {
			return failure("WORKFLOW_RUN_STALE")
		}
		after = id
	}
}

// Original PG operator boundary remains an actual borrowed transaction wrapper.
func InvalidateAuthorities(ctx context.Context, tx pgx.Tx) error {
	return InvalidateAuthoritiesWithin(ctx, transaction.PostgreSQLTransaction(tx))
}

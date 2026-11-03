package workflows

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// knowledgeExecutor binds the composed account19 owner to its original schema1
// SQL. It borrows the same transaction; it cannot commit or open another pool.
// Future platform/plugin columns are deliberately absent from this catalog.
type knowledgeExecutor struct{ transaction.Executor }

func (s *Service) selectedExecutor(q transaction.Executor) transaction.Executor {
	if s.Identity.Config.WorkflowsOnly() {
		return knowledgeExecutor{q}
	}
	return q
}

const knowledgeRunFields = `r.id::text,r.workflow_id::text,r.conversation_id::text,r.version_id::text,r.draft_revision,r.graph_sha256,r.state,r.question,r.answer,r.error_code,r.cancellation_requested_at,r.created_at,r.started_at,r.finished_at,r.deadline_at,r.lease_expires_at,r.owner_id::text,r.requested_audience,r.execution_authority::text,r.membership_authority::text,r.graph_snapshot,r.evidence,r.alias_snapshot,r.usage,r.process_owner::text,r.lease_id::text,'answer'::text,NULL::jsonb,NULL::jsonb`

// Explicit schema1 statements preserve the historical platform statements and
// scanners. This is selection of authored SQL, never text rewriting at runtime.
func knowledgeStatement(query workflowQuery, args ...any) transaction.Statement {
	sql := query.postgres
	switch sql {
	case workflowRunVisible.postgres:
		sql = "SELECT " + knowledgeRunFields + " FROM workflow_runs r WHERE r.tenant_id=$1 AND r.workspace_id=$2 AND r.workflow_id=$3 AND r.owner_id=$4 AND r.id=$5 AND " + visibleRunConversation
	case workflowRunLock.postgres:
		sql = "SELECT " + knowledgeRunFields + " FROM workflow_runs r WHERE r.tenant_id=$1 AND r.workspace_id=$2 AND r.workflow_id=$3 AND r.owner_id=$4 AND r.id=$5 FOR UPDATE OF r"
	case workflowRunByID.postgres:
		sql = "SELECT " + knowledgeRunFields + " FROM workflow_runs r WHERE r.id=$1"
	case workflowRunCreate.postgres:
		if len(args) != 17 || args[16] != "answer" {
			return transaction.SQL(transaction.PostgreSQL, "")
		}
		args = args[:16]
		sql = `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,conversation_id,turn_number,version_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'pending',$15,clock_timestamp()+make_interval(secs=>$16))`
	case workflowRunFailure.postgres:
		sql = `UPDATE workflow_runs SET state=$2,error_code=$3,answer=NULL,evidence=NULL,alias_snapshot=NULL,usage=NULL,process_owner=NULL,lease_id=NULL,lease_expires_at=NULL,finished_at=clock_timestamp() WHERE id=$1`
	case workflowRunInterrupt.postgres:
		sql = `UPDATE workflow_runs SET state='interrupted',error_code='WORKFLOW_RUN_INTERRUPTED',answer=NULL,evidence=NULL,alias_snapshot=NULL,usage=NULL,process_owner=NULL,lease_id=NULL,lease_expires_at=NULL,finished_at=clock_timestamp() WHERE id=$1`
	case workflowRestoreRuns.postgres:
		sql = `UPDATE workflow_runs SET state='interrupted',error_code='WORKFLOW_RUN_INTERRUPTED',answer=NULL,evidence=NULL,alias_snapshot=NULL,usage=NULL,process_owner=NULL,lease_id=NULL,lease_expires_at=NULL,finished_at=clock_timestamp() WHERE state IN ('pending','running')`
	case workflowRunToolSuccess.postgres:
		return transaction.SQL(transaction.PostgreSQL, "")
	}
	return transaction.SQL(transaction.PostgreSQL, sql, args...)
}

// InvalidateKnowledgeAuthoritiesWithin is the current composed restore leaf.
// All preceding owners and this reduction retain one commit/rollback outcome.
func InvalidateKnowledgeAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	if q == nil || q.Dialect() != transaction.PostgreSQL {
		return failure("WORKFLOW_STORAGE_MISMATCH")
	}
	if _, pool := q.(transaction.Store); pool {
		return failure("WORKFLOW_TRANSACTION_REQUIRED")
	}
	return InvalidateAuthoritiesWithin(ctx, knowledgeExecutor{q})
}

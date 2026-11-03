package workflows

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type RetentionResult struct {
	Runs          int64 `json:"runs"`
	Conversations int64 `json:"conversations"`
	Intents       int64 `json:"intents"`
}

var workflowRetentionWorkspace = workflowQuery{dameng: `SELECT w.tenant_id,w.id FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id AND i.id=1 AND i.bootstrap_complete=1 WHERE EXISTS(SELECT 1 FROM workflows f WHERE f.tenant_id=w.tenant_id AND f.workspace_id=w.id) AND (? IS NULL OR w.id>?) ORDER BY w.id LIMIT 1`, damengOrder: []int{1, 1},
	postgres: `SELECT w.tenant_id::text,w.id::text FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id AND i.id=1 AND i.bootstrap_complete WHERE EXISTS(SELECT 1 FROM workflows f WHERE f.tenant_id=w.tenant_id AND f.workspace_id=w.id) AND ($1::uuid IS NULL OR w.id>$1::uuid) ORDER BY w.id LIMIT 1`,
	mysql:    `SELECT w.tenant_id,w.id FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id AND i.id=1 AND i.bootstrap_complete WHERE EXISTS(SELECT 1 FROM workflows f WHERE f.tenant_id=w.tenant_id AND f.workspace_id=w.id) AND (? IS NULL OR w.id>?) ORDER BY w.id LIMIT 1`, mysqlOrder: []int{1, 1},
}
var workflowRetentionRuns = workflowQuery{dameng: `SELECT r.id FROM workflow_runs r WHERE r.tenant_id=? AND r.workspace_id=? AND r.state NOT IN('pending','running') AND (r.finished_at<TIMESTAMPADD(SQL_TSI_DAY,0-?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) OR EXISTS(SELECT 1 FROM workflow_conversations c WHERE c.tenant_id=r.tenant_id AND c.workspace_id=r.workspace_id AND c.workflow_id=r.workflow_id AND c.id=r.conversation_id AND c.deleted_at IS NOT NULL)) ORDER BY r.finished_at,r.id`, damengLimit: 4, damengSuffix: ` FOR UPDATE SKIP LOCKED`,
	postgres: `SELECT r.id::text FROM workflow_runs r WHERE r.tenant_id=$1 AND r.workspace_id=$2 AND r.state NOT IN('pending','running') AND (r.finished_at<clock_timestamp()-make_interval(days=>$3) OR EXISTS(SELECT 1 FROM workflow_conversations c WHERE c.tenant_id=r.tenant_id AND c.workspace_id=r.workspace_id AND c.workflow_id=r.workflow_id AND c.id=r.conversation_id AND c.deleted_at IS NOT NULL)) ORDER BY r.finished_at,r.id LIMIT $4 FOR UPDATE SKIP LOCKED`,
	mysql:    `SELECT r.id FROM workflow_runs r WHERE r.tenant_id=? AND r.workspace_id=? AND r.state NOT IN('pending','running') AND (r.finished_at<TIMESTAMPADD(DAY,0-?,UTC_TIMESTAMP(6)) OR EXISTS(SELECT 1 FROM workflow_conversations c WHERE c.tenant_id=r.tenant_id AND c.workspace_id=r.workspace_id AND c.workflow_id=r.workflow_id AND c.id=r.conversation_id AND c.deleted_at IS NOT NULL)) ORDER BY r.finished_at,r.id LIMIT ? FOR UPDATE SKIP LOCKED`,
}
var workflowRetentionConversations = workflowQuery{dameng: `SELECT c.id FROM workflow_conversations c WHERE c.tenant_id=? AND c.workspace_id=? AND c.deleted_at IS NOT NULL AND NOT EXISTS(SELECT 1 FROM workflow_runs r WHERE r.tenant_id=c.tenant_id AND r.workspace_id=c.workspace_id AND r.conversation_id=c.id) ORDER BY c.deleted_at,c.id`, damengLimit: 3, damengSuffix: ` FOR UPDATE SKIP LOCKED`,
	postgres: `SELECT c.id::text FROM workflow_conversations c WHERE c.tenant_id=$1 AND c.workspace_id=$2 AND c.deleted_at IS NOT NULL AND NOT EXISTS(SELECT 1 FROM workflow_runs r WHERE r.tenant_id=c.tenant_id AND r.workspace_id=c.workspace_id AND r.conversation_id=c.id) ORDER BY c.deleted_at,c.id LIMIT $3 FOR UPDATE SKIP LOCKED`,
	mysql:    `SELECT c.id FROM workflow_conversations c WHERE c.tenant_id=? AND c.workspace_id=? AND c.deleted_at IS NOT NULL AND NOT EXISTS(SELECT 1 FROM workflow_runs r WHERE r.tenant_id=c.tenant_id AND r.workspace_id=c.workspace_id AND r.conversation_id=c.id) ORDER BY c.deleted_at,c.id LIMIT ? FOR UPDATE SKIP LOCKED`,
}
var workflowRetentionIntents = workflowQuery{dameng: `SELECT i.actor_id,i.idempotency_key FROM workflow_mutation_intents i WHERE i.tenant_id=? AND i.workspace_id=? AND i.purpose<>'create_workflow' AND i.created_at<TIMESTAMPADD(SQL_TSI_SECOND,0-?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) AND NOT EXISTS(SELECT 1 FROM workflow_runs r WHERE r.tenant_id=i.tenant_id AND r.workspace_id=i.workspace_id AND r.id=i.entity_id AND r.state IN('pending','running')) ORDER BY i.created_at,i.idempotency_key,i.actor_id`, damengLimit: 4, damengSuffix: ` FOR UPDATE SKIP LOCKED`,
	postgres: `SELECT i.actor_id::text,i.idempotency_key::text FROM workflow_mutation_intents i WHERE i.tenant_id=$1 AND i.workspace_id=$2 AND i.purpose<>'create_workflow' AND i.created_at<clock_timestamp()-make_interval(secs=>$3) AND NOT EXISTS(SELECT 1 FROM workflow_runs r WHERE r.tenant_id=i.tenant_id AND r.workspace_id=i.workspace_id AND r.id=i.entity_id AND r.state IN('pending','running')) ORDER BY i.created_at,i.idempotency_key,i.actor_id LIMIT $4 FOR UPDATE SKIP LOCKED`,
	mysql:    `SELECT i.actor_id,i.idempotency_key FROM workflow_mutation_intents i WHERE i.tenant_id=? AND i.workspace_id=? AND i.purpose<>'create_workflow' AND i.created_at<TIMESTAMPADD(SECOND,0-?,UTC_TIMESTAMP(6)) AND NOT EXISTS(SELECT 1 FROM workflow_runs r WHERE r.tenant_id=i.tenant_id AND r.workspace_id=i.workspace_id AND r.id=i.entity_id AND r.state IN('pending','running')) ORDER BY i.created_at,i.idempotency_key,i.actor_id LIMIT ? FOR UPDATE SKIP LOCKED`,
}
var workflowDeleteRun = workflowQuery{dameng: `DELETE FROM workflow_runs WHERE tenant_id=? AND workspace_id=? AND id=? AND state NOT IN('pending','running')`, postgres: `DELETE FROM workflow_runs WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND state NOT IN('pending','running')`, mysql: `DELETE FROM workflow_runs WHERE tenant_id=? AND workspace_id=? AND id=? AND state NOT IN('pending','running')`}
var workflowDeleteConversation = workflowQuery{dameng: `DELETE FROM workflow_conversations WHERE tenant_id=? AND workspace_id=? AND id=? AND deleted_at IS NOT NULL`, postgres: `DELETE FROM workflow_conversations WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND deleted_at IS NOT NULL`, mysql: `DELETE FROM workflow_conversations WHERE tenant_id=? AND workspace_id=? AND id=? AND deleted_at IS NOT NULL`}
var workflowDeleteIntent = workflowQuery{dameng: `DELETE FROM workflow_mutation_intents WHERE tenant_id=? AND workspace_id=? AND actor_id=? AND idempotency_key=?`, postgres: `DELETE FROM workflow_mutation_intents WHERE tenant_id=$1 AND workspace_id=$2 AND actor_id=$3 AND idempotency_key=$4`, mysql: `DELETE FROM workflow_mutation_intents WHERE tenant_id=? AND workspace_id=? AND actor_id=? AND idempotency_key=?`}
var workflowRetentionAudit = workflowQuery{dameng: `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),NULL,?,?,'admin','workflow.retention_completed','success','workspace',?,'Bounded terminal workflow retention applied',?)`, damengOrder: []int{1, 2, 3, 3, 4},
	postgres: `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),NULL,$2,$3,'admin','workflow.retention_completed','success','workspace',$3,'Bounded terminal workflow retention applied',$4)`,
	mysql:    `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),NULL,?,?,'admin','workflow.retention_completed','success','workspace',?,'Bounded terminal workflow retention applied',?)`, mysqlOrder: []int{1, 2, 3, 3, 4},
}

func workflowRetentionIDs(ctx context.Context, q transaction.Executor, query workflowQuery, args ...any) ([]string, error) {
	rows, e := q.Query(ctx, workflowStatement(q, query, args...))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Cleanup visits one installed Workspace, locks exact bounded eligible keys and
// audits their scoped deletion in the same selected transaction. Active work and
// the permanent create-workflow intent remain retained.
func (s *Service) Cleanup(ctx context.Context) (RetentionResult, error) {
	s.maintenanceMu.Lock()
	defer s.maintenanceMu.Unlock()
	store, e := s.repository()
	if e != nil {
		return RetentionResult{}, workflowRepositoryError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	var tenant, workspace string
	var after any
	if s.maintenanceAfter != "" {
		after = s.maintenanceAfter
	}
	e = store.QueryRow(ctx, workflowStatement(store, workflowRetentionWorkspace, after)).Scan(&tenant, &workspace)
	if errors.Is(e, sql.ErrNoRows) && after != nil {
		e = store.QueryRow(ctx, workflowStatement(store, workflowRetentionWorkspace, nil)).Scan(&tenant, &workspace)
	}
	if errors.Is(e, sql.ErrNoRows) {
		s.maintenanceAfter = ""
		return RetentionResult{}, nil
	}
	if e != nil {
		return RetentionResult{}, workflowRepositoryError(e)
	}
	var out RetentionResult
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var locked string
		if e := q.QueryRow(ctx, workflowStatement(q, workflowLockWorkspace, tenant, workspace)).Scan(&locked); e != nil {
			return e
		}
		p := s.policy()
		runs, e := workflowRetentionIDs(ctx, q, workflowRetentionRuns, tenant, workspace, p.RetentionDays, p.MaintenanceBatch)
		if e != nil {
			return e
		}
		for _, id := range runs {
			n, e := q.Exec(ctx, workflowStatement(q, workflowDeleteRun, tenant, workspace, id))
			if e != nil {
				return e
			}
			if n != 1 {
				return failure("WORKFLOW_RUN_STALE")
			}
			out.Runs += n
		}
		conversations, e := workflowRetentionIDs(ctx, q, workflowRetentionConversations, tenant, workspace, p.MaintenanceBatch)
		if e != nil {
			return e
		}
		for _, id := range conversations {
			n, e := q.Exec(ctx, workflowStatement(q, workflowDeleteConversation, tenant, workspace, id))
			if e != nil {
				return e
			}
			if n != 1 {
				return failure("WORKFLOW_RUN_STALE")
			}
			out.Conversations += n
		}
		rows, e := q.Query(ctx, workflowStatement(q, workflowRetentionIntents, tenant, workspace, s.Identity.Config.Policy().Identity.IdempotencySeconds, p.MaintenanceBatch))
		if e != nil {
			return e
		}
		type intentKey struct{ actor, key string }
		var keys []intentKey
		for rows.Next() {
			var key intentKey
			if e = rows.Scan(&key.actor, &key.key); e != nil {
				rows.Close()
				return e
			}
			keys = append(keys, key)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, key := range keys {
			n, e := q.Exec(ctx, workflowStatement(q, workflowDeleteIntent, tenant, workspace, key.actor, key.key))
			if e != nil {
				return e
			}
			if n != 1 {
				return failure("WORKFLOW_RUN_STALE")
			}
			out.Intents += n
		}
		if out.Runs+out.Conversations+out.Intents > 0 {
			e = workflowExecOne(ctx, q, workflowStatement(q, workflowRetentionAudit, uuid.NewString(), tenant, workspace, uuid.NewString()))
			return e
		}
		return nil
	})
	if e != nil {
		return RetentionResult{}, workflowRepositoryError(e)
	}
	s.maintenanceAfter = workspace
	return out, nil
}

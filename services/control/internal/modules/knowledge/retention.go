package knowledge

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type RetentionResult struct{ Chunks, Attempts, Sources, Documents, Generations, Intents int64 }

// Cleanup owns one row-bounded base batch and preserves every live reference.
func (s *Service) Cleanup(ctx context.Context) (RetentionResult, error) {
	s.maintenanceMu.Lock()
	defer s.maintenanceMu.Unlock()
	repo, e := s.repository()
	if e != nil {
		return RetentionResult{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	var out RetentionResult
	var tenant, workspace, base string
	var after any
	if s.maintenanceAfter != "" {
		after = s.maintenanceAfter
	}
	query := func(after any) error {
		return repo.QueryRow(ctx, knowledgeSQL(repo, `SELECT b.tenant_id::text,b.workspace_id::text,b.id::text FROM knowledge_bases b JOIN installation_state i ON i.tenant_id=b.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND($1::uuid IS NULL OR b.id>$1::uuid) ORDER BY b.id LIMIT 1`, `SELECT b.tenant_id,b.workspace_id,b.id FROM knowledge_bases b JOIN installation_state i ON i.tenant_id=b.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND b.initialization_state='ready' AND(? IS NULL OR b.id>?) ORDER BY b.id LIMIT 1`, []any{after}, []any{after, after})).Scan(&tenant, &workspace, &base)
	}
	e = query(after)
	if errors.Is(e, pgx.ErrNoRows) && s.maintenanceAfter != "" {
		s.maintenanceAfter = ""
		e = query(nil)
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return RetentionResult{}, knowledgeError(e)
	}
	s.maintenanceAfter = base
	e = repo.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var locked string
		if e = tx.QueryRow(ctx, knowledgeSQL(tx, "SELECT id::text FROM workspaces WHERE tenant_id=$1 AND id=$2 FOR NO KEY UPDATE", "SELECT id FROM workspaces WHERE tenant_id=? AND id=? FOR UPDATE", []any{tenant, workspace}, []any{tenant, workspace})).Scan(&locked); e != nil {
			return e
		}
		if e = tx.QueryRow(ctx, knowledgeSQL(tx, "SELECT id::text FROM knowledge_bases WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", "SELECT id FROM knowledge_bases WHERE tenant_id=? AND workspace_id=? AND id=? AND initialization_state='ready' FOR UPDATE", []any{tenant, workspace, base}, []any{tenant, workspace, base})).Scan(&locked); e != nil {
			return e
		}
		policy := s.policy()
		// Tombstones lose their private current-attempt reference only after the configured window.
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_documents SET current_attempt_id=NULL WHERE id IN(SELECT id FROM knowledge_documents WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND deleted_at<clock_timestamp()-make_interval(days=>$4) AND current_attempt_id IS NOT NULL ORDER BY deleted_at,id LIMIT $5)`, `UPDATE knowledge_documents SET current_attempt_id=NULL WHERE id IN(SELECT id FROM (SELECT id FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND deleted_at<TIMESTAMPADD(DAY,-?,CURRENT_TIMESTAMP(6)) AND current_attempt_id IS NOT NULL ORDER BY deleted_at,id LIMIT ?) AS owned_retention)`, []any{tenant, workspace, base, policy.RetentionDays, policy.CleanupBatch}, []any{tenant, workspace, base, policy.RetentionDays, policy.CleanupBatch})); e != nil {
			return e
		}
		eligible := `((g.state IN ('failed','retired') AND g.finished_at<clock_timestamp()-make_interval(days=>$4) AND g.id<>b.active_generation_id AND g.id IS DISTINCT FROM b.building_generation_id) OR(v.superseded_at<clock_timestamp()-make_interval(days=>$4) AND v.id IS DISTINCT FROM d.visible_source_id AND v.id IS DISTINCT FROM d.pending_source_id AND(d.deleted_at IS NULL OR d.deleted_at<clock_timestamp()-make_interval(days=>$4))))`
		result, e := tx.Exec(ctx, knowledgeSQL(tx, `DELETE FROM knowledge_chunks WHERE ctid IN(SELECT c.ctid FROM knowledge_chunks c JOIN knowledge_attempts a ON a.id=c.attempt_id JOIN knowledge_generations g ON g.id=c.generation_id JOIN knowledge_bases b ON b.id=c.base_id JOIN knowledge_documents d ON d.id=c.document_id JOIN knowledge_source_versions v ON v.id=c.source_id WHERE c.tenant_id=$1 AND c.workspace_id=$2 AND c.base_id=$3 AND a.state NOT IN ('pending','running') AND `+eligible+` ORDER BY c.attempt_id,c.ordinal LIMIT $5)`, `DELETE FROM knowledge_chunks WHERE (attempt_id,ordinal) IN(SELECT attempt_id,ordinal FROM (SELECT c.attempt_id,c.ordinal FROM knowledge_chunks c JOIN knowledge_attempts a ON a.id=c.attempt_id JOIN knowledge_generations g ON g.id=c.generation_id JOIN knowledge_bases b ON b.id=c.base_id JOIN knowledge_documents d ON d.id=c.document_id JOIN knowledge_source_versions v ON v.id=c.source_id WHERE c.tenant_id=? AND c.workspace_id=? AND c.base_id=? AND a.state NOT IN ('pending','running') AND ((g.state IN ('failed','retired') AND g.finished_at<TIMESTAMPADD(DAY,-?,CURRENT_TIMESTAMP(6)) AND g.id<>b.active_generation_id AND NOT (g.id <=> b.building_generation_id)) OR(v.superseded_at<TIMESTAMPADD(DAY,-?,CURRENT_TIMESTAMP(6)) AND NOT (v.id <=> d.visible_source_id) AND NOT (v.id <=> d.pending_source_id) AND(d.deleted_at IS NULL OR d.deleted_at<TIMESTAMPADD(DAY,-?,CURRENT_TIMESTAMP(6))))) ORDER BY c.attempt_id,c.ordinal LIMIT ?) AS owned_retention)`, []any{tenant, workspace, base, policy.RetentionDays, policy.CleanupBatch}, []any{tenant, workspace, base, policy.RetentionDays, policy.RetentionDays, policy.RetentionDays, policy.CleanupBatch}))
		if e != nil {
			return e
		}
		out.Chunks = result
		result, e = tx.Exec(ctx, knowledgeSQL(tx, `DELETE FROM knowledge_attempts WHERE id IN(SELECT a.id FROM knowledge_attempts a JOIN knowledge_generations g ON g.id=a.generation_id JOIN knowledge_bases b ON b.id=a.base_id JOIN knowledge_documents d ON d.id=a.document_id JOIN knowledge_source_versions v ON v.id=a.source_id WHERE a.tenant_id=$1 AND a.workspace_id=$2 AND a.base_id=$3 AND a.state NOT IN ('pending','running') AND a.id IS DISTINCT FROM d.current_attempt_id AND NOT EXISTS(SELECT 1 FROM knowledge_chunks c WHERE c.attempt_id=a.id) AND `+eligible+` ORDER BY a.created_at,a.id LIMIT $5)`, `DELETE FROM knowledge_attempts WHERE id IN(SELECT id FROM (SELECT a.id FROM knowledge_attempts a JOIN knowledge_generations g ON g.id=a.generation_id JOIN knowledge_bases b ON b.id=a.base_id JOIN knowledge_documents d ON d.id=a.document_id JOIN knowledge_source_versions v ON v.id=a.source_id WHERE a.tenant_id=? AND a.workspace_id=? AND a.base_id=? AND a.state NOT IN ('pending','running') AND NOT (a.id <=> d.current_attempt_id) AND NOT EXISTS(SELECT 1 FROM knowledge_chunks c WHERE c.attempt_id=a.id) AND ((g.state IN ('failed','retired') AND g.finished_at<TIMESTAMPADD(DAY,-?,CURRENT_TIMESTAMP(6)) AND g.id<>b.active_generation_id AND NOT (g.id <=> b.building_generation_id)) OR(v.superseded_at<TIMESTAMPADD(DAY,-?,CURRENT_TIMESTAMP(6)) AND NOT (v.id <=> d.visible_source_id) AND NOT (v.id <=> d.pending_source_id) AND(d.deleted_at IS NULL OR d.deleted_at<TIMESTAMPADD(DAY,-?,CURRENT_TIMESTAMP(6))))) ORDER BY a.created_at,a.id LIMIT ?) AS owned_retention)`, []any{tenant, workspace, base, policy.RetentionDays, policy.CleanupBatch}, []any{tenant, workspace, base, policy.RetentionDays, policy.RetentionDays, policy.RetentionDays, policy.CleanupBatch}))
		if e != nil {
			return e
		}
		out.Attempts = result
		result, e = tx.Exec(ctx, knowledgeSQL(tx, `DELETE FROM knowledge_source_versions WHERE id IN(SELECT v.id FROM knowledge_source_versions v JOIN knowledge_documents d ON d.id=v.document_id WHERE v.tenant_id=$1 AND v.workspace_id=$2 AND v.base_id=$3 AND v.superseded_at<clock_timestamp()-make_interval(days=>$4) AND v.id IS DISTINCT FROM d.visible_source_id AND v.id IS DISTINCT FROM d.pending_source_id AND(d.deleted_at IS NULL OR d.deleted_at<clock_timestamp()-make_interval(days=>$4)) AND NOT EXISTS(SELECT 1 FROM knowledge_attempts a WHERE a.source_id=v.id) ORDER BY v.created_at,v.id LIMIT $5)`, `DELETE FROM knowledge_source_versions WHERE id IN(SELECT id FROM (SELECT v.id FROM knowledge_source_versions v JOIN knowledge_documents d ON d.id=v.document_id WHERE v.tenant_id=? AND v.workspace_id=? AND v.base_id=? AND v.superseded_at<TIMESTAMPADD(DAY,-?,CURRENT_TIMESTAMP(6)) AND NOT (v.id <=> d.visible_source_id) AND NOT (v.id <=> d.pending_source_id) AND(d.deleted_at IS NULL OR d.deleted_at<TIMESTAMPADD(DAY,-?,CURRENT_TIMESTAMP(6))) AND NOT EXISTS(SELECT 1 FROM knowledge_attempts a WHERE a.source_id=v.id) ORDER BY v.created_at,v.id LIMIT ?) AS owned_retention)`, []any{tenant, workspace, base, policy.RetentionDays, policy.CleanupBatch}, []any{tenant, workspace, base, policy.RetentionDays, policy.RetentionDays, policy.CleanupBatch}))
		if e != nil {
			return e
		}
		out.Sources = result
		result, e = tx.Exec(ctx, knowledgeSQL(tx, `DELETE FROM knowledge_documents WHERE id IN(SELECT d.id FROM knowledge_documents d WHERE d.tenant_id=$1 AND d.workspace_id=$2 AND d.base_id=$3 AND d.deleted_at<clock_timestamp()-make_interval(days=>$4) AND d.current_attempt_id IS NULL AND NOT EXISTS(SELECT 1 FROM knowledge_source_versions v WHERE v.document_id=d.id) AND NOT EXISTS(SELECT 1 FROM knowledge_attempts a WHERE a.document_id=d.id) ORDER BY d.deleted_at,d.id LIMIT $5)`, `DELETE FROM knowledge_documents WHERE id IN(SELECT id FROM (SELECT d.id FROM knowledge_documents d WHERE d.tenant_id=? AND d.workspace_id=? AND d.base_id=? AND d.deleted_at<TIMESTAMPADD(DAY,-?,CURRENT_TIMESTAMP(6)) AND d.current_attempt_id IS NULL AND NOT EXISTS(SELECT 1 FROM knowledge_source_versions v WHERE v.document_id=d.id) AND NOT EXISTS(SELECT 1 FROM knowledge_attempts a WHERE a.document_id=d.id) ORDER BY d.deleted_at,d.id LIMIT ?) AS owned_retention)`, []any{tenant, workspace, base, policy.RetentionDays, policy.CleanupBatch}, []any{tenant, workspace, base, policy.RetentionDays, policy.CleanupBatch}))
		if e != nil {
			return e
		}
		out.Documents = result
		result, e = tx.Exec(ctx, knowledgeSQL(tx, `DELETE FROM knowledge_generations WHERE id IN(SELECT g.id FROM knowledge_generations g JOIN knowledge_bases b ON b.id=g.base_id WHERE g.tenant_id=$1 AND g.workspace_id=$2 AND g.base_id=$3 AND g.state IN ('failed','retired') AND g.finished_at<clock_timestamp()-make_interval(days=>$4) AND g.id<>b.active_generation_id AND g.id IS DISTINCT FROM b.building_generation_id AND NOT EXISTS(SELECT 1 FROM knowledge_attempts a WHERE a.generation_id=g.id) ORDER BY g.finished_at,g.id LIMIT $5)`, `DELETE FROM knowledge_generations WHERE id IN(SELECT id FROM (SELECT g.id FROM knowledge_generations g JOIN knowledge_bases b ON b.id=g.base_id WHERE g.tenant_id=? AND g.workspace_id=? AND g.base_id=? AND g.state IN ('failed','retired') AND g.finished_at<TIMESTAMPADD(DAY,-?,CURRENT_TIMESTAMP(6)) AND g.id<>b.active_generation_id AND NOT (g.id <=> b.building_generation_id) AND NOT EXISTS(SELECT 1 FROM knowledge_attempts a WHERE a.generation_id=g.id) ORDER BY g.finished_at,g.id LIMIT ?) AS owned_retention)`, []any{tenant, workspace, base, policy.RetentionDays, policy.CleanupBatch}, []any{tenant, workspace, base, policy.RetentionDays, policy.CleanupBatch}))
		if e != nil {
			return e
		}
		out.Generations = result
		result, e = tx.Exec(ctx, knowledgeSQL(tx, `DELETE FROM knowledge_mutation_intents WHERE (tenant_id,workspace_id,actor_id,idempotency_key) IN(SELECT tenant_id,workspace_id,actor_id,idempotency_key FROM knowledge_mutation_intents WHERE tenant_id=$1 AND workspace_id=$2 AND purpose<>'create_base' AND created_at<clock_timestamp()-make_interval(secs=>$3) ORDER BY created_at,idempotency_key LIMIT $4)`, `DELETE FROM knowledge_mutation_intents WHERE (tenant_id,workspace_id,actor_id,idempotency_key) IN(SELECT tenant_id,workspace_id,actor_id,idempotency_key FROM (SELECT tenant_id,workspace_id,actor_id,idempotency_key FROM knowledge_mutation_intents WHERE tenant_id=? AND workspace_id=? AND purpose<>'create_base' AND created_at<TIMESTAMPADD(SECOND,-?,CURRENT_TIMESTAMP(6)) ORDER BY created_at,idempotency_key LIMIT ?) AS owned_retention)`, []any{tenant, workspace, s.Identity.Config.Policy().Identity.IdempotencySeconds, policy.CleanupBatch}, []any{tenant, workspace, s.Identity.Config.Policy().Identity.IdempotencySeconds, policy.CleanupBatch}))
		if e != nil {
			return e
		}
		out.Intents = result
		if out.Chunks+out.Attempts+out.Sources+out.Documents+out.Generations+out.Intents > 0 {
			if _, e = tx.Exec(ctx, knowledgeSQL(tx, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),NULL,$2,$3,'admin','knowledge.retention_completed','success','knowledge_base',$4,'Bounded retired knowledge retention applied',$5)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,CURRENT_TIMESTAMP(6),NULL,?,?,'admin','knowledge.retention_completed','success','knowledge_base',?,'Bounded retired knowledge retention applied',?)`, []any{uuid.NewString(), tenant, workspace, base, uuid.NewString()}, []any{uuid.NewString(), tenant, workspace, base, uuid.NewString()})); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return RetentionResult{}, knowledgeError(e)
	}
	if out.Chunks+out.Attempts+out.Sources+out.Documents+out.Generations+out.Intents > 0 {
		slog.Info("knowledge_retention_completed", "chunks", out.Chunks, "attempts", out.Attempts, "sources", out.Sources, "documents", out.Documents, "generations", out.Generations, "intents", out.Intents)
	}
	return out, nil
}

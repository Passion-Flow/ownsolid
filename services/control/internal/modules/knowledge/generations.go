package knowledge

import (
	"context"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

func (s *Service) finishGeneration(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, b baseRecord, g Generation, authority bool) error {
	if b.buildingID == nil || *b.buildingID != g.ID || g.State != "building" {
		return nil
	}
	var pending, failed, ready, current int
	if e := tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT count(*) FILTER(WHERE state IN ('pending','running')),count(*) FILTER(WHERE state IN ('failed','cancelled')),count(*) FILTER(WHERE state='ready') FROM knowledge_attempts WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND generation_id=$4`, `SELECT COALESCE(SUM(state IN ('pending','running')),0),COALESCE(SUM(state IN ('failed','cancelled')),0),COALESCE(SUM(state='ready'),0) FROM knowledge_attempts WHERE tenant_id=? AND workspace_id=? AND base_id=? AND generation_id=?`, []any{w.TenantID, w.ID, b.ID, g.ID}, []any{w.TenantID, w.ID, b.ID, g.ID})).Scan(&pending, &failed, &ready); e != nil {
		return e
	}
	if e := tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT count(*) FROM knowledge_documents WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND deleted_at IS NULL AND visible_source_id IS NOT NULL`, `SELECT count(*) FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND deleted_at IS NULL AND visible_source_id IS NOT NULL`, []any{w.TenantID, w.ID, b.ID}, []any{w.TenantID, w.ID, b.ID})).Scan(&current); e != nil {
		return e
	}
	code := ""
	if !authority {
		code = "KNOWLEDGE_AUTHORITY_INVALID"
	} else if !b.Enabled {
		code = "KNOWLEDGE_BASE_DISABLED"
	} else if failed > 0 || current != g.ExpectedDocuments {
		code = "KNOWLEDGE_REBUILD_FAILED"
	} else if pending > 0 {
		return nil
	} else if ready != g.ExpectedDocuments {
		code = "KNOWLEDGE_REBUILD_FAILED"
	}
	if code == "" {
		var pinned int
		if e := tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT count(*) FROM knowledge_attempts a JOIN knowledge_documents d ON d.id=a.document_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id WHERE a.tenant_id=$1 AND a.workspace_id=$2 AND a.base_id=$3 AND a.generation_id=$4 AND a.state='ready' AND d.deleted_at IS NULL AND d.visible_source_id=a.source_id`, `SELECT count(*) FROM knowledge_attempts a JOIN knowledge_documents d ON d.id=a.document_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id WHERE a.tenant_id=? AND a.workspace_id=? AND a.base_id=? AND a.generation_id=? AND a.state='ready' AND d.deleted_at IS NULL AND d.visible_source_id=a.source_id`, []any{w.TenantID, w.ID, b.ID, g.ID}, []any{w.TenantID, w.ID, b.ID, g.ID})).Scan(&pinned); e != nil {
			return e
		}
		if pinned != g.ExpectedDocuments || g.CatalogSHA256 != CatalogDigest() {
			code = "KNOWLEDGE_REBUILD_FAILED"
		}
	}
	if code != "" {
		if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_attempts SET state='cancelled',lease_hash=NULL,lease_expires_at=NULL,error_code=$2,finished_at=clock_timestamp() WHERE generation_id=$1 AND state IN ('pending','running')`, `UPDATE knowledge_attempts SET state='cancelled',lease_hash=NULL,lease_expires_at=NULL,error_code=?,finished_at=CURRENT_TIMESTAMP(6) WHERE generation_id=? AND state IN ('pending','running')`, []any{g.ID, code}, []any{code, g.ID})); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_generations SET state='failed',error_code=$2,finished_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_generations SET state='failed',error_code=?,finished_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{g.ID, code}, []any{code, g.ID})); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_bases SET building_generation_id=NULL,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_bases SET building_generation_id=NULL,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{b.ID}, []any{b.ID})); e != nil {
			return e
		}
		return s.audit(ctx, tx, a, w, "knowledge.rebuild_failed", "knowledge_base", b.ID, code, uuid.NewString())
	}
	if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_generations SET state='retired',finished_at=clock_timestamp() WHERE id=$1 AND state='active'`, `UPDATE knowledge_generations SET state='retired',finished_at=CURRENT_TIMESTAMP(6) WHERE id=? AND state='active'`, []any{b.activeID}, []any{b.activeID})); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_generations SET state='active',activated_at=clock_timestamp(),finished_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_generations SET state='active',activated_at=CURRENT_TIMESTAMP(6),finished_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{g.ID}, []any{g.ID})); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_documents d SET current_attempt_id=a.id,state='ready',revision=d.revision+1,updated_at=clock_timestamp() FROM knowledge_attempts a WHERE a.tenant_id=$1 AND a.workspace_id=$2 AND a.base_id=$3 AND a.generation_id=$4 AND a.state='ready' AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id AND d.id=a.document_id AND d.visible_source_id=a.source_id AND d.pending_source_id IS NULL AND d.deleted_at IS NULL`, `UPDATE knowledge_documents d JOIN knowledge_attempts a ON d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id AND d.id=a.document_id SET d.current_attempt_id=a.id,d.state='ready',d.revision=d.revision+1,d.updated_at=CURRENT_TIMESTAMP(6) WHERE a.tenant_id=? AND a.workspace_id=? AND a.base_id=? AND a.generation_id=? AND a.state='ready' AND d.visible_source_id=a.source_id AND d.pending_source_id IS NULL AND d.deleted_at IS NULL`, []any{w.TenantID, w.ID, b.ID, g.ID}, []any{w.TenantID, w.ID, b.ID, g.ID})); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_bases SET active_generation_id=$2,building_generation_id=NULL,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_bases SET active_generation_id=?,building_generation_id=NULL,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{b.ID, g.ID}, []any{g.ID, b.ID})); e != nil {
		return e
	}
	return s.audit(ctx, tx, a, w, "knowledge.rebuild_completed", "knowledge_base", b.ID, "Complete generation activated atomically", uuid.NewString())
}
func (s *Service) Rebuild(ctx context.Context, a identity.Auth, workspace, base string, input RevisionReason, key, request string) (Base, error) {
	var out Base
	if e := s.enabled(); e != nil {
		return knowledgeCommitted(out, e)
	}
	if !validReason(input.Reason) || input.ExpectedRevision < 1 {
		return out, failure("VALIDATION_FAILED")
	}
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		b, e := lockedBase(ctx, tx, w, base)
		if e != nil {
			return e
		}
		previous, digest, e := s.intent(ctx, tx, a, w, "rebuild", key, struct {
			Base  string
			Input RevisionReason
		}{base, input})
		if e != nil {
			return e
		}
		if previous != "" {
			if previous != base {
				return failure("IDEMPOTENCY_CONFLICT")
			}
			out, e = hydrateBase(ctx, tx, w, b)
			return e
		}
		if b.Revision != input.ExpectedRevision {
			return failure("KNOWLEDGE_REVISION_CONFLICT")
		}
		if !b.Enabled {
			return failure("KNOWLEDGE_BASE_DISABLED")
		}
		if b.buildingID != nil {
			return failure("KNOWLEDGE_BUSY")
		}
		var busy, generations int
		if e = tx.QueryRow(ctx, knowledgeSQL(tx, "SELECT count(*) FROM knowledge_attempts WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND state IN ('pending','running')", `SELECT count(*) FROM knowledge_attempts WHERE tenant_id=? AND workspace_id=? AND base_id=? AND state IN ('pending','running')`, []any{w.TenantID, w.ID, base}, []any{w.TenantID, w.ID, base})).Scan(&busy); e != nil {
			return e
		}
		if busy > 0 {
			return failure("KNOWLEDGE_BUSY")
		}
		if e = tx.QueryRow(ctx, knowledgeSQL(tx, "SELECT count(*) FROM knowledge_generations WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3", `SELECT count(*) FROM knowledge_generations WHERE tenant_id=? AND workspace_id=? AND base_id=?`, []any{w.TenantID, w.ID, base}, []any{w.TenantID, w.ID, base})).Scan(&generations); e != nil {
			return e
		}
		if generations >= s.policy().MaximumGenerations {
			return failure("KNOWLEDGE_LIMIT_REACHED")
		}
		rows, e := tx.Query(ctx, knowledgeSQL(tx, "SELECT id::text,visible_source_id::text,revision FROM knowledge_documents WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND deleted_at IS NULL AND visible_source_id IS NOT NULL ORDER BY id", `SELECT id,visible_source_id,revision FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND deleted_at IS NULL AND visible_source_id IS NOT NULL ORDER BY id`, []any{w.TenantID, w.ID, base}, []any{w.TenantID, w.ID, base}))
		if e != nil {
			return e
		}
		type source struct {
			id, source string
			revision   int64
		}
		var sources []source
		for rows.Next() {
			var v source
			if e = rows.Scan(&v.id, &v.source, &v.revision); e != nil {
				rows.Close()
				return e
			}
			sources = append(sources, v)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		generation := uuid.NewString()
		member, e := delegation(ctx, tx, a, w)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, `INSERT INTO knowledge_generations(id,tenant_id,workspace_id,base_id,catalog_sha256,dimension,chunk_tokens,overlap_tokens,state,requested_by,requested_audience,requested_epoch,membership_authority,expected_documents) VALUES($1,$2,$3,$4,$5,384,$6,$7,'building',$8,$9,$10,$11,$12)`, `INSERT INTO knowledge_generations(id,tenant_id,workspace_id,base_id,catalog_sha256,dimension,chunk_tokens,overlap_tokens,state,requested_by,requested_audience,requested_epoch,membership_authority,expected_documents) VALUES(?,?,?,?,?,384,?,?,'building',?,?,?,?,?)`, []any{generation, w.TenantID, w.ID, base, CatalogDigest(), s.policy().ChunkTokens, s.policy().OverlapTokens, a.Me.User.ID, a.Me.Audience, a.Epoch, member, len(sources)}, []any{generation, w.TenantID, w.ID, base, CatalogDigest(), s.policy().ChunkTokens, s.policy().OverlapTokens, a.Me.User.ID, a.Me.Audience, a.Epoch, member, len(sources)})); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_bases SET building_generation_id=$2,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_bases SET building_generation_id=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{base, generation}, []any{generation, base})); e != nil {
			return e
		}
		for _, source := range sources {
			if _, e = s.insertAttempt(ctx, tx, a, w, base, source.id, source.source, generation, "rebuild", source.revision); e != nil {
				return e
			}
		}
		if e = saveIntent(ctx, tx, a, w, "rebuild", key, base, digest); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "knowledge.rebuild_started", "knowledge_base", base, input.Reason, request); e != nil {
			return e
		}
		b, e = lockedBase(ctx, tx, w, base)
		if e != nil {
			return e
		}
		g, e := loadGeneration(ctx, tx, w, base, generation)
		if e != nil {
			return e
		}
		if e = s.finishGeneration(ctx, tx, a, w, b, g, true); e != nil {
			return e
		}
		b, e = lockedBase(ctx, tx, w, base)
		if e != nil {
			return e
		}
		out, e = hydrateBase(ctx, tx, w, b)
		return e
	})
	return knowledgeCommitted(out, e)
}
func (s *Service) ReconcileGeneration(ctx context.Context) error {
	repo, e := s.repository()
	if e != nil {
		return e
	}

	var c delegatedActor
	var base, id string
	e = repo.QueryRow(ctx, knowledgeSQL(repo, `SELECT g.tenant_id::text,g.workspace_id::text,g.base_id::text,g.id::text,g.requested_by::text,g.requested_audience,g.requested_epoch,g.membership_authority::text FROM knowledge_generations g JOIN installation_state i ON i.tenant_id=g.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND g.state='building' ORDER BY g.created_at,g.id LIMIT 1`, `SELECT g.tenant_id,g.workspace_id,g.base_id,g.id,g.requested_by,g.requested_audience,g.requested_epoch,g.membership_authority FROM knowledge_generations g JOIN knowledge_bases b ON b.tenant_id=g.tenant_id AND b.workspace_id=g.workspace_id AND b.id=g.base_id AND b.initialization_state='ready' JOIN installation_state i ON i.tenant_id=g.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND g.state='building' ORDER BY g.created_at,g.id LIMIT 1`, nil, nil)).Scan(&c.tenant, &c.workspace, &base, &id, &c.actor, &c.audience, &c.epoch, &c.membership)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().CommitSeconds))
	defer cancel()
	e = repo.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		a, w, valid, e := s.lockDelegation(ctx, tx, c)
		if e != nil {
			return e
		}
		b, e := lockedBase(ctx, tx, w, base)
		if e != nil {
			return e
		}
		var locked string
		if e = tx.QueryRow(ctx, knowledgeSQL(tx, "SELECT id::text FROM knowledge_generations WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND base_id=$4 FOR UPDATE", `SELECT id FROM knowledge_generations WHERE id=? AND tenant_id=? AND workspace_id=? AND base_id=? FOR UPDATE`, []any{id, w.TenantID, w.ID, base}, []any{id, w.TenantID, w.ID, base})).Scan(&locked); e != nil {
			return e
		}
		g, e := loadGeneration(ctx, tx, w, base, locked)
		if e != nil {
			return e
		}
		if e = s.finishGeneration(ctx, tx, a, w, b, g, valid); e != nil {
			return e
		}
		return nil
	})
	return knowledgeError(e)
}

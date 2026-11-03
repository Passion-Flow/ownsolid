package knowledge

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) Update(ctx context.Context, a identity.Auth, workspace, id string, input UpdateBase, request string) (Base, error) {
	var out Base
	if !validReason(input.Reason) || input.ExpectedRevision < 1 {
		return out, failure("VALIDATION_FAILED")
	}
	name, key, e := normalizedName(input.Name, 100)
	if e != nil {
		return knowledgeCommitted(out, e)
	}
	e = s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		b, e := lockedBase(ctx, tx, w, id)
		if e != nil {
			return e
		}
		if b.Revision != input.ExpectedRevision {
			return failure("KNOWLEDGE_REVISION_CONFLICT")
		}
		if !input.Enabled {
			if _, e = tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_attempts SET state='cancelled',lease_hash=NULL,lease_expires_at=NULL,error_code='KNOWLEDGE_CANCELLED',finished_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND state IN ('pending','running')`, `UPDATE knowledge_attempts SET state='cancelled',lease_hash=NULL,lease_expires_at=NULL,error_code='KNOWLEDGE_CANCELLED',finished_at=CURRENT_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND base_id=? AND state IN ('pending','running')`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_documents SET state='failed',revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND state IN ('pending','running')`, `UPDATE knowledge_documents SET state='failed',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND base_id=? AND state IN ('pending','running')`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_generations SET state='failed',error_code='KNOWLEDGE_CANCELLED',finished_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND base_id=$3 AND state='building'`, `UPDATE knowledge_generations SET state='failed',error_code='KNOWLEDGE_CANCELLED',finished_at=CURRENT_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND base_id=? AND state='building'`, []any{w.TenantID, w.ID, id}, []any{w.TenantID, w.ID, id})); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(ctx, knowledgeSQL(tx, `UPDATE knowledge_bases SET name=$2,name_key=$3,enabled=$4,revision=revision+1,building_generation_id=CASE WHEN $4 THEN building_generation_id ELSE NULL END,updated_at=clock_timestamp() WHERE id=$1`, `UPDATE knowledge_bases SET name=?,name_key=?,enabled=?,revision=revision+1,building_generation_id=CASE WHEN ? THEN building_generation_id ELSE NULL END,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{id, name, key, input.Enabled}, []any{name, key, input.Enabled, input.Enabled, id})); e != nil {
			return knowledgeUnique(e, "KNOWLEDGE_BASE_EXISTS")
		}
		if e = s.audit(ctx, tx, a, w, "knowledge.base_updated", "knowledge_base", id, input.Reason, request); e != nil {
			return e
		}
		b, e = lockedBase(ctx, tx, w, id)
		if e != nil {
			return e
		}
		out, e = hydrateBase(ctx, tx, w, b)
		return e
	})
	return knowledgeCommitted(out, e)
}

package knowledge

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// CheckWorkflowBaseInTransaction validates a trusted Go workflow reference in
// its caller-owned transaction. It performs no encoder/model invocation and
// never accepts a browser-provided authority or source result.
func (s *Service) CheckWorkflowBaseInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, workspace, base string) (Base, error) {
	return s.CheckWorkflowBaseWithin(ctx, transaction.PostgreSQLTransaction(tx), a, workspace, base)
}

func (s *Service) CheckWorkflowBaseWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, workspace, base string) (Base, error) {
	if e := s.borrowed(ctx, tx); e != nil {
		return Base{}, e
	}

	var zero Base
	if !validID(workspace) || !validID(base) {
		return zero, failure("KNOWLEDGE_BASE_NOT_FOUND")
	}
	if _, e := tx.Exec(ctx, knowledgeSQL(tx, "SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE", `SELECT id FROM users WHERE id=? FOR UPDATE`, []any{a.Me.User.ID}, []any{a.Me.User.ID})); e != nil {
		return zero, e
	}
	if e := s.Identity.RecheckOrderedWithin(ctx, tx, a); e != nil {
		return zero, e
	}
	w, e := s.Workspaces.RecheckScopeWithin(ctx, tx, a, workspace)
	if e != nil {
		return zero, e
	}
	if w.Status != "active" || w.Role == nil {
		return zero, failure("ACCESS_DENIED")
	}
	b, e := lockedBase(ctx, tx, w, base)
	if e != nil {
		return zero, e
	}
	if !b.Enabled {
		return zero, failure("KNOWLEDGE_BASE_DISABLED")
	}
	generation, e := loadGeneration(ctx, tx, w, base, b.activeID)
	if e != nil {
		return zero, e
	}
	if generation.CatalogSHA256 != CatalogDigest() || generation.Dimension != EmbeddingDimension || generation.State != "active" {
		return zero, failure("KNOWLEDGE_INDEX_INCOMPATIBLE")
	}
	return hydrateBase(ctx, tx, w, b)
}

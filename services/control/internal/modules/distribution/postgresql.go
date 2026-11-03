package distribution

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) CurrentAnswerAliasInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, workspace, id string) (Alias, error) {
	return s.CurrentAnswerAliasWithin(ctx, transaction.PostgreSQLTransaction(tx), a, workspace, id)
}
func (s *Service) RecheckAnswerAliasInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, workspace string, expected Alias) error {
	return s.RecheckAnswerAliasWithin(ctx, transaction.PostgreSQLTransaction(tx), a, workspace, expected)
}
func (s *Service) CheckHistoricalAliasAccessInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, workspace string, snapshot Alias) error {
	return s.CheckHistoricalAliasAccessWithin(ctx, transaction.PostgreSQLTransaction(tx), a, workspace, snapshot)
}

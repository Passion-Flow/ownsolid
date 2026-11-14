package plugins

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Retained PostgreSQL entrypoints borrow the selected owner implementation.
func (s *Service) CheckToolInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, workspace, version string) error {
	return s.CheckToolWithin(ctx, transaction.PostgreSQLTransaction(tx), a, workspace, version)
}
func (s *Service) ExecuteModelListInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, workspace, version, run, question string) (*CompletedModelList, error) {
	return s.ExecuteModelListWithin(ctx, transaction.PostgreSQLTransaction(tx), a, workspace, version, run, question)
}
func (s *Service) RecheckCompletedModelListInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, workspace, version, run, question string, c *CompletedModelList) error {
	return s.RecheckCompletedModelListWithin(ctx, transaction.PostgreSQLTransaction(tx), a, workspace, version, run, question, c)
}
func (s *Service) RecheckStoredModelListInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, workspace, version string, proof, result []byte) error {
	return s.RecheckStoredModelListWithin(ctx, transaction.PostgreSQLTransaction(tx), a, workspace, version, proof, result)
}
func (s *Service) RecordPublicationInTransaction(ctx context.Context, tx pgx.Tx, a identity.Auth, workspace, version, workflow, publication string) error {
	return s.RecordPublicationWithin(ctx, transaction.PostgreSQLTransaction(tx), a, workspace, version, workflow, publication)
}

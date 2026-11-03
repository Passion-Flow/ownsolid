package identity

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// revokeAuthority borrows the legacy caller transaction;the caller owns user locks.
func revokeAuthority(ctx context.Context, tx pgx.Tx, user, reason string) error {
	if tx == nil {
		return failure("IDENTITY_TRANSACTION_REQUIRED")
	}
	return revokeAuthorityWithin(ctx, transaction.PostgreSQLTransaction(tx), user, reason)
}

// InvalidateAccountAuthorities retains the trusted PostgreSQL restore entrance.
func (s *Service) InvalidateAccountAuthorities(ctx context.Context, tx pgx.Tx, reason string) error {
	q, e := s.sessionTransaction(tx)
	if e != nil {
		return e
	}
	return s.InvalidateAccountAuthoritiesWithin(ctx, q, reason)
}

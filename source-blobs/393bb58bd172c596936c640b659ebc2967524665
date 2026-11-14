package identity

import (
	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// RecheckOrderedWithin preserves the existing entrance. RecheckWithin now owns
// the same user/source/session order for every caller, including legacy ones.
// Multi-target callers still acquire all users/connectors in canonical order.
func (s *Service) RecheckOrderedWithin(ctx context.Context, q transaction.Executor, a Auth) error {
	return s.RecheckWithin(ctx, q, a)
}

package licensing

import (
	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// CheckWithin verifies real signed whole-platform license history in the caller's
// owning transaction. Callers first authenticate current actor/scope and must not
// expose a result before their outer commit succeeds. It never grants a module tier.
func (s *Service) CheckWithin(ctx context.Context, q transaction.Executor) error {
	if s == nil || s.identity == nil || s.now == nil || ctx == nil || q == nil || s.policy.Validate() != nil {
		return fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	if _, store := q.(transaction.Store); store {
		return fail("LICENSE_TRANSACTION_REQUIRED")
	}
	owner, e := s.identity.Repository()
	if e != nil || owner == nil || owner.Dialect() != q.Dialect() {
		return fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	// This only reads, and image pulls of the whole fleet come through here: they must not
	// wait for each other on the singleton row (ADR 0020).
	state, _, _, e := s.read(ctx, q, "")
	if e != nil {
		return repositoryError(e)
	}
	if !state.NewBusinessAllowed {
		return fail("LICENSE_NEW_BUSINESS_UNAVAILABLE")
	}
	return nil
}

// UsesIdentity lets an owning component reject a mismatched license authority.
func (s *Service) UsesIdentity(owner *identity.Service) bool {
	return s != nil && owner != nil && s.identity == owner
}

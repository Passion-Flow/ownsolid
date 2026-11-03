package licensing

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// MonitoringWithin validates actual whole-platform history in the caller's owning
// transaction. Expiry permits diagnostics only; this method grants no new work.
// readLocked advances the original trusted-clock fence, so outer known commit is
// required even though the returned projection contains no mutation capability.
func (s *Service) MonitoringWithin(ctx context.Context, q transaction.Executor, a identity.Auth) (PortalState, error) {
	if s == nil || s.identity == nil || s.now == nil || ctx == nil || q == nil || s.policy.Validate() != nil {
		return PortalState{}, fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	if e := ctx.Err(); e != nil {
		return PortalState{}, repositoryError(e)
	}
	if _, pool := q.(transaction.Store); pool {
		return PortalState{}, fail("LICENSE_TRANSACTION_REQUIRED")
	}
	owner, e := s.identity.Repository()
	if e != nil || owner == nil || owner.Dialect() != q.Dialect() {
		return PortalState{}, fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	if e = s.actor(ctx, q, a, true); e != nil {
		return PortalState{}, repositoryError(e)
	}
	state, _, _, e := s.readLocked(ctx, q)
	if e != nil {
		return PortalState{}, repositoryError(e)
	}
	return PortalState{State: state.State, NewBusinessAllowed: state.NewBusinessAllowed, Warning: state.Warning}, nil
}

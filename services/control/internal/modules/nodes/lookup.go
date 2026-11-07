package nodes

import (
	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// LookupWithin reads one node of a workspace the caller has already scoped and fenced in q,
// without locking it. It grants no authority over the node.
func (s *Service) LookupWithin(ctx context.Context, q transaction.Executor, tenant, workspace, id string) (Node, error) {
	return s.lookupWithin(ctx, q, tenant, workspace, id)
}

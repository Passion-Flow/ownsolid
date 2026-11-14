package deployments

import (
	"context"
	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// HardwareInTransaction borrows the existing PostgreSQL owner's transaction.
func (s *Service) HardwareInTransaction(ctx context.Context, tx pgx.Tx, n nodes.Node) (wire.InferenceObservation, error) {
	return s.HardwareWithin(ctx, transaction.PostgreSQLTransaction(tx), n)
}

package deployments

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"time"
)

type GatewayTarget struct {
	ID           string
	Revision     int64
	ModelID      string
	BackendModel string
}

// ConfirmGatewayStop delegates the retained PostgreSQL borrowed interface.
func (s *Service) ConfirmGatewayStop(ctx context.Context, tx pgx.Tx, tenant, workspace, node, deployment string, revision int64, after time.Time) error {
	return s.ConfirmGatewayStopWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, node, deployment, revision, after)
}
func (s *Service) ReadyGatewayTarget(ctx context.Context, tx pgx.Tx, tenant, workspace, node, deployment string, revision int64) (GatewayTarget, error) {
	return s.ReadyGatewayTargetWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, workspace, node, deployment, revision)
}

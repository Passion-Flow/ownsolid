package nodes

import (
	"context"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"slices"
)

// TrainingTargetWithin borrows the training owner's current authority and node
// inventory locks. It grants neither a machine lease nor independent commit.
func (s *Service) TrainingTargetWithin(ctx context.Context, q transaction.Executor, tenant, workspace, id string) (Node, error) {
	n, e := s.PreparationHistoryWithin(ctx, q, tenant, workspace, id)
	if e != nil {
		return Node{}, e
	}
	if n.Status != "active" {
		return Node{}, fail("NODE_UNAVAILABLE")
	}
	if !wire.IsTrainingProfile(n.ExecutionProfile) || n.ProtocolVersion != wire.ProfileVersion(n.ExecutionProfile) || !slices.Contains(n.Capabilities, wire.TrainingRunOperation) {
		return Node{}, fail("AGENT_PROFILE_REJECTED")
	}
	return n, nil
}

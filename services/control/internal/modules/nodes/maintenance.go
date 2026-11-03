package nodes

import (
	"context"
	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ComponentResultObserver keeps maintenance data writes with the owning module.
type ComponentResultObserver interface {
	RecordResult(context.Context, pgx.Tx, Node, Task, wire.Result) error
}

func (s *Service) validateNodeResult(ctx context.Context, tx pgx.Tx, n Node, t Task, input wire.Result) error {
	if t.Operation == wire.ModelPreparationRunOperation {
		return fail("MODEL_PREPARATION_AUTHORITY_REQUIRED")
	}
	if !nodeOperationAllowed(n, t.Operation) {
		return fail("AGENT_PROFILE_REJECTED")
	}
	if wire.IsInferenceOperation(t.Operation) {
		command, e := s.ReadInferenceCommand(ctx, tx, n, t)
		if e != nil {
			return e
		}
		return input.ValidateInference(command)
	}
	return s.ValidateComponentResultWithin(ctx, transaction.PostgreSQLTransaction(tx), n, t, input)
}

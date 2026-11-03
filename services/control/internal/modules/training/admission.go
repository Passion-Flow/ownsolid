package training

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// AdmissionCheck binds an outside-lock immutable source analysis to the final current-authority transaction.
type AdmissionCheck interface {
	ValidateWithin(context.Context, transaction.Executor, workspaces.Workspace, datasets.TrainingInputs) error
	Validate(context.Context, pgx.Tx, workspaces.Workspace, datasets.TrainingInputs) error
}
type HoldoutGuard interface {
	HasReservationsWithin(context.Context, transaction.Executor, workspaces.Workspace) (bool, error)
	HasReservations(context.Context, pgx.Tx, workspaces.Workspace) (bool, error)
	PrepareTrainingAdmission(context.Context, identity.Auth, CreateRequest) (AdmissionCheck, error)
}

// A custom composition may omit the guard only while its scoped registry is empty.
func (s *Service) requireEmptyHoldoutsWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
	// The checked cumulative M09 catalog has no M10 holdout owner or table.
	// Platform compositions still require the existing reservation guard below.
	if s.Identity.Config.TrainingOnly() {
		return nil
	}
	var exists bool
	if e := q.QueryRow(ctx, acceptedAdapterSQL(q, "SELECT EXISTS(SELECT 1 FROM model_holdouts WHERE tenant_id=$1 AND workspace_id=$2)", "SELECT EXISTS(SELECT 1 FROM model_holdouts WHERE tenant_id=? AND workspace_id=?)", w.TenantID, w.ID)).Scan(&exists); e != nil {
		return e
	}
	if exists {
		return fail("MODEL_HOLDOUT_GUARD_REQUIRED")
	}
	return nil
}

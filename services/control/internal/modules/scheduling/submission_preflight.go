package scheduling

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
)

// ErrPreflightSnapshotRequired requires a read-only PostgreSQL REPEATABLE READ transaction.
var ErrPreflightSnapshotRequired = errors.New("SCHEDULING_PREFLIGHT_SNAPSHOT_REQUIRED")

// SubmissionEstimate is the whole demand and guidance from one policy and
// reservation snapshot. The workload owner still performs transactional admission.
type SubmissionEstimate struct {
	PolicyRevision int64
	Demand         Resources
	Reasons        []WaitReason
}

// EstimateSubmissionWithin evaluates a new submission from the workload
// owner's read-only REPEATABLE READ transaction. The owner authorizes the
// workspace and supplies capacity from its trusted cell observation. The
// estimate is advisory and carries no reservation or placement guarantee.
func EstimateSubmissionWithin(ctx context.Context, tx pgx.Tx, tenant, workspaceID string, intent JobIntent, cell CellCapacity) (SubmissionEstimate, error) {
	if err := requireSchedulingSnapshot(ctx, tx, ErrPreflightSnapshotRequired); err != nil {
		return SubmissionEstimate{}, err
	}
	if !canonicalReservationID(tenant) || !canonicalReservationID(workspaceID) {
		return SubmissionEstimate{}, ErrQuotaScopeNotFound
	}

	q := transaction.PostgreSQLTransaction(tx)
	revision, tree, err := readQuotaPolicyWithin(ctx, q)
	if err != nil {
		return SubmissionEstimate{}, err
	}
	if tree == nil {
		return SubmissionEstimate{}, ErrQuotaUnconfigured
	}
	workspace, err := tree.workspace(tenant, workspaceID)
	if err != nil {
		return SubmissionEstimate{}, err
	}
	_, demand, err := intent.Normalize()
	if err != nil {
		return SubmissionEstimate{}, err
	}
	used, err := readWorkspaceUsageWithin(ctx, q, tenant, workspaceID)
	if err != nil {
		return SubmissionEstimate{}, err
	}

	// Each held workspace fits its ceiling and every ancestor bounds the sum
	// of those ceilings. A new workspace fit preserves those same hard limits.
	limits := workspaceCapacityLimits(demand, workspace.Limits.Burst, used)
	preview := PreflightResult{Demand: demand, Limits: limits}
	if err := preview.checkCell(slices.Sorted(maps.Keys(demand)), cell); err != nil {
		return SubmissionEstimate{}, err
	}
	result := SubmissionEstimate{PolicyRevision: revision, Demand: demand, Reasons: preview.WaitReasons()}
	members := tree.borrowingMembers(workspace.ParentID)
	if len(limits) != 0 || members == nil {
		return result, nil
	}

	state, err := readCohortUsage(ctx, q, tenant, workspace.ParentID, members)
	if err != nil {
		return SubmissionEstimate{}, err
	}
	_, err = state.allocate(workspaceID, members, demand, false)
	reasons := WaitReasons(err)
	if err != nil && len(reasons) == 0 {
		return SubmissionEstimate{}, err
	}
	result.Reasons = append(result.Reasons, reasons...)
	return result, nil
}

func requireSchedulingSnapshot(ctx context.Context, tx pgx.Tx, unsupported error) error {
	if ctx == nil || tx == nil {
		return unsupported
	}
	var snapshot bool
	if err := tx.QueryRow(ctx, `SELECT current_setting('transaction_read_only')='on'
 AND current_setting('transaction_isolation')='repeatable read'`).Scan(&snapshot); err != nil {
		return err
	}
	if !snapshot {
		return unsupported
	}
	return nil
}

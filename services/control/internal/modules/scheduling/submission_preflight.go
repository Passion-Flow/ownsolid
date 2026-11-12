package scheduling

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrPreflightSnapshotRequired requires a read-only PostgreSQL REPEATABLE READ transaction.
	ErrPreflightSnapshotRequired = errors.New("SCHEDULING_PREFLIGHT_SNAPSHOT_REQUIRED")
	// ErrCellCapacityUnavailable asks the caller to retry with a fresh, complete local sample.
	ErrCellCapacityUnavailable = errors.New("SCHEDULING_CELL_CAPACITY_UNAVAILABLE")
)

// CellCapacitySample carries an observation from the selected cell's owner.
// ObservedAt is the measurement time and ValidUntil is its exclusive expiry
// under the owner's freshness policy. Both vectors explicitly cover every
// requested resource, including measured zero. The owner verifies the source
// and retains the same cell identity across measurement and estimation.
type CellCapacitySample struct {
	SiteID, CellID         string
	ObservedAt, ValidUntil time.Time
	Capacity               CellCapacity
}

// SubmissionEstimate is the whole demand and guidance from one policy and
// reservation snapshot. The workload owner still performs transactional admission.
type SubmissionEstimate struct {
	PolicyRevision int64
	Demand         Resources
	Reasons        []WaitReason
}

// EstimateSubmissionWithin evaluates a new submission from the workload
// owner's read-only REPEATABLE READ transaction. The owner authorizes the
// workspace and supplies a trusted sample for the cell bound to this database.
// The estimate is advisory and carries no reservation or placement guarantee.
func EstimateSubmissionWithin(ctx context.Context, tx pgx.Tx, tenant, workspaceID string, intent JobIntent, sample CellCapacitySample) (SubmissionEstimate, error) {
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
	resources := slices.Sorted(maps.Keys(demand))
	if err := checkCellCapacitySample(ctx, tx, sample, resources); err != nil {
		return SubmissionEstimate{}, err
	}
	if err := preview.checkCell(resources, sample.Capacity); err != nil {
		return SubmissionEstimate{}, ErrCellCapacityUnavailable
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

func checkCellCapacitySample(ctx context.Context, tx pgx.Tx, sample CellCapacitySample, resources []Resource) error {
	var siteID, cellID string
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT COALESCE(local_site_id(),''),COALESCE(local_cell_id(),''),clock_timestamp()`).
		Scan(&siteID, &cellID, &now); err != nil {
		return err
	}
	if siteID == "" || cellID == "" || sample.SiteID != siteID || sample.CellID != cellID ||
		sample.ObservedAt.IsZero() || sample.ObservedAt.After(now) || !now.Before(sample.ValidUntil) {
		return ErrCellCapacityUnavailable
	}
	for _, resource := range resources {
		_, totalKnown := sample.Capacity.Total[resource]
		_, reservedKnown := sample.Capacity.Reserved[resource]
		if !totalKnown || !reservedKnown {
			return ErrCellCapacityUnavailable
		}
	}
	return nil
}

func requireSchedulingSnapshot(ctx context.Context, tx pgx.Tx, unsupported error) error {
	if tx == nil {
		return unsupported
	}
	return requireSchedulingExecutorSnapshot(ctx, transaction.PostgreSQLTransaction(tx), unsupported)
}

func requireSchedulingExecutorSnapshot(ctx context.Context, q transaction.Executor, unsupported error) error {
	if ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL {
		return unsupported
	}
	if _, pool := q.(transaction.Store); pool {
		return unsupported
	}
	var snapshot bool
	if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT current_setting('transaction_read_only')='on'
 AND current_setting('transaction_isolation')='repeatable read'`)).Scan(&snapshot); err != nil {
		return err
	}
	if !snapshot {
		return unsupported
	}
	return nil
}

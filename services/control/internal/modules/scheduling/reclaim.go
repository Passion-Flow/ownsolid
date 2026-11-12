package scheduling

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

var (
	// ErrReclaimPending indicates a workspace is waiting for its selected releases.
	ErrReclaimPending = errors.New("SCHEDULING_RECLAIM_PENDING")
	// ErrReclaimNotNeeded indicates that no whole nominal claim is blocked by loans.
	ErrReclaimNotNeeded = errors.New("SCHEDULING_RECLAIM_NOT_NEEDED")
)

// ReclaimState describes the durable claim on a lender's own guarantee.
type ReclaimState string

const (
	// ReclaimPending is waiting for verified releases and admission.
	ReclaimPending ReclaimState = "pending"
	// ReclaimAdmitted is a reclaim consumed by its exact admitted attempt.
	ReclaimAdmitted ReclaimState = "admitted"
	// ReclaimCancelled is a reclaim withdrawn by the workload owner.
	ReclaimCancelled ReclaimState = "cancelled"
)

// ReclaimPlan records the exact attempts in the order the owner should request
// their stop. Only pending plans are actionable.
type ReclaimPlan struct {
	State   ReclaimState
	Victims []string
}

func readReclaimWithin(ctx context.Context, q transaction.Executor, a Attempt, body []byte) (ReclaimPlan, error) {
	var wanted any
	if body != nil {
		wanted = body
	}
	var plan ReclaimPlan
	var same bool
	err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT attempt_id=$6 AND ($7::jsonb IS NULL OR intent=$7::jsonb)
  AND site_id=local_site_id() AND cell_id=local_cell_id(),state,victims::text[]
 FROM scheduling_quota_reclaims
 WHERE tenant_id=$1 AND workspace_id=$2 AND kind=$3 AND source_id=$4 AND attempt_number=$5`,
		append(a.scopeArgs(), a.ID, wanted)...)).Scan(&same, &plan.State, &plan.Victims)
	if errors.Is(err, sql.ErrNoRows) {
		return ReclaimPlan{}, nil
	}
	if err != nil {
		return ReclaimPlan{}, err
	}
	if !same {
		return ReclaimPlan{}, ErrReservationConflict
	}
	return plan, nil
}

// RequestReclaimWithin records a whole nominal claim without releasing a
// reservation. The owner must authorize the request and commit its state and
// audit with the plan before delivering any stop requests.
func RequestReclaimWithin(ctx context.Context, q transaction.Executor, a Attempt, intent JobIntent) (ReclaimPlan, error) {
	body, demand, err := reservationPayload(a, intent)
	if err != nil {
		return ReclaimPlan{}, err
	}
	if _, err := lockReservationScope(ctx, q, a); err != nil {
		return ReclaimPlan{}, err
	}
	plan, err := readReclaimWithin(ctx, q, a, body)
	if err != nil || plan.State != "" {
		return plan, err
	}
	workspace, members, err := loadReclaimCohort(ctx, q, a)
	if err != nil {
		return ReclaimPlan{}, err
	}
	if err := checkWorkspaceCapacity(ctx, q, a, workspace.Limits.Burst, demand); err != nil {
		return ReclaimPlan{}, err
	}
	state, err := readCohortUsage(ctx, q, a.TenantID, workspace.ParentID, members)
	if err != nil {
		return ReclaimPlan{}, err
	}
	if state.pending[a.WorkspaceID] {
		return ReclaimPlan{}, ErrReclaimPending
	}
	var reserved bool
	err = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT EXISTS (SELECT 1 FROM scheduling_reservations WHERE attempt_id=$6 OR
 (tenant_id=$1 AND workspace_id=$2 AND kind=$3 AND source_id=$4 AND attempt_number=$5))`,
		append(a.scopeArgs(), a.ID)...)).Scan(&reserved)
	if err != nil {
		return ReclaimPlan{}, err
	}
	if reserved {
		return ReclaimPlan{}, ErrReservationConflict
	}
	victims, err := state.reclaimVictims(a.WorkspaceID, workspace.Limits.Guaranteed, demand)
	if err != nil {
		return ReclaimPlan{}, err
	}
	if err := requireDurableReservationCommit(ctx, q); err != nil {
		return ReclaimPlan{}, err
	}
	err = updateBorrowingRow(ctx, q, `INSERT INTO scheduling_quota_reclaims
 (tenant_id,workspace_id,kind,source_id,attempt_number,attempt_id,intent,cohort_id,victims)
 VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9::uuid[]) ON CONFLICT DO NOTHING`,
		append(a.scopeArgs(), a.ID, body, workspace.ParentID, victims)...)
	if err != nil {
		return ReclaimPlan{}, err
	}
	return ReclaimPlan{State: ReclaimPending, Victims: victims}, nil
}

// CancelReclaimWithin withdraws the exact pending claim. False with no error
// means the same claim was previously cancelled. Victim holds remain charged.
func CancelReclaimWithin(ctx context.Context, q transaction.Executor, a Attempt) (bool, error) {
	if !a.valid() {
		return false, ErrInvalidAttempt
	}
	if _, err := lockReservationScope(ctx, q, a); err != nil {
		return false, err
	}
	plan, err := readReclaimWithin(ctx, q, a, nil)
	if err != nil {
		return false, err
	}
	switch plan.State {
	case ReclaimCancelled:
		return false, nil
	case ReclaimPending:
	default:
		return false, ErrReservationConflict
	}
	if _, _, err := loadReclaimCohort(ctx, q, a); err != nil {
		return false, err
	}
	if err := requireDurableReservationCommit(ctx, q); err != nil {
		return false, err
	}
	err = updateBorrowingRow(ctx, q, `UPDATE scheduling_quota_reclaims SET state='cancelled' WHERE attempt_id=$1 AND state='pending' AND site_id=local_site_id() AND cell_id=local_cell_id()`, a.ID)
	return err == nil, err
}

func loadReclaimCohort(ctx context.Context, q transaction.Executor, a Attempt) (QuotaNode, map[string]Quota, error) {
	_, tree, err := readQuotaPolicyWithin(ctx, q)
	if err != nil {
		return QuotaNode{}, nil, err
	}
	if tree == nil {
		return QuotaNode{}, nil, ErrQuotaUnconfigured
	}
	workspace, err := tree.workspace(a.TenantID, a.WorkspaceID)
	if err != nil {
		return QuotaNode{}, nil, err
	}
	members := tree.borrowingMembers(workspace.ParentID)
	if members == nil {
		return QuotaNode{}, nil, ErrReclaimNotNeeded
	}
	if err := lockBorrowingCohort(ctx, q, a.TenantID, workspace.ParentID); err != nil {
		return QuotaNode{}, nil, err
	}
	return workspace, members, nil
}

func (s cohortUsage) reclaimVictims(workspace string, guaranteed, demand Resources) ([]string, error) {
	deficit := make(Resources)
	for resource, requested := range demand {
		room := max(0, guaranteed[resource]-s.owned[workspace][resource])
		if requested > room {
			return nil, ErrReclaimNotNeeded
		}
		if available := max(0, room-s.lent[workspace][resource]); requested > available {
			deficit[resource] = requested - available
		}
	}
	if len(deficit) == 0 {
		return nil, ErrReclaimNotNeeded
	}
	var victims []string
	for _, held := range s.holds {
		reclaims := false
		for resource, amount := range held.loans[workspace] {
			if deficit[resource] == 0 {
				continue
			}
			reclaims = true
			if amount >= deficit[resource] {
				delete(deficit, resource)
			} else {
				deficit[resource] -= amount
			}
		}
		if reclaims {
			victims = append(victims, held.id)
		}
		if len(deficit) == 0 {
			return victims, nil
		}
	}
	return nil, ErrQuotaPolicyUnavailable
}

func requireReclaimReleases(ctx context.Context, q transaction.Executor, tenant string, victims []string) error {
	// A missing reservation cannot serve as release evidence.
	var released bool
	err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT count(*)=cardinality($2::uuid[]) FROM scheduling_reservations
 WHERE tenant_id=$1 AND attempt_id=ANY($2::uuid[]) AND released_at IS NOT NULL
 AND site_id=local_site_id() AND cell_id=local_cell_id()`, tenant, victims)).Scan(&released)
	if err != nil {
		return err
	}
	if !released {
		return errReclaimReleasePending
	}
	return nil
}

package scheduling

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

var (
	// ErrBorrowingUnavailable indicates that eligible idle guarantees cannot fund the attempt.
	ErrBorrowingUnavailable = errors.New("SCHEDULING_BORROWING_UNAVAILABLE")
	// ErrReclaimRequired indicates a request within guarantee is blocked by its workspace's loans.
	ErrReclaimRequired = errors.New("SCHEDULING_RECLAIM_REQUIRED")
	// ErrBorrowingPolicyInUse prevents changes to a cohort with held work or pending reclaim.
	ErrBorrowingPolicyInUse = errors.New("SCHEDULING_BORROWING_POLICY_IN_USE")
)

// loanAllocation maps a lender workspace to the exact resources it lends.
type loanAllocation map[string]Resources

type loanHold struct {
	id    string
	loans loanAllocation
}

type cohortUsage struct {
	owned   map[string]Resources
	lent    map[string]Resources
	holds   []loanHold
	pending map[string]bool
}

func (t *QuotaTree) borrowingMembers(department string) map[string]Quota {
	if !t.nodes[department].Borrowing {
		return nil
	}
	members := make(map[string]Quota)
	for _, n := range t.nodes {
		if n.Level == WorkspaceQuota && n.ParentID == department {
			members[n.ID] = n.Limits
		}
	}
	return members
}

func lockBorrowingCohort(ctx context.Context, q transaction.Executor, tenant, department string) error {
	const cohortLockNamespace = 713090004
	_, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL,
		"SELECT pg_advisory_xact_lock($1,hashtext($2))", cohortLockNamespace, tenant+"/"+department))
	return err
}

func readCohortUsage(ctx context.Context, q transaction.Executor, tenant, department string, members map[string]Quota) (cohortUsage, error) {
	state := cohortUsage{owned: make(map[string]Resources), lent: make(map[string]Resources), pending: make(map[string]bool)}
	ids := slices.Collect(maps.Keys(members))
	for _, id := range ids {
		state.owned[id], state.lent[id] = make(Resources), make(Resources)
	}
	rows, err := q.Query(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT attempt_id::text,workspace_id::text,demand,loan_allocations,
 (site_id=local_site_id() AND cell_id=local_cell_id()) IS TRUE
 FROM scheduling_reservations WHERE tenant_id=$1 AND workspace_id=ANY($2::uuid[]) AND released_at IS NULL
 ORDER BY borrowing_order DESC`, tenant, ids))
	if err != nil {
		return cohortUsage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, workspace string
		var demandJSON, loansJSON []byte
		var local bool
		if err := rows.Scan(&id, &workspace, &demandJSON, &loansJSON, &local); err != nil {
			return cohortUsage{}, err
		}
		var demand Resources
		var loans loanAllocation
		if !local || json.Unmarshal(demandJSON, &demand) != nil || json.Unmarshal(loansJSON, &loans) != nil ||
			state.addHold(workspace, demand, loans) != nil {
			return cohortUsage{}, ErrQuotaPolicyUnavailable
		}
		state.holds = append(state.holds, loanHold{id, loans})
	}
	if err := rows.Err(); err != nil {
		return cohortUsage{}, err
	}
	rows.Close()
	rows, err = q.Query(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT workspace_id::text,(site_id=local_site_id() AND cell_id=local_cell_id()) IS TRUE
 FROM scheduling_quota_reclaims
 WHERE tenant_id=$1 AND cohort_id=$2 AND state='pending'`, tenant, department))
	if err != nil {
		return cohortUsage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var workspace string
		var local bool
		if err := rows.Scan(&workspace, &local); err != nil {
			return cohortUsage{}, err
		}
		if !local {
			return cohortUsage{}, ErrQuotaPolicyUnavailable
		}
		state.pending[workspace] = true
	}
	return state, rows.Err()
}

func (s cohortUsage) addHold(workspace string, demand Resources, loans loanAllocation) error {
	owned, err := normalizeResources(demand)
	if err != nil || len(owned) == 0 || s.owned[workspace] == nil {
		return ErrQuotaPolicyUnavailable
	}
	for lender, resources := range loans {
		amounts, err := normalizeResources(resources)
		if err != nil || lender == workspace || s.lent[lender] == nil || len(amounts) == 0 || !amounts.fits(owned) {
			return ErrQuotaPolicyUnavailable
		}
		for resource, quantity := range amounts {
			owned[resource] -= quantity
		}
		if err := s.lent[lender].addScaled(amounts, 1); err != nil {
			return err
		}
	}
	return s.owned[workspace].addScaled(owned, 1)
}

func (s cohortUsage) allocate(workspace string, members map[string]Quota, demand Resources, hasClaim bool) (loanAllocation, error) {
	if !hasClaim && s.pending[workspace] {
		return nil, ErrReclaimPending
	}
	need := make(Resources)
	var ownLent bool
	whollyNominal := true
	for resource, requested := range demand {
		room := max(0, members[workspace].Guaranteed[resource]-s.owned[workspace][resource])
		own := min(requested, room)
		if own > max(0, room-s.lent[workspace][resource]) {
			ownLent = true
		}
		if requested > room {
			whollyNominal = false
			need[resource] = requested - room
		}
	}
	if ownLent {
		if whollyNominal {
			return nil, ErrReclaimRequired
		}
		return nil, ErrBorrowingUnavailable
	}
	if len(need) == 0 {
		return nil, nil
	}
	loans := make(loanAllocation)
	for _, lender := range slices.Sorted(maps.Keys(members)) {
		if lender == workspace || s.pending[lender] {
			continue
		}
		for resource, remaining := range need {
			room := max(0, members[lender].Guaranteed[resource]-s.owned[lender][resource])
			amount := min(remaining, max(0, room-s.lent[lender][resource]))
			if amount == 0 {
				continue
			}
			if loans[lender] == nil {
				loans[lender] = make(Resources)
			}
			loans[lender][resource] = amount
			if amount == remaining {
				delete(need, resource)
			} else {
				need[resource] -= amount
			}
		}
		if len(need) == 0 {
			return loans, nil
		}
	}
	return nil, ErrBorrowingUnavailable
}

func borrowingForReservation(ctx context.Context, q transaction.Executor, a Attempt, tree *QuotaTree, workspace QuotaNode, body []byte, demand Resources) (loanAllocation, bool, error) {
	members := tree.borrowingMembers(workspace.ParentID)
	if members == nil {
		return nil, false, nil
	}
	if err := lockBorrowingCohort(ctx, q, a.TenantID, workspace.ParentID); err != nil {
		return nil, false, err
	}
	plan, err := readReclaimWithin(ctx, q, a, body)
	if err != nil {
		return nil, false, err
	}
	if plan.State == ReclaimAdmitted {
		return nil, false, ErrReservationConflict
	}
	claimed := plan.State == ReclaimPending
	if claimed {
		if err := requireReclaimReleases(ctx, q, a.TenantID, plan.Victims); err != nil {
			return nil, false, err
		}
	}
	state, err := readCohortUsage(ctx, q, a.TenantID, workspace.ParentID, members)
	if err != nil {
		return nil, false, err
	}
	loans, err := state.allocate(a.WorkspaceID, members, demand, claimed)
	return loans, claimed, err
}

func recordBorrowingWithin(ctx context.Context, q transaction.Executor, a Attempt, loans loanAllocation, reclaimed bool) error {
	if len(loans) != 0 {
		data, err := json.Marshal(loans)
		if err != nil {
			return err
		}
		if err := updateBorrowingRow(ctx, q, `UPDATE scheduling_reservations SET loan_allocations=$2::jsonb WHERE attempt_id=$1 AND site_id=local_site_id() AND cell_id=local_cell_id()`, a.ID, data); err != nil {
			return err
		}
	}
	if reclaimed {
		return updateBorrowingRow(ctx, q, `UPDATE scheduling_quota_reclaims SET state='admitted' WHERE attempt_id=$1 AND state='pending' AND site_id=local_site_id() AND cell_id=local_cell_id()`, a.ID)
	}
	return nil
}

func updateBorrowingRow(ctx context.Context, q transaction.Executor, stmt string, args ...any) error {
	count, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, stmt, args...))
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrReservationConflict
	}
	return nil
}

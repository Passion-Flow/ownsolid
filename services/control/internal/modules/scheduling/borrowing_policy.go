package scheduling

import (
	"context"
	"maps"
	"reflect"
	"slices"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func checkBorrowingPolicyWithin(ctx context.Context, q transaction.Executor, previous, next *QuotaTree) error {
	departments := make(map[string]bool)
	for _, tree := range []*QuotaTree{previous, next} {
		if tree == nil {
			continue
		}
		for id, node := range tree.nodes {
			if node.Borrowing {
				departments[id] = true
			}
		}
	}
	var changed []string
	affected := make(map[string]bool)
	for id := range departments {
		var oldMembers map[string]Quota
		var oldTenant string
		if previous != nil {
			oldMembers, oldTenant = previous.borrowingMembers(id), previous.nodes[id].TenantID
		}
		newMembers := next.borrowingMembers(id)
		if oldTenant == next.nodes[id].TenantID && reflect.DeepEqual(oldMembers, newMembers) {
			continue
		}
		changed = append(changed, id)
		for workspace := range oldMembers {
			affected[workspace] = true
		}
		for workspace := range newMembers {
			affected[workspace] = true
		}
	}
	if len(changed) == 0 {
		return nil
	}
	// The exclusive policy lock prevents a new hold or claim between this check
	// and the policy write. Idle cutover also excludes untracked legacy bursts.
	var busy bool
	err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT EXISTS (SELECT 1 FROM scheduling_reservations
 WHERE workspace_id::text=ANY($1::text[]) AND released_at IS NULL)
 OR EXISTS (SELECT 1 FROM scheduling_quota_reclaims
 WHERE cohort_id=ANY($2::text[]) AND state='pending')`,
		slices.Collect(maps.Keys(affected)), changed)).Scan(&busy)
	if err != nil {
		return policyError(ctx)
	}
	if busy {
		return ErrBorrowingPolicyInUse
	}
	return nil
}

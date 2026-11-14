package scheduling

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ErrQueueTurnPending means another eligible entry currently precedes this attempt.
var ErrQueueTurnPending = errors.New("SCHEDULING_QUEUE_TURN_PENDING")

// QueueReader reads the owner's complete, currently eligible queue through the
// supplied transaction after the tenant admission fence is held. It derives
// scope, priority and order from persisted authority and must not write, acquire
// other jobs' row locks, open another transaction or perform network I/O.
type QueueReader func(context.Context, transaction.Executor) ([]QueueEntry, error)

// ReserveQueuedWithin reserves a current queue head in the workload owner's
// READ COMMITTED transaction. The owner supplies its authorized workspace domain,
// validates the selected job and commits dequeue, physical holds and audit in this
// same transaction. Invoke it before other scheduling writes in the transaction.
// False with no error is an exact held replay and must not enqueue another task.
// The caller must roll back on error and publish no result until commit.
func ReserveQueuedWithin(ctx context.Context, q transaction.Executor, a Attempt, intent JobIntent, workspaces []string, read QueueReader) (bool, error) {
	body, demand, err := reservationPayload(a, intent)
	if err != nil {
		return false, err
	}
	if read == nil {
		return false, ErrInvalidQueue
	}
	domain := slices.Clone(workspaces)
	slices.Sort(domain)
	domain = slices.Compact(domain)
	if _, found := slices.BinarySearch(domain, a.WorkspaceID); !found {
		return false, ErrQuotaScopeNotFound
	}
	for _, id := range domain {
		if !canonicalReservationID(id) {
			return false, ErrQuotaScopeNotFound
		}
	}
	if _, err := lockReservationPolicy(ctx, q); err != nil {
		return false, err
	}
	_, tree, err := readQuotaPolicyWithin(ctx, q)
	if err != nil {
		return false, err
	}
	if tree == nil {
		return false, ErrQuotaUnconfigured
	}
	for _, id := range domain {
		if _, err := tree.workspace(a.TenantID, id); err != nil {
			return false, err
		}
	}
	if err := lockReservationTenant(ctx, q, a.TenantID, true); err != nil {
		return false, err
	}
	if err := lockReservationWorkspace(ctx, q, a.TenantID, a.WorkspaceID); err != nil {
		return false, err
	}
	requested, err := json.Marshal(demand)
	if err != nil {
		return false, ErrInvalidResources
	}
	replayed, err := reservationReplayWithin(ctx, q, a, body, requested)
	if err != nil || replayed {
		return false, err
	}
	entries, err := read(ctx, q)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if _, found := slices.BinarySearch(domain, entry.QueueID); !found {
			return false, ErrQuotaScopeNotFound
		}
	}
	ranked, err := rankWorkspaceQueueWithin(ctx, q, a.TenantID, tree, entries)
	if err != nil {
		return false, err
	}
	position := slices.IndexFunc(ranked, func(entry QueueEntry) bool {
		return entry.JobID == a.SourceID && entry.QueueID == a.WorkspaceID
	})
	if position < 0 {
		return false, ErrInvalidQueue
	}
	if position > 0 {
		return false, ErrQueueTurnPending
	}
	return ReserveWithin(ctx, q, a, intent)
}

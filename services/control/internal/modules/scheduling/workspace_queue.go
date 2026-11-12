package scheduling

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
)

// ErrQueueSnapshotRequired requires a read-only PostgreSQL REPEATABLE READ transaction.
var ErrQueueSnapshotRequired = errors.New("SCHEDULING_QUEUE_SNAPSHOT_REQUIRED")

// RankWorkspaceQueueWithin orders the owner's authorized, eligible entries for
// one tenant. QueueID identifies the entry's workspace and its burst allocation
// supplies the queue capacity. The owner assigns priority and durable sequence,
// and opens a fresh read-only REPEATABLE READ snapshot after each dispatch.
func RankWorkspaceQueueWithin(ctx context.Context, tx pgx.Tx, tenant string, entries []QueueEntry) ([]QueueEntry, error) {
	if tx == nil {
		return nil, ErrQueueSnapshotRequired
	}
	return RankWorkspaceQueueExecutorWithin(ctx, transaction.PostgreSQLTransaction(tx), tenant, entries)
}

// RankWorkspaceQueueExecutorWithin ranks the owner's authorized eligible batch
// through its selected borrowed PostgreSQL read-only REPEATABLE READ executor.
// Read the entries in that same snapshot; the caller retains transaction
// completion and must recheck current admission in a fresh write transaction.
func RankWorkspaceQueueExecutorWithin(ctx context.Context, q transaction.Executor, tenant string, entries []QueueEntry) ([]QueueEntry, error) {
	if err := requireSchedulingExecutorSnapshot(ctx, q, ErrQueueSnapshotRequired); err != nil {
		return nil, err
	}
	if !canonicalReservationID(tenant) {
		return nil, ErrQuotaScopeNotFound
	}
	if len(entries) == 0 {
		return RankQueue(entries, nil)
	}
	_, tree, err := readQuotaPolicyWithin(ctx, q)
	if err != nil {
		return nil, err
	}
	if tree == nil {
		return nil, ErrQuotaUnconfigured
	}

	queues := make(map[string]QueueShare)
	var workspaces []string
	for _, entry := range entries {
		id := entry.QueueID
		if _, checked := queues[id]; checked {
			continue
		}
		if !canonicalReservationID(id) {
			return nil, ErrQuotaScopeNotFound
		}
		workspace, err := tree.workspace(tenant, id)
		if err != nil {
			return nil, err
		}
		queues[id] = QueueShare{Capacity: workspace.Limits.Burst}
		workspaces = append(workspaces, id)
	}
	usage, err := readUsageByWorkspaceWithin(ctx, q, tenant, workspaces)
	if err != nil {
		return nil, err
	}
	for id, queue := range queues {
		queue.Reserved = usage[id]
		queues[id] = queue
	}
	return RankQueue(entries, queues)
}

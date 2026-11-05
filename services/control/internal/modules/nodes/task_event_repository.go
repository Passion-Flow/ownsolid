package nodes

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// TaskEventSourceWithin returns current scoped facts for event correlation. It
// borrows the caller's real selected transaction and never claims, renews,
// completes, cancels or republishes a physical task. A human caller first holds
// its ordinary current Identity fence; this helper adds no human authority.
func (s *Service) TaskEventSourceWithin(ctx context.Context, q transaction.Executor, tenant, workspace, node, task string) (Node, Task, error) {
	return s.taskEventSourceWithin(ctx, q, tenant, workspace, node, task, true)
}

// TaskEventHistorySourceWithin reads retained facts for expiry/retention and the
// restore owner. It adds no human or machine authority; inactive Workspace facts
// cannot authorize task claim, execution, renewal or user-visible reads.
func (s *Service) TaskEventHistorySourceWithin(ctx context.Context, q transaction.Executor, tenant, workspace, node, task string) (Node, Task, error) {
	return s.taskEventSourceWithin(ctx, q, tenant, workspace, node, task, false)
}
func (s *Service) taskEventSourceWithin(ctx context.Context, q transaction.Executor, tenant, workspace, node, task string, requireActive bool) (Node, Task, error) {
	if ctx == nil || q == nil {
		return Node{}, Task{}, fail("STORAGE_UNAVAILABLE")
	}
	if _, pool := q.(transaction.Store); pool {
		return Node{}, Task{}, fail("STORAGE_UNAVAILABLE")
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return Node{}, Task{}, fail("STORAGE_UNAVAILABLE")
	}
	for _, value := range []string{tenant, workspace, node, task} {
		if !nodeID(value) {
			return Node{}, Task{}, fail("RESOURCE_NOT_FOUND")
		}
	}
	// Reuse the existing selected task executor/pool coherence contract.
	if err := s.taskExecutor(ctx, q); err != nil {
		return Node{}, Task{}, err
	}
	// The caller holds the node lock, which fences this read on PostgreSQL (ADR 0020); a
	// Workspace row lock here would make every result wait on, or starve, the Workspace's
	// writers. Native DM uses the stronger FOR UPDATE fence: its caller must already
	// acquire the Workspace fence before its node lock, including every publishing callback.
	var status string
	err := q.QueryRow(ctx, nodeSQL(q,
		"SELECT status FROM workspaces WHERE tenant_id=$1 AND id=$2",
		"SELECT status FROM workspaces WHERE tenant_id=? AND id=? FOR SHARE", `SELECT status FROM workspaces WHERE tenant_id=? AND id=? FOR UPDATE`, tenant, workspace)).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, Task{}, fail("RESOURCE_NOT_FOUND")
	}
	if err != nil {
		return Node{}, Task{}, nodeRepositoryError(err)
	}
	if requireActive && status != "active" {
		return Node{}, Task{}, fail("NODE_UNAVAILABLE")
	}
	installed, err := nodeTenant(ctx, q)
	if err != nil {
		return Node{}, Task{}, nodeRepositoryError(err)
	}
	if installed != tenant {
		return Node{}, Task{}, fail("RESOURCE_NOT_FOUND")
	}
	current, err := s.lockedUpdateNode(ctx, q, tenant, workspace, node)
	if err != nil {
		return Node{}, Task{}, nodeRepositoryError(err)
	}
	value, err := s.taskByIDWithin(ctx, q, current, task, true)
	if err != nil {
		return Node{}, Task{}, nodeRepositoryError(err)
	}
	return current, value, nil
}

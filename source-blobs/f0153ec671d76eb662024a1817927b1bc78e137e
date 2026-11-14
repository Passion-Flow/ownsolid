package nodes

import (
	"context"
	"sync"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// taskPruneCursor is the last finished task a pass looked at. Tasks that other tables still refer
// to stay, so a walk from the start would read them forever: each pass moves on and returns to the
// start only at the end of the list.
type taskPruneCursor struct {
	mu sync.Mutex
	at time.Time
	id string
}

func (c *taskPruneCursor) read() (time.Time, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.id == "" {
		return time.Unix(0, 0).UTC(), "00000000-0000-0000-0000-000000000000"
	}
	return c.at, c.id
}

func (c *taskPruneCursor) put(at time.Time, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at, c.id = at, id
}

// pruneTasks removes one batch of finished tasks past their retention from node_tasks. It removes
// only rows with a history record and that nothing refers to, and its cost is one batch of index
// reads whatever the table holds. Other chains keep their tasks until CMP-201 and CMP-202.
func (s *Service) pruneTasks(ctx context.Context) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	if store.Dialect() != transaction.PostgreSQL {
		return nil
	}
	policy := s.Identity.Config.Policy()
	after, id := s.prune.read()
	var examined int
	var last time.Time
	var lastID string
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		return q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `WITH candidates AS (
 SELECT id,completed_at FROM node_tasks WHERE completed_at IS NOT NULL AND completed_at<clock_timestamp()-make_interval(days=>$1) AND (completed_at,id)>($2,$3::uuid) ORDER BY completed_at,id LIMIT $4
), removed AS (
 DELETE FROM node_tasks t USING candidates c WHERE t.id=c.id
  AND EXISTS(SELECT 1 FROM node_task_history h WHERE h.id=c.id AND h.completed_at=c.completed_at)
  AND NOT EXISTS(SELECT 1 FROM managed_components x WHERE x.last_task_id=c.id)
  AND NOT EXISTS(SELECT 1 FROM deployments x WHERE x.last_task_id=c.id)
  AND NOT EXISTS(SELECT 1 FROM training_attempts x WHERE x.task_id=c.id)
  AND NOT EXISTS(SELECT 1 FROM model_preparation_attempts x WHERE x.task_id=c.id)
  AND NOT EXISTS(SELECT 1 FROM task_event_outbox x WHERE x.task_id=c.id)
  AND NOT EXISTS(SELECT 1 FROM image_registry_grants x WHERE x.task_id=c.id)
 RETURNING t.id
)
SELECT (SELECT count(*) FROM candidates),COALESCE((SELECT max(completed_at) FROM candidates),'epoch'::timestamptz),COALESCE((SELECT id::text FROM candidates ORDER BY completed_at DESC,id DESC LIMIT 1),''),(SELECT count(*) FROM removed)`,
			policy.Retention.TaskDays, after, id, policy.Nodes.TaskSweepBatch)).Scan(&examined, &last, &lastID, new(int))
	})
	if e != nil {
		return e
	}
	if examined < policy.Nodes.TaskSweepBatch {
		s.prune.put(time.Time{}, "")
		return nil
	}
	s.prune.put(last, lastID)
	return nil
}

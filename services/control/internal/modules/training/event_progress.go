package training

import (
	"context"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// eventBatchProgress belongs to one authorized transaction callback. The
// job/attempt locks serialize its members; each event and audit stays separate.
type eventBatchProgress struct {
	sequence, step int64
	loaded, dirty  bool
	started        bool
}

func (p *eventBatchProgress) load(ctx context.Context, q transaction.Executor, w workspaces.Workspace, job, attempt string) error {
	if p.loaded {
		return nil
	}
	err := q.QueryRow(ctx, transferStatement(q,
		"SELECT last_event_sequence,last_global_step FROM training_attempts WHERE tenant_id=$1 AND workspace_id=$2 AND job_id=$3 AND id=$4 FOR UPDATE",
		"SELECT last_event_sequence,last_global_step FROM training_attempts WHERE tenant_id=? AND workspace_id=? AND job_id=? AND id=? FOR UPDATE",
		[]any{w.TenantID, w.ID, job, attempt}, []any{w.TenantID, w.ID, job, attempt})).Scan(&p.sequence, &p.step)
	if err == nil {
		p.loaded = true
	}
	return err
}

func (p *eventBatchProgress) persist(ctx context.Context, q transaction.Executor, w workspaces.Workspace, job, attempt string) error {
	if !p.dirty {
		return nil
	}
	// A failed final write rolls back the event rows, phase change and audits.
	// A duplicate-only batch leaves progress and its timestamp untouched.
	n, err := q.Exec(ctx, transferStatement(q,
		"UPDATE training_attempts SET last_event_sequence=$5,last_global_step=$6,state=CASE WHEN $7 THEN 'running' ELSE state END,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND job_id=$3 AND id=$4",
		"UPDATE training_attempts SET last_event_sequence=?,last_global_step=?,state=CASE WHEN ? THEN 'running' ELSE state END,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND job_id=? AND id=?",
		[]any{w.TenantID, w.ID, job, attempt, p.sequence, p.step, p.started},
		[]any{p.sequence, p.step, p.started, w.TenantID, w.ID, job, attempt}))
	if err != nil {
		return err
	}
	if n != 1 {
		return fail("TRAINING_ATTEMPT_STALE")
	}
	return nil
}

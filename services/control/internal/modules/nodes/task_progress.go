package nodes

import (
	"context"
	"encoding/json"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// RecordTaskProgress stores what a running task reports, under its lease. A repeated report
// changes nothing: an entry is identified by the attempt and its sequence, so an agent resends
// one it has no answer for. Progress never moves a task; only its result does.
func (s *Service) RecordTaskProgress(ctx context.Context, id, credential, taskID string, report wire.TaskProgress) (wire.TaskProgressAccepted, error) {
	var out wire.TaskProgressAccepted
	if e := report.Validate(); e != nil {
		return out, fail(e.Error())
	}
	entries, e := json.Marshal(report.Entries)
	if e != nil {
		return out, fail("TASK_PROGRESS_INVALID")
	}
	e = s.WithLeaseExecutor(ctx, id, credential, taskID, report.LeaseToken, func(ctx context.Context, q transaction.Executor, n Node, t Task) error {
		if q.Dialect() != transaction.PostgreSQL {
			return fail("TASK_PROGRESS_UNAVAILABLE")
		}
		accepted, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO node_task_progress(tenant_id,workspace_id,node_id,task_id,task_created_at,attempt,sequence,kind,step,step_state,level,message,reported_at)
 SELECT $1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,e.sequence,e.kind,e.step,e.state,e.level,e.message,e.at
 FROM jsonb_to_recordset($7::jsonb) AS e(sequence integer,kind text,step text,state text,level text,message text,at timestamptz)
 ON CONFLICT DO NOTHING`, t.TenantID, t.WorkspaceID, t.NodeID, t.ID, t.CreatedAt, t.Attempt, string(entries)))
		if e != nil {
			return e
		}
		out.Accepted = int(accepted)
		return nil
	})
	if e != nil {
		return wire.TaskProgressAccepted{}, e
	}
	return out, nil
}

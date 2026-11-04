package nodes

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"math"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Task delivery (ARC-006, ADR 0011). Sequences, acknowledgements and resync exist on the
// PostgreSQL lineage only; the MySQL and Dameng chains lack the columns until CMP-201 and
// CMP-202, so there leases carry sequence 0, acknowledgements only check the lease, and
// claims never release or resync.

// taskDeliveriesExhausted is the failure of a task that ran out of deliveries or attempts.
const taskDeliveriesExhausted = "TASK_ATTEMPTS_EXHAUSTED"

// resyncBucket is the fleet-wide token bucket for full resyncs in this control process.
// The per-node interval is stored on the node, so it holds across processes.
type resyncBucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// take spends one token. The bucket holds at most one second of grants.
func (b *resyncBucket) take(rate int, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.last.IsZero() {
		b.tokens = float64(rate)
	} else if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(float64(rate), b.tokens+elapsed*float64(rate))
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// resyncDecision reports whether an agent at cursor must resync with control, whose node
// has assigned sequence assigned, recorded acknowledged as its highest acknowledged
// sequence, and holds the unfinished task inflight (zero when none).
func resyncDecision(cursor, assigned, acknowledged, inflight uint64) bool {
	return cursor > assigned || cursor < acknowledged || inflight != 0 && inflight < cursor
}

// claimPreflight runs before every claim path. It releases a lease the agent never
// acknowledged: the agent claims only when it holds no task, so such a lease was lost on
// the way. With a cursor, it also decides whether the agent must resync first.
func (s *Service) claimPreflight(ctx context.Context, id, credential, request string, cursor *uint64) (*wire.Resync, error) {
	var out *wire.Resync
	deferred := false
	e := s.machineWithin(ctx, id, credential, func(ctx context.Context, q transaction.Executor, n Node, active bool, _ pgx.Tx) error {
		if !active || n.Status != "active" {
			return fail("ACCESS_DENIED")
		}
		if q.Dialect() != transaction.PostgreSQL {
			return nil
		}
		if e := s.releaseUnacknowledgedWithin(ctx, q, n, request, false); e != nil {
			return e
		}
		if cursor == nil {
			return nil
		}
		var assigned, acknowledged int64
		var inflight sql.NullInt64
		var recent bool
		policy := s.Identity.Config.Policy().Nodes
		if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT n.task_sequence,n.task_acknowledged_sequence,COALESCE(n.resync_granted_at>clock_timestamp()-make_interval(secs=>$4),false),(SELECT t.sequence FROM node_tasks t WHERE t.tenant_id=n.tenant_id AND t.workspace_id=n.workspace_id AND t.node_id=n.id AND t.state IN ('pending','running')) FROM managed_nodes n WHERE n.tenant_id=$1 AND n.workspace_id=$2 AND n.id=$3 FOR UPDATE`, n.TenantID, n.WorkspaceID, n.ID, policy.ResyncIntervalSeconds)).Scan(&assigned, &acknowledged, &recent, &inflight); e != nil {
			return e
		}
		if !resyncDecision(*cursor, uint64(assigned), uint64(acknowledged), uint64(inflight.Int64)) {
			return nil
		}
		if recent || !s.resync.take(policy.ResyncPerSecond, time.Now()) {
			deferred = true
			return nil
		}
		if _, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE managed_nodes SET resync_granted_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3`, n.TenantID, n.WorkspaceID, n.ID)); e != nil {
			return e
		}
		if _, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) VALUES($1,clock_timestamp(),$2,$3,'system','node.resync_granted','success','node',$4,$5)`, uuid.NewString(), n.TenantID, n.WorkspaceID, n.ID, request)); e != nil {
			return e
		}
		out = &wire.Resync{AcknowledgedSequence: uint64(acknowledged)}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if deferred {
		// The release above committed; the agent retries the resync after the hint.
		return nil, &identity.RateError{RetryAfter: wire.HeartbeatSeconds}
	}
	return out, nil
}

// releaseUnacknowledgedWithin returns a running lease that was never acknowledged to
// pending without spending an attempt, so any claim path delivers it again. With
// expiredOnly it releases only a lease that has expired. A task delivered
// MaximumTaskDeliveries times without an acknowledgement fails instead. Either way the
// attempt goes back, so attempt 0 always means no agent acknowledged the task.
func (s *Service) releaseUnacknowledgedWithin(ctx context.Context, q transaction.Executor, n Node, request string, expiredOnly bool) error {
	if q.Dialect() != transaction.PostgreSQL {
		return nil
	}
	var id string
	var deliveries int
	e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT id::text,deliveries FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND state='running' AND acknowledged_attempt<attempt AND ($4=false OR lease_expires_at<=clock_timestamp()) FOR UPDATE`, n.TenantID, n.WorkspaceID, n.ID, expiredOnly)).Scan(&id, &deliveries)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if deliveries >= s.Identity.Config.Policy().Nodes.MaximumTaskDeliveries {
		changed, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE node_tasks SET state='failed',error_code=$5,attempt=attempt-1,lease_token_hash=NULL,lease_expires_at=NULL,completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 AND state='running'`, n.TenantID, n.WorkspaceID, n.ID, id, taskDeliveriesExhausted))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("AGENT_LEASE_STALE")
		}
		return s.taskAuditWithin(ctx, q, "", "system", n, "node.task_failed", id, "", request)
	}
	changed, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE node_tasks SET state='pending',attempt=attempt-1,lease_token_hash=NULL,lease_expires_at=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 AND state='running' AND acknowledged_attempt<attempt`, n.TenantID, n.WorkspaceID, n.ID, id))
	if e != nil {
		return e
	}
	if changed != 1 {
		return fail("AGENT_LEASE_STALE")
	}
	return s.taskAuditWithin(ctx, q, "", "system", n, "node.task_released", id, "", request)
}

// ReleaseUnacknowledgedWithin lets the owners of training and model preparation tasks
// release an expired lease nobody acknowledged before they judge whether it was delivered.
func (s *Service) ReleaseUnacknowledgedWithin(ctx context.Context, q transaction.Executor, n Node, request string) error {
	return s.releaseUnacknowledgedWithin(ctx, q, n, request, true)
}

// NeverDelivered reports whether a task proves that no agent executed its command. An
// acknowledged attempt is never given back, so attempt 0 means no agent acknowledged a
// lease, and a stop authority bound to a lease that was released reached no executing
// agent. The task must be pending, or failed by the delivery bound: attempt 0 with
// TASK_ATTEMPTS_EXHAUSTED is written only there, because a task that runs out of attempts
// has spent them. Any other finished task proves nothing and keeps its physical owner.
func NeverDelivered(state string, code *string, attempt int, events int64, hasOutcome bool) bool {
	if attempt != 0 || events != 0 || hasOutcome {
		return false
	}
	return state == "pending" || state == "failed" && code != nil && *code == taskDeliveriesExhausted
}

// deliveredLeaseWithin reads what a just-issued lease needs from its task row and counts
// the delivery: the lease expiry and, on PostgreSQL, the task sequence.
func deliveredLeaseWithin(ctx context.Context, q transaction.Executor, n Node, id string, lease *wire.Lease) error {
	if q.Dialect() != transaction.PostgreSQL {
		return q.QueryRow(ctx, nodeSQL(q, "", "SELECT lease_expires_at FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?", `SELECT lease_expires_at FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, n.TenantID, n.WorkspaceID, n.ID, id)).Scan(&lease.LeaseExpiresAt)
	}
	var sequence int64
	if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE node_tasks SET deliveries=deliveries+1 WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 RETURNING lease_expires_at,sequence`, n.TenantID, n.WorkspaceID, n.ID, id)).Scan(&lease.LeaseExpiresAt, &sequence); e != nil {
		return e
	}
	lease.Sequence = uint64(sequence)
	return nil
}

// Acknowledge confirms that the node's agent holds a lease and is about to execute it,
// and renews the lease. Acknowledging the same lease again answers the same way.
func (s *Service) Acknowledge(ctx context.Context, id, credential, taskID string, input wire.TaskAcknowledgementRequest, request string) (wire.RenewedLease, error) {
	var out wire.RenewedLease
	if !nodeID(taskID) || !nodeID(request) || !security.ValidToken(input.LeaseToken) || input.Sequence > wire.MaxTaskSequence {
		return out, fail("VALIDATION_FAILED")
	}
	store, e := s.repository()
	if e != nil {
		return out, e
	}
	if store.Dialect() != transaction.PostgreSQL {
		// Without sequences an acknowledgement is a renewal of the lease it names.
		if input.Sequence != 0 {
			return out, fail("AGENT_LEASE_STALE")
		}
		return s.renewSelected(ctx, id, credential, taskID, input.LeaseToken)
	}
	e = s.machineWithin(ctx, id, credential, func(ctx context.Context, q transaction.Executor, n Node, active bool, _ pgx.Tx) error {
		if !active || n.Status != "active" {
			return fail("ACCESS_DENIED")
		}
		var hash []byte
		var state string
		var sequence int64
		if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT state,lease_token_hash,sequence FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 AND lease_expires_at>clock_timestamp() AND deadline_at>clock_timestamp() FOR UPDATE`, n.TenantID, n.WorkspaceID, n.ID, taskID)).Scan(&state, &hash, &sequence); e != nil {
			return leaseMissing(e)
		}
		if state != "running" || uint64(sequence) != input.Sequence || subtle.ConstantTimeCompare(hash, digest("node-lease", input.LeaseToken)) != 1 {
			return fail("AGENT_LEASE_STALE")
		}
		if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE node_tasks SET acknowledged_attempt=attempt,lease_expires_at=LEAST(clock_timestamp()+make_interval(secs=>$5),deadline_at),updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 RETURNING lease_expires_at,GREATEST(0,floor(extract(epoch FROM (lease_expires_at-clock_timestamp()))*1000))::integer`, n.TenantID, n.WorkspaceID, n.ID, taskID, s.Identity.Config.Policy().Nodes.TaskLeaseSeconds)).Scan(&out.LeaseExpiresAt, &out.ValidForMilliseconds); e != nil {
			return e
		}
		_, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE managed_nodes SET task_acknowledged_sequence=GREATEST(task_acknowledged_sequence,$4) WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3`, n.TenantID, n.WorkspaceID, n.ID, sequence))
		return e
	})
	if e != nil {
		return wire.RenewedLease{}, e
	}
	out.LeaseExpiresAt = out.LeaseExpiresAt.UTC()
	return out, nil
}

func leaseMissing(e error) error {
	if errors.Is(e, sql.ErrNoRows) {
		return fail("AGENT_LEASE_STALE")
	}
	return e
}

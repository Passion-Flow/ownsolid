package nodes

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Service) createTaskSelected(ctx context.Context, a identity.Auth, id, operation, key, request string) (Task, error) {
	if operation != wire.CollectFacts {
		return Task{}, fail("AGENT_OPERATION_UNSUPPORTED")
	}
	if !nodeID(id) || !nodeID(key) || !nodeID(request) {
		return Task{}, fail("VALIDATION_FAILED")
	}
	var out Task
	e := s.taskActor(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		n, e := s.taskNodeWithin(ctx, q, a, id)
		if e != nil {
			return e
		}
		if !nodeOperationAllowed(n, operation) {
			return fail("AGENT_PROFILE_REJECTED")
		}
		previous, e := s.taskReplayWithin(ctx, q, n, key)
		if e == nil {
			if previous.Operation != operation {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out = previous
			return nil
		}
		if e.Error() != "RESOURCE_NOT_FOUND" {
			return e
		}
		if e = s.taskIdleWithin(ctx, q, n, request); e != nil {
			return e
		}
		out, e = s.insertTaskWithin(ctx, q, a.Me.User.ID, n, operation, key, nil, nil, s.Identity.Config.Policy().Nodes.TaskSeconds, nil)
		if e != nil {
			return e
		}
		return s.taskAuditWithin(ctx, q, a.Me.User.ID, "admin", n, "node.task_requested", out.ID, "", request)
	})
	if e != nil {
		return Task{}, e
	}
	return out, nil
}
func (s *Service) componentCommandWithin(ctx context.Context, q transaction.Executor, n Node, t Task) (wire.ComponentCommand, error) {
	return s.ReadComponentCommandWithin(ctx, q, n, t)
}

func (s *Service) claimSelected(ctx context.Context, id, credential, request string, cursor *uint64) (wire.Claim, error) {
	if !nodeID(request) {
		return wire.Claim{}, fail("VALIDATION_FAILED")
	}
	var out wire.Claim
	var rateLimited, delegate, separate bool
	e := s.machineWithin(ctx, id, credential, func(ctx context.Context, q transaction.Executor, n Node, active bool, legacy pgx.Tx) error {
		if !active || n.Status != "active" {
			return fail("ACCESS_DENIED")
		}
		preflight := transaction.Watch(q)
		var err error
		out.Resync, rateLimited, err = s.claimPreflightWithin(ctx, preflight, n, request, cursor)
		if err != nil || out.Resync != nil || rateLimited {
			return err
		}
		postgres := q.Dialect() == transaction.PostgreSQL
		delegate = !postgres ||
			s.Preparations != nil && wire.OperationAllowed(n.ExecutionProfile, wire.ModelPreparationRunOperation) ||
			s.Training != nil && wire.OperationAllowed(n.ExecutionProfile, wire.TrainingRunOperation)
		// A release must stay committed even if the next claim is refused. Delegated
		// owners also open their own transactions under their own lock order.
		separate = delegate || preflight.Changed()
		if separate {
			return nil
		}
		return s.claimNodeWithin(ctx, q, n, request, legacy, &out)
	})
	if e != nil {
		return wire.Claim{}, e
	}
	if rateLimited {
		return wire.Claim{}, &identity.RateError{RetryAfter: wire.HeartbeatSeconds}
	}
	if !separate {
		return out, nil
	}
	if delegate {
		if claim, handled, e := s.claimDelegated(ctx, id, credential, request); handled || e != nil {
			return claim, e
		}
	}
	e = s.machineWithin(ctx, id, credential, func(ctx context.Context, q transaction.Executor, n Node, active bool, legacy pgx.Tx) error {
		if !active || n.Status != "active" {
			return fail("ACCESS_DENIED")
		}
		return s.claimNodeWithin(ctx, q, n, request, legacy, &out)
	})
	if e != nil {
		return wire.Claim{}, e
	}
	return out, nil
}

func (s *Service) claimDelegated(ctx context.Context, id, credential, request string) (wire.Claim, bool, error) {
	if s.Preparations != nil {
		if out, handled, e := s.Preparations.ClaimPreparation(ctx, id, credential, request); handled || e != nil {
			if e != nil {
				return wire.Claim{}, true, e
			}
			return out, true, nil
		}
	}
	if s.Training != nil {
		if out, handled, e := s.Training.ClaimTraining(ctx, id, credential, request); handled || e != nil {
			if e != nil {
				return wire.Claim{}, true, e
			}
			return out, true, nil
		}
	}
	return wire.Claim{}, false, nil
}

func (s *Service) claimNodeWithin(ctx context.Context, q transaction.Executor, n Node, request string, legacy pgx.Tx, out *wire.Claim) error {
	if e := s.expireTasksWithin(ctx, q, n, request); e != nil {
		return e
	}
	var taskID, operation, state string
	var unexpired bool
	var attempt int
	e := q.QueryRow(ctx, nodeSQL(q, `SELECT id::text,operation,state,attempt,COALESCE(lease_expires_at>clock_timestamp(),false) FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND state IN ('pending','running') ORDER BY created_at,id LIMIT 1 FOR UPDATE`, `SELECT id,operation,state,attempt,COALESCE(lease_expires_at>UTC_TIMESTAMP(6),false) FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND state IN ('pending','running') ORDER BY created_at,id LIMIT 1 FOR UPDATE`, `SELECT id,operation,state,attempt,CASE WHEN lease_expires_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND state IN ('pending','running') ORDER BY created_at,id LIMIT 1 FOR UPDATE`, n.TenantID, n.WorkspaceID, n.ID)).Scan(&taskID, &operation, &state, &attempt, &unexpired)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if state == "running" && unexpired {
		return nil
	}
	if operation == wire.TrainingRunOperation {
		return fail("TRAINING_AUTHORITY_REQUIRED")
	}
	if operation == wire.ModelPreparationRunOperation {
		return fail("MODEL_PREPARATION_AUTHORITY_REQUIRED")
	}
	if !nodeOperationAllowed(n, operation) {
		return fail("AGENT_PROFILE_REJECTED")
	}
	if attempt >= s.Identity.Config.Policy().Nodes.MaximumTaskAttempts {
		return fail("AGENT_LEASE_STALE")
	}
	task := Task{ID: taskID, TenantID: n.TenantID, WorkspaceID: n.WorkspaceID, NodeID: n.ID, Operation: operation}
	raw := security.Token()
	lease := wire.Lease{TaskID: taskID, Operation: operation, LeaseToken: raw, Attempt: attempt + 1}
	if wire.IsInferenceOperation(operation) {
		command, e := s.ReadInferenceCommandWithin(ctx, q, n, task)
		if e != nil {
			return e
		}
		lease.Inference = &command
	}
	if wire.IsGatewayOperation(operation) {
		command, e := s.ReadGatewayCommandWithin(ctx, q, n, task)
		if e != nil {
			return e
		}
		lease.Gateway = &command
	}
	if wire.IsComponentOperation(operation) {
		if _, ok := s.Components.(ComponentObserverWithin); !ok && legacy == nil {
			return fail("COMPONENT_PROFILE_UNAVAILABLE")
		}
		command, e := s.componentCommandWithin(ctx, q, n, task)
		if e != nil {
			return e
		}
		lease.Component = &command
	}
	if wire.IsKubernetesOperation(operation) {
		command, e := s.kubernetesLeaseWithin(ctx, q, n, task)
		if e != nil {
			return e
		}
		lease.Kubernetes = &command
	}
	changed, e := q.Exec(ctx, nodeSQL(q, `UPDATE node_tasks SET state='running',attempt=attempt+1,lease_token_hash=$1,lease_expires_at=LEAST(clock_timestamp()+make_interval(secs=>$2),deadline_at),updated_at=clock_timestamp() WHERE tenant_id=$3 AND workspace_id=$4 AND node_id=$5 AND id=$6 AND deadline_at>clock_timestamp() AND attempt=$7`, `UPDATE node_tasks SET state='running',attempt=attempt+1,lease_token_hash=?,lease_expires_at=LEAST(TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),deadline_at),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND deadline_at>UTC_TIMESTAMP(6) AND attempt=?`, `UPDATE node_tasks SET state='running',attempt=attempt+1,lease_token_hash=?,lease_expires_at=LEAST(DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),deadline_at),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND deadline_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) AND attempt=?`, digest("node-lease", raw), s.Identity.Config.Policy().Nodes.TaskLeaseSeconds, n.TenantID, n.WorkspaceID, n.ID, task.ID, attempt))
	if e != nil {
		return e
	}
	if changed != 1 {
		return fail("AGENT_LEASE_STALE")
	}
	if e = deliveredLeaseWithin(ctx, q, n, task.ID, &lease); e != nil {
		return e
	}
	if e = s.taskAuditWithin(ctx, q, "", "system", n, "node.task_claimed", task.ID, "", request); e != nil {
		return e
	}
	if e = s.checkDerivedTaskAuthorityWithin(ctx, q, n, task); e != nil {
		return e
	}
	lease.LeaseExpiresAt = lease.LeaseExpiresAt.UTC()
	out.Lease = &lease
	return nil
}

func (s *Service) validateTaskResultWithin(ctx context.Context, q transaction.Executor, n Node, t Task, input wire.Result) error {
	if t.Operation == wire.TrainingRunOperation {
		return fail("TRAINING_AUTHORITY_REQUIRED")
	}
	if t.Operation == wire.ModelPreparationRunOperation {
		return fail("MODEL_PREPARATION_AUTHORITY_REQUIRED")
	}
	if !nodeOperationAllowed(n, t.Operation) {
		return fail("AGENT_PROFILE_REJECTED")
	}
	if wire.IsKubernetesOperation(t.Operation) {
		command, e := s.ReadKubernetesCommandWithin(ctx, q, n, t)
		if e != nil {
			return e
		}
		return input.ValidateKubernetes(command, t.Operation)
	}
	if input.Kubernetes != nil {
		return fail("VALIDATION_FAILED")
	}
	if wire.IsInferenceOperation(t.Operation) {
		command, e := s.ReadInferenceCommandWithin(ctx, q, n, t)
		if e != nil {
			return e
		}
		return input.ValidateInference(command)
	}
	if input.Inference != nil {
		return fail("VALIDATION_FAILED")
	}
	if wire.IsGatewayOperation(t.Operation) {
		command, e := s.ReadGatewayCommandWithin(ctx, q, n, t)
		if e != nil {
			return e
		}
		return input.ValidateGateway(command)
	}
	if input.Gateway != nil {
		return fail("VALIDATION_FAILED")
	}
	return s.ValidateComponentResultWithin(ctx, q, n, t, input)
}

func (s *Service) completeSelected(ctx context.Context, id, credential, taskID string, input wire.Result, request string) (string, error) {
	if !nodeID(taskID) || !nodeID(request) || !security.ValidToken(input.LeaseToken) {
		return "", fail("VALIDATION_FAILED")
	}
	encoded, e := json.Marshal(input)
	if e != nil {
		return "", fail("VALIDATION_FAILED")
	}
	resultHash := digest("node-result", string(encoded))
	out := ""
	e = s.machineWithin(ctx, id, credential, func(ctx context.Context, q transaction.Executor, n Node, active bool, legacy pgx.Tx) error {
		if !active || n.Status != "active" {
			return fail("ACCESS_DENIED")
		}
		task, e := s.taskByIDWithin(ctx, q, n, taskID, true)
		if e != nil {
			return e
		}
		if e = s.validateTaskResultWithin(ctx, q, n, task, input); e != nil {
			return e
		}
		var hash, completed, previous []byte
		var valid bool
		e = q.QueryRow(ctx, nodeSQL(q, `SELECT lease_token_hash,completed_lease_hash,result_hash,COALESCE(deadline_at>clock_timestamp(),false) FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4`, `SELECT lease_token_hash,completed_lease_hash,result_hash,COALESCE(deadline_at>UTC_TIMESTAMP(6),false) FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, `SELECT lease_token_hash,completed_lease_hash,result_hash,CASE WHEN deadline_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, n.TenantID, n.WorkspaceID, n.ID, task.ID)).Scan(&hash, &completed, &previous, &valid)
		if e != nil {
			return e
		}
		if task.State == "succeeded" || task.State == "failed" {
			if subtle.ConstantTimeCompare(completed, digest("node-lease", input.LeaseToken)) == 1 && subtle.ConstantTimeCompare(previous, resultHash) == 1 {
				out = task.State
				return s.checkDerivedTaskAuthorityWithin(ctx, q, n, task)
			}
			return fail("AGENT_LEASE_STALE")
		}
		if task.State != "running" || !valid || subtle.ConstantTimeCompare(hash, digest("node-lease", input.LeaseToken)) != 1 {
			return fail("AGENT_LEASE_STALE")
		}
		out = "succeeded"
		action := "node.task_succeeded"
		if input.Outcome == "failure" {
			out = "failed"
			action = "node.task_failed"
		} else if task.Operation == wire.CollectFacts {
			raw, e := json.Marshal(input.Facts)
			if e != nil {
				return fail("VALIDATION_FAILED")
			}
			if _, e = q.Exec(ctx, nodeSQL(q, "UPDATE managed_nodes SET facts=$1,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND id=$4", "UPDATE managed_nodes SET facts=CAST(? AS JSON),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?", `UPDATE managed_nodes SET facts=?,updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND workspace_id=? AND id=?`, nodeJSON(q, raw), n.TenantID, n.WorkspaceID, n.ID)); e != nil {
				return e
			}
		}
		if input.Component != nil {
			if owner, ok := s.Components.(ComponentResultObserverWithin); ok {
				e = owner.RecordResultWithin(ctx, q, n, task, input)
			} else if legacy != nil {
				if owner, ok := s.Components.(ComponentResultObserver); ok {
					e = owner.RecordResult(ctx, legacy, n, task, input)
				} else if s.Components != nil && n.ExecutionProfile != wire.SystemdMaintenanceProfile {
					e = s.Components.Observe(ctx, legacy, n, *input.Component, task.ID)
				} else {
					return fail("COMPONENT_PROFILE_UNAVAILABLE")
				}
			} else {
				return fail("COMPONENT_PROFILE_UNAVAILABLE")
			}
			if e != nil {
				return e
			}
		}
		if input.Inference != nil {
			owner, ok := s.Inference.(InferenceObserverWithin)
			if !ok {
				return fail("AGENT_PROFILE_REJECTED")
			}
			if e = owner.RecordInferenceResultWithin(ctx, q, n, task, input); e != nil {
				return e
			}
		}
		if input.Kubernetes != nil {
			command, e := s.ReadKubernetesCommandWithin(ctx, q, n, task)
			if e != nil {
				return e
			}
			if e = s.Kubernetes.RecordKubernetesResultWithin(ctx, q, n, task, command, input, request); e != nil {
				return e
			}
		}
		if input.Gateway != nil {
			if s.Gateways == nil {
				return fail("AGENT_PROFILE_REJECTED")
			}
			command, e := s.ReadGatewayCommandWithin(ctx, q, n, task)
			if e != nil {
				return e
			}
			if e = s.Gateways.RecordGatewayResultWithin(ctx, q, n, task, command, input); e != nil {
				return e
			}
		}
		// Owning callbacks may perform IO. Refuse an expired lease and roll back
		// their SQL effects before committing any terminal projection.
		if _, e = s.leasedTaskWithin(ctx, q, n, task.ID, input.LeaseToken); e != nil {
			return e
		}
		changed, e := q.Exec(ctx, nodeSQL(q, `UPDATE node_tasks SET state=$1,error_code=NULLIF($2,''),completed_lease_hash=lease_token_hash,result_hash=$3,lease_token_hash=NULL,lease_expires_at=NULL,completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE tenant_id=$4 AND workspace_id=$5 AND node_id=$6 AND id=$7 AND state='running'`, `UPDATE node_tasks SET state=?,error_code=NULLIF(?,''),completed_lease_hash=lease_token_hash,result_hash=?,lease_token_hash=NULL,lease_expires_at=NULL,completed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND state='running'`, `UPDATE node_tasks SET state=?,error_code=NULLIF(?,''),completed_lease_hash=lease_token_hash,result_hash=?,lease_token_hash=NULL,lease_expires_at=NULL,completed_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND state='running'`, out, input.ErrorCode, resultHash, n.TenantID, n.WorkspaceID, n.ID, task.ID))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("AGENT_LEASE_STALE")
		}
		if e = s.taskAuditWithin(ctx, q, "", "system", n, action, task.ID, "", request); e != nil {
			return e
		}
		return s.checkDerivedTaskAuthorityWithin(ctx, q, n, task)
	})
	if e != nil {
		return "", e
	}
	return out, nil
}
func (s *Service) sweepSelected(ctx context.Context) error {
	if ctx == nil {
		return fail("VALIDATION_FAILED")
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	policy := s.Identity.Config.Policy().Nodes
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(policy.ReconcileTimeoutSeconds))
	defer cancel()
	rows, e := store.Query(bounded, nodeSQL(store, `SELECT DISTINCT tenant_id::text,workspace_id::text,node_id::text FROM node_tasks WHERE tenant_id=(SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete) AND state IN ('pending','running') AND (deadline_at<=clock_timestamp() OR (state='running' AND lease_expires_at<=clock_timestamp() AND acknowledged_attempt<attempt)) ORDER BY node_id::text LIMIT $1`, `SELECT DISTINCT tenant_id,workspace_id,node_id FROM node_tasks WHERE tenant_id=(SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1) AND state IN ('pending','running') AND deadline_at<=UTC_TIMESTAMP(6) ORDER BY node_id LIMIT ?`, `SELECT DISTINCT tenant_id,workspace_id,node_id FROM node_tasks WHERE tenant_id=(SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1) AND state IN ('pending','running') AND deadline_at<=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) ORDER BY node_id LIMIT ?`, policy.TaskSweepBatch))
	if e != nil {
		return taskRuntimeError(e)
	}
	type scope struct{ tenant, workspace, node string }
	scopes := []scope{}
	for rows.Next() {
		var v scope
		if e = rows.Scan(&v.tenant, &v.workspace, &v.node); e != nil {
			rows.Close()
			return taskRuntimeError(e)
		}
		scopes = append(scopes, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return taskRuntimeError(e)
	}
	for _, v := range scopes {
		e = store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error {
			var workspace string
			if e := q.QueryRow(ctx, nodeSQL(q, "SELECT id::text FROM workspaces WHERE tenant_id=$1 AND id=$2 FOR NO KEY UPDATE", "SELECT id FROM workspaces WHERE tenant_id=? AND id=? FOR UPDATE", `SELECT id FROM workspaces WHERE tenant_id=? AND id=? FOR UPDATE`, v.tenant, v.workspace)).Scan(&workspace); e != nil {
				return e
			}
			n, e := s.lockedUpdateNode(ctx, q, v.tenant, v.workspace, v.node)
			if e != nil {
				return e
			}
			return s.expireTasksBeyondWithin(ctx, q, n, uuid.NewString(), noAttemptLimit)
		})
		if e != nil {
			return taskRuntimeError(e)
		}
	}
	return nil
}

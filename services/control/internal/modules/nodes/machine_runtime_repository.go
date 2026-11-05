package nodes

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// The common CredentialWithin owner rechecks the exact credential under node lock.
// Scope discovery grants no authority and every value is checked again in the transaction.
func (s *Service) machineWithin(ctx context.Context, id, credential string, fn func(context.Context, transaction.Executor, Node, bool, pgx.Tx) error) error {
	if ctx == nil || !nodeID(id) || !security.ValidToken(credential) || fn == nil {
		return fail("AGENT_UNAUTHORIZED")
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	var tenant, workspace, profile string
	e = store.QueryRow(bounded, nodeSQL(store, `SELECT n.tenant_id::text,n.workspace_id::text,n.execution_profile FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND n.id=$1 AND n.credential_hash=$2 AND n.status<>'removed'`, `SELECT n.tenant_id,n.workspace_id,n.execution_profile FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND n.id=? AND n.credential_hash=? AND n.status<>'removed'`, `SELECT n.tenant_id,n.workspace_id,n.execution_profile FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND n.id=? AND n.credential_hash=? AND n.status<>'removed'`, id, digest("node-credential", credential))).Scan(&tenant, &workspace, &profile)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("AGENT_UNAUTHORIZED")
	}
	if e != nil {
		return taskRuntimeError(e)
	}
	operation := func(ctx context.Context, q transaction.Executor, legacy pgx.Tx) error {
		// ADR 0020: on PostgreSQL the node lock fences the status read, so no agent call
		// waits for another on the Workspace row. The other chains lock it first.
		var active bool
		var e error
		if q.Dialect() != transaction.PostgreSQL {
			if active, e = scopeActiveWithin(ctx, q, tenant, workspace); e != nil {
				return e
			}
		}
		n, e := s.CredentialWithin(ctx, q, tenant, workspace, id, credential)
		if e != nil {
			return e
		}
		if q.Dialect() == transaction.PostgreSQL {
			if active, e = scopeActiveWithin(ctx, q, tenant, workspace); e != nil {
				return e
			}
		}
		return fn(ctx, q, n, active, legacy)
	}
	// Preserve the actual PG component owner's callbacks until it provides its
	// selected interface. This branch cannot execute on MySQL or an inference node.
	_, selectedObserve := s.Components.(ComponentObserverWithin)
	_, selectedResult := s.Components.(ComponentResultObserverWithin)
	if store.Dialect() == transaction.PostgreSQL && wire.IsManagementProfile(profile) && s.Components != nil && (!selectedObserve || !selectedResult) {
		return s.postgresTaskTransaction(bounded, func(ctx context.Context, tx pgx.Tx, q transaction.Executor) error { return operation(ctx, q, tx) })
	}
	return taskRuntimeError(store.Within(bounded, false, func(ctx context.Context, q transaction.Executor) error { return operation(ctx, q, nil) }))
}

// scopeActiveWithin reads whether a node's Workspace and tenant are active. On PostgreSQL
// the caller holds the node lock and the read takes no lock: a status change locks the
// Workspace's nodes, so it waits for this call or this call sees it (ADR 0020).
func scopeActiveWithin(ctx context.Context, q transaction.Executor, tenant, workspace string) (bool, error) {
	var active bool
	e := q.QueryRow(ctx, nodeSQL(q, `SELECT w.status='active' AND t.status='active' FROM workspaces w JOIN tenants t ON t.id=w.tenant_id WHERE w.tenant_id=$1 AND w.id=$2`, `SELECT w.status='active' AND t.status='active' FROM workspaces w JOIN tenants t ON t.id=w.tenant_id WHERE w.tenant_id=? AND w.id=? FOR UPDATE`, `SELECT CASE WHEN w.status='active' AND t.status='active' THEN 1 ELSE 0 END FROM workspaces w JOIN tenants t ON t.id=w.tenant_id WHERE w.tenant_id=? AND w.id=? FOR UPDATE`, tenant, workspace)).Scan(&active)
	if errors.Is(e, sql.ErrNoRows) {
		return false, fail("AGENT_UNAUTHORIZED")
	}
	return active, e
}

func (s *Service) heartbeatSelected(ctx context.Context, id, credential string, h wire.Handshake) (wire.Disposition, error) {
	if e := h.Validate(); e != nil {
		return wire.Disposition{}, e
	}
	var out wire.Disposition
	e := s.machineWithin(ctx, id, credential, func(ctx context.Context, q transaction.Executor, n Node, active bool, legacy pgx.Tx) error {
		if h.ProtocolVersion != n.ProtocolVersion || h.Profile() != n.ExecutionProfile || !slices.Equal(h.Capabilities, n.Capabilities) {
			return fail("AGENT_PROFILE_REJECTED")
		}
		if h.Inference != nil {
			if !active || n.Status != "active" {
				return fail("ACCESS_DENIED")
			}
			owner, ok := s.Inference.(InferenceObserverWithin)
			if !ok {
				return fail("AGENT_PROFILE_REJECTED")
			}
			if e := owner.ObserveInferenceWithin(ctx, q, n, *h.Inference); e != nil {
				return e
			}
		}
		if h.Component != nil {
			if owner, ok := s.Components.(ComponentObserverWithin); ok {
				if e := owner.ObserveWithin(ctx, q, n, *h.Component, ""); e != nil {
					return e
				}
			} else if legacy != nil && s.Components != nil {
				if e := s.Components.Observe(ctx, legacy, n, *h.Component, ""); e != nil {
					return e
				}
			} else {
				return fail("COMPONENT_PROFILE_UNAVAILABLE")
			}
		}
		raw, e := json.Marshal(h.Facts)
		if e != nil {
			return fail("VALIDATION_FAILED")
		}
		changed, e := q.Exec(ctx, nodeSQL(q, `UPDATE managed_nodes SET facts=$1,last_heartbeat_at=clock_timestamp(),updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND id=$4`, `UPDATE managed_nodes SET facts=CAST(? AS JSON),last_heartbeat_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`, `UPDATE managed_nodes SET facts=?,last_heartbeat_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND workspace_id=? AND id=?`, nodeJSON(q, raw), n.TenantID, n.WorkspaceID, n.ID))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("AGENT_UNAUTHORIZED")
		}
		out.Status = n.Status
		if !active {
			out.Status = "disabled"
		}
		return q.QueryRow(ctx, nodeSQL(q, "SELECT clock_timestamp()", "SELECT UTC_TIMESTAMP(6)", `SELECT SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) FROM DUAL`)).Scan(&out.ServerTime)
	})
	if e != nil {
		return wire.Disposition{}, e
	}
	out.ServerTime = out.ServerTime.UTC()
	pacing := s.currentPacing(ctx)
	out.Pacing = &pacing
	return out, nil
}
func (s *Service) leasedTaskWithin(ctx context.Context, q transaction.Executor, n Node, taskID, lease string) (Task, error) {
	if !nodeID(taskID) || !security.ValidToken(lease) {
		return Task{}, fail("VALIDATION_FAILED")
	}
	t, e := s.taskByIDWithin(ctx, q, n, taskID, true)
	if e != nil {
		return Task{}, e
	}
	var hash []byte
	var valid bool
	e = q.QueryRow(ctx, nodeSQL(q, `SELECT lease_token_hash,COALESCE(lease_expires_at>clock_timestamp() AND deadline_at>clock_timestamp(),false) FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4`, `SELECT lease_token_hash,COALESCE(lease_expires_at>UTC_TIMESTAMP(6) AND deadline_at>UTC_TIMESTAMP(6),false) FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, `SELECT lease_token_hash,CASE WHEN lease_expires_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) AND deadline_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, n.TenantID, n.WorkspaceID, n.ID, t.ID)).Scan(&hash, &valid)
	if e != nil {
		return Task{}, e
	}
	if t.State != "running" || !valid || subtle.ConstantTimeCompare(hash, digest("node-lease", lease)) != 1 {
		return Task{}, fail("AGENT_LEASE_STALE")
	}
	if !nodeOperationAllowed(n, t.Operation) {
		return Task{}, fail("AGENT_PROFILE_REJECTED")
	}
	if t.Operation == wire.TrainingRunOperation {
		return Task{}, fail("TRAINING_AUTHORITY_REQUIRED")
	}
	if t.Operation == wire.ModelPreparationRunOperation {
		return Task{}, fail("MODEL_PREPARATION_AUTHORITY_REQUIRED")
	}
	if e = s.checkDerivedTaskAuthorityWithin(ctx, q, n, t); e != nil {
		return Task{}, e
	}
	return t, nil
}

// WithLeaseExecutor bounds a selected owning effect by current credential, scope,
// physical lease and derived actor epoch before and after callback IO.
func (s *Service) WithLeaseExecutor(ctx context.Context, id, credential, taskID, lease string, fn func(context.Context, transaction.Executor, Node, Task) error) error {
	if fn == nil {
		return fail("VALIDATION_FAILED")
	}
	return s.machineWithin(ctx, id, credential, func(ctx context.Context, q transaction.Executor, n Node, active bool, _ pgx.Tx) error {
		if !active || n.Status != "active" {
			return fail("ACCESS_DENIED")
		}
		t, e := s.leasedTaskWithin(ctx, q, n, taskID, lease)
		if e != nil {
			return e
		}
		if e = fn(ctx, q, n, t); e != nil {
			return e
		}
		// A callback may consume time. Recheck physical expiry as well as actor epoch.
		_, e = s.leasedTaskWithin(ctx, q, n, taskID, lease)
		return e
	})
}
func (s *Service) withPostgreSQLLease(ctx context.Context, id, credential, taskID, lease string, fn func(pgx.Tx, Node, Task) error) error {
	if fn == nil || !security.ValidToken(lease) {
		return fail("VALIDATION_FAILED")
	}
	return s.postgresTaskTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q transaction.Executor) error {
		// Use the same real transaction; never invoke a PG callback on a selected MySQL executor.
		var tenant, workspace string
		e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT n.tenant_id::text,n.workspace_id::text FROM managed_nodes n JOIN installation_state i ON i.tenant_id=n.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND n.id=$1 AND n.credential_hash=$2 AND n.status<>'removed'`, id, digest("node-credential", credential))).Scan(&tenant, &workspace)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("AGENT_UNAUTHORIZED")
		}
		if e != nil {
			return e
		}
		n, e := s.CredentialWithin(ctx, q, tenant, workspace, id, credential)
		if e != nil {
			return e
		}
		active, e := scopeActiveWithin(ctx, q, tenant, workspace)
		if e != nil {
			return e
		}
		if !active {
			return fail("ACCESS_DENIED")
		}
		if n.Status != "active" {
			return fail("ACCESS_DENIED")
		}
		t, e := s.leasedTaskWithin(ctx, q, n, taskID, lease)
		if e != nil {
			return e
		}
		if e = fn(tx, n, t); e != nil {
			return e
		}
		_, e = s.leasedTaskWithin(ctx, q, n, taskID, lease)
		return e
	})
}
func (s *Service) renewSelected(ctx context.Context, id, credential, taskID, lease string) (wire.RenewedLease, error) {
	if s.Preparations != nil {
		if out, handled, e := s.Preparations.RenewPreparation(ctx, id, credential, taskID, lease); handled || e != nil {
			if e != nil {
				return wire.RenewedLease{}, e
			}
			return out, nil
		}
	}
	if s.Training != nil {
		if out, handled, e := s.Training.RenewTraining(ctx, id, credential, taskID, lease); handled || e != nil {
			if e != nil {
				return wire.RenewedLease{}, e
			}
			return out, nil
		}
	}
	var out wire.RenewedLease
	e := s.WithLeaseExecutor(ctx, id, credential, taskID, lease, func(ctx context.Context, q transaction.Executor, n Node, t Task) error {
		changed, e := q.Exec(ctx, nodeSQL(q, `UPDATE node_tasks SET lease_expires_at=LEAST(clock_timestamp()+make_interval(secs=>$1),deadline_at),updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND node_id=$4 AND id=$5`, `UPDATE node_tasks SET lease_expires_at=LEAST(TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),deadline_at),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, `UPDATE node_tasks SET lease_expires_at=LEAST(DATEADD(SECOND,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))),deadline_at),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, s.Identity.Config.Policy().Nodes.TaskLeaseSeconds, n.TenantID, n.WorkspaceID, n.ID, t.ID))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("AGENT_LEASE_STALE")
		}
		if e = q.QueryRow(ctx, nodeSQL(q, `SELECT lease_expires_at,GREATEST(0,floor(extract(epoch FROM (lease_expires_at-clock_timestamp()))*1000))::integer FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4`, `SELECT lease_expires_at,GREATEST(0,TIMESTAMPDIFF(MICROSECOND,UTC_TIMESTAMP(6),lease_expires_at) DIV 1000) FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, `SELECT lease_expires_at,CAST(GREATEST(0,FLOOR(BIGDATEDIFF(MICROSECOND,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),lease_expires_at) / 1000)) AS BIGINT) FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, n.TenantID, n.WorkspaceID, n.ID, t.ID)).Scan(&out.LeaseExpiresAt, &out.ValidForMilliseconds); e != nil {
			return e
		}
		_, e = q.Exec(ctx, nodeSQL(q, `UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp(),updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3`, `UPDATE managed_nodes SET last_heartbeat_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`, `UPDATE managed_nodes SET last_heartbeat_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND workspace_id=? AND id=?`, n.TenantID, n.WorkspaceID, n.ID))
		return e
	})
	if e != nil {
		return wire.RenewedLease{}, e
	}
	out.LeaseExpiresAt = out.LeaseExpiresAt.UTC()
	return out, nil
}

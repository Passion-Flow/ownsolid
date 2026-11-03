package components

import (
	"context"
	"encoding/json"
	"errors"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) ObserveWithin(ctx context.Context, q transaction.Executor, n nodes.Node, o wire.ComponentObservation, taskID string) error {
	if e := s.borrowed(ctx, q); e != nil {
		return e
	}
	current, e := s.Nodes.PreparationHistoryWithin(ctx, q, n.TenantID, n.WorkspaceID, n.ID)
	if e != nil {
		return e
	}
	// A disabled node retains its credential and may report actual observations.
	// Only passive heartbeats use an empty task ID; task settlement still requires
	// an active node and its current lease in the node owner.
	if current.ExecutionProfile != n.ExecutionProfile ||
		current.Status != "active" && (current.Status != "disabled" || taskID != "") {
		return errors.New("AGENT_PROFILE_REJECTED")
	}
	n = current
	if !wire.IsManagementProfile(n.ExecutionProfile) {
		return errors.New("AGENT_PROFILE_REJECTED")
	}
	if e := o.ValidateForProfile(n.ExecutionProfile); e != nil {
		return e
	}
	verifiedManaged := o.Managed && o.ErrorCode == "" && (o.State == "stopped" || o.State == "running" && o.Metrics != nil)
	verifiedAbsent := !o.Managed && o.State == "not_installed" && o.ErrorCode == ""
	var applied *wire.ComponentConfig
	previous := ""
	desired := "not_installed"
	defaults := wire.DefaultComponentConfig()
	if verifiedManaged {
		applied = o.Config
		if applied == nil {
			applied = &defaults
		}
		previous = o.PreviousVersion
		desired = o.State
	}
	desiredConfig := &defaults
	if verifiedManaged {
		desiredConfig = applied
	}
	body, _ := json.Marshal(o)
	if q.Dialect() == transaction.Dameng {
		return s.observeNativeWithin(ctx, q, n, o, desired, body, taskID, desiredConfig, applied, previous, verifiedManaged, verifiedAbsent)
	}
	_, e = q.Exec(ctx, componentSQL(q, `INSERT INTO managed_components(tenant_id,workspace_id,node_id,component,version,desired_state,observed_state,managed,observation,observed_at,last_task_id,desired_version,desired_config,applied_config,previous_version,last_verified_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,clock_timestamp(),NULLIF($10,'')::uuid,$5,$11,$12,NULLIF($13,''),CASE WHEN $14 THEN clock_timestamp() ELSE NULL END)
 ON CONFLICT(node_id,component) DO UPDATE SET version=CASE WHEN $15 THEN EXCLUDED.version ELSE managed_components.version END,observed_state=EXCLUDED.observed_state,managed=EXCLUDED.managed,observation=EXCLUDED.observation,observed_at=clock_timestamp(),updated_at=clock_timestamp(),last_task_id=COALESCE(EXCLUDED.last_task_id,managed_components.last_task_id),applied_config=CASE WHEN $14 THEN EXCLUDED.applied_config ELSE managed_components.applied_config END,previous_version=CASE WHEN $14 THEN EXCLUDED.previous_version ELSE managed_components.previous_version END,last_verified_at=CASE WHEN $14 THEN clock_timestamp() ELSE managed_components.last_verified_at END`, `INSERT INTO managed_components(tenant_id,workspace_id,node_id,component,version,desired_state,observed_state,managed,observation,observed_at,last_task_id,desired_version,desired_config,applied_config,previous_version,last_verified_at)
 VALUES(?,?,?,?,?,?,?,?,CAST(? AS JSON),UTC_TIMESTAMP(6),NULLIF(?,''),?,CAST(? AS JSON),CAST(? AS JSON),NULLIF(?,''),CASE WHEN ? THEN UTC_TIMESTAMP(6) ELSE NULL END) AS incoming
 ON DUPLICATE KEY UPDATE version=CASE WHEN ? THEN incoming.version ELSE managed_components.version END,observed_state=incoming.observed_state,managed=incoming.managed,observation=incoming.observation,observed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6),last_task_id=COALESCE(incoming.last_task_id,managed_components.last_task_id),applied_config=CASE WHEN ? THEN incoming.applied_config ELSE managed_components.applied_config END,previous_version=CASE WHEN ? THEN incoming.previous_version ELSE managed_components.previous_version END,last_verified_at=CASE WHEN ? THEN UTC_TIMESTAMP(6) ELSE managed_components.last_verified_at END`, "", observationArgs(q, n, o, desired, body, taskID, desiredConfig, applied, previous, verifiedManaged, verifiedAbsent)...))
	return e
}

func observationArgs(q transaction.Executor, n nodes.Node, o wire.ComponentObservation, desired string, body []byte, task string, dc, ac *wire.ComponentConfig, previous string, managed, absent bool) []any {
	args := []any{n.TenantID, n.WorkspaceID, n.ID, wire.NodeExporter, o.Version, desired, o.State, o.Managed, body, task}
	if q.Dialect() == transaction.MySQL {
		return append(args, o.Version, encodeConfig(dc), encodeConfig(ac), previous, managed || absent, managed, managed || absent, managed || absent, managed || absent)
	}
	return append(args, encodeConfig(dc), encodeConfig(ac), previous, managed || absent, managed)
}
func (s *Service) RecordResultWithin(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, result wire.Result) error {
	if e := s.borrowed(ctx, q); e != nil {
		return e
	}
	if result.Component == nil {
		return errors.New("VALIDATION_FAILED")
	}
	if e := s.Nodes.ValidateComponentResultWithin(ctx, q, n, t, result); e != nil {
		return e
	}
	if e := s.ObserveWithin(ctx, q, n, *result.Component, t.ID); e != nil {
		return e
	}
	if result.Outcome != "success" {
		return nil
	}
	if t.Operation == wire.ComponentOperation("rollback") {
		configuration := result.Component.Config
		if configuration == nil {
			v := wire.DefaultComponentConfig()
			configuration = &v
		}
		if _, e := q.Exec(ctx, componentSQL(q, "UPDATE managed_components SET desired_config=$4,desired_version=$5 WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND component='node_exporter'", "UPDATE managed_components SET desired_config=CAST(? AS JSON),desired_version=? WHERE tenant_id=? AND workspace_id=? AND node_id=? AND component='node_exporter'", `UPDATE managed_components SET desired_config=?,desired_version=? WHERE tenant_id=? AND workspace_id=? AND node_id=? AND component='node_exporter'`, rollbackArgs(q, n, configuration, result.Component.Version)...)); e != nil {
			return e
		}
	}
	if t.Operation == wire.ComponentOperation("remove") && !s.Nodes.Identity.Config.ComponentsOnly() {
		_, e := q.Exec(ctx, componentSQL(q, "DELETE FROM component_log_snapshots WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3", "DELETE FROM component_log_snapshots WHERE tenant_id=? AND workspace_id=? AND node_id=?", `DELETE FROM component_log_snapshots WHERE tenant_id=? AND workspace_id=? AND node_id=?`, n.TenantID, n.WorkspaceID, n.ID))
		return e
	}
	if t.Operation == wire.ComponentOperation("logs") {
		if result.Logs == nil || result.Logs.Validate() != nil {
			return errors.New("VALIDATION_FAILED")
		}
		body, _ := json.Marshal(result.Logs)
		if q.Dialect() == transaction.Dameng {
			return s.logsNativeWithin(ctx, q, n, t.ID, body)
		}
		_, e := q.Exec(ctx, componentSQL(q, "INSERT INTO component_log_snapshots(tenant_id,workspace_id,node_id,component,task_id,snapshot) VALUES($1,$2,$3,'node_exporter',$4,$5) ON CONFLICT(node_id,component) DO UPDATE SET task_id=EXCLUDED.task_id,snapshot=EXCLUDED.snapshot,received_at=clock_timestamp()", "INSERT INTO component_log_snapshots(tenant_id,workspace_id,node_id,component,task_id,snapshot) VALUES(?,?,?,'node_exporter',?,CAST(? AS JSON)) AS incoming ON DUPLICATE KEY UPDATE task_id=incoming.task_id,snapshot=incoming.snapshot,received_at=UTC_TIMESTAMP(6)", "", n.TenantID, n.WorkspaceID, n.ID, t.ID, body))
		return e
	}
	return nil
}
func rollbackArgs(q transaction.Executor, n nodes.Node, c *wire.ComponentConfig, v string) []any {
	if q.Dialect() == transaction.MySQL || q.Dialect() == transaction.Dameng {
		return []any{componentJSON(q, encodeConfig(c)), v, n.TenantID, n.WorkspaceID, n.ID}
	}
	return []any{n.TenantID, n.WorkspaceID, n.ID, encodeConfig(c), v}
}

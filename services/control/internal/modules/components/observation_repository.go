package components

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
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
	verifiedManaged, verifiedAbsent := verifiedObservation(o)
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

// verifiedObservation says whether an observation proves a managed component or its absence.
func verifiedObservation(o wire.ComponentObservation) (managed, absent bool) {
	managed = o.Managed && o.ErrorCode == "" && (o.State == "stopped" || o.State == "running" && o.Metrics != nil)
	absent = !o.Managed && o.State == "not_installed" && o.ErrorCode == ""
	return managed, absent
}

// RecordComponentSightingsWithin writes the latest reports of heartbeats whose state did not
// change (ADR 0022). The observed time guards a newer synchronous write.
func (s *Service) RecordComponentSightingsWithin(ctx context.Context, q transaction.Executor, batch []nodes.ComponentSighting) error {
	if e := s.borrowed(ctx, q); e != nil {
		return e
	}
	if q.Dialect() != transaction.PostgreSQL {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	ids, times, bodies, verified := make([]string, len(batch)), make([]time.Time, len(batch)), make([]string, len(batch)), make([]bool, len(batch))
	for i, v := range batch {
		body, e := json.Marshal(v.Observation)
		if e != nil {
			return errors.New("VALIDATION_FAILED")
		}
		managed, absent := verifiedObservation(v.Observation)
		ids[i], times[i], bodies[i], verified[i] = v.NodeID, v.At, string(body), managed || absent
	}
	_, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `WITH t AS (SELECT c.node_id,c.component,v.at,v.body,v.verified FROM managed_components c JOIN unnest($1::text[],$2::timestamptz[],$3::text[],$4::boolean[]) AS v(node,at,body,verified) ON c.node_id=v.node::uuid WHERE c.component='node_exporter' AND c.observed_at<v.at ORDER BY c.node_id FOR NO KEY UPDATE OF c SKIP LOCKED) UPDATE managed_components c SET observation=t.body::jsonb,observed_at=t.at,updated_at=t.at,last_verified_at=CASE WHEN t.verified THEN t.at ELSE c.last_verified_at END FROM t WHERE c.node_id=t.node_id AND c.component=t.component`, ids, times, bodies, verified))
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
	if t.Operation == wire.ComponentOperation("remove") {
		s.enqueueLogs(telemetrystore.Record{Scope: logScope(n), Time: time.Now(), TaskID: t.ID, Removed: true})
		return nil
	}
	if t.Operation == wire.ComponentOperation("logs") {
		if result.Logs == nil || result.Logs.Validate() != nil {
			return errors.New("VALIDATION_FAILED")
		}
		body, e := json.Marshal(result.Logs)
		if e != nil {
			return errors.New("VALIDATION_FAILED")
		}
		s.enqueueLogs(telemetrystore.Record{Scope: logScope(n), Time: time.Now(), TaskID: t.ID, Body: body})
	}
	return nil
}

// logScope is the node's own scope, read from the server's record of it.
func logScope(n nodes.Node) telemetrystore.Scope {
	return telemetrystore.Scope{TenantID: n.TenantID, WorkspaceID: n.WorkspaceID, NodeID: n.ID, Component: wire.NodeExporter}
}

func (s *Service) enqueueLogs(r telemetrystore.Record) {
	if s.LogStore != nil {
		s.LogStore.Enqueue(r)
	}
}

func rollbackArgs(q transaction.Executor, n nodes.Node, c *wire.ComponentConfig, v string) []any {
	if q.Dialect() == transaction.MySQL || q.Dialect() == transaction.Dameng {
		return []any{componentJSON(q, encodeConfig(c)), v, n.TenantID, n.WorkspaceID, n.ID}
	}
	return []any{n.TenantID, n.WorkspaceID, n.ID, encodeConfig(c), v}
}

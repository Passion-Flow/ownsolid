package components

import (
	"context"
	"database/sql"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func componentJSON(q transaction.Executor, raw []byte) any {
	if q.Dialect() == transaction.Dameng {
		if raw == nil {
			return nil
		}
		return string(raw)
	}
	return raw
}

// The caller already holds the current node's exclusive lock. Native upserts
// inspect then insert/update under that lock; an unexpected SQL conflict aborts
// the transaction. A poisoned native connection is never reused for a retry.
func componentNativeExists(ctx context.Context, q transaction.Executor, n nodes.Node) (bool, error) {
	query := `SELECT component FROM managed_components WHERE tenant_id=? AND workspace_id=? AND node_id=? AND component='node_exporter' FOR UPDATE`
	var component string
	e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, query, n.TenantID, n.WorkspaceID, n.ID)).Scan(&component)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	return e == nil, e
}

func nativeComponentWrite(ctx context.Context, q transaction.Executor, statement transaction.Statement) error {
	changed, e := q.Exec(ctx, statement)
	if e == nil && changed != 1 {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	return e
}

func (s *Service) requestNativeWithin(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task, c wire.ComponentCommand, desired string, configuration *wire.ComponentConfig, preserve, replace bool) error {
	exists, e := componentNativeExists(ctx, q, n)
	if e != nil {
		return e
	}
	configurationJSON := componentJSON(q, encodeConfig(configuration))
	if !exists {
		return nativeComponentWrite(ctx, q, transaction.SQL(transaction.Dameng, `INSERT INTO managed_components(tenant_id,workspace_id,node_id,component,version,desired_state,observed_state,managed,observation,observed_at,last_task_id,desired_version,desired_config) VALUES(?,?,?,'node_exporter',?,?,'unknown',0,'{}',SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,?,?)`, n.TenantID, n.WorkspaceID, n.ID, c.Version, desired, t.ID, c.Version, configurationJSON))
	}
	return nativeComponentWrite(ctx, q, transaction.SQL(transaction.Dameng, `UPDATE managed_components SET desired_state=CASE WHEN ?=1 THEN desired_state ELSE ? END,last_task_id=?,desired_version=CASE WHEN ?=1 THEN desired_version ELSE ? END,desired_config=CASE WHEN ?=1 THEN TO_CLOB(?) ELSE desired_config END WHERE tenant_id=? AND workspace_id=? AND node_id=? AND component='node_exporter'`, preserve, desired, t.ID, preserve, c.Version, replace, configurationJSON, n.TenantID, n.WorkspaceID, n.ID))
}

func (s *Service) observeNativeWithin(ctx context.Context, q transaction.Executor, n nodes.Node, o wire.ComponentObservation, desired string, body []byte, task string, dc, ac *wire.ComponentConfig, previous string, managed, absent bool) error {
	exists, e := componentNativeExists(ctx, q, n)
	if e != nil {
		return e
	}
	verified := managed || absent
	if !exists {
		return nativeComponentWrite(ctx, q, transaction.SQL(transaction.Dameng, `INSERT INTO managed_components(tenant_id,workspace_id,node_id,component,version,desired_state,observed_state,managed,observation,observed_at,last_task_id,desired_version,desired_config,applied_config,previous_version,last_verified_at) VALUES(?,?,?,?,?,?,?,?,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),NULLIF(?,''),?,?,?,NULLIF(?,''),CASE WHEN ?=1 THEN SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) ELSE NULL END)`, n.TenantID, n.WorkspaceID, n.ID, wire.NodeExporter, o.Version, desired, o.State, o.Managed, componentJSON(q, body), task, o.Version, componentJSON(q, encodeConfig(dc)), componentJSON(q, encodeConfig(ac)), previous, verified))
	}
	return nativeComponentWrite(ctx, q, transaction.SQL(transaction.Dameng, `UPDATE managed_components SET version=CASE WHEN ?=1 THEN ? ELSE version END,observed_state=?,managed=?,observation=?,observed_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),updated_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),last_task_id=COALESCE(NULLIF(?,''),last_task_id),applied_config=CASE WHEN ?=1 THEN TO_CLOB(?) ELSE applied_config END,previous_version=CASE WHEN ?=1 THEN NULLIF(?,'') ELSE previous_version END,last_verified_at=CASE WHEN ?=1 THEN SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) ELSE last_verified_at END WHERE tenant_id=? AND workspace_id=? AND node_id=? AND component='node_exporter'`, managed, o.Version, o.State, o.Managed, componentJSON(q, body), task, verified, componentJSON(q, encodeConfig(ac)), verified, previous, verified, n.TenantID, n.WorkspaceID, n.ID))
}


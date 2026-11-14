package components

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// MonitoringObservation projects approved stored resource facts only. It never
// installs/scrapes/retries a component or returns its logs/configuration/secrets.
type MonitoringObservation struct {
	NodeID         string                 `json:"node_id"`
	Name           string                 `json:"name"`
	Status         string                 `json:"status"`
	Connectivity   string                 `json:"connectivity"`
	Profile        string                 `json:"profile"`
	Present        bool                   `json:"present"`
	Stale          bool                   `json:"stale"`
	ReceivedAt     *time.Time             `json:"received_at"`
	ObservedAt     *time.Time             `json:"observed_at"`
	ComponentState string                 `json:"component_state"`
	Version        string                 `json:"version"`
	ErrorCode      string                 `json:"error_code"`
	Metrics        *wire.ComponentMetrics `json:"metrics"`
}

func (s *Service) MonitoringWithin(ctx context.Context, q transaction.Executor, a identity.Auth, workspace, node string) (MonitoringObservation, error) {
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return MonitoringObservation{}, errors.New("ACCESS_DENIED")
	}
	if e := s.borrowed(ctx, q); e != nil {
		return MonitoringObservation{}, e
	}
	n, e := s.Nodes.ComponentNodeWithin(ctx, q, a, workspace, node)
	if e != nil {
		return MonitoringObservation{}, componentError(e)
	}
	out := MonitoringObservation{NodeID: n.ID, Name: n.Name, Status: n.Status, Connectivity: n.Connectivity, Profile: n.ExecutionProfile, Stale: true}
	var raw []byte
	var received time.Time
	e = q.QueryRow(ctx, componentSQL(q, `SELECT observation,updated_at FROM managed_components WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND component=$4`, `SELECT observation,updated_at FROM managed_components WHERE tenant_id=? AND workspace_id=? AND node_id=? AND component=?`, `SELECT observation,updated_at FROM managed_components WHERE tenant_id=? AND workspace_id=? AND node_id=? AND component=?`, n.TenantID, n.WorkspaceID, n.ID, wire.NodeExporter)).Scan(&raw, &received)
	if errors.Is(e, sql.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return MonitoringObservation{}, componentError(e)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return MonitoringObservation{}, errors.New("STORAGE_UNAVAILABLE")
	}
	if len(fields) == 0 {
		return out, nil
	}
	var value wire.ComponentObservation
	if json.Unmarshal(raw, &value) != nil || value.ValidateForProfile(n.ExecutionProfile) != nil || received.IsZero() {
		return MonitoringObservation{}, errors.New("STORAGE_UNAVAILABLE")
	}
	observed := value.ObservedAt.UTC()
	received = received.UTC()
	out.Present = true
	out.Stale = componentStale(s, n, received)
	out.ReceivedAt = &received
	out.ObservedAt = &observed
	out.ComponentState = value.State
	out.Version = value.Version
	out.ErrorCode = value.ErrorCode
	out.Metrics = value.Metrics
	return out, nil
}

package components

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
	"strings"
	"time"
)

func componentSQL(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, args...)
	default:
		return transaction.SQL(q.Dialect(), "")
	}
}
func componentError(e error) error {
	if e == nil {
		return nil
	}
	if kind, ok := transaction.CommitOutcome(e); ok {
		return errors.New(string(kind))
	}
	if errors.Is(e, context.Canceled) {
		return errors.New(string(transaction.Cancelled))
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return errors.New(string(transaction.Timeout))
	}
	c := e.Error()
	if len(c) > 100 {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	for _, r := range c {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return errors.New("STORAGE_UNAVAILABLE")
		}
	}
	for _, p := range []string{"COMPONENT_", "AGENT_", "AUTH_", "NODE_", "STORAGE_"} {
		if strings.HasPrefix(c, p) {
			return errors.New(c)
		}
	}
	switch c {
	case "ACCESS_DENIED", "RESOURCE_NOT_FOUND", "VALIDATION_FAILED", "IDEMPOTENCY_CONFLICT", "VERSION_CONFLICT", "RESOURCE_LIMIT_REACHED":
		return errors.New(c)
	}
	return errors.New("STORAGE_UNAVAILABLE")
}
func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Nodes == nil || s.Nodes.Identity == nil || s.Nodes.Workspaces == nil || s.Nodes.Workspaces.Identity != s.Nodes.Identity {
		return nil, errors.New("COMPONENT_IDENTITY_REQUIRED")
	}
	if s.Nodes.DB != nil && s.Nodes.DB != s.Nodes.Identity.DB || s.Nodes.Workspaces.DB != nil && s.Nodes.Workspaces.DB != s.Nodes.Identity.DB {
		return nil, errors.New("COMPONENT_STORAGE_MISMATCH")
	}
	store, e := s.Nodes.Identity.Repository()
	if e != nil {
		return nil, e
	}
	switch store.Dialect() {
	case transaction.PostgreSQL, transaction.MySQL, transaction.Dameng:
		return store, nil
	default:
		return nil, errors.New("COMPONENT_STORAGE_MISMATCH")
	}
}
func (s *Service) borrowed(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	if e := ctx.Err(); e != nil {
		return componentError(e)
	}
	if _, pool := q.(transaction.Store); pool {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	r, e := s.repository()
	if e != nil {
		return e
	}
	if q.Dialect() != r.Dialect() || q.Dialect() != transaction.MySQL && q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.Dameng {
		return errors.New("COMPONENT_STORAGE_MISMATCH")
	}
	return nil
}
func (s *Service) within(ctx context.Context, a identity.Auth, workspace, node string, fn func(context.Context, transaction.Executor, nodes.Node) error) error {
	if ctx == nil {
		return errors.New("VALIDATION_FAILED")
	}
	r, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Nodes.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	e = r.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		n, e := s.Nodes.ComponentNodeWithin(ctx, q, a, workspace, node)
		if e != nil {
			return e
		}
		return fn(ctx, q, n)
	})
	return componentError(e)
}
func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, nodeID string) (State, error) {
	if _, e := s.repository(); e != nil {
		return State{}, e
	}
	// Archive hashing is bounded and precedes the final current-authority read.
	versions := []string{"1.12.0", wire.NodeExporterVersion}
	availability := map[string]bool{}
	for _, v := range versions {
		f, _, e := s.OpenArtifactVersionContext(ctx, v)
		if e == nil {
			availability[v] = true
			f.Close()
		}
	}
	var out State
	e := s.within(ctx, a, workspace, nodeID, func(ctx context.Context, q transaction.Executor, n nodes.Node) error {
		defaults := wire.DefaultComponentConfig()
		out = State{NodeID: n.ID, Component: wire.NodeExporter, Version: wire.NodeExporterVersion, DesiredVersion: wire.NodeExporterVersion, DesiredConfig: &defaults, DesiredState: "not_installed", Stale: true, ManagementAvailable: wire.IsManagementProfile(n.ExecutionProfile) && n.Status == "active", MaintenanceAvailable: n.ExecutionProfile == wire.SystemdMaintenanceProfile && n.Status == "active", ApprovedVersions: []RecipeAvailability{}, RemovalPolicy: "owned_installation_only"}
		var observation, desired, applied []byte
		var received time.Time
		e := q.QueryRow(ctx, componentSQL(q, "SELECT desired_state,version,desired_version,desired_config,applied_config,previous_version,last_verified_at,observation,updated_at,last_task_id::text FROM managed_components WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND component=$4", "SELECT desired_state,version,desired_version,desired_config,applied_config,previous_version,last_verified_at,observation,updated_at,last_task_id FROM managed_components WHERE tenant_id=? AND workspace_id=? AND node_id=? AND component=?", `SELECT desired_state,version,desired_version,desired_config,applied_config,previous_version,last_verified_at,observation,updated_at,last_task_id FROM managed_components WHERE tenant_id=? AND workspace_id=? AND node_id=? AND component=?`, n.TenantID, n.WorkspaceID, n.ID, wire.NodeExporter)).Scan(&out.DesiredState, &out.Version, &out.DesiredVersion, &desired, &applied, &out.PreviousVersion, &out.LastVerifiedAt, &observation, &received, &out.LastTaskID)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			if out.DesiredConfig, e = decodeConfig(desired); e != nil {
				return e
			}
			if out.AppliedConfig, e = decodeConfig(applied); e != nil {
				return e
			}
			var value wire.ComponentObservation
			var fields map[string]json.RawMessage
			if json.Unmarshal(observation, &fields) != nil {
				return errors.New("STORAGE_UNAVAILABLE")
			}
			if len(fields) > 0 {
				if json.Unmarshal(observation, &value) != nil || value.ValidateForProfile(n.ExecutionProfile) != nil {
					return errors.New("STORAGE_UNAVAILABLE")
				}
				received = received.UTC()
				out.Observation = &value
				out.ReceivedAt = &received
				out.Stale = componentStale(s, n, received)
			}
			if out.LastVerifiedAt != nil {
				v := out.LastVerifiedAt.UTC()
				out.LastVerifiedAt = &v
			}
		}
		for _, v := range versions {
			out.ApprovedVersions = append(out.ApprovedVersions, RecipeAvailability{Version: v, Available: availability[v], ForwardUpgrade: wire.IsForwardUpgrade(out.Version, v)})
		}
		out.ArtifactAvailable = availability[wire.NodeExporterVersion]
		return nil
	})
	if e != nil {
		return State{}, e
	}
	return out, nil
}
func componentStale(s *Service, n nodes.Node, received time.Time) bool {
	return n.Connectivity != "online" || n.Status != "active" || time.Since(received) > config.Seconds(s.Nodes.Identity.Config.Policy().Nodes.OnlineSeconds)
}
// recentLogRecords is how far back Logs looks past records of rolled-back settlements.
const recentLogRecords = 16

// settledRecord returns the newest record whose task succeeded. PostgreSQL decides: the
// store also holds records from settlements that rolled back.
func (s *Service) settledRecord(ctx context.Context, a identity.Auth, nodeID string, records []telemetrystore.Record) (*telemetrystore.Record, error) {
	if len(records) == 0 {
		return nil, nil
	}
	var settled *telemetrystore.Record
	e := s.within(ctx, a, "", nodeID, func(ctx context.Context, q transaction.Executor, n nodes.Node) error {
		for i := range records {
			if uuid.Validate(records[i].TaskID) != nil {
				continue
			}
			var state string
			e := q.QueryRow(ctx, componentSQL(q, "SELECT state FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4", "SELECT state FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?", "SELECT state FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?", n.TenantID, n.WorkspaceID, n.ID, records[i].TaskID)).Scan(&state)
			if errors.Is(e, sql.ErrNoRows) {
				continue
			}
			if e != nil {
				return e
			}
			if state == "succeeded" {
				settled = &records[i]
				return nil
			}
		}
		return nil
	})
	return settled, e
}

func (s *Service) Logs(ctx context.Context, a identity.Auth, nodeID string) (LogSnapshot, error) {
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return LogSnapshot{}, errors.New("ACCESS_DENIED")
	}
	// The node is read in the transaction; the store is asked after it ends, so no
	// database connection waits on the network.
	var n nodes.Node
	if e := s.within(ctx, a, "", nodeID, func(_ context.Context, _ transaction.Executor, current nodes.Node) error {
		n = current
		return nil
	}); e != nil {
		return LogSnapshot{}, e
	}
	if s.LogStore == nil {
		return LogSnapshot{}, errors.New("STORAGE_UNAVAILABLE")
	}
	records, e := s.LogStore.Recent(ctx, logScope(n), recentLogRecords)
	if e != nil {
		return LogSnapshot{}, errors.New("STORAGE_UNAVAILABLE")
	}
	record, e := s.settledRecord(ctx, a, nodeID, records)
	if e != nil {
		return LogSnapshot{}, e
	}
	out := LogSnapshot{NodeID: n.ID, Stale: true}
	if record == nil || record.Removed {
		return out, nil
	}
	var value wire.ComponentLogs
	if json.Unmarshal(record.Body, &value) != nil || value.Validate() != nil {
		return LogSnapshot{}, errors.New("STORAGE_UNAVAILABLE")
	}
	received := record.Time.UTC()
	out.Snapshot = &value
	out.ReceivedAt = &received
	out.TaskID = &record.TaskID
	out.Stale = componentStale(s, n, received)
	return out, nil
}

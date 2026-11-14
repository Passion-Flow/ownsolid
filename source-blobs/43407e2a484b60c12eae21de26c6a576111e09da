package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// FleetHistory is a scoped, sparse snapshot of the requested evaluation grid.
type FleetHistory struct {
	Tier        string             `json:"tier"`
	StepSeconds int64              `json:"step_seconds"`
	From        time.Time          `json:"from"`
	To          time.Time          `json:"to"`
	Cells       []FleetHistoryCell `json:"cells"`
}

// FleetHistoryCell identifies a cell within the authorized workspace.
type FleetHistoryCell struct {
	SiteID string              `json:"site_id"`
	CellID string              `json:"cell_id"`
	Points []FleetHistoryPoint `json:"points"`
}

// FleetHistoryPoint exists only when the cell had a fresh source sample.
type FleetHistoryPoint struct {
	At        time.Time `json:"at"`
	Observed  uint64    `json:"observed"`
	Reachable uint64    `json:"reachable"`
}

// GetFleetHistory applies the current Admin diagnostics authority and audit to a
// bounded read from one metrics tier. The caller supplies whole-second endpoints,
// a workspace and a request ID. Tenant and source selectors are server-owned.
func (s *Service) GetFleetHistory(ctx context.Context, a identity.Auth, workspace string, from, to time.Time, requestID string) (FleetHistory, error) {
	return s.getFleetHistory(ctx, a, workspace, from, to, requestID, nil)
}

const maximumHistoryJSONBytes = 1 << 20

// GetFleetHistoryJSON uses the same scope, tier and audit as GetFleetHistory.
// It rejects an oversized HTTP body before committing the success audit.
func (s *Service) GetFleetHistoryJSON(ctx context.Context, a identity.Auth, workspace string, from, to time.Time, requestID string) (json.RawMessage, error) {
	var body json.RawMessage
	_, err := s.getFleetHistory(ctx, a, workspace, from, to, requestID, func(out FleetHistory) error {
		encoded, err := json.Marshal(out)
		if err != nil {
			return errors.New("STORAGE_UNAVAILABLE")
		}
		if len(encoded) > maximumHistoryJSONBytes {
			return errors.New("MONITORING_HISTORY_RESPONSE_LIMIT")
		}
		body = encoded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (s *Service) getFleetHistory(ctx context.Context, a identity.Auth, workspace string, from, to time.Time, requestID string, prepare func(FleetHistory) error) (FleetHistory, error) {
	if ctx == nil || !s.ready() {
		return FleetHistory{}, errors.New("MONITORING_OWNER_REQUIRED")
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return FleetHistory{}, errors.New("ACCESS_DENIED")
	}
	if !validID(workspace) || !validID(requestID) || from.Unix() < 0 || from.Nanosecond() != 0 || to.Nanosecond() != 0 || !from.Before(to) {
		return FleetHistory{}, errors.New("VALIDATION_FAILED")
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return FleetHistory{}, errors.New("MONITORING_BUSY")
	}
	store, e := s.identity.Repository()
	if e != nil {
		return FleetHistory{}, safeError(e)
	}
	operation, cancel := context.WithTimeout(ctx, time.Duration(s.config.OperationSeconds)*time.Second)
	defer cancel()
	var scope workspaces.Workspace
	var now time.Time
	e = store.Within(operation, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		scope, e = s.authorizedScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		now, e = observationTimeWithin(ctx, q)
		return e
	})
	if e != nil {
		return FleetHistory{}, safeError(e)
	}
	if s.fleetHistory == nil {
		return FleetHistory{}, errors.New("MONITORING_HISTORY_NOT_CONFIGURED")
	}

	// Leave SQL unlocked during store I/O. A subsequent authority change
	// invalidates the assembled snapshot before any response is released.
	out, e := s.fleetHistory.read(operation, scope, now, from, to)
	if e != nil {
		return FleetHistory{}, safeError(e)
	}
	var preparationError error
	if prepare != nil {
		preparationError = prepare(out)
	}
	e = store.Within(operation, false, func(ctx context.Context, q transaction.Executor) error {
		current, e := s.authorizedScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if current.ID != scope.ID || current.TenantID != scope.TenantID {
			return errors.New("ACCESS_DENIED")
		}
		if _, e := s.license.MonitoringWithin(ctx, q, a); e != nil {
			return e
		}
		if preparationError != nil {
			return preparationError
		}
		return insertSnapshotAuditWithin(ctx, q, uuid.NewString(), a.Me.User.ID, scope.ID, requestID, scope.TenantID)
	})
	if e != nil {
		return FleetHistory{}, safeError(e)
	}
	if e := operation.Err(); e != nil {
		return FleetHistory{}, safeError(e)
	}
	return out, nil
}

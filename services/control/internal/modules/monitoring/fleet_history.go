package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
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

func encodeFleetHistoryJSON(out FleetHistory) (json.RawMessage, error) {
	return encodeMetricsJSON(out, maximumHistoryJSONBytes, "MONITORING_HISTORY_RESPONSE_LIMIT")
}

// GetFleetHistoryJSON uses the same scope, tier and audit as GetFleetHistory.
// Its body budget includes the serving data envelope and newline, and is
// enforced before committing the success audit.
func (s *Service) GetFleetHistoryJSON(ctx context.Context, a identity.Auth, workspace string, from, to time.Time, requestID string) (json.RawMessage, error) {
	var body json.RawMessage
	_, err := s.getFleetHistory(ctx, a, workspace, from, to, requestID, func(out FleetHistory) error {
		var err error
		body, err = encodeFleetHistoryJSON(out)
		return err
	})
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (s *Service) getFleetHistory(ctx context.Context, a identity.Auth, workspace string, from, to time.Time, requestID string, prepare func(FleetHistory) error) (FleetHistory, error) {
	var out FleetHistory
	err := s.observeMetrics(ctx, a, workspace, requestID, metricsObservation{
		validate: func() error {
			if from.Unix() < 0 || from.Nanosecond() != 0 || to.Nanosecond() != 0 || !from.Before(to) {
				return errors.New("VALIDATION_FAILED")
			}
			return nil
		},
		read: func(ctx context.Context, scope workspaces.Workspace, now time.Time) error {
			if s.fleetHistory == nil {
				return errors.New("MONITORING_HISTORY_NOT_CONFIGURED")
			}
			var err error
			out, err = s.fleetHistory.read(ctx, scope, now, from, to)
			return err
		},
		prepare: func() error {
			if prepare != nil {
				return prepare(out)
			}
			return nil
		},
	})
	if err != nil {
		return FleetHistory{}, err
	}
	return out, nil
}

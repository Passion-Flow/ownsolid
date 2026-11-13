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

type metricsObservation struct {
	validate func() error
	read     func(context.Context, workspaces.Workspace, time.Time) error
	prepare  func() error
}

func (s *Service) observeMetrics(ctx context.Context, a identity.Auth, workspace, requestID string, source metricsObservation) error {
	if ctx == nil || !s.ready() || source.read == nil {
		return errors.New("MONITORING_OWNER_REQUIRED")
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return errors.New("ACCESS_DENIED")
	}
	if !validID(workspace) || !validID(requestID) {
		return errors.New("VALIDATION_FAILED")
	}
	if source.validate != nil {
		if err := source.validate(); err != nil {
			return err
		}
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return errors.New("MONITORING_BUSY")
	}
	store, err := s.identity.Repository()
	if err != nil {
		return safeError(err)
	}
	operation, cancel := context.WithTimeout(ctx, time.Duration(s.config.OperationSeconds)*time.Second)
	defer cancel()
	var scope workspaces.Workspace
	var now time.Time
	err = store.Within(operation, false, func(ctx context.Context, q transaction.Executor) error {
		var err error
		scope, err = s.authorizedScopeWithin(ctx, q, a, workspace)
		if err != nil {
			return err
		}
		now, err = observationTimeWithin(ctx, q)
		return err
	})
	if err != nil {
		return safeError(err)
	}

	// Do not hold SQL locks while a store is queried or its response decoded.
	// Any authority change invalidates the result before its success commit.
	if err := source.read(operation, scope, now); err != nil {
		return safeError(err)
	}
	var preparationError error
	if source.prepare != nil {
		preparationError = source.prepare()
	}
	err = store.Within(operation, false, func(ctx context.Context, q transaction.Executor) error {
		current, err := s.authorizedScopeWithin(ctx, q, a, workspace)
		if err != nil {
			return err
		}
		if current.ID != scope.ID || current.TenantID != scope.TenantID {
			return errors.New("ACCESS_DENIED")
		}
		if _, err := s.license.MonitoringWithin(ctx, q, a); err != nil {
			return err
		}
		if preparationError != nil {
			return preparationError
		}
		return insertSnapshotAuditWithin(ctx, q, uuid.NewString(), a.Me.User.ID, scope.ID, requestID, scope.TenantID)
	})
	if err != nil {
		return safeError(err)
	}
	return safeError(operation.Err())
}

// The serving owner adds a data envelope and the encoder's newline.
const metricsJSONEnvelopeBytes = len("{\"data\":") + len("}\n")

func encodeMetricsJSON(value any, maximum int, limitCode string) (json.RawMessage, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("STORAGE_UNAVAILABLE")
	}
	if len(body) > maximum-metricsJSONEnvelopeBytes {
		return nil, errors.New(limitCode)
	}
	return body, nil
}

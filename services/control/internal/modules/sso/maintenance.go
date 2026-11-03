package sso

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// InvalidateAuthorities participates in the existing offline restore transaction.
// Connector/binding history and ciphertext are retained; old callbacks cannot revive.
func InvalidateAuthorities(ctx context.Context, tx pgx.Tx) error {
	return InvalidateAuthoritiesWithin(ctx, transaction.PostgreSQLTransaction(tx))
}

func (s *Service) Cleanup(ctx context.Context) error {
	if s == nil || s.Identity == nil || ctx == nil {
		return fail("SSO_CONFIGURATION_REQUIRED")
	}
	if e := s.policy.Validate(); e != nil {
		return e
	}
	store, e := s.Identity.Repository()
	if e != nil {
		return maintenanceError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy.CleanupTimeoutSeconds))
	defer cancel()
	return maintenanceError(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		return cleanupFlowsWithin(ctx, q, s.policy.CleanupBatchSize, s.policy.FlowRetentionDays)
	}))
}

// Run is joined by the caller on shutdown; no detached maintenance service is created.
func (s *Service) Run(ctx context.Context, report func(error)) {
	if s == nil || s.Identity == nil || ctx == nil {
		if report != nil {
			report(fail("SSO_CONFIGURATION_REQUIRED"))
		}
		return
	}
	if e := s.policy.Validate(); e != nil {
		if report != nil {
			report(e)
		}
		return
	}
	ticker := time.NewTicker(config.Seconds(s.policy.CleanupSeconds))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e := s.Cleanup(ctx)
			if ctx.Err() != nil {
				return
			}
			if e != nil && report != nil {
				report(maintenanceError(e))
			}
		}
	}
}

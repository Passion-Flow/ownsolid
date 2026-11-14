package metering

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

var (
	// ErrReportAccessDenied requires workspace ownership or an Admin platform session.
	ErrReportAccessDenied = errors.New("ACCESS_DENIED")
	// ErrReportStorageUnsupported requires the selected PostgreSQL repository.
	ErrReportStorageUnsupported = errors.New("METERING_REPORT_STORAGE_UNSUPPORTED")
)

// WorkspaceReport pairs an authorized showback with optional budget status.
type WorkspaceReport struct {
	Showback ShowbackReport
	Budget   *BudgetStatus
}

// ReadWorkspaceReport uses the Workspace owner's current session and scope
// checks before reading metering data. The caller supplies a server-authenticated
// actor, a workspace selector, and its installed rate and budget policy.
func ReadWorkspaceReport(ctx context.Context, authority *workspaces.Service, actor identity.Auth, workspace string, from, to time.Time, card *RateCard, budget *BudgetPolicy) (WorkspaceReport, error) {
	if ctx == nil || authority == nil || authority.Identity == nil {
		return WorkspaceReport{}, ErrUnavailable
	}
	store, err := authority.Identity.Repository()
	if err != nil {
		return WorkspaceReport{}, err
	}
	if store.Dialect() != transaction.PostgreSQL {
		return WorkspaceReport{}, ErrReportStorageUnsupported
	}

	ctx, cancel := context.WithTimeout(ctx, config.Seconds(authority.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	var out WorkspaceReport
	err = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		resolved, err := authority.ReadScopeWithin(ctx, q, actor, workspace)
		if err != nil {
			return err
		}
		if !(actor.Me.Audience == "admin" && actor.Me.User.IsPlatformAdmin) &&
			(resolved.Role == nil || *resolved.Role != "owner") {
			return ErrReportAccessDenied
		}
		tenant, tenantErr := uuid.Parse(resolved.TenantID)
		workspaceID, workspaceErr := uuid.Parse(resolved.ID)
		if tenantErr != nil || workspaceErr != nil {
			return ErrUnavailable
		}
		scope := Scope{TenantID: tenant, WorkspaceID: workspaceID}
		start, end, err := reportingWindow(scope, from, to)
		if err != nil {
			return err
		}
		if card == nil || len(card.versions) == 0 {
			return ErrInvalidRateCard
		}
		var limit *big.Int
		if budget != nil {
			limit, err = budgetLimit(card, *budget)
			if err != nil {
				return err
			}
		}
		out.Showback, err = readRatedReport(ctx, q, scope, start, end, card)
		if err != nil || budget == nil {
			return err
		}
		status, err := budgetFromReport(out.Showback, *budget, limit)
		if err == nil {
			out.Budget = &status
		}
		return err
	})
	if err != nil {
		return WorkspaceReport{}, err
	}
	return out, nil
}

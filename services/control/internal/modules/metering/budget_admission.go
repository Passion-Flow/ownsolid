package metering

import (
	"context"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

// ErrInvalidBudgetAdmissionMode requires an explicit supported preview mode.
var ErrInvalidBudgetAdmissionMode = errors.New("METERING_INVALID_BUDGET_ADMISSION_MODE")

// BudgetAdmissionMode selects the operator policy to preview.
type BudgetAdmissionMode string

const (
	// BudgetAlertOnly previews alerts at the warning and limit thresholds.
	BudgetAlertOnly BudgetAdmissionMode = "alert_only"
	// BudgetDenyAtKnownLimit previews a denial when the known subtotal reaches the limit.
	BudgetDenyAtKnownLimit BudgetAdmissionMode = "deny_at_known_limit"
)

// BudgetAdmissionPolicy pairs the threshold and explicit mode supplied by the policy owner.
type BudgetAdmissionPolicy struct {
	Budget BudgetPolicy
	Mode   BudgetAdmissionMode
}

// BudgetAdmissionAction describes the policy preview over reported usage.
type BudgetAdmissionAction string

const (
	// BudgetAdmissionClear means the complete estimate is below the warning.
	BudgetAdmissionClear BudgetAdmissionAction = "clear"
	// BudgetAdmissionAlert identifies a threshold that warrants an alert.
	BudgetAdmissionAlert BudgetAdmissionAction = "alert"
	// BudgetAdmissionDeny means the known subtotal meets the explicit deny policy.
	BudgetAdmissionDeny BudgetAdmissionAction = "deny"
	// BudgetAdmissionIndeterminate means unpriced or unmeasured usage prevents a decision.
	BudgetAdmissionIndeterminate BudgetAdmissionAction = "indeterminate"
)

// BudgetAdmissionPreview carries the decision and its authorized price window.
type BudgetAdmissionPreview struct {
	Report WorkspaceReport
	Action BudgetAdmissionAction
}

// PreviewWorkspaceBudgetAdmission applies an explicit policy to the same authorized
// report snapshot. It does not reserve budget, enforce admission or send an alert.
func PreviewWorkspaceBudgetAdmission(ctx context.Context, authority *workspaces.Service, actor identity.Auth, workspace string, from, to time.Time, card *RateCard, policy BudgetAdmissionPolicy) (BudgetAdmissionPreview, error) {
	if policy.Mode != BudgetAlertOnly && policy.Mode != BudgetDenyAtKnownLimit {
		return BudgetAdmissionPreview{}, ErrInvalidBudgetAdmissionMode
	}
	report, err := ReadWorkspaceReport(ctx, authority, actor, workspace, from, to, card, &policy.Budget)
	if err != nil {
		return BudgetAdmissionPreview{}, err
	}

	var action BudgetAdmissionAction
	switch report.Budget.State {
	case BudgetBelowWarning:
		action = BudgetAdmissionClear
	case BudgetWarning:
		action = BudgetAdmissionAlert
	case BudgetLimitReached:
		action = BudgetAdmissionAlert
		if policy.Mode == BudgetDenyAtKnownLimit {
			action = BudgetAdmissionDeny
		}
	case BudgetUnratedUsage, BudgetUnmeasuredUsage:
		action = BudgetAdmissionIndeterminate
	default:
		return BudgetAdmissionPreview{}, ErrUnavailable
	}
	return BudgetAdmissionPreview{Report: report, Action: action}, nil
}

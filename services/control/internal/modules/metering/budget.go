package metering

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	// ErrInvalidBudgetPolicy indicates an invalid limit, warning fraction or currency.
	ErrInvalidBudgetPolicy = errors.New("METERING_INVALID_BUDGET_POLICY")
	// ErrBudgetCurrencyMismatch indicates that the rate card and budget use different currencies.
	ErrBudgetCurrencyMismatch = errors.New("METERING_BUDGET_CURRENCY_MISMATCH")
)

const basisPointsPerWhole = 10000

// BudgetPolicy is an operator's limit for a reporting period. LimitMicros is
// a positive canonical decimal integer in millionths of Currency.
// WarningBasisPoints is a fraction of the limit from 1 through 9999.
type BudgetPolicy struct {
	Currency           string
	LimitMicros        string
	WarningBasisPoints int64
}

// BudgetState describes the rated usage estimate relative to the policy.
type BudgetState string

const (
	// BudgetBelowWarning means fully rated usage is below the warning threshold.
	BudgetBelowWarning BudgetState = "below_warning"
	// BudgetWarning means fully rated usage meets the warning and is below the limit.
	BudgetWarning BudgetState = "warning"
	// BudgetLimitReached means the known subtotal meets or exceeds the limit.
	BudgetLimitReached BudgetState = "limit_reached"
	// BudgetUnratedUsage means unrated usage prevents a below-limit assessment.
	BudgetUnratedUsage BudgetState = "unrated_usage"
	// BudgetUnmeasuredUsage means incomplete token measurements prevent a below-limit assessment.
	BudgetUnmeasuredUsage BudgetState = "unmeasured_usage"
)

// BudgetStatus reports the estimate in one authorized workspace and half-open
// period. FullyRated indicates that the ledger quantities are known and priced.
type BudgetStatus struct {
	Currency                string
	From                    time.Time
	To                      time.Time
	LimitMicros             string
	KnownAmountMicros       string
	UnmeasuredTokenRequests string
	FullyRated              bool
	State                   BudgetState
}

// BudgetWithin evaluates an operator's threshold over a single scoped
// showback snapshot. It returns the observed budget state for reporting.
func BudgetWithin(ctx context.Context, tx pgx.Tx, scope Scope, from, to time.Time, card *RateCard, policy BudgetPolicy) (BudgetStatus, error) {
	limit, err := budgetLimit(card, policy)
	if err != nil {
		return BudgetStatus{}, err
	}
	report, err := ShowbackWithin(ctx, tx, scope, from, to, card)
	if err != nil {
		return BudgetStatus{}, err
	}
	return budgetFromReport(report, policy, limit)
}

func budgetLimit(card *RateCard, policy BudgetPolicy) (*big.Int, error) {
	limit, ok := new(big.Int).SetString(policy.LimitMicros, 10)
	if !ok || limit.Sign() <= 0 || limit.String() != policy.LimitMicros ||
		!accountingCurrency.MatchString(policy.Currency) || policy.WarningBasisPoints <= 0 ||
		policy.WarningBasisPoints >= basisPointsPerWhole {
		return nil, ErrInvalidBudgetPolicy
	}
	if card == nil || len(card.versions) == 0 {
		return nil, ErrInvalidRateCard
	}
	if card.currency != policy.Currency {
		return nil, ErrBudgetCurrencyMismatch
	}
	return limit, nil
}

func budgetFromReport(report ShowbackReport, policy BudgetPolicy, limit *big.Int) (BudgetStatus, error) {
	known, ok := new(big.Int).SetString(report.KnownAmountMicros, 10)
	if !ok || known.Sign() < 0 {
		return BudgetStatus{}, ErrUnavailable
	}

	status := BudgetStatus{
		Currency: report.Currency, From: report.From, To: report.To, LimitMicros: policy.LimitMicros,
		KnownAmountMicros: report.KnownAmountMicros, FullyRated: report.FullyRated,
		UnmeasuredTokenRequests: report.UnmeasuredTokenRequests,
	}
	switch {
	case known.Cmp(limit) >= 0:
		status.State = BudgetLimitReached
	case report.UnmeasuredTokenRequests != "0":
		status.State = BudgetUnmeasuredUsage
	case !report.FullyRated:
		status.State = BudgetUnratedUsage
	default:
		var used, warning big.Int
		used.Mul(known, big.NewInt(basisPointsPerWhole))
		warning.Mul(limit, big.NewInt(policy.WarningBasisPoints))
		status.State = BudgetBelowWarning
		if used.Cmp(&warning) >= 0 {
			status.State = BudgetWarning
		}
	}
	return status, nil
}

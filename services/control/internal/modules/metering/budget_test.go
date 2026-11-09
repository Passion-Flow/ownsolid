package metering

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgresBudgetEvaluatesExactThresholdsAndUnratedUsage(t *testing.T) {
	pool := newTestLedger(t)
	scope := addTestScope(t, pool)
	other := addTestScope(t, pool)
	card := testCard(t, `{"currency":"XTS","versions":[{"id":"v1","effective_at":"2026-11-09T08:00:00Z",
 "rates":[{"meter":"requests","units":1,"amount_micros":1}]}]}`)
	at := time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)
	end := at.Add(time.Hour)
	policy := BudgetPolicy{Currency: "XTS", LimitMicros: "10", WarningBasisPoints: 3333}
	usage := func(id string, units Usage) Event {
		return Event{Producer: "fixture/budget", ID: id, OccurredAt: at, Usage: units}
	}
	appendUsage := func(s Scope, event Event, wantInserted bool) {
		t.Helper()
		if inserted, err := writeEvent(t.Context(), pool, s, event); err != nil || inserted != wantInserted {
			t.Fatalf("budget fixture append: %v, %v", inserted, err)
		}
	}
	read := func(p BudgetPolicy) (BudgetStatus, error) {
		t.Helper()
		var status BudgetStatus
		err := pgx.BeginTxFunc(t.Context(), pool, reportSnapshot, func(tx pgx.Tx) error {
			var err error
			status, err = BudgetWithin(t.Context(), tx, scope, at, end, card, p)
			return err
		})
		return status, err
	}
	assertStatus := func(p BudgetPolicy, state BudgetState, known string, fullyRated bool) {
		t.Helper()
		got, err := read(p)
		want := BudgetStatus{Currency: p.Currency, From: at, To: end, LimitMicros: p.LimitMicros,
			KnownAmountMicros: known, FullyRated: fullyRated, State: state}
		if err != nil || got != want {
			t.Fatalf("budget status: %+v, %v, want %+v", got, err, want)
		}
	}

	appendUsage(scope, usage("initial", Usage{Requests: 3}), true)
	appendUsage(other, usage("foreign", Usage{Requests: 50}), true)
	assertStatus(policy, BudgetBelowWarning, "3", true)
	assertStatus(BudgetPolicy{Currency: "XTS", LimitMicros: "6", WarningBasisPoints: 5000}, BudgetWarning, "3", true)

	appendUsage(scope, usage("warning", Usage{Requests: 1}), true)
	assertStatus(policy, BudgetWarning, "4", true)
	appendUsage(scope, usage("unrated", Usage{Tokens: 2}), true)
	assertStatus(policy, BudgetUnratedUsage, "4", false)

	last := usage("limit", Usage{Requests: 6})
	appendUsage(scope, last, true)
	appendUsage(scope, last, false)
	assertStatus(policy, BudgetLimitReached, "10", false)

	wrong := policy
	wrong.Currency = "EUR"
	if got, err := read(wrong); got != (BudgetStatus{}) || !errors.Is(err, ErrBudgetCurrencyMismatch) {
		t.Fatalf("currency mismatch accepted: %+v, %v", got, err)
	}
	bad := policy
	bad.LimitMicros = "0"
	if got, err := read(bad); got != (BudgetStatus{}) || !errors.Is(err, ErrInvalidBudgetPolicy) {
		t.Fatalf("zero limit accepted: %+v, %v", got, err)
	}
}

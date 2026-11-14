package productagent

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/google/uuid"
)

func TestExecutionBudgetBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name          string
		used, ceiling BudgetCeiling
		tokens        int64
		want          bool
	}{
		{"zero executions", BudgetCeiling{}, BudgetCeiling{Tokens: 1}, 0, false},
		{"deterministic command", BudgetCeiling{}, BudgetCeiling{Executions: 1}, 0, true},
		{"no model allowance", BudgetCeiling{}, BudgetCeiling{Executions: 1}, 1, false},
		{"exact token capacity", BudgetCeiling{1, 2}, BudgetCeiling{2, 5}, 3, true},
		{"execution exhausted", BudgetCeiling{2, 2}, BudgetCeiling{2, 5}, 1, false},
		{"lowered token ceiling", BudgetCeiling{1, 5}, BudgetCeiling{2, 4}, 0, false},
		{"maximum token charge", BudgetCeiling{}, BudgetCeiling{math.MaxInt64, math.MaxInt64}, math.MaxInt64, true},
		{"token overflow denied", BudgetCeiling{1, math.MaxInt64}, BudgetCeiling{math.MaxInt64, math.MaxInt64}, 1, false},
		{"execution overflow denied", BudgetCeiling{math.MaxInt64, 0}, BudgetCeiling{math.MaxInt64, math.MaxInt64}, 0, false},
		{"negative stored usage", BudgetCeiling{1, -1}, BudgetCeiling{2, 5}, 1, false},
		{"negative charge", BudgetCeiling{}, BudgetCeiling{2, 5}, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := budgetFits(tc.used, tc.ceiling, tc.tokens); got != tc.want {
				t.Fatalf("capacity decision %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExecutionBudgetPolicyRequiresCompleteExplicitWindow(t *testing.T) {
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	valid := ExecutionBudgetPolicy{UserAccess: BudgetAccessEnabled, WindowStart: start, WindowEnd: start.Add(time.Hour)}
	if !validBudgetPolicy(valid) {
		t.Fatal("zero ceilings must be valid finite denial, not an unlimited default")
	}
	for _, mutate := range []func(*ExecutionBudgetPolicy){
		func(p *ExecutionBudgetPolicy) { p.UserAccess = "" },
		func(p *ExecutionBudgetPolicy) { p.UserAccess = "unknown" },
		func(p *ExecutionBudgetPolicy) { p.WindowStart = time.Time{} },
		func(p *ExecutionBudgetPolicy) { p.WindowEnd = p.WindowStart },
		func(p *ExecutionBudgetPolicy) { p.WindowEnd = p.WindowStart.Add(-time.Hour) },
		func(p *ExecutionBudgetPolicy) { p.WindowStart = p.WindowStart.Add(time.Nanosecond) },
		func(p *ExecutionBudgetPolicy) { p.WindowEnd = p.WindowEnd.Add(time.Nanosecond) },
		func(p *ExecutionBudgetPolicy) { p.Installation.Executions = -1 },
		func(p *ExecutionBudgetPolicy) { p.Installation.Tokens = -1 },
		func(p *ExecutionBudgetPolicy) { p.User.Executions = -1 },
		func(p *ExecutionBudgetPolicy) { p.User.Tokens = -1 },
		func(p *ExecutionBudgetPolicy) { p.RunSteps.Installation = -1 },
		func(p *ExecutionBudgetPolicy) { p.RunSteps.User = -1 },
	} {
		p := valid
		mutate(&p)
		if validBudgetPolicy(p) {
			t.Fatalf("incomplete policy admitted: %+v", p)
		}
	}
}

func TestExecutionBudgetRejectsUntrustedRequestBeforeStorage(t *testing.T) {
	var gate *ExecutionBudgets
	valid := BudgetExecution{ID: uuid.NewString(), ExecutionSHA256: strings.Repeat("a", 64)}
	for _, execution := range []BudgetExecution{
		{},
		{ID: "client choice", ExecutionSHA256: valid.ExecutionSHA256},
		{ID: valid.ID, ExecutionSHA256: strings.Repeat("A", 64)},
		{ID: valid.ID, ExecutionSHA256: strings.Repeat("a", 63)},
		{ID: valid.ID, ExecutionSHA256: valid.ExecutionSHA256, MaximumTokens: -1},
	} {
		result, err := gate.Reserve(t.Context(), identity.Auth{}, execution)
		if result != (BudgetReservation{}) || err == nil || err.Error() != "VALIDATION_FAILED" {
			t.Fatalf("malformed execution returned %+v, %v", result, err)
		}
	}
	for _, ctx := range []context.Context{nil, t.Context()} {
		if result, err := gate.Reserve(ctx, identity.Auth{}, valid); result != (BudgetReservation{}) || err != ErrCommandDenied {
			t.Fatalf("missing authority returned %+v, %v", result, err)
		}
	}
	if _, err := NewExecutionBudgets(nil, nil); err == nil {
		t.Fatal("constructed a gate without authority")
	}
	for _, settlement := range []BudgetSettlement{
		{},
		{ID: valid.ID, ReceiptSHA256: strings.Repeat("c", 63)},
		{ID: valid.ID, ReceiptSHA256: strings.Repeat("C", 64)},
		{ID: valid.ID, ActualTokens: -1, ReceiptSHA256: strings.Repeat("c", 64)},
	} {
		if result, err := gate.Settle(t.Context(), identity.Auth{}, settlement); result != (BudgetReservation{}) || err == nil || err.Error() != "VALIDATION_FAILED" {
			t.Fatalf("malformed settlement returned %+v, %v", result, err)
		}
	}
	if result, err := gate.Settle(nil, identity.Auth{}, BudgetSettlement{ID: valid.ID, ReceiptSHA256: valid.ExecutionSHA256}); result != (BudgetReservation{}) || err != ErrCommandDenied {
		t.Fatalf("settlement without authority returned %+v, %v", result, err)
	}
}

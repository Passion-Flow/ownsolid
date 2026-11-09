package metering

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

func TestPostgresWorkspaceBudgetAdmissionPreview(t *testing.T) {
	accounts := identityfixture.New(t)
	password := identityfixture.Bootstrap(t, accounts)
	owner := identityfixture.Login(t, accounts, "admin@example.invalid", password, "portal")
	if owner.Me.Workspace == nil || owner.Me.Workspace.Role != "owner" {
		t.Fatal("fixture owner session lacks workspace authority")
	}
	authority := &workspaces.Service{DB: accounts.DB, Identity: accounts}
	ctx := t.Context()
	if _, err := accounts.DB.Exec(ctx, testSchemaSQL); err != nil {
		t.Fatal(err)
	}
	scope := Scope{TenantID: uuid.MustParse(owner.Me.Workspace.TenantID), WorkspaceID: uuid.MustParse(owner.Me.Workspace.ID)}
	workspace := scope.WorkspaceID.String()
	card := testCard(t, testRateCard)
	cutover := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	from, to := cutover.Add(-time.Hour), cutover.Add(time.Hour)
	threshold := BudgetPolicy{Currency: "XTS", LimitMicros: "2", WarningBasisPoints: 5000}
	preview := func(mode BudgetAdmissionMode, workspace string) (BudgetAdmissionPreview, error) {
		return PreviewWorkspaceBudgetAdmission(ctx, authority, owner, workspace, from, to, card,
			BudgetAdmissionPolicy{Budget: threshold, Mode: mode})
	}
	appendEvent := func(id string, at time.Time, usage Usage, wantInserted bool) {
		t.Helper()
		event := Event{Producer: "fixture/budget-admission", ID: id, OccurredAt: at, Usage: usage}
		if inserted, err := writeEvent(ctx, accounts.DB, scope, event); err != nil || inserted != wantInserted {
			t.Fatalf("write %s = %v, %v", id, inserted, err)
		}
	}
	check := func(state BudgetState, known, unmeasured string, complete bool, alert, deny BudgetAdmissionAction) WorkspaceReport {
		t.Helper()
		advisory, err := preview(BudgetAlertOnly, workspace)
		if err != nil {
			t.Fatal(err)
		}
		enforcing, err := preview(BudgetDenyAtKnownLimit, workspace)
		if err != nil {
			t.Fatal(err)
		}
		if advisory.Action != alert || enforcing.Action != deny || !reflect.DeepEqual(advisory.Report, enforcing.Report) {
			t.Fatalf("actions or snapshots differ: %+v, %+v", advisory, enforcing)
		}
		report, budget := advisory.Report.Showback, advisory.Report.Budget
		if budget == nil || budget.State != state || budget.KnownAmountMicros != known ||
			budget.UnmeasuredTokenRequests != unmeasured || budget.FullyRated != complete ||
			budget.From != from || budget.To != to || budget.Currency != threshold.Currency ||
			report.KnownAmountMicros != budget.KnownAmountMicros || report.UnmeasuredTokenRequests != budget.UnmeasuredTokenRequests ||
			report.FullyRated != budget.FullyRated || report.From != budget.From || report.To != budget.To || report.Currency != budget.Currency {
			t.Fatalf("preview and report disagree: %+v, %+v", report, budget)
		}
		return advisory.Report
	}

	appendEvent("excluded-end", to, Usage{Requests: 900}, true)
	appendEvent("zero-rate", cutover, Usage{StorageByteSeconds: 11}, true)
	zero := check(BudgetBelowWarning, "0", "0", true, BudgetAdmissionClear, BudgetAdmissionClear)
	if len(zero.Showback.Lines) != 1 || zero.Showback.Lines[0].Rate == nil || zero.Showback.Lines[0].AmountMicros != "0" {
		t.Fatalf("valid zero rate disappeared: %+v", zero.Showback)
	}
	old := cutover.Add(-time.Microsecond)
	appendEvent("late", old, Usage{Requests: 4}, true)
	appendEvent("late", old, Usage{Requests: 4}, false)
	warning := check(BudgetWarning, "1", "0", true, BudgetAdmissionAlert, BudgetAdmissionAlert)
	if warning.Showback.Lines[0].RateVersion != "first" {
		t.Fatalf("late event used the wrong price window: %+v", warning.Showback)
	}
	appendEvent("missing-rate", cutover, Usage{Tokens: 7}, true)
	check(BudgetUnratedUsage, "1", "0", false, BudgetAdmissionIndeterminate, BudgetAdmissionIndeterminate)
	appendEvent("missing-measurement", old, Usage{Requests: 1, UnmeasuredTokenRequests: 1}, true)
	check(BudgetUnmeasuredUsage, "1", "1", false, BudgetAdmissionIndeterminate, BudgetAdmissionIndeterminate)
	appendEvent("limit", cutover, Usage{Requests: 3}, true)
	threshold.LimitMicros = "3"
	check(BudgetLimitReached, "3", "1", false, BudgetAdmissionAlert, BudgetAdmissionDeny)
	threshold.LimitMicros = "2"
	check(BudgetLimitReached, "3", "1", false, BudgetAdmissionAlert, BudgetAdmissionDeny)

	for _, mode := range []BudgetAdmissionMode{"", "unknown"} {
		if got, err := preview(mode, workspace); !errors.Is(err, ErrInvalidBudgetAdmissionMode) ||
			!reflect.DeepEqual(got, BudgetAdmissionPreview{}) {
			t.Fatalf("an invalid mode returned a decision: %+v, %v", got, err)
		}
	}
	if got, err := preview(BudgetDenyAtKnownLimit, uuid.NewString()); err == nil || err.Error() != "RESOURCE_NOT_FOUND" ||
		!reflect.DeepEqual(got, BudgetAdmissionPreview{}) {
		t.Fatalf("an unavailable workspace exposed a decision: %+v, %v", got, err)
	}
	if _, err := accounts.DB.Exec(ctx, "UPDATE workspace_memberships SET role='member' WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3",
		scope.TenantID, scope.WorkspaceID, owner.Me.User.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := preview(BudgetDenyAtKnownLimit, workspace); !errors.Is(err, ErrReportAccessDenied) ||
		!reflect.DeepEqual(got, BudgetAdmissionPreview{}) {
		t.Fatalf("a stale owner session exposed a decision: %+v, %v", got, err)
	}
}

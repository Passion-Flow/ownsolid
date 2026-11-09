package metering

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestPostgresAuthenticatedWorkspaceReport(t *testing.T) {
	accounts := identityfixture.New(t)
	password := identityfixture.Bootstrap(t, accounts)
	admin := identityfixture.Login(t, accounts, "admin@example.invalid", password, "admin")
	owner := identityfixture.Login(t, accounts, "admin@example.invalid", password, "portal")
	if owner.Me.Workspace == nil || owner.Me.Workspace.Role != "owner" {
		t.Fatal("fixture owner session lacks workspace authority")
	}
	authority := &workspaces.Service{DB: accounts.DB, Identity: accounts}
	ctx := t.Context()
	if _, err := accounts.DB.Exec(ctx, testSchemaSQL); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := accounts.DB.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}

	local := Scope{TenantID: uuid.MustParse(owner.Me.Workspace.TenantID), WorkspaceID: uuid.MustParse(owner.Me.Workspace.ID)}
	foreign := Scope{TenantID: uuid.New(), WorkspaceID: uuid.New()}
	exec("INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign tenant','active')", foreign.TenantID)
	exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign workspace','active')", foreign.WorkspaceID, foreign.TenantID)
	exec("INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')",
		owner.Me.User.ID, foreign.TenantID, foreign.WorkspaceID)

	cutover := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	from := cutover.Add(-time.Hour)
	end := cutover.Add(time.Hour)
	appendUsage := func(scope Scope, id string, at time.Time, usage Usage) {
		t.Helper()
		event := Event{Producer: "fixture/authenticated-report", ID: id, OccurredAt: at, Usage: usage}
		if inserted, err := writeEvent(ctx, accounts.DB, scope, event); err != nil || !inserted {
			t.Fatalf("append fixture usage: %v, %v", inserted, err)
		}
	}
	appendUsage(local, "old", cutover.Add(-time.Microsecond), Usage{Requests: 2})
	appendUsage(local, "new", cutover, Usage{Requests: 4, Tokens: 7, StorageByteSeconds: 11})
	appendUsage(foreign, "foreign", cutover, Usage{Requests: 900})

	card := testCard(t, testRateCard)
	policy := BudgetPolicy{Currency: "XTS", LimitMicros: "10", WarningBasisPoints: 5000}
	read := func(actor identity.Auth, id string) (WorkspaceReport, error) {
		return ReadWorkspaceReport(ctx, authority, actor, id, from, end, card, &policy)
	}
	want := WorkspaceReport{
		Showback: ShowbackReport{Currency: "XTS", From: from, To: end, KnownAmountMicros: "4", UnmeasuredTokenRequests: "0", Lines: []ShowbackLine{
			{Meter: Requests, RateVersion: "first", Quantity: "2", Rate: &MeterRate{Units: 4, AmountMicros: 1}, AmountMicros: "1"},
			{Meter: Tokens, RateVersion: "next", Quantity: "7"},
			{Meter: StorageByteSeconds, RateVersion: "next", Quantity: "11", Rate: &MeterRate{Units: 1}, AmountMicros: "0"},
			{Meter: Requests, RateVersion: "next", Quantity: "4", Rate: &MeterRate{Units: 3, AmountMicros: 2}, AmountMicros: "3"},
		}},
		Budget: &BudgetStatus{Currency: "XTS", From: from, To: end, LimitMicros: "10",
			KnownAmountMicros: "4", UnmeasuredTokenRequests: "0", State: BudgetUnratedUsage},
	}
	for _, actor := range []identity.Auth{owner, admin} {
		if got, err := read(actor, local.WorkspaceID.String()); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("authorized workspace report: %+v, %v", got, err)
		}
		for _, id := range []uuid.UUID{foreign.WorkspaceID, uuid.New()} {
			got, err := read(actor, id.String())
			if err == nil || err.Error() != "RESOURCE_NOT_FOUND" || !reflect.DeepEqual(got, WorkspaceReport{}) {
				t.Fatalf("foreign or missing workspace exposed a report: %+v, %v", got, err)
			}
		}
	}

	supplied := owner
	suppliedWorkspace := *owner.Me.Workspace
	suppliedWorkspace.ID, suppliedWorkspace.TenantID = foreign.WorkspaceID.String(), foreign.TenantID.String()
	supplied.Me.Workspace = &suppliedWorkspace
	if got, err := read(supplied, local.WorkspaceID.String()); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("cached actor workspace overrode the server scope: %+v, %v", got, err)
	}

	exec("UPDATE workspace_memberships SET role='member' WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3",
		local.TenantID, local.WorkspaceID, owner.Me.User.ID)
	if got, err := read(owner, local.WorkspaceID.String()); !errors.Is(err, ErrReportAccessDenied) ||
		!reflect.DeepEqual(got, WorkspaceReport{}) {
		t.Fatalf("stale owner role exposed a report: %+v, %v", got, err)
	}
	exec("UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1", admin.SessionID)
	if got, err := read(admin, local.WorkspaceID.String()); err == nil || err.Error() != "AUTH_SESSION_EXPIRED" ||
		!reflect.DeepEqual(got, WorkspaceReport{}) {
		t.Fatalf("revoked session exposed a report: %+v, %v", got, err)
	}
}

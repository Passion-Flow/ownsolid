package distribution

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

type nativeDistributionProbe struct {
	transaction.Executor
	count int64
	err   error
	calls int
}

func (*nativeDistributionProbe) Dialect() transaction.Dialect { return transaction.Dameng }
func (p *nativeDistributionProbe) Exec(context.Context, transaction.Statement) (int64, error) {
	p.calls++
	return p.count, p.err
}

func TestNativeHistoryCursorPreservesScopeAndMicroseconds(t *testing.T) {
	stamp := time.Date(2026, 10, 24, 1, 2, 3, 123456000, time.UTC)
	actor := "actor' OR 1=1 --"
	statement, e := nativeHistoryPage("requests", "tenant", "workspace", actor, false, "", stamp, "cursor", 100)
	if e != nil {
		t.Fatal(e)
	}
	expected := "SELECT " + nativeRequestFields + nativeRequestJoins + " WHERE R.TENANT_ID=? AND R.WORKSPACE_ID=? AND R.ACTOR_USER_ID=? AND (R.ADMITTED_AT<? OR (R.ADMITTED_AT=? AND R.ID<?)) ORDER BY R.ADMITTED_AT DESC,R.ID DESC LIMIT 101"
	if !reflect.DeepEqual(statement, transaction.SQL(transaction.Dameng, expected, "tenant", "workspace", actor, stamp, stamp, "cursor")) {
		t.Fatal("cursor/actor predicates or binding order changed")
	}
	// Statement redaction must remain intact even for adversarial data.
	if strings.Contains(statement.String(), actor) {
		t.Fatal("statement exposed a business value")
	}
	for _, invalid := range []time.Time{stamp.Add(time.Nanosecond), time.Date(1969, 12, 31, 23, 59, 59, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), stamp.In(time.FixedZone("offset", 3600)), stamp.In(time.FixedZone("zero-offset", 0))} {
		if _, e := nativeHistoryPage("requests", "tenant", "workspace", actor, false, "", invalid, "cursor", 1); e == nil || e.Error() != "INVALID_CURSOR" {
			t.Fatal("unrepresentable native cursor must be rejected, never rounded")
		}
	}
	for _, limit := range []int{0, -1, 101} {
		if _, e := nativeHistoryPage("keys", "t", "w", "u", false, "", time.Time{}, "", limit); e == nil {
			t.Fatal("page bound accepted")
		}
	}
	if _, e := nativeHistoryPage("injected table", "t", "w", "u", true, "", time.Time{}, "", 1); e == nil {
		t.Fatal("unowned selector accepted")
	}
}
func TestNativeHistoryNarrowsRequestsToOneKey(t *testing.T) {
	key := "8d3f0c52-1b7a-4f60-9a3e-2c51d0e7b4a9"
	statement, e := nativeHistoryPage("requests", "tenant", "workspace", "actor", true, key, time.Time{}, "", 20)
	if e != nil {
		t.Fatal(e)
	}
	expected := "SELECT " + nativeRequestFields + nativeRequestJoins + " WHERE R.TENANT_ID=? AND R.WORKSPACE_ID=? AND R.API_KEY_ID=? ORDER BY R.ADMITTED_AT DESC,R.ID DESC LIMIT 21"
	if !reflect.DeepEqual(statement, transaction.SQL(transaction.Dameng, expected, "tenant", "workspace", key)) {
		t.Fatal("the key predicate or its binding changed")
	}
	if _, e := nativeHistoryPage("keys", "tenant", "workspace", "actor", true, key, time.Time{}, "", 20); e == nil {
		t.Fatal("a key filter was accepted on a list that is not of requests")
	}
}
func TestNativeMutationDoesNotRetryPoisonedConnection(t *testing.T) {
	for _, kind := range []transaction.ErrorKind{transaction.UniqueConflict, transaction.CommitUnknown, transaction.Deadlock} {
		original := &transaction.Error{Kind: kind}
		probe := &nativeDistributionProbe{err: original}
		_, e := distributionExecOne(t.Context(), probe, transaction.SQL(transaction.Dameng, "native"))
		if e != original || probe.calls != 1 {
			t.Fatal("original failure must exit without retry")
		}
	}
	for _, count := range []int64{-1, 0, 2} {
		probe := &nativeDistributionProbe{count: count}
		if _, e := distributionExecOne(t.Context(), probe, transaction.SQL(transaction.Dameng, "native")); e == nil {
			t.Fatal("non-single mutation accepted")
		}
	}
	q := &nativeDistributionProbe{}
	if !reflect.DeepEqual(distributionStatement(q, "pg", "mysql", nil, nil), transaction.SQL(transaction.Dameng, "")) {
		t.Fatal("native execution fell through to PostgreSQL")
	}
}

// This probe deliberately gives SELECT-style Exec row counts (-1 or 0).
// The real alias entrypoints must ignore those counts and consume the row.
type nativeAliasLockProbe struct {
	transaction.Executor
	user                             string
	rowError                         error
	affected                         int64
	execCalls, queryCalls, scanCalls int
	statement                        transaction.Statement
}

func (*nativeAliasLockProbe) Dialect() transaction.Dialect { return transaction.Dameng }
func (p *nativeAliasLockProbe) Exec(context.Context, transaction.Statement) (int64, error) {
	p.execCalls++
	return p.affected, nil
}
func (p *nativeAliasLockProbe) QueryRow(_ context.Context, statement transaction.Statement) transaction.Row {
	p.queryCalls++
	p.statement = statement
	return nativeAliasLockRow{p}
}

type nativeAliasLockRow struct{ probe *nativeAliasLockProbe }

func (r nativeAliasLockRow) Scan(dest ...any) error {
	r.probe.scanCalls++
	if r.probe.rowError != nil {
		return r.probe.rowError
	}
	if len(dest) != 1 {
		panic("unexpected user-lock projection")
	}
	*(dest[0].(*string)) = r.probe.user
	return nil
}

type nativeAliasLockPool struct{ *nativeAliasLockProbe }

func (*nativeAliasLockPool) Within(context.Context, bool, func(context.Context, transaction.Executor) error) error {
	panic("borrowed alias check must not open a transaction")
}

func TestNativeAliasUserLocksConsumeRows(t *testing.T) {
	const user = "00000000-0000-4000-8000-000000000001"
	const workspace = "00000000-0000-4000-8000-000000000002"
	const alias = "00000000-0000-4000-8000-000000000003"
	noRows := &transaction.Error{Kind: transaction.NoRows}
	deadlock := &transaction.Error{Kind: transaction.Deadlock}
	cases := []struct {
		name, returned, code string
		rowError             error
	}{
		// An intentionally absent session makes the next authority check reject.
		// Reaching AUTH_SESSION_EXPIRED proves that a real row passed this lock,
		// while preserving current-session revalidation (no fake full admission).
		{"expected_row", user, "AUTH_SESSION_EXPIRED", nil},
		{"different_user", alias, "ACCESS_DENIED", nil},
		{"empty_user", "", "ACCESS_DENIED", nil},
		{"missing_row", "", string(transaction.NoRows), noRows},
		{"sql_failure", "", string(transaction.Deadlock), deadlock},
	}
	for _, method := range []string{"current", "historical"} {
		for _, affected := range []int64{-1, 0} {
			for _, test := range cases {
				name := method + "/" + test.name + "/" + strconv.FormatInt(affected, 10)
				t.Run(name, func(t *testing.T) {
					probe := &nativeAliasLockProbe{user: test.returned, rowError: test.rowError, affected: affected}
					owner := &identity.Service{Storage: &nativeAliasLockPool{probe}}
					service := &Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}}
					auth := identity.Auth{Me: identity.Me{User: identity.User{ID: user}, Audience: "portal"}}
					var e error
					if method == "current" {
						var result Alias
						result, e = service.CurrentAnswerAliasWithin(t.Context(), probe, auth, workspace, alias)
						if !reflect.DeepEqual(result, Alias{}) {
							t.Fatal("failed authority leaked alias DTO")
						}
					} else {
						e = service.CheckHistoricalAliasAccessWithin(t.Context(), probe, auth, workspace, Alias{ID: alias, WorkspaceID: workspace})
					}
					if e == nil || e.Error() != test.code {
						t.Fatalf("expected %s, got %v", test.code, e)
					}
					if test.rowError != nil && e != test.rowError {
						t.Fatal("user-lock row error was replaced")
					}
					if probe.execCalls != 0 || probe.queryCalls != 1 || probe.scanCalls != 1 {
						t.Fatal("user SELECT must consume exactly one row and never use affected-row mutation checks")
					}
					expected := transaction.SQL(transaction.Dameng, "SELECT ID FROM USERS WHERE ID=? FOR UPDATE", user)
					if !reflect.DeepEqual(probe.statement, expected) {
						t.Fatal("user lock lost its exact bound identity or FOR UPDATE")
					}
				})
			}
		}
	}
}

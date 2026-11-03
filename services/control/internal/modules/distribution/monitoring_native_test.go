package distribution

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type monitoringWindowDialect struct {
	transaction.Executor
	dialect transaction.Dialect
}

func (q monitoringWindowDialect) Dialect() transaction.Dialect { return q.dialect }

func TestMonitoringNativeWindowBindsScopeAndHalfOpenUTC(t *testing.T) {
	q := monitoringWindowDialect{dialect: transaction.Dameng}
	from := time.Date(2026, 11, 3, 0, 1, 2, 123456000, time.UTC)
	until := from.Add(time.Hour)
	tenant, workspace := "tenant' OR 1=1 --", uuid.NewString()
	want := transaction.SQL(transaction.Dameng, `SELECT "ID","STATE","CAPACITY_HELD","ADMITTED_AT","DEADLINE_AT","RESOLVED_AT","REPORTED_AT","OUTCOME","OUTCOME_SHA256","MAXIMUM_OUTPUT_TOKENS","MAXIMUM_CONTEXT_TOKENS","MAXIMUM_REQUEST_SECONDS" FROM "GATEWAY_REQUESTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ADMITTED_AT">=? AND "ADMITTED_AT"<? ORDER BY "ADMITTED_AT" DESC,"ID" DESC LIMIT 10001`, tenant, workspace, from, until)
	got := monitoringWindowStatement(q, tenant, workspace, from, until, 10000)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("window projection, original CLOB text/NULL, scope, endpoint or binding order changed")
	}
	if strings.Contains(got.String(), tenant) {
		t.Fatal("statement exposed scope")
	}
	// This monitor bound must not relax the separate101-row history helper.
	if !reflect.DeepEqual(distributionNativePageBound("SELECT ID FROM GATEWAY_REQUESTS", []any{102}), transaction.SQL(transaction.Dameng, "")) {
		t.Fatal("ordinary gateway page policy relaxed")
	}
	for _, maximum := range []int{-1, 0, 10001, int(^uint(0) >> 1)} {
		if !reflect.DeepEqual(monitoringWindowStatement(q, tenant, workspace, from, until, maximum), transaction.SQL(transaction.Dameng, "")) {
			t.Fatal("unbounded literal admitted")
		}
	}
	for _, bad := range []time.Time{from.Add(time.Nanosecond), from.In(time.FixedZone("offset", 3600)), from.In(time.FixedZone("zero", 0)), time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC)} {
		for _, pair := range [][2]time.Time{{bad, until}, {from, bad}} {
			if !reflect.DeepEqual(monitoringWindowStatement(q, tenant, workspace, pair[0], pair[1], 1), transaction.SQL(transaction.Dameng, "")) {
				t.Fatal("native time rounded or timezone guessed")
			}
		}
	}
}

func TestMonitoringWindowPreservesPGMySQLAndUnknownRefusal(t *testing.T) {
	from := time.Date(2026, 11, 3, 0, 1, 2, 123456000, time.UTC)
	until := from.Add(time.Hour)
	for _, dialect := range []transaction.Dialect{transaction.PostgreSQL, transaction.MySQL} {
		q := monitoringWindowDialect{dialect: dialect}
		pg := `SELECT id::text,state,capacity_held,admitted_at,deadline_at,resolved_at,reported_at,outcome,outcome_sha256,maximum_output_tokens,maximum_context_tokens,maximum_request_seconds FROM gateway_requests WHERE tenant_id=$1 AND workspace_id=$2 AND admitted_at>=$3 AND admitted_at<$4 ORDER BY admitted_at DESC,id DESC LIMIT $5`
		my := `SELECT id,state,capacity_held,admitted_at,deadline_at,resolved_at,reported_at,outcome,outcome_sha256,maximum_output_tokens,maximum_context_tokens,maximum_request_seconds FROM gateway_requests WHERE tenant_id=? AND workspace_id=? AND admitted_at>=? AND admitted_at<? ORDER BY admitted_at DESC,id DESC LIMIT ?`
		query := pg
		if dialect == transaction.MySQL {
			query = my
		}
		if !reflect.DeepEqual(monitoringWindowStatement(q, "t", "w", from, until, 25), transaction.SQL(dialect, query, "t", "w", from, until, 26)) {
			t.Fatal("legacy monitoring SQL changed")
		}
	}
	q := monitoringWindowDialect{dialect: transaction.Dialect("unknown")}
	if !reflect.DeepEqual(monitoringWindowStatement(q, "t", "w", from, until, 1), transaction.SQL(q.Dialect(), "")) {
		t.Fatal("unknown dialect used fallback")
	}
}

// These exercise real entrypoint admission before a repository query, not a
// mock assertion that a dialect name establishes monitoring or native capacity.
func TestMonitoringWindowNegativeAdmissionKeepsZeroProjection(t *testing.T) {
	from := time.Date(2026, 11, 3, 0, 1, 2, 123456000, time.UTC)
	until := from.Add(time.Hour)
	i := &identity.Service{}
	s := &Service{Identity: i, Workspaces: &workspaces.Service{Identity: i}}
	for _, a := range []identity.Auth{{}} {
		out, e := s.MonitoringWithin(t.Context(), monitoringWindowDialect{dialect: transaction.Dameng}, a, uuid.NewString(), from, until, 1)
		if e == nil || !reflect.DeepEqual(out, MonitoringWindow{}) {
			t.Fatal("unauthorized snapshot projection escaped")
		}
	}
}

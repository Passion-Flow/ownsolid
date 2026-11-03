package notifications

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

type nativeNoticeRow struct {
	value Notification
	at    time.Time
	err   error
}

func (r nativeNoticeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 1 {
		*dest[0].(*time.Time) = r.at
		return nil
	}
	*dest[0].(*string), *dest[1].(*string), *dest[2].(*string), *dest[3].(*string) = r.value.ID, r.value.WorkspaceID, r.value.WorkflowID, r.value.RunID
	*dest[4].(*string), *dest[5].(**string), *dest[6].(*time.Time), *dest[7].(**time.Time) = r.value.Outcome, r.value.ErrorCode, r.value.OccurredAt, r.value.ReadAt
	return nil
}

type nativeNoticeExecutor struct {
	transaction.Executor
	t                 *testing.T
	rows              []nativeNoticeRow
	counts            []int64
	execErrors        []error
	queries, executes int
}

func (*nativeNoticeExecutor) Dialect() transaction.Dialect { return transaction.Dameng }
func (q *nativeNoticeExecutor) QueryRow(_ context.Context, statement transaction.Statement) transaction.Row {
	q.t.Helper()
	if q.queries >= len(q.rows) {
		q.t.Fatal("query continued after failure")
	}
	want := transaction.SQL(transaction.Dameng, "SELECT "+damengFields+damengVisible+" AND n.id=? FOR UPDATE", "actor", "actor", "actor", "tenant", -30, "actor", "notice")
	if q.queries == 1 {
		want = transaction.SQL(transaction.Dameng, "SELECT read_at FROM terminal_notifications WHERE tenant_id=? AND recipient_id=? AND id=?", "tenant", "actor", "notice")
	}
	if !reflect.DeepEqual(statement, want) {
		q.t.Fatal("native current-source recipient/tenant binding or readback changed")
	}
	r := q.rows[q.queries]
	q.queries++
	return r
}
func (q *nativeNoticeExecutor) Exec(_ context.Context, statement transaction.Statement) (int64, error) {
	q.t.Helper()
	if q.executes >= len(q.counts) {
		q.t.Fatal("statement continued after failure")
	}
	if q.executes == 0 && !reflect.DeepEqual(statement, transaction.SQL(transaction.Dameng, "UPDATE terminal_notifications SET read_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE tenant_id=? AND recipient_id=? AND id=? AND read_at IS NULL", "tenant", "actor", "notice")) {
		q.t.Fatal("native read guard or parameter order changed")
	}
	i := q.executes
	q.executes++
	return q.counts[i], q.execErrors[i]
}

func TestDamengNotificationReadAuditAndRevocationBoundary(t *testing.T) {
	at := time.Date(2026, 10, 24, 7, 0, 0, 123456000, time.UTC)
	notice := Notification{ID: "notice", WorkspaceID: "workspace", WorkflowID: "workflow", RunID: "run", Outcome: "succeeded", OccurredAt: at}
	read := notice
	read.ReadAt = &at
	unknown := errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitUnknown})
	for _, tc := range []struct {
		name           string
		rows           []nativeNoticeRow
		counts         []int64
		execErrors     []error
		wantCode       string
		wantQueries    int
		wantExecutions int
	}{
		{"success", []nativeNoticeRow{{value: notice}, {at: at}}, []int64{1, 1}, []error{nil, nil}, "", 2, 2},
		{"already_read", []nativeNoticeRow{{value: read}}, nil, nil, "", 1, 0},
		{"revoked_or_foreign_source", []nativeNoticeRow{{err: sql.ErrNoRows}}, nil, nil, "NOTIFICATION_NOT_FOUND", 1, 0},
		{"zero_update", []nativeNoticeRow{{value: notice}}, []int64{0}, []error{nil}, "STORAGE_UNAVAILABLE", 1, 1},
		{"multi_update", []nativeNoticeRow{{value: notice}}, []int64{2}, []error{nil}, "STORAGE_UNAVAILABLE", 1, 1},
		{"poisoned_update", []nativeNoticeRow{{value: notice}}, []int64{0}, []error{unknown}, string(transaction.CommitUnknown), 1, 1},
		{"readback_failed", []nativeNoticeRow{{value: notice}, {err: errors.New("PRIVATE_DRIVER")}}, []int64{1}, []error{nil}, "STORAGE_UNAVAILABLE", 2, 1},
		{"zero_audit", []nativeNoticeRow{{value: notice}, {at: at}}, []int64{1, 0}, []error{nil, nil}, "STORAGE_UNAVAILABLE", 2, 2},
		{"multi_audit", []nativeNoticeRow{{value: notice}, {at: at}}, []int64{1, 2}, []error{nil, nil}, "STORAGE_UNAVAILABLE", 2, 2},
		{"unknown_audit", []nativeNoticeRow{{value: notice}, {at: at}}, []int64{1, 0}, []error{nil, unknown}, string(transaction.CommitUnknown), 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{Identity: &identity.Service{}}
			a := identity.Auth{}
			a.Me.User.ID, a.Me.Audience = "actor", "portal"
			q := &nativeNoticeExecutor{t: t, rows: tc.rows, counts: tc.counts, execErrors: tc.execErrors}
			got, err := s.readWithin(t.Context(), q, a, "tenant", "notice", "request")
			err = notificationError(err)
			if tc.wantCode == "" {
				if err != nil || !reflect.DeepEqual(got, read) {
					t.Fatal("native notice read/readback failed", err)
				}
			} else if err == nil || err.Error() != tc.wantCode || !reflect.DeepEqual(got, Notification{}) {
				t.Fatal("failure leaked DTO or changed outcome", err)
			}
			if q.queries != tc.wantQueries || q.executes != tc.wantExecutions {
				t.Fatal("statement continued after failure or audit was skipped")
			}
		})
	}
}

func TestDamengNotificationPagingRetentionAndSourceScope(t *testing.T) {
	s := &Service{Identity: &identity.Service{}}
	a := identity.Auth{}
	a.Me.User.ID = "actor"
	q := &nativeNoticeExecutor{}
	at := time.Date(2026, 10, 24, 7, 0, 0, 123456000, time.UTC)
	for _, token := range []string{"w.status='active'", "m.user_id=?", "r.owner_id=?", "n.recipient_id=?", "n.tenant_id=?", "c.owner_id=?", "c.deleted_at IS NULL", "c.tenant_id=r.tenant_id", "c.workspace_id=r.workspace_id"} {
		if !strings.Contains(damengVisible, token) {
			t.Fatal("native notification visibility lost current source authority")
		}
	}
	cursor := pagination.Cursor{Time: at, ID: "11111111-1111-4111-8111-111111111111"}
	got, err := s.pageQuery(q, a, "tenant", cursor, 7)
	want := transaction.SQL(transaction.Dameng, "SELECT "+damengFields+damengVisible+" AND (n.occurred_at<? OR (n.occurred_at=? AND n.id<?)) ORDER BY n.occurred_at DESC,n.id DESC LIMIT 8", "actor", "actor", "actor", "tenant", -30, "actor", at, at, cursor.ID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("native cursor/retention argument order changed", err)
	}
	got, err = s.cleanupQuery(q, "tenant")
	want = transaction.SQL(transaction.Dameng, "SELECT id FROM terminal_notifications WHERE tenant_id=? AND occurred_at<TIMESTAMPADD(SQL_TSI_DAY,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) ORDER BY occurred_at,id LIMIT 100 FOR UPDATE SKIP LOCKED", "tenant", -30)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("native retention bound or lock changed", err)
	}
	for _, invalid := range []time.Time{{}, at.Add(time.Nanosecond), time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		cursor.Time = invalid
		if _, err := s.pageQuery(q, a, "tenant", cursor, 7); err == nil || err.Error() != "INVALID_CURSOR" {
			t.Fatal("unrepresentable native cursor accepted", err)
		}
	}
	for _, invalid := range []int{-1, 0, pagination.MaximumLimit + 1} {
		if _, err := s.pageQuery(q, a, "tenant", pagination.Cursor{}, invalid); err == nil {
			t.Fatal("unbounded native LIMIT accepted")
		}
	}
}

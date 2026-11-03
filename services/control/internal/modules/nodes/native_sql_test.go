package nodes

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

type nativeSQLExecutor struct{ dialect transaction.Dialect }

func (q nativeSQLExecutor) Dialect() transaction.Dialect { return q.dialect }
func (nativeSQLExecutor) Exec(context.Context, transaction.Statement) (int64, error) {
	panic("statement contract must not execute SQL")
}
func (nativeSQLExecutor) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	panic("statement contract must not execute SQL")
}
func (nativeSQLExecutor) QueryRow(context.Context, transaction.Statement) transaction.Row {
	panic("statement contract must not execute SQL")
}

func TestNativeNodeStatementBindingAndSelection(t *testing.T) {
	q := nativeSQLExecutor{transaction.Dameng}
	got := nodeSQL(q, "PG MUST NOT EXECUTE", "MYSQL MUST NOT EXECUTE", "SELECT ? FROM DUAL", "literal $1 ?")
	want := transaction.SQL(transaction.Dameng, "SELECT ? FROM DUAL", "literal $1 ?")
	if !reflect.DeepEqual(got, want) {
		t.Fatal("native branch rewrote business bytes or selected another vendor")
	}
	raw := []byte("{\"text\":\"\u4e16\u754c ? $1\"}")
	if nodeJSON(q, raw) != string(raw) || !reflect.DeepEqual(nodeJSON(nativeSQLExecutor{transaction.PostgreSQL}, raw), raw) || nodeJSON(q, nil) != nil {
		t.Fatal("JSON CLOB binding changed null or nonnative behavior")
	}
	hash := []byte{0, 1, 255}
	args := componentInsertArgs(q, "task", Node{ID: "node", TenantID: "tenant", WorkspaceID: "workspace"}, "op", "key", "actor", raw, hash, 37)
	if len(args) != 10 || args[7] != 37 || args[8] != string(raw) || !reflect.DeepEqual(args[9], hash) {
		t.Fatal("native task JSON and binary fence bindings were reordered")
	}
	unknown := nativeSQLExecutor{transaction.Dialect("unsupported")}
	if !reflect.DeepEqual(nodeSQL(unknown, "pg", "my", "dm"), transaction.SQL(unknown.Dialect(), "")) {
		t.Fatal("unknown engine fell back to a known vendor")
	}
}

func TestNativeNodeCursorPreservesRepresentableOrder(t *testing.T) {
	q := nativeSQLExecutor{transaction.Dameng}
	now := time.Date(2026, 10, 24, 1, 2, 3, 123456000, time.UTC)
	cursor := pagination.Cursor{Time: now, ID: "id"}
	got := nodeSeek(q, "pg", "my", "SELECT id FROM node_tasks WHERE tenant_id=?", []any{"tenant"}, []any{"tenant"}, "created_at", "id", cursor, pagination.MaximumLimit)
	want := transaction.SQL(transaction.Dameng, "SELECT id FROM node_tasks WHERE tenant_id=? AND (created_at<? OR (created_at=? AND id<?)) ORDER BY created_at DESC,id DESC LIMIT 101", "tenant", now, now, "id")
	if !reflect.DeepEqual(got, want) {
		t.Fatal("native cursor lost tied-time ordering or bound business values")
	}
	for _, value := range []time.Time{now.Add(time.Nanosecond), time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC), now.In(time.FixedZone("offset", 3600)), now.In(time.FixedZone("zero-offset-alias", 0))} {
		if nodeNativeTime(value) {
			t.Fatal("unrepresentable native timestamp accepted")
		}
		cursor.Time = value
		if !reflect.DeepEqual(nodeSeek(q, "pg", "my", "dm", nil, nil, "created_at", "id", cursor, 1), transaction.SQL(transaction.Dameng, "")) {
			t.Fatal("invalid cursor silently truncated or executed")
		}
	}
	if !nodeNativeTime(now) {
		t.Fatal("valid native timestamp refused")
	}
}

func TestNativeNodeTimeMatchesDriverContract(t *testing.T) {
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	upper := time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
	for _, test := range []struct {
		name  string
		value time.Time
		want  bool
	}{
		{name: "UTC lower year", value: epoch, want: true},
		{name: "UTC first microsecond", value: epoch.Add(time.Microsecond), want: true},
		{name: "UTC upper year and microsecond", value: upper, want: true},
		{name: "zero time", value: time.Time{}, want: false},
		{name: "before lower year", value: epoch.Add(-time.Microsecond), want: false},
		{name: "after upper year", value: upper.Add(time.Microsecond), want: false},
		{name: "submicrosecond after lower bound", value: epoch.Add(time.Nanosecond), want: false},
		{name: "submicrosecond before next microsecond", value: epoch.Add(999 * time.Nanosecond), want: false},
		{name: "submicrosecond after upper bound", value: upper.Add(time.Nanosecond), want: false},
		{name: "named zero-offset alias", value: epoch.In(time.FixedZone("zero-offset-alias", 0)), want: false},
		{name: "UTC named custom location", value: epoch.In(time.FixedZone("UTC", 0)), want: false},
		{name: "nonzero offset", value: epoch.In(time.FixedZone("offset", 3600)), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nodeNativeTime(test.value); got != test.want {
				t.Fatalf("native timestamp admission = %t, want %t", got, test.want)
			}
		})
	}
}

func TestNativeNodeCommitUnknownPrecedesCancellation(t *testing.T) {
	e := errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitUnknown})
	for _, normalize := range []func(error) error{nodeRepositoryError, taskRuntimeError, enrollmentError, joinWriteError} {
		if got := normalize(e); got == nil || got.Error() != string(transaction.CommitUnknown) {
			t.Fatal("uncertain native outcome was flattened to cancellation", got)
		}
	}
	for _, fields := range []string{damengNodeFields, damengJoinFields, damengTaskFields} {
		for _, forbidden := range []string{"::", "UTC_TIMESTAMP", "make_interval"} {
			if strings.Contains(fields, forbidden) {
				t.Fatal("native field projection contains another vendor's syntax")
			}
		}
	}
}

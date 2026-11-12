package audit_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type scopedExecutor struct {
	dialect transaction.Dialect
	calls   int
	rows    int64
	err     error
}

func (q *scopedExecutor) Dialect() transaction.Dialect { return q.dialect }
func (q *scopedExecutor) Exec(context.Context, transaction.Statement) (int64, error) {
	q.calls++
	return q.rows, q.err
}
func (*scopedExecutor) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	panic("unexpected query")
}
func (*scopedExecutor) QueryRow(context.Context, transaction.Statement) transaction.Row {
	panic("unexpected query row")
}

type scopedPool struct{ *scopedExecutor }

func (*scopedPool) Within(context.Context, bool, func(context.Context, transaction.Executor) error) error {
	panic("writer must not own a transaction")
}
func scopedTestEvent() audit.ScopedEvent {
	return audit.ScopedEvent{
		ActorUserID: "aaaaaaaa-1111-4111-8111-111111111111",
		TenantID:    "bbbbbbbb-2222-4222-8222-222222222222",
		WorkspaceID: "cccccccc-3333-4333-8333-333333333333",
		RequestID:   "dddddddd-4444-4444-8444-444444444444",
		SiteID:      "site-a", CellID: "cell-a", Audience: "admin",
		Action: "audit.events_read", Outcome: "success",
		Reason: "workspace_audit_read;limit=2;returned=1;assistant=false",
	}
}
func TestScopedAuditRejectsInvalidEventsBeforeSQL(t *testing.T) {
	cases := []struct {
		name string
		edit func(*audit.ScopedEvent)
	}{
		{"missing actor", func(e *audit.ScopedEvent) { e.ActorUserID = "" }},
		{"nil tenant", func(e *audit.ScopedEvent) { e.TenantID = "00000000-0000-0000-0000-000000000000" }},
		{"noncanonical workspace", func(e *audit.ScopedEvent) { e.WorkspaceID = strings.ToUpper(e.WorkspaceID) }},
		{"missing request", func(e *audit.ScopedEvent) { e.RequestID = "" }},
		{"missing site", func(e *audit.ScopedEvent) { e.SiteID = "" }},
		{"invalid cell", func(e *audit.ScopedEvent) { e.CellID = "../cell" }},
		{"portal actor", func(e *audit.ScopedEvent) { e.Audience = "portal" }},
		{"empty action", func(e *audit.ScopedEvent) { e.Action = "" }},
		{"unbounded action", func(e *audit.ScopedEvent) { e.Action = strings.Repeat("a", 65) }},
		{"invalid action", func(e *audit.ScopedEvent) { e.Action = "audit.read\nprivate" }},
		{"assistant namespace", func(e *audit.ScopedEvent) { e.Action = "product_assistant.read" }},
		{"invalid outcome", func(e *audit.ScopedEvent) { e.Outcome = "private failure text" }},
		{"missing target id", func(e *audit.ScopedEvent) { e.TargetType = "workspace" }},
		{"missing target type", func(e *audit.ScopedEvent) { e.TargetID = e.WorkspaceID }},
		{"invalid target type", func(e *audit.ScopedEvent) { e.TargetType = "private target"; e.TargetID = e.WorkspaceID }},
		{"unbounded reason", func(e *audit.ScopedEvent) { e.Reason = strings.Repeat("a", 501) }},
		{"invalid UTF8 reason", func(e *audit.ScopedEvent) { e.Reason = string([]byte{0xff}) }},
		{"control reason", func(e *audit.ScopedEvent) { e.Reason = "safe\x00private" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := scopedTestEvent()
			tc.edit(&e)
			q := &scopedExecutor{dialect: transaction.PostgreSQL, rows: 1}
			if err := audit.WriteScopedWithin(t.Context(), q, e); !errors.Is(err, audit.ErrScopedEventInvalid) || q.calls != 0 {
				t.Fatalf("invalid event reached SQL: error=%v calls=%d", err, q.calls)
			}
		})
	}
}
func TestScopedAuditAcceptsBoundedServerFacts(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "accepted"} {
		event := scopedTestEvent()
		event.Action = strings.Repeat("a", 64)
		event.TargetType, event.TargetID = strings.Repeat("b", 64), event.WorkspaceID
		event.Reason, event.Outcome = strings.Repeat("c", 500), outcome
		q := &scopedExecutor{dialect: transaction.PostgreSQL, rows: 1}
		if err := audit.WriteScopedWithin(t.Context(), q, event); err != nil || q.calls != 1 {
			t.Fatalf("bounded server facts rejected: %v", err)
		}
	}
}

func TestScopedAuditBorrowAndAppendOutcomes(t *testing.T) {
	event := scopedTestEvent()
	for _, tc := range []struct {
		name string
		rows int64
		fail error
		want string
	}{
		{"confirmed", 1, nil, ""},
		{"scope denial", 0, nil, "AUDIT_SCOPE_DENIED"},
		{"unconfirmed", 2, nil, "AUDIT_WRITE_UNCONFIRMED"},
		{"append failure", 0, errors.New("fixture append unavailable"), "fixture append unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &scopedExecutor{dialect: transaction.PostgreSQL, rows: tc.rows, err: tc.fail}
			err := audit.WriteScopedWithin(t.Context(), q, event)
			if q.calls != 1 || (err == nil) != (tc.want == "") || err != nil && err.Error() != tc.want {
				t.Fatalf("append outcome: %v calls=%d", err, q.calls)
			}
		})
	}
	q := &scopedExecutor{dialect: transaction.PostgreSQL, rows: 1}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := audit.WriteScopedWithin(ctx, q, event); !errors.Is(err, context.Canceled) || q.calls != 0 {
		t.Fatalf("cancelled append reached SQL: %v", err)
	}
	if err := audit.WriteScopedWithin(nil, q, event); !errors.Is(err, audit.ErrScopedEventInvalid) || q.calls != 0 {
		t.Fatalf("nil context reached SQL: %v", err)
	}
	for _, invalid := range []transaction.Executor{nil, &scopedPool{q}} {
		if err := audit.WriteScopedWithin(t.Context(), invalid, event); err == nil || err.Error() != "AUDIT_TRANSACTION_REQUIRED" {
			t.Fatalf("nontransaction accepted: %v", err)
		}
	}
	q.dialect = transaction.MySQL
	if err := audit.WriteScopedWithin(t.Context(), q, event); err == nil || err.Error() != "AUDIT_DIALECT_UNSUPPORTED" || q.calls != 0 {
		t.Fatalf("unsupported dialect reached SQL: %v", err)
	}
}

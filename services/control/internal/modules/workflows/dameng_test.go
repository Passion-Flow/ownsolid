package workflows

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	"reflect"
	"strings"
	"testing"
	"time"
)

type nativeWorkflowExecutor struct {
	transaction.Executor
	dialect transaction.Dialect
	calls   int
	err     error
}

func (q *nativeWorkflowExecutor) Dialect() transaction.Dialect { return q.dialect }
func (q *nativeWorkflowExecutor) Exec(context.Context, transaction.Statement) (int64, error) {
	q.calls++
	return 0, q.err
}

type nativeWorkflowOutcomeStore struct{ nativeWorkflowExecutor }

func (q *nativeWorkflowOutcomeStore) Within(context.Context, bool, func(context.Context, transaction.Executor) error) error {
	q.calls++
	return q.err
}

func TestDamengWorkflowBindings(t *testing.T) {
	q := &nativeWorkflowExecutor{dialect: transaction.Dameng}
	graph := []byte(`{"schema_version":2,"nodes":[],"edges":[]}`)
	input := []any{"id", "tenant", "workspace", "name", "name-key", "authority", graph, "hash", "actor"}
	original := append([]any(nil), input...)
	got := workflowStatement(q, workflowCreate, input...)
	expected := append([]any(nil), input...)
	expected[6] = string(graph)
	if !reflect.DeepEqual(got, transaction.SQL(transaction.Dameng, workflowCreate.dameng, expected...)) || !reflect.DeepEqual(input, original) {
		t.Fatal("CLOB binding or caller arguments changed")
	}
	got = workflowStatement(q, workflowUpdate, "id", "name", "key", false, "authority", graph, "hash")
	if !reflect.DeepEqual(got, transaction.SQL(transaction.Dameng, workflowUpdate.dameng, "name", "key", false, "authority", string(graph), "hash", "id")) {
		t.Fatal("native update did not preserve explicit false and positional JSON")
	}
	for _, bad := range []any{[]byte(`invalid`), nil, 13} {
		input[6] = bad
		if !reflect.DeepEqual(workflowStatement(q, workflowCreate, input...), transaction.SQL(transaction.Dameng, "")) {
			t.Fatal("invalid JSON reached executor")
		}
	}
	digest := []byte{0, 1, 2}
	if !reflect.DeepEqual(workflowStatement(q, workflowSaveIntent, "t", "w", "a", "k", "p", digest, "i"), transaction.SQL(transaction.Dameng, workflowSaveIntent.dameng, "t", "w", "a", "k", "p", digest, "i")) {
		t.Fatal("binary digest converted to text")
	}
	for _, limit := range []any{0, 1001, "2"} {
		if !reflect.DeepEqual(workflowStatement(q, workflowExpiredCandidates, limit), transaction.SQL(transaction.Dameng, "")) {
			t.Fatal("unbounded native limit")
		}
	}
	if !reflect.DeepEqual(workflowStatement(q, workflowRetentionRuns, "t", "w", 30, 7), transaction.SQL(transaction.Dameng, workflowRetentionRuns.dameng+" LIMIT 7 FOR UPDATE SKIP LOCKED", "t", "w", 30)) {
		t.Fatal("retention lost locking or scoped arguments")
	}
	for _, dialect := range []transaction.Dialect{transaction.PostgreSQL, transaction.MySQL} {
		q.dialect = dialect
		query := workflowConversationCreate
		text := query.postgres
		if dialect == transaction.MySQL {
			text = query.mysql
		}
		if !reflect.DeepEqual(workflowStatement(q, query, "1", "2", "3", "4", "5", "6"), transaction.SQL(dialect, text, "1", "2", "3", "4", "5", "6")) {
			t.Fatal("existing dialect changed")
		}
	}
	q.dialect = "unknown"
	if !reflect.DeepEqual(workflowStatement(q, workflowCreate), transaction.SQL(q.dialect, "")) {
		t.Fatal("unknown dialect fell back")
	}
}

func TestDamengWorkflowNativeCursorAndCommitBoundary(t *testing.T) {
	cursor := pagination.Cursor{ID: uuid.NewString(), Time: time.Date(2026, 10, 24, 1, 2, 3, 123456000, time.UTC)}
	got, e := workflowNativePage("SELECT id FROM workflows WHERE workspace_id=?", "created_at", "id", []any{"w"}, cursor, 1)
	expected := transaction.SQL(transaction.Dameng, "SELECT id FROM workflows WHERE workspace_id=? AND (created_at<? OR (created_at=? AND id<?)) ORDER BY created_at DESC,id DESC LIMIT 2", "w", cursor.Time, cursor.Time, cursor.ID)
	if e != nil || !reflect.DeepEqual(got, expected) {
		t.Fatal("native timestamp tie seek", e)
	}
	for _, stamp := range []time.Time{cursor.Time.Add(time.Nanosecond), time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC), cursor.Time.In(time.FixedZone("offset", 3600))} {
		cursor.Time = stamp
		if _, e = workflowNativePage("SELECT id FROM workflows", "created_at", "id", nil, cursor, 1); e == nil || e.Error() != "INVALID_CURSOR" {
			t.Fatal("native cursor silently normalized")
		}
	}
	unknown := errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitUnknown})
	if kind, ok := transaction.CommitOutcome(workflowRepositoryError(unknown)); !ok || kind != transaction.CommitUnknown {
		t.Fatal("unknown commit lost to cancellation")
	}
	if e := workflowRepositoryError(errors.Join(context.Canceled, errors.New("private secret"))); strings.Contains(e.Error(), "private") || e.Error() == string(transaction.Cancelled) {
		t.Fatal("mixed error leaked or cancelled")
	}
	store := &nativeWorkflowOutcomeStore{nativeWorkflowExecutor{dialect: transaction.Dameng, err: unknown}}
	owner := &identity.Service{Storage: store}
	s := &Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	execution := &runExecution{actor: identity.Auth{Me: identity.Me{Audience: "portal"}}, workspace: uuid.NewString(), id: uuid.NewString()}
	s.execute(ctx, cancel, execution, "synthetic")
	if store.calls != 1 {
		t.Fatal("uncertain claim was compensated or replayed")
	}
	q := &nativeWorkflowExecutor{dialect: transaction.Dameng, err: &transaction.Error{Kind: transaction.Unavailable}}
	if InvalidateAuthoritiesWithin(context.Background(), q) == nil || q.calls != 1 {
		t.Fatal("restore continued after native SQL failure")
	}
}

package datasets

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// These narrow executor tests check dispatch and failure control, not DM syntax
// or driver behavior. The tagged native lifecycle test exercises the real store.
type datasetNativeRecorder struct {
	transaction.Executor
	dialect    transaction.Dialect
	statements []transaction.Statement
	failAt     int
}

func (r *datasetNativeRecorder) Dialect() transaction.Dialect { return r.dialect }
func (r *datasetNativeRecorder) Exec(_ context.Context, s transaction.Statement) (int64, error) {
	r.statements = append(r.statements, s)
	if len(r.statements) == r.failAt {
		return 0, &transaction.Error{Kind: transaction.UniqueConflict}
	}
	return 1, nil
}

func TestDatasetDamengDispatchAndExactTime(t *testing.T) {
	dm := transaction.SQL(transaction.Dameng, "native", 1)
	for _, dialect := range []transaction.Dialect{transaction.Dameng, transaction.PostgreSQL, transaction.MySQL, "unknown"} {
		q := &datasetNativeRecorder{dialect: dialect}
		got := datasetSQL(q, "pg", "my", []any{2}, []any{3}, dm)
		want := transaction.Statement{}
		switch dialect {
		case transaction.Dameng:
			want = dm
		case transaction.PostgreSQL:
			want = transaction.SQL(dialect, "pg", 2)
		case transaction.MySQL:
			want = transaction.SQL(dialect, "my", 3)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("vendor dispatch changed")
		}
	}
	for _, c := range []struct {
		value time.Time
		valid bool
	}{
		{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC), true},
		{time.Date(1969, 12, 31, 23, 59, 59, 0, time.UTC), false},
		{time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), false},
		{time.Date(2026, 10, 24, 1, 2, 3, 1, time.UTC), false},
		{time.Date(2026, 10, 24, 1, 2, 3, 0, time.FixedZone("offset", 3600)), false},
		{time.Date(2026, 10, 24, 1, 2, 3, 0, time.FixedZone("zero-offset", 0)), false},
	} {
		if datasetDamengTime(c.value) != c.valid {
			t.Fatal("cursor representability changed", c.value)
		}
	}
}

func TestDatasetDamengInspectionStopsOnStatementFailure(t *testing.T) {
	rows := [][]any{
		{"row-a", "tenant", "workspace", "dataset", "draft", int64(1), "a", "\u4e16\u754c", "response", nil, "", false},
		{"row-b", "tenant", "workspace", "dataset", "draft", int64(2), nil, nil, nil, "INVALID", "bad", true},
	}
	history := [][]any{{"tenant", "workspace", "dataset", "draft", "row-a", int64(1), "actor", "imported", "digest", "reason"}}
	for _, failAt := range []int{0, 2, 3} {
		q := &datasetNativeRecorder{dialect: transaction.Dameng, failAt: failAt}
		e := insertInspectedRows(context.Background(), q, rows, history)
		wantCount := 3
		if failAt > 0 {
			wantCount = failAt
			var storage *transaction.Error
			if !errors.As(e, &storage) || storage.Kind != transaction.UniqueConflict {
				t.Fatal("native failure swallowed", e)
			}
		} else if e != nil {
			t.Fatal(e)
		}
		if len(q.statements) != wantCount {
			t.Fatal("SQL continued after poisoned statement")
		}
		if !reflect.DeepEqual(q.statements[0], transaction.SQL(transaction.Dameng, dmInsertInspectedRow, rows[0]...)) {
			t.Fatal("first row binding changed")
		}
		if len(q.statements) == 3 && !reflect.DeepEqual(q.statements[2], transaction.SQL(transaction.Dameng, dmInsertInspectedHistory, history[0]...)) {
			t.Fatal("history binding changed")
		}
	}
}

func TestDatasetDamengUnknownCommitSuppressesProjection(t *testing.T) {
	result, e := committed(Dataset{ID: "must-not-escape"}, &transaction.Error{Kind: transaction.CommitUnknown})
	if result.ID != "" || e == nil || e.Error() != string(transaction.CommitUnknown) {
		t.Fatal("uncertain commit exposed success")
	}
}

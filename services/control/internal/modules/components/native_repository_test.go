package components

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// An explicitly scripted executor tests the abort boundary, not native SQL
// execution. The actual native profile has a separate build-tagged test.
type nativeComponentProbe struct {
	statements            []transaction.Statement
	readError, writeError error
	changed               int64
}

func (*nativeComponentProbe) Dialect() transaction.Dialect { return transaction.Dameng }
func (q *nativeComponentProbe) Exec(_ context.Context, s transaction.Statement) (int64, error) {
	q.statements = append(q.statements, s)
	return q.changed, q.writeError
}
func (*nativeComponentProbe) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	panic("unexpected native component rows query")
}
func (q *nativeComponentProbe) QueryRow(_ context.Context, s transaction.Statement) transaction.Row {
	q.statements = append(q.statements, s)
	return nativeComponentRow{q.readError}
}

type nativeComponentRow struct{ err error }

func (r nativeComponentRow) Scan(dest ...any) error {
	if r.err == nil {
		*dest[0].(*string) = wire.NodeExporter
	}
	return r.err
}

func TestNativeComponentStatementAndOutcome(t *testing.T) {
	q := &nativeComponentProbe{}
	if !reflect.DeepEqual(componentSQL(q, "pg", "my", "SELECT ? FROM DUAL", "? $1"), transaction.SQL(transaction.Dameng, "SELECT ? FROM DUAL", "? $1")) {
		t.Fatal("native statement selected another dialect or rewrote business data")
	}
	raw := []byte("{\"message\":\"\u4e16\u754c\"}")
	if componentJSON(q, raw) != string(raw) || componentJSON(q, nil) != nil {
		t.Fatal("native JSON CLOB binding changed bytes or NULL")
	}
	if e := componentError(errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitUnknown})); e == nil || e.Error() != string(transaction.CommitUnknown) {
		t.Fatal("unknown commit flattened to cancellation", e)
	}
}

func TestNativeComponentConflictAbortsWithoutReread(t *testing.T) {
	s := &Service{}
	n := nodes.Node{TenantID: "tenant", WorkspaceID: "workspace", ID: "node"}
	c := wire.DefaultComponentConfig()
	operations := []struct {
		name string
		call func(*nativeComponentProbe) error
	}{
		{"request", func(q *nativeComponentProbe) error {
			return s.requestNativeWithin(context.Background(), q, n, nodes.Task{ID: "task"}, wire.ComponentCommand{Version: wire.NodeExporterVersion}, "running", &c, false, true)
		}},
		{"observation", func(q *nativeComponentProbe) error {
			return s.observeNativeWithin(context.Background(), q, n, wire.ComponentObservation{Version: wire.NodeExporterVersion, State: "running", Managed: true}, "running", []byte(`{}`), "task", &c, &c, "", true, false)
		}},
		{"logs", func(q *nativeComponentProbe) error {
			return s.logsNativeWithin(context.Background(), q, n, "task", []byte(`{}`))
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			conflict := &transaction.Error{Kind: transaction.UniqueConflict}
			for _, read := range []error{nil, sql.ErrNoRows} {
				q := &nativeComponentProbe{readError: read, writeError: conflict}
				if e := operation.call(q); e != conflict || len(q.statements) != 2 {
					t.Fatal("failed native write was hidden or followed by another SQL call", e)
				}
			}
			q := &nativeComponentProbe{readError: conflict}
			if e := operation.call(q); e != conflict || len(q.statements) != 1 {
				t.Fatal("failed native locked read was followed by SQL", e)
			}
			for _, changed := range []int64{0, 2} {
				q = &nativeComponentProbe{changed: changed}
				if e := operation.call(q); e == nil || e.Error() != "STORAGE_UNAVAILABLE" || len(q.statements) != 2 {
					t.Fatal("unexpected native row count accepted", e)
				}
			}
		})
	}
}

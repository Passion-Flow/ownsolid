package plugins

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	"reflect"
	"testing"
	"time"
)

type nativePluginRow struct{ err error }

func (r nativePluginRow) Scan(...any) error { return r.err }

type nativePluginFailureExecutor struct {
	transaction.Executor
	calls int
}

func (q *nativePluginFailureExecutor) Dialect() transaction.Dialect { return transaction.Dameng }
func (q *nativePluginFailureExecutor) QueryRow(context.Context, transaction.Statement) transaction.Row {
	q.calls++
	return nativePluginRow{&transaction.Error{Kind: transaction.Unavailable}}
}

func TestDamengPluginTypedBindings(t *testing.T) {
	capsule := []byte{0, 255, 42}
	manifest := []byte(`{"plugin_id":"synthetic"}`)
	args := []any{"id", "tenant", "plugin", "1.0.0", 1, 0, 0, "abi", "executor", capsule, "digest", manifest, "manifest-hash", "key", "actor"}
	want := append([]any(nil), args...)
	want[11] = string(manifest)
	if !reflect.DeepEqual(nativePluginVersionCreate.statement(args...), transaction.SQL(transaction.Dameng, nativePluginVersionCreate.text, want...)) || !reflect.DeepEqual(args[9], capsule) || !reflect.DeepEqual(args[11], manifest) {
		t.Fatal("capsule BLOB or manifest CLOB binding")
	}
	args[11] = []byte(`broken`)
	if !reflect.DeepEqual(nativePluginVersionCreate.statement(args...), transaction.SQL(transaction.Dameng, "")) {
		t.Fatal("invalid manifest bound")
	}
	if !reflect.DeepEqual(nativePluginGrantUpdate.statement(false, "authority", "tenant", "workspace", "id"), transaction.SQL(transaction.Dameng, nativePluginGrantUpdate.text, false, "authority", "tenant", "workspace", "id")) {
		t.Fatal("false grant converted")
	}
	refs := nativePluginReferences.statement("t", "p", "t", "p", "t", "p", "t", "p", 9)
	if !reflect.DeepEqual(refs, transaction.SQL(transaction.Dameng, nativePluginReferences.text+" LIMIT 9", "t", "p", "p", "p", "t", "p", "p", "p", "t", "p", "t", "p", "p", "p")) {
		t.Fatal("reference positions lost tenant or node scope")
	}
	for _, limit := range []any{0, 10002, "4"} {
		if !reflect.DeepEqual(nativePluginReferences.statement("t", "p", "t", "p", "t", "p", "t", "p", limit), transaction.SQL(transaction.Dameng, "")) {
			t.Fatal("reference limit not bounded")
		}
	}
	for _, query := range []pluginNativeQuery{nativePluginBootstrapReady, nativePluginRelationsPresent} {
		if reflect.DeepEqual(query.statement(), transaction.SQL(transaction.Dameng, "")) {
			t.Fatal("readiness omitted")
		}
	}
	q := &nativePluginFailureExecutor{}
	if e := invalidateNativeGrants(context.Background(), q, "tenant", "version"); e == nil || q.calls != 1 {
		t.Fatal("SQL failure continued grant invalidation")
	}
}
func TestDamengPluginCursorAndOutcome(t *testing.T) {
	cursor := pagination.Cursor{ID: uuid.NewString(), Time: time.Date(2026, 10, 24, 0, 0, 0, 1, time.UTC)}
	if _, e := pluginNativePage("SELECT id FROM plugin_versions", "created_at", "id", nil, cursor, 1); e == nil || e.Error() != "INVALID_CURSOR" {
		t.Fatal("submicrosecond native cursor accepted")
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		e := pluginError(errors.Join(cause, &transaction.Error{Kind: transaction.CommitUnknown}))
		if kind, ok := transaction.CommitOutcome(e); !ok || kind != transaction.CommitUnknown {
			t.Fatal("unknown commit downgraded")
		}
	}
	if pluginError(errors.Join(context.Canceled, errors.New("driver private value"))).Error() != "STORAGE_UNAVAILABLE" {
		t.Fatal("mixed cancellation leaked")
	}
}

// A native absent row is a normalized transaction error, including through a
// wrapper. Only absence becomes the domain not-found result; storage failures
// must remain storage failures with their sanitized diagnostic fields intact.
func TestDamengPluginGrantNoRows(t *testing.T) {
	absent := &transaction.Error{Kind: transaction.NoRows}
	unavailable := &transaction.Error{Kind: transaction.Unavailable, SQLState: "HY000", NativeCode: -1}
	for _, tc := range []struct {
		name        string
		err         error
		wantCode    string
		wantStorage *transaction.Error
	}{
		{name: "sql sentinel", err: sql.ErrNoRows, wantCode: "PLUGIN_GRANT_NOT_FOUND"},
		{name: "native typed", err: absent, wantCode: "PLUGIN_GRANT_NOT_FOUND"},
		{name: "native wrapped", err: fmt.Errorf("native row: %w", absent), wantCode: "PLUGIN_GRANT_NOT_FOUND"},
		{name: "native unavailable", err: unavailable, wantCode: string(transaction.Unavailable), wantStorage: unavailable},
		{name: "wrapped unavailable", err: fmt.Errorf("native row: %w", unavailable), wantCode: string(transaction.Unavailable), wantStorage: unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grant, e := scanGrant(nativePluginRow{err: tc.err})
			if e == nil || e.Error() != tc.wantCode || !reflect.DeepEqual(grant, grantRecord{}) {
				t.Fatalf("grant absence/error boundary: grant=%#v error=%v", grant, e)
			}
			if tc.wantStorage != nil {
				var storage *transaction.Error
				if !errors.As(e, &storage) || *storage != *tc.wantStorage {
					t.Fatalf("native storage failure lost diagnostic identity: %v", e)
				}
			}
		})
	}
}

package datasets

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This scripted executor drives the actual ProcessNext claim callback. It does
// not run SQL or qualify a driver: it observes the error returned to Within and
// any forbidden Exec after a failed snapshot Query/Scan/iteration/close.
type snapshotClaimStore struct {
	dialect                           transaction.Dialect
	reads                             [][]any
	readCount, execCount, withinCount int
	phase                             string
	cause, callbackError              error
	rows                              *snapshotClaimRows
}

func (s *snapshotClaimStore) Dialect() transaction.Dialect { return s.dialect }
func (s *snapshotClaimStore) Within(ctx context.Context, _ bool, fn func(context.Context, transaction.Executor) error) error {
	s.withinCount++
	s.callbackError = fn(ctx, s)
	return s.callbackError
}
func (s *snapshotClaimStore) Exec(context.Context, transaction.Statement) (int64, error) {
	s.execCount++
	if s.cause != nil {
		return 0, &transaction.Error{Kind: transaction.Unavailable}
	}
	if !s.rows.closed {
		return 0, errors.New("snapshot result not closed")
	}
	return 1, nil
}
func (s *snapshotClaimStore) QueryRow(context.Context, transaction.Statement) transaction.Row {
	if s.readCount >= len(s.reads) {
		return snapshotClaimRow{err: errors.New("unexpected claim read")}
	}
	row := snapshotClaimRow{values: s.reads[s.readCount]}
	s.readCount++
	return row
}
func (s *snapshotClaimStore) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	if s.phase == "query" {
		return nil, s.cause
	}
	return s.rows, nil
}

type snapshotClaimRow struct {
	values []any
	err    error
}

func (r snapshotClaimRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("claim fixture arity mismatch")
	}
	for i, v := range r.values {
		target := reflect.ValueOf(dest[i]).Elem()
		if v == nil {
			target.SetZero()
			continue
		}
		value := reflect.ValueOf(v)
		if target.Kind() == reflect.Pointer {
			target.Set(reflect.New(target.Type().Elem()))
			target = target.Elem()
		}
		if !value.Type().AssignableTo(target.Type()) {
			return errors.New("claim fixture type mismatch")
		}
		target.Set(value)
	}
	return nil
}

type snapshotClaimRows struct {
	snapshotClaimRow
	phase        string
	cause        error
	closeError   error
	closed, next bool
	cancel       context.CancelFunc
}

func (r *snapshotClaimRows) Next() bool {
	if r.phase == "iteration" || r.phase == "empty" || r.next {
		return false
	}
	r.next = true
	return true
}
func (r *snapshotClaimRows) Scan(dest ...any) error {
	if r.cancel != nil {
		r.cancel()
	}
	if r.phase == "scan" {
		return r.cause
	}
	return r.snapshotClaimRow.Scan(dest...)
}
func (r *snapshotClaimRows) Close() { r.closed = true }
func (r *snapshotClaimRows) Err() error {
	if r.closed && r.closeError != nil {
		return r.closeError
	}
	if r.phase == "iteration" || r.phase == "close" && r.closed {
		return r.cause
	}
	return nil
}

func snapshotClaimService(t *testing.T, dialect transaction.Dialect, phase string, cause error) (*Service, *snapshotClaimStore) {
	t.Helper()
	tenant, workspace, actor, dataset, draft, job, version := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	digest := strings.Repeat("a", 64)
	policy := config.DefaultDatasetPolicy()
	raw, e := json.Marshal(jobPolicy{Limits: policy.Limits, Resources: policy.Resources})
	if e != nil {
		t.Fatal(e)
	}
	rows := &snapshotClaimRows{phase: phase, cause: cause, snapshotClaimRow: snapshotClaimRow{values: []any{uuid.NewString(), int64(1), int64(1), nil, nil, nil, "INVALID", "invalid source row", false}}}
	if phase == "record" {
		rows.values = []any{uuid.NewString(), int64(1), int64(1), "", "prompt", "completion", nil, "", false}
	}
	store := &snapshotClaimStore{dialect: dialect, phase: phase, cause: cause, rows: rows, reads: [][]any{
		{job, tenant, workspace, dataset, draft, actor, "admin", int64(1), nil},
		{int64(1), "active", true},
		{"active", true},
		{dataset, workspace, "Fixture dataset", "Synthetic", "CC0", true, actor, int64(1), now, now, nil, nil},
		{nil, draft, dataset, workspace, actor, nil, "fixture.jsonl", digest, int64(10), now, "publishing", int64(1), int64(1), int64(0), now, false, []byte("[]"), false, nil, job, nil, now, now},
		{nil, "Snapshot claim fixture", job, dataset, draft, workspace, "publish", actor, int64(1), "pending", version, digest, "00112233445566778899aabbccddeeff", 2000, 0, nil, nil, nil, nil, false, now, now, nil},
		{true},
		{raw, uuid.NewString(), int64(10)},
	}}
	// Neither private files nor a processor process may be touched: all cases
	// terminate within the claim transaction, before work.claimed becomes true.
	service := &Service{Identity: &identity.Service{Storage: store}, Store: &Store{}, Processor: &Processor{}}
	return service, store
}

func TestProcessNextSnapshotFailureStopsClaimWrites(t *testing.T) {
	for _, dialect := range []transaction.Dialect{transaction.PostgreSQL, transaction.MySQL, transaction.Dameng} {
		for _, c := range []struct {
			name, phase string
			cause       error
			code        string
		}{
			{"deadlock", "query", &transaction.Error{Kind: transaction.Deadlock}, string(transaction.Deadlock)},
			{"timeout", "query", &transaction.Error{Kind: transaction.Timeout}, string(transaction.Timeout)},
			{"cancelled", "query", context.Canceled, string(transaction.Cancelled)},
			{"scan", "scan", errors.New("synthetic scan conversion failure"), string(transaction.Unavailable)},
			{"scan_domain_text", "scan", errors.New("DATASET_VALIDATION_FAILED"), "DATASET_VALIDATION_FAILED"},
			{"iteration", "iteration", &transaction.Error{Kind: transaction.LockTimeout}, string(transaction.LockTimeout)},
			{"close_over_validation", "close", &transaction.Error{Kind: transaction.Deadlock}, string(transaction.Deadlock)},
		} {
			t.Run(string(dialect)+"/"+c.name, func(t *testing.T) {
				service, store := snapshotClaimService(t, dialect, c.phase, c.cause)
				if c.name == "scan" {
					store.rows.closeError = &transaction.Error{Kind: transaction.Unavailable}
				}
				owned, e := service.ProcessNext(t.Context())
				if !owned || e == nil || e.Error() != c.code {
					t.Fatal("claim did not preserve safe failure", owned, e)
				}
				if store.callbackError != c.cause {
					t.Fatal("original error did not reach transaction rollback")
				}
				if store.execCount != 0 || store.withinCount != 1 || store.readCount != 8 {
					t.Fatal("failed snapshot attempted additional claim/failure SQL", store.execCount, store.withinCount, store.readCount)
				}
				if c.phase != "query" && !store.rows.closed {
					t.Fatal("snapshot rows leaked")
				}
			})
		}
		for _, phase := range []string{"validation", "empty", "record"} {
			t.Run(string(dialect)+"/business_"+phase, func(t *testing.T) {
				service, store := snapshotClaimService(t, dialect, phase, nil)
				owned, e := service.ProcessNext(t.Context())
				if !owned || e != nil || store.callbackError != nil || store.execCount != 3 || !store.rows.closed {
					t.Fatal("pure validation no longer records terminal job/draft/audit", owned, e, store.execCount)
				}
			})
		}
		t.Run(string(dialect)+"/cancel_during_validation", func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			service, store := snapshotClaimService(t, dialect, "validation", nil)
			store.rows.cancel = cancel
			owned, e := service.ProcessNext(ctx)
			if !owned || e == nil || e.Error() != string(transaction.Cancelled) || store.callbackError != context.Canceled || store.execCount != 0 {
				t.Fatal("cancelled validation wrote terminal state", owned, e, store.execCount)
			}
		})
	}
}

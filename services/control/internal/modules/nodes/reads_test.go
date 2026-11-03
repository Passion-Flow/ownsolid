package nodes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This explicit boundary double observes deadline ownership without SQL or a
// fabricated product response. Vendor behavior is exercised by tagged tests.
type nodeReadDeadlineStore struct {
	transaction.Store
	deadline  time.Time
	cancelled error
}

func (*nodeReadDeadlineStore) Dialect() transaction.Dialect { return transaction.MySQL }
func (s *nodeReadDeadlineStore) Within(ctx context.Context, _ bool, _ func(context.Context, transaction.Executor) error) error {
	s.deadline, _ = ctx.Deadline()
	s.cancelled = ctx.Err()
	return &transaction.Error{Kind: transaction.Unavailable}
}
func TestNodeReadOwnerDeadlineAndRedaction(t *testing.T) {
	p := config.DefaultRuntimePolicy()
	p.HTTP.RequestSeconds = 11
	store := &nodeReadDeadlineStore{}
	owner := &identity.Service{Storage: store, Config: config.Config{Runtime: &p}}
	s := &Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}}
	a := identity.Auth{}
	a.Me.Audience = "admin"
	before := time.Now()
	value, e := s.List(context.Background(), a, "", nil)
	if e == nil || e.Error() != "STORAGE_UNAVAILABLE" || value.Items != nil || value.NextCursor != nil || store.deadline.Before(before.Add(10*time.Second)) || store.deadline.After(before.Add(12*time.Second)) {
		t.Fatal("node read lost owned deadline or published state", e)
	}
	short, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	expected, _ := short.Deadline()
	s.List(short, a, "", nil)
	if !store.deadline.Equal(expected) {
		t.Fatal("node read extended caller deadline")
	}
	cancel()
	s.List(short, a, "", nil)
	if !errors.Is(store.cancelled, context.Canceled) {
		t.Fatal("node read replaced caller cancellation")
	}
	for _, code := range []transaction.ErrorKind{transaction.Unavailable, transaction.Deadlock, transaction.LockTimeout, transaction.Cancelled, transaction.Timeout, transaction.CommitUnknown, transaction.CommitRolledBack} {
		if e := nodeRepositoryError(&transaction.Error{Kind: code}); e == nil || e.Error() != string(code) {
			t.Fatal("node storage outcome lost", code)
		}
	}
	if nodeRepositoryError(nil) != nil || nodeRepositoryError(errors.New("private SQL credential payload")).Error() != "STORAGE_UNAVAILABLE" {
		t.Fatal("node diagnostics leaked or success changed")
	}
}
func TestNodeReadDependencyOwnership(t *testing.T) {
	var absent *Service
	if _, e := absent.Get(context.Background(), identity.Auth{}, "", ""); e == nil {
		t.Fatal("absent node owner accepted")
	}
	owner := &identity.Service{Storage: &nodeReadDeadlineStore{}}
	s := &Service{Identity: owner, Workspaces: &workspaces.Service{Identity: &identity.Service{Storage: owner.Storage}}}
	if _, e := s.repository(); e == nil || e.Error() != "NODE_IDENTITY_REQUIRED" {
		t.Fatal("different identity owners accepted", e)
	}
}

type nodeReadRows struct {
	next                 int
	scanError, tailError error
	closed               bool
}

func (r *nodeReadRows) Next() bool { r.next++; return r.next == 1 }
func (r *nodeReadRows) Scan(dest ...any) error {
	if r.scanError != nil {
		return r.scanError
	}
	*dest[0].(*int) = 7
	return nil
}
func (r *nodeReadRows) Err() error { return r.tailError }
func (r *nodeReadRows) Close()     { r.closed = true }

type nodeReadQuery struct {
	transaction.Executor
	rows *nodeReadRows
	err  error
}

func (q nodeReadQuery) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	return q.rows, q.err
}
func TestNodeReadPartialRowsNeverEscape(t *testing.T) {
	for _, test := range []struct {
		name              string
		query, scan, tail error
	}{
		{"query", errors.New("private query"), nil, nil},
		{"scan", nil, errors.New("private row"), nil},
		{"tail", nil, nil, errors.New("private stream")},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows := &nodeReadRows{scanError: test.scan, tailError: test.tail}
			q := nodeReadQuery{rows: rows, err: test.query}
			out, e := nodeListRows(context.Background(), q, transaction.Statement{}, 1, "synthetic", func(r transaction.Row) (int, error) { var v int; e := r.Scan(&v); return v, e }, func(int) (time.Time, string) { return time.Time{}, "" })
			if e == nil || out.Items != nil || out.NextCursor != nil || test.query == nil && !rows.closed {
				t.Fatal("partial private row escaped or stream not closed", e)
			}
		})
	}
}

// This scanner double models a valid driver time with a non-UTC location. It
// checks the public projection invariant independently from host TZ settings.
type nodeReadTimeRow struct{ stamp time.Time }

func (r nodeReadTimeRow) Scan(dest ...any) error {
	switch len(dest) {
	case 14:
		*dest[7].(*[]byte), *dest[13].(*[]byte) = []byte(`{}`), []byte(`["collect_facts.v1"]`)
		for _, i := range []int{8, 9, 10} {
			*dest[i].(*time.Time) = r.stamp
		}
	case 10:
		for _, i := range []int{6, 7} {
			*dest[i].(*time.Time) = r.stamp
		}
	case 12:
		for _, i := range []int{8, 9, 10} {
			*dest[i].(*time.Time) = r.stamp
		}
		stamp := r.stamp
		*dest[11].(**time.Time) = &stamp
	default:
		return errors.New("unexpected synthetic scanner shape")
	}
	return nil
}
func TestNodeReadUTCProjection(t *testing.T) {
	stamp := time.Date(2026, 10, 20, 8, 9, 10, 123456000, time.FixedZone("synthetic-offset", 8*60*60))
	assert := func(t *testing.T, values ...time.Time) {
		t.Helper()
		for _, v := range values {
			if !v.Equal(stamp) || v.Location() != time.UTC || v.Nanosecond() != 123456000 {
				t.Fatal("UTC projection changed instant or precision", v)
			}
		}
	}
	t.Run("node", func(t *testing.T) {
		v, e := scanNode(nodeReadTimeRow{stamp})
		if e != nil {
			t.Fatal(e)
		}
		assert(t, v.LastHeartbeatAt, v.CreatedAt, v.UpdatedAt)
	})
	t.Run("join", func(t *testing.T) {
		v, e := scanJoin(nodeReadTimeRow{stamp})
		if e != nil {
			t.Fatal(e)
		}
		assert(t, v.CreatedAt, v.ExpiresAt)
	})
	t.Run("task", func(t *testing.T) {
		v, e := scanTask(nodeReadTimeRow{stamp})
		if e != nil || v.CompletedAt == nil {
			t.Fatal(e)
		}
		assert(t, v.CreatedAt, v.UpdatedAt, v.DeadlineAt, *v.CompletedAt)
	})
}

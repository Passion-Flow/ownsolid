package scheduling

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

type queueSnapshotPool struct{ transaction.Store }

func (queueSnapshotPool) Dialect() transaction.Dialect { return transaction.PostgreSQL }

type queueOtherEngine struct{ transaction.Executor }

func (queueOtherEngine) Dialect() transaction.Dialect { return transaction.MySQL }

func TestQueueExecutorSnapshotRejectsUnborrowedInputs(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  context.Context
		q    transaction.Executor
	}{
		{"missing context", nil, queueSnapshotPool{}},
		{"missing executor", t.Context(), nil},
		{"pool", t.Context(), queueSnapshotPool{}},
		{"other engine", t.Context(), queueOtherEngine{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := RankWorkspaceQueueExecutorWithin(test.ctx, test.q, uuid.NewString(), nil)
			if !errors.Is(err, ErrQueueSnapshotRequired) || got != nil {
				t.Fatalf("unborrowed input returned rank: %+v, %v", got, err)
			}
		})
	}
}

const queueSnapshotSQL = "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ"

func TestPostgresQueueExecutorUsesSelectedOwnerSnapshot(t *testing.T) {
	f := newQueuedFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	first := QueueEntry{JobID: f.first.SourceID, QueueID: f.first.WorkspaceID, Sequence: 1}
	second := QueueEntry{JobID: f.second.SourceID, QueueID: f.second.WorkspaceID, Sequence: 2}
	third := QueueEntry{JobID: uuid.NewString(), QueueID: f.second.WorkspaceID, Sequence: 3}
	rank := func(ctx context.Context, q transaction.Executor, want []QueueEntry) error {
		t.Helper()
		entries, err := f.read(ctx, q)
		if err != nil {
			return err
		}
		got, err := RankWorkspaceQueueExecutorWithin(ctx, q, f.first.TenantID, entries)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("selected snapshot rank: %+v, %v, want %+v", got, err, want)
		}
		return nil
	}
	if err := f.store.Within(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		if _, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, queueSnapshotSQL)); err != nil {
			return err
		}
		if err := rank(ctx, q, []QueueEntry{first, second}); err != nil {
			return err
		}
		reserveBorrowing(t, f.store, f.reservationFixture.first, 2)
		if _, err := f.pool.Exec(ctx, `INSERT INTO owner_fixture_queue
 (job_id,tenant_id,workspace_id,priority,sequence,state)
 VALUES($1,$2,$3,0,3,'queued')`, third.JobID, f.first.TenantID, third.QueueID); err != nil {
			return err
		}
		return rank(ctx, q, []QueueEntry{first, second})
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Within(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		if _, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, queueSnapshotSQL)); err != nil {
			return err
		}
		return rank(ctx, q, []QueueEntry{second, third, first})
	}); err != nil {
		t.Fatal(err)
	}
	f.assertState(t, f.first, "queued", 1, 1)
	f.assertState(t, f.second, "queued", 1, 1)
}

func TestPostgresQueueExecutorRejectsWrongModeAndScope(t *testing.T) {
	f := newQueuedFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	entry := QueueEntry{JobID: f.first.SourceID, QueueID: f.first.WorkspaceID, Sequence: 1}
	for _, mode := range []struct {
		readOnly bool
		setup    string
	}{
		{true, ""}, {false, ""},
		{false, queueSnapshotSQL},
		{true, "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE"},
	} {
		err := f.store.Within(ctx, mode.readOnly, func(ctx context.Context, q transaction.Executor) error {
			if mode.setup != "" {
				if _, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, mode.setup)); err != nil {
					return err
				}
			}
			got, err := RankWorkspaceQueueExecutorWithin(ctx, q, f.first.TenantID, []QueueEntry{entry})
			if got != nil {
				t.Fatalf("wrong transaction mode returned rank: %+v", got)
			}
			return err
		})
		if !errors.Is(err, ErrQueueSnapshotRequired) {
			t.Fatalf("wrong mode accepted or misclassified: %+v, %v", mode, err)
		}
	}
	if err := f.store.Within(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		if _, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, queueSnapshotSQL)); err != nil {
			return err
		}
		for _, test := range []struct {
			tenant string
			entry  QueueEntry
		}{
			{uuid.NewString(), entry},
			{f.first.TenantID, QueueEntry{JobID: entry.JobID, QueueID: uuid.NewString(), Sequence: 1}},
		} {
			got, err := RankWorkspaceQueueExecutorWithin(ctx, q, test.tenant, []QueueEntry{test.entry})
			if !errors.Is(err, ErrQuotaScopeNotFound) || got != nil {
				t.Fatalf("invalid scope returned a partial rank: %+v, %v", got, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

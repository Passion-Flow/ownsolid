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
	f := newReservationFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	first := QueueEntry{JobID: f.first.SourceID, QueueID: f.first.WorkspaceID, Sequence: 1}
	second := QueueEntry{JobID: uuid.NewString(), QueueID: f.other, Sequence: 2}
	third := QueueEntry{JobID: uuid.NewString(), QueueID: f.other, Sequence: 3}
	createQueueExecutorOwnerEntries(t, f, []QueueEntry{first, second})
	competing := newBorrowingAttempt(f.first, f.first.WorkspaceID)
	rank := func(ctx context.Context, q transaction.Executor, want []QueueEntry) error {
		t.Helper()
		entries, err := readQueueExecutorOwnerEntries(ctx, q, f.first.TenantID)
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
		reserveBorrowing(t, f.store, competing, 2)
		if _, err := f.pool.Exec(ctx, `INSERT INTO queue_executor_owner
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
	for _, entry := range []QueueEntry{first, second, third} {
		var state string
		var revision int64
		if err := f.pool.QueryRow(ctx, "SELECT state,revision FROM queue_executor_owner WHERE job_id=$1", entry.JobID).
			Scan(&state, &revision); err != nil || state != "queued" || revision != 1 {
			t.Fatalf("ranking changed persisted owner state: %q, %d, %v", state, revision, err)
		}
	}
	var held int
	var heldID string
	if err := f.pool.QueryRow(ctx, `SELECT count(*),COALESCE(min(attempt_id::text),'')
 FROM scheduling_reservations WHERE released_at IS NULL`).
		Scan(&held, &heldID); err != nil || held != 1 || heldID != competing.ID {
		t.Fatalf("ranking changed the competing hold: %d, %q, %v", held, heldID, err)
	}
}

func TestPostgresQueueExecutorRejectsWrongModeAndScope(t *testing.T) {
	f := newReservationFixture(t)
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

// Persistent owner rows and committed reservations must use the same snapshot;
// this fixture deliberately has no dependency on the pending admission API.
func createQueueExecutorOwnerEntries(t *testing.T, f reservationFixture, entries []QueueEntry) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), `CREATE TABLE queue_executor_owner (
 job_id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 priority integer NOT NULL,
 sequence bigint NOT NULL UNIQUE CHECK(sequence > 0),
 state text NOT NULL DEFAULT 'queued',
 revision bigint NOT NULL DEFAULT 1,
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
)`); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, err := f.pool.Exec(t.Context(), `INSERT INTO queue_executor_owner
 (job_id,tenant_id,workspace_id,priority,sequence) VALUES($1,$2,$3,$4,$5)`,
			entry.JobID, f.first.TenantID, entry.QueueID, entry.Priority, entry.Sequence); err != nil {
			t.Fatal(err)
		}
	}
}

func readQueueExecutorOwnerEntries(ctx context.Context, q transaction.Executor, tenant string) ([]QueueEntry, error) {
	rows, err := q.Query(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT job_id::text,workspace_id::text,priority,sequence FROM queue_executor_owner
 WHERE tenant_id=$1 AND state='queued' ORDER BY sequence`, tenant))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []QueueEntry
	for rows.Next() {
		var entry QueueEntry
		if err := rows.Scan(&entry.JobID, &entry.QueueID, &entry.Priority, &entry.Sequence); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

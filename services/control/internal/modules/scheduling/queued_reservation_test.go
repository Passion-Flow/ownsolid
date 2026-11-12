package scheduling

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type queuedFixture struct {
	reservationFixture
	first, second Attempt
	domain        []string
}

func newQueuedFixture(t *testing.T) queuedFixture {
	t.Helper()
	f := newReservationFixture(t)
	first := newBorrowingAttempt(f.first, f.first.WorkspaceID)
	second := newBorrowingAttempt(f.first, f.other)
	if _, err := f.pool.Exec(t.Context(), `
CREATE TABLE owner_fixture_queue (
 job_id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL,
 site_id varchar NOT NULL DEFAULT local_site_id(),cell_id varchar NOT NULL DEFAULT local_cell_id(),
 priority integer NOT NULL,sequence bigint NOT NULL UNIQUE,
 state text NOT NULL,revision integer NOT NULL DEFAULT 1
)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO owner_fixture_queue
 (job_id,tenant_id,workspace_id,priority,sequence,state)
 VALUES($1,$2,$3,0,1,'queued'),($4,$2,$5,0,2,'queued')`,
		first.SourceID, first.TenantID, first.WorkspaceID, second.SourceID, second.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	return queuedFixture{f, first, second, []string{first.WorkspaceID, second.WorkspaceID}}
}

func (f queuedFixture) read(ctx context.Context, q transaction.Executor) ([]QueueEntry, error) {
	rows, err := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `
SELECT job_id::text,workspace_id::text,priority,sequence
 FROM owner_fixture_queue WHERE tenant_id=$1 AND site_id=local_site_id()
 AND cell_id=local_cell_id() AND state='queued' ORDER BY sequence`, f.first.TenantID))
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

func dequeueQueuedFixture(ctx context.Context, q transaction.Executor, a Attempt) error {
	count, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `
UPDATE owner_fixture_queue SET state='preparing',revision=revision+1
 WHERE tenant_id=$1 AND workspace_id=$2 AND job_id=$3 AND state='queued'
 AND site_id=local_site_id() AND cell_id=local_cell_id()`, a.TenantID, a.WorkspaceID, a.SourceID))
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrInvalidQueue
	}
	return nil
}

func (f queuedFixture) admit(ctx context.Context, a Attempt, read QueueReader, after error) (bool, error) {
	var changed bool
	err := ownerTransaction(ctx, f.store, func(ctx context.Context, q transaction.Executor) error {
		var err error
		changed, err = ReserveQueuedWithin(ctx, q, a, borrowingIntent(a, 2), f.domain, read)
		if err != nil || !changed {
			return err
		}
		if err := dequeueQueuedFixture(ctx, q, a); err != nil {
			return err
		}
		return after
	})
	return changed && err == nil, err
}

func (f queuedFixture) assertState(t *testing.T, a Attempt, state string, revision, holds int) {
	t.Helper()
	var gotState string
	var gotRevision, gotHolds int
	if err := f.pool.QueryRow(t.Context(), "SELECT state,revision FROM owner_fixture_queue WHERE job_id=$1", a.SourceID).
		Scan(&gotState, &gotRevision); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM scheduling_reservations WHERE released_at IS NULL").Scan(&gotHolds); err != nil {
		t.Fatal(err)
	}
	if gotState != state || gotRevision != revision || gotHolds != holds {
		t.Fatalf("owner state=%s revision=%d holds=%d, want %s/%d/%d", gotState, gotRevision, gotHolds, state, revision, holds)
	}
}

func TestPostgresQueuedReservationUsesCurrentSharesAndOwnerCommit(t *testing.T) {
	f := newQueuedFixture(t)
	reserveBorrowing(t, f.store, f.reservationFixture.first, 2)
	if changed, err := f.admit(t.Context(), f.first, f.read, nil); changed || !errors.Is(err, ErrQueueTurnPending) ||
		!slices.Equal(WaitReasons(err), []WaitReason{{Code: ErrQueueTurnPending.Error(), Action: WaitForQueueTurn}}) {
		t.Fatalf("non-head admission: %v, %v", changed, err)
	}
	f.assertState(t, f.first, "queued", 1, 1)
	fail := errors.New("owner audit failed")
	if changed, err := f.admit(t.Context(), f.second, f.read, fail); changed || !errors.Is(err, fail) {
		t.Fatalf("owner failure: %v, %v", changed, err)
	}
	f.assertState(t, f.second, "queued", 1, 1)
	if changed, err := f.admit(t.Context(), f.second, f.read, nil); err != nil || !changed {
		t.Fatalf("fair head admission: %v, %v", changed, err)
	}
	f.assertState(t, f.second, "preparing", 2, 2)
	noRead := func(context.Context, transaction.Executor) ([]QueueEntry, error) {
		t.Fatal("held replay read the dequeued owner entry again")
		return nil, nil
	}
	if changed, err := f.admit(t.Context(), f.second, noRead, nil); err != nil || changed {
		t.Fatalf("held replay: %v, %v", changed, err)
	}
	f.assertState(t, f.second, "preparing", 2, 2)
	changedAttempt := f.second
	changedAttempt.ID = uuid.NewString()
	if changed, err := f.admit(t.Context(), changedAttempt, noRead, nil); changed || !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("changed attempt replay: %v, %v", changed, err)
	}
	if changed, err := releaseInTransaction(t.Context(), f.store, f.second); err != nil || !changed {
		t.Fatalf("verified owner release: %v, %v", changed, err)
	}
	if changed, err := f.admit(t.Context(), f.second, noRead, nil); changed || !errors.Is(err, ErrReservationReleased) {
		t.Fatalf("released attempt reused: %v, %v", changed, err)
	}
	f.assertState(t, f.second, "preparing", 2, 1)
}

func TestPostgresQueuedReservationReadsAfterConcurrentDequeue(t *testing.T) {
	f := newQueuedFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	first, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx)
	q := transaction.PostgreSQLTransaction(first)
	if changed, err := ReserveQueuedWithin(ctx, q, f.first, borrowingIntent(f.first, 2), f.domain, f.read); err != nil || !changed {
		t.Fatalf("first reserve: %v, %v", changed, err)
	}
	if err := dequeueQueuedFixture(ctx, q, f.first); err != nil {
		t.Fatal(err)
	}
	writers := make(chan uint32, 1)
	result := make(chan error, 1)
	var reads atomic.Int32
	go func() {
		result <- pgx.BeginFunc(ctx, f.pool, func(tx pgx.Tx) error {
			writers <- tx.Conn().PgConn().PID()
			read := func(ctx context.Context, q transaction.Executor) ([]QueueEntry, error) {
				reads.Add(1)
				return f.read(ctx, q)
			}
			q := transaction.PostgreSQLTransaction(tx)
			domain := slices.Clone(f.domain)
			slices.Reverse(domain)
			changed, err := ReserveQueuedWithin(ctx, q, f.second, borrowingIntent(f.second, 2), domain, read)
			if err != nil {
				return err
			}
			if !changed {
				return errors.New("second candidate did not reserve")
			}
			return dequeueQueuedFixture(ctx, q, f.second)
		})
	}()
	var pid uint32
	select {
	case pid = <-writers:
	case err := <-result:
		t.Fatalf("consumer ended before acquiring connection: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blockers int
		if err := f.pool.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1::integer))", int64(pid)).Scan(&blockers); err != nil {
			t.Fatal(err)
		}
		if blockers != 0 {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("consumer bypassed queued dispatch locks: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if reads.Load() != 0 {
		t.Fatal("queue reader ran before the preceding dequeue committed")
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if reads.Load() != 1 {
		t.Fatalf("queue reads=%d", reads.Load())
	}
	f.assertState(t, f.first, "preparing", 2, 2)
	f.assertState(t, f.second, "preparing", 2, 2)
}

func TestPostgresQueuedReservationRefusesChangedOwnerScope(t *testing.T) {
	f := newQueuedFixture(t)
	var reads atomic.Int32
	read := func(ctx context.Context, q transaction.Executor) ([]QueueEntry, error) {
		reads.Add(1)
		return f.read(ctx, q)
	}
	err := ownerTransaction(t.Context(), f.store, func(ctx context.Context, q transaction.Executor) error {
		_, err := ReserveQueuedWithin(ctx, q, f.first, borrowingIntent(f.first, 2), []string{f.first.WorkspaceID, uuid.NewString()}, read)
		return err
	})
	if !errors.Is(err, ErrQuotaScopeNotFound) || reads.Load() != 0 {
		t.Fatalf("invalid domain: %v, reads=%d", err, reads.Load())
	}
	for _, field := range []string{"site_id", "cell_id"} {
		if _, err := f.pool.Exec(t.Context(), "UPDATE owner_fixture_queue SET "+field+"='foreign' WHERE job_id=$1", f.first.SourceID); err != nil {
			t.Fatal(err)
		}
		if changed, err := f.admit(t.Context(), f.first, read, nil); changed || !errors.Is(err, ErrInvalidQueue) {
			t.Fatalf("foreign owner entry: %v, %v", changed, err)
		}
		f.assertState(t, f.first, "queued", 1, 0)
		if _, err := f.pool.Exec(t.Context(), "UPDATE owner_fixture_queue SET site_id=local_site_id(),cell_id=local_cell_id() WHERE job_id=$1", f.first.SourceID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE owner_fixture_queue SET state='cancelled',revision=revision+1 WHERE job_id=$1", f.first.SourceID); err != nil {
		t.Fatal(err)
	}
	if changed, err := f.admit(t.Context(), f.first, read, nil); changed || !errors.Is(err, ErrInvalidQueue) {
		t.Fatalf("cancelled entry admitted: %v, %v", changed, err)
	}
	f.assertState(t, f.first, "cancelled", 2, 0)
	if changed, err := f.admit(t.Context(), f.second, read, nil); err != nil || !changed {
		t.Fatalf("next durable candidate after cancellation: %v, %v", changed, err)
	}
}

func TestPostgresQueuedReservationRejectsForeignHeldUsage(t *testing.T) {
	f := newQueuedFixture(t)
	reserveBorrowing(t, f.store, f.reservationFixture.first, 2)
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO cells VALUES ('foreign'); UPDATE scheduling_reservations SET cell_id='foreign'"); err != nil {
		t.Fatal(err)
	}
	if changed, err := f.admit(t.Context(), f.second, f.read, nil); changed || !errors.Is(err, ErrQuotaPolicyUnavailable) {
		t.Fatalf("foreign held usage advanced dispatch: %v, %v", changed, err)
	}
	f.assertState(t, f.second, "queued", 1, 1)
	var revision int64
	if err := f.pool.QueryRow(t.Context(), "SELECT revision FROM scheduling_quota_policy").Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("foreign held usage changed policy: %d, %v", revision, err)
	}
}

func TestPostgresQueuedReservationRollsBackAfterConcurrentCancellation(t *testing.T) {
	f := newQueuedFixture(t)
	read := func(ctx context.Context, q transaction.Executor) ([]QueueEntry, error) {
		entries, err := f.read(ctx, q)
		if err != nil {
			return nil, err
		}
		// Commit a separate owner cancellation after the queue read. The final
		// owner state transition must reject this stale entry and roll back its hold.
		_, err = f.pool.Exec(ctx, "UPDATE owner_fixture_queue SET state='cancelled',revision=revision+1 WHERE job_id=$1", f.first.SourceID)
		return entries, err
	}
	if changed, err := f.admit(t.Context(), f.first, read, nil); changed || !errors.Is(err, ErrInvalidQueue) {
		t.Fatalf("cancelled owner entry dispatched: %v, %v", changed, err)
	}
	f.assertState(t, f.first, "cancelled", 2, 0)
}

func TestPostgresQueuedReservationConcurrentReplayDequeuesOnce(t *testing.T) {
	f := newQueuedFixture(t)
	type outcome struct {
		changed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for range 2 {
		go func() {
			<-start
			changed, err := f.admit(t.Context(), f.first, f.read, nil)
			results <- outcome{changed, err}
		}()
	}
	close(start)
	var admitted int
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.changed {
			admitted++
		}
	}
	if admitted != 1 {
		t.Fatalf("successful dequeues=%d", admitted)
	}
	f.assertState(t, f.first, "preparing", 2, 1)
}

func TestPostgresQueuedReservationRefusesReaderFailureAndOutOfDomainEntries(t *testing.T) {
	f := newQueuedFixture(t)
	failedRead := errors.New("owner queue unavailable")
	read := func(context.Context, transaction.Executor) ([]QueueEntry, error) {
		return nil, failedRead
	}
	if changed, err := f.admit(t.Context(), f.first, read, nil); changed || !errors.Is(err, failedRead) {
		t.Fatalf("failed queue read: %v, %v", changed, err)
	}
	f.assertState(t, f.first, "queued", 1, 0)

	err := ownerTransaction(t.Context(), f.store, func(ctx context.Context, q transaction.Executor) error {
		_, err := ReserveQueuedWithin(ctx, q, f.first, borrowingIntent(f.first, 2), []string{f.first.WorkspaceID}, f.read)
		return err
	})
	if !errors.Is(err, ErrQuotaScopeNotFound) {
		t.Fatalf("reader returned an entry outside the authorized domain: %v", err)
	}
	f.assertState(t, f.first, "queued", 1, 0)
	if changed, err := f.admit(t.Context(), f.first, nil, nil); changed || !errors.Is(err, ErrInvalidQueue) {
		t.Fatalf("missing queue reader: %v, %v", changed, err)
	}
	f.assertState(t, f.first, "queued", 1, 0)
	if changed, err := f.admit(t.Context(), f.first, f.read, nil); err != nil || !changed {
		t.Fatalf("retry after reader failure: %v, %v", changed, err)
	}
	f.assertState(t, f.first, "preparing", 2, 1)
}

func TestPostgresQueuedReservationReleaseSharesOwnerCancellationCommit(t *testing.T) {
	f := newQueuedFixture(t)
	if changed, err := f.admit(t.Context(), f.first, f.read, nil); err != nil || !changed {
		t.Fatalf("initial admission: %v, %v", changed, err)
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE owner_fixture_queue SET state='cancelling',revision=revision+1 WHERE job_id=$1", f.first.SourceID); err != nil {
		t.Fatal(err)
	}
	f.assertState(t, f.first, "cancelling", 3, 1)

	// The real owner must validate positive stop evidence before this point.
	// This fixture qualifies only the atomic owner transition and release.
	stop := func(after error) (bool, error) {
		var released bool
		err := ownerTransaction(t.Context(), f.store, func(ctx context.Context, q transaction.Executor) error {
			var err error
			released, err = ReleaseWithin(ctx, q, f.first)
			if err != nil || !released {
				return err
			}
			count, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `
UPDATE owner_fixture_queue SET state='cancelled',revision=revision+1
 WHERE tenant_id=$1 AND workspace_id=$2 AND job_id=$3 AND state='cancelling'
 AND site_id=local_site_id() AND cell_id=local_cell_id()`, f.first.TenantID, f.first.WorkspaceID, f.first.SourceID))
			if err != nil {
				return err
			}
			if count != 1 {
				return ErrInvalidQueue
			}
			return after
		})
		return released && err == nil, err
	}
	failedAudit := errors.New("owner stop audit failed")
	if changed, err := stop(failedAudit); changed || !errors.Is(err, failedAudit) {
		t.Fatalf("failed owner stop: %v, %v", changed, err)
	}
	f.assertState(t, f.first, "cancelling", 3, 1)
	if changed, err := stop(nil); err != nil || !changed {
		t.Fatalf("confirmed owner stop: %v, %v", changed, err)
	}
	f.assertState(t, f.first, "cancelled", 4, 0)
	if changed, err := stop(nil); err != nil || changed {
		t.Fatalf("owner stop replay: %v, %v", changed, err)
	}
	f.assertState(t, f.first, "cancelled", 4, 0)
	if changed, err := f.admit(t.Context(), f.first, f.read, nil); changed || !errors.Is(err, ErrReservationReleased) {
		t.Fatalf("cancelled attempt readmitted: %v, %v", changed, err)
	}
	f.assertState(t, f.first, "cancelled", 4, 0)
}

func TestPostgresQueueWaitReasonTracksQuotaConfiguration(t *testing.T) {
	f := newQueuedFixture(t)
	if _, err := f.pool.Exec(t.Context(), "DELETE FROM scheduling_quota_policy"); err != nil {
		t.Fatal(err)
	}
	noRead := func(context.Context, transaction.Executor) ([]QueueEntry, error) {
		t.Fatal("unconfigured admission read the owner queue")
		return nil, nil
	}
	changed, err := f.admit(t.Context(), f.first, noRead, nil)
	want := []WaitReason{{Code: ErrQuotaUnconfigured.Error(), Action: ConfigureQuotaPolicy}}
	if changed || !errors.Is(err, ErrQuotaUnconfigured) || !slices.Equal(WaitReasons(err), want) {
		t.Fatalf("missing policy: changed=%v reasons=%+v error=%v", changed, WaitReasons(err), err)
	}
	f.assertState(t, f.first, "queued", 1, 0)
	if err := pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
		_, changed, err := ReplaceQuotaWithin(t.Context(), tx, 0, f.policy)
		if err == nil && !changed {
			return errors.New("policy installation did not change state")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	changed, err = f.admit(t.Context(), f.first, f.read, nil)
	if err != nil || !changed || len(WaitReasons(err)) != 0 {
		t.Fatalf("configured retry: changed=%v reasons=%+v error=%v", changed, WaitReasons(err), err)
	}
	f.assertState(t, f.first, "preparing", 2, 1)
}

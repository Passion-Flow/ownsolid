package scheduling

import (
	"context"
	_ "embed"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed testdata/reservations.sql
var testReservationSQL string

const reservationParentsSQL = `
CREATE TABLE sites (id varchar(32) PRIMARY KEY);
CREATE TABLE cells (id varchar(32) PRIMARY KEY);
INSERT INTO sites VALUES ('default');
INSERT INTO cells VALUES ('default');
CREATE FUNCTION local_site_id() RETURNS varchar LANGUAGE sql STABLE AS $$ SELECT 'default'::varchar $$;
CREATE FUNCTION local_cell_id() RETURNS varchar LANGUAGE sql STABLE AS $$ SELECT 'default'::varchar $$;
CREATE TABLE workspaces (
  tenant_id uuid NOT NULL,
  id uuid NOT NULL,
  PRIMARY KEY (tenant_id, id)
);
CREATE TABLE owner_write_probe (id int PRIMARY KEY, state text NOT NULL);
INSERT INTO owner_write_probe VALUES (1, 'queued');
`

type reservationFixture struct {
	pool   *pgxpool.Pool
	store  transaction.Store
	policy []QuotaNode
	first  Attempt
	other  string
}

func newReservationFixture(t *testing.T) reservationFixture {
	t.Helper()
	pool := quotaDatabase(t)
	store, err := transaction.FromPostgreSQL(pool)
	if err != nil {
		t.Fatal(err)
	}
	a := Attempt{TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), ID: uuid.NewString(), Number: 1, Kind: Training, SourceID: uuid.NewString()}
	other := uuid.NewString()
	if _, err := pool.Exec(t.Context(), "INSERT INTO workspaces VALUES ($1,$2),($1,$3)", a.TenantID, a.WorkspaceID, other); err != nil {
		t.Fatal(err)
	}
	policy := quotaFixture()
	policy[0].ID, policy[4].ID = a.WorkspaceID, other
	policy[2].ParentID, policy[3].ID = a.TenantID, a.TenantID
	for _, i := range []int{0, 2, 3, 4} {
		policy[i].TenantID = a.TenantID
	}
	if err := pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
		rev, changed, err := ReplaceQuotaWithin(t.Context(), tx, 0, policy)
		if err == nil && (rev != 1 || !changed) {
			t.Fatalf("initial policy: revision=%d changed=%v", rev, changed)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return reservationFixture{pool: pool, store: store, policy: policy, first: a, other: other}
}

func reservationIntent(a Attempt, gpu int64) JobIntent {
	return JobIntent{Version: IntentVersion, Kind: a.Kind, SourceID: a.SourceID, Groups: []ReplicaGroup{
		{Name: "worker", Replicas: 1, Requests: Resources{NVIDIAGPU: gpu}},
		{Name: "driver", Replicas: 1, Requests: Resources{CPU: 1000}},
	}}
}

func ownerTransaction(ctx context.Context, store transaction.Store, fn func(context.Context, transaction.Executor) error) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return store.Within(ctx, false, fn)
}

func reserveInTransaction(ctx context.Context, store transaction.Store, a Attempt, intent JobIntent) (bool, error) {
	var changed bool
	err := ownerTransaction(ctx, store, func(ctx context.Context, q transaction.Executor) error {
		var err error
		changed, err = ReserveWithin(ctx, q, a, intent)
		return err
	})
	return changed && err == nil, err
}

func releaseInTransaction(ctx context.Context, store transaction.Store, a Attempt) (bool, error) {
	var changed bool
	err := ownerTransaction(ctx, store, func(ctx context.Context, q transaction.Executor) error {
		var err error
		changed, err = ReleaseWithin(ctx, q, a)
		return err
	})
	return changed && err == nil, err
}

func TestPostgresReservationAndReleaseReplay(t *testing.T) {
	f := newReservationFixture(t)
	a := f.first
	intent := reservationIntent(a, 2)
	if changed, err := reserveInTransaction(t.Context(), f.store, a, intent); err != nil || !changed {
		t.Fatalf("initial reserve: %v %v", changed, err)
	}
	slices.Reverse(intent.Groups)
	if changed, err := reserveInTransaction(t.Context(), f.store, a, intent); err != nil || changed {
		t.Fatalf("exact replay: %v %v", changed, err)
	}
	if _, err := reserveInTransaction(t.Context(), f.store, a, reservationIntent(a, 1)); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("changed demand: %v", err)
	}
	next := a
	next.ID = uuid.NewString()
	if _, err := reserveInTransaction(t.Context(), f.store, next, intent); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("changed attempt at the same ordinal: %v", err)
	}
	next.Number++
	if _, err := reserveInTransaction(t.Context(), f.store, next, intent); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("parallel attempt for one job: %v", err)
	}
	foreign := a
	foreign.WorkspaceID = f.other
	if _, err := releaseInTransaction(t.Context(), f.store, foreign); !errors.Is(err, ErrReservationNotFound) {
		t.Fatalf("cross-workspace release: %v", err)
	}
	if changed, err := releaseInTransaction(t.Context(), f.store, a); err != nil || !changed {
		t.Fatalf("initial release: %v %v", changed, err)
	}
	if _, err := reserveInTransaction(t.Context(), f.store, a, intent); !errors.Is(err, ErrReservationReleased) {
		t.Fatalf("released attempt could be restarted: %v", err)
	}
	if changed, err := reserveInTransaction(t.Context(), f.store, next, intent); err != nil || !changed {
		t.Fatalf("replacement attempt: %v %v", changed, err)
	}
	if changed, err := releaseInTransaction(t.Context(), f.store, a); err != nil || changed {
		t.Fatalf("old release replay: %v %v", changed, err)
	}
	if changed, err := reserveInTransaction(t.Context(), f.store, next, intent); err != nil || changed {
		t.Fatalf("old release affected replacement: %v %v", changed, err)
	}
}

func TestPostgresReservationSharesOwnerRollbackAndSavepoint(t *testing.T) {
	f := newReservationFixture(t)
	a := f.first
	intent := reservationIntent(a, 4)
	sql := func(ctx context.Context, q transaction.Executor, text string) error {
		_, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, text))
		return err
	}
	// This is the owner's SQL savepoint shape with a disposable owner-side
	// write probe. The scheduling package does not run the training service.
	if err := ownerTransaction(t.Context(), f.store, func(ctx context.Context, q transaction.Executor) error {
		if err := sql(ctx, q, "SAVEPOINT training_dispatch_reservation"); err != nil {
			return err
		}
		if changed, err := ReserveWithin(ctx, q, a, intent); err != nil || !changed {
			t.Fatalf("reserve in savepoint: %v %v", changed, err)
		}
		err := sql(ctx, q, "INSERT INTO owner_write_probe VALUES (1, 'preparing')")
		var conflict *transaction.Error
		if !errors.As(err, &conflict) || conflict.Kind != transaction.UniqueConflict {
			t.Fatalf("expected downstream failure, got %v", err)
		}
		for _, stmt := range []string{
			"ROLLBACK TO SAVEPOINT training_dispatch_reservation",
			"RELEASE SAVEPOINT training_dispatch_reservation",
			"UPDATE owner_write_probe SET state='waiting' WHERE id=1",
		} {
			if err := sql(ctx, q, stmt); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var held int
	var state string
	if err := f.pool.QueryRow(t.Context(), `SELECT state,(SELECT count(*) FROM scheduling_reservations) FROM owner_write_probe WHERE id=1`).Scan(&state, &held); err != nil || state != "waiting" || held != 0 {
		t.Fatalf("after savepoint rollback: state=%q reservations=%d error=%v", state, held, err)
	}
	if changed, err := reserveInTransaction(t.Context(), f.store, a, intent); err != nil || !changed {
		t.Fatalf("retry after rollback: %v %v", changed, err)
	}
	failedAudit := errors.New("synthetic failure before owner commit")
	if err := ownerTransaction(t.Context(), f.store, func(ctx context.Context, q transaction.Executor) error {
		if changed, err := ReleaseWithin(ctx, q, a); err != nil || !changed {
			t.Fatalf("release before owner audit: %v %v", changed, err)
		}
		return failedAudit
	}); !errors.Is(err, failedAudit) {
		t.Fatalf("outer rollback: %v", err)
	}
	if changed, err := reserveInTransaction(t.Context(), f.store, a, intent); err != nil || changed {
		t.Fatalf("rolled-back release did not retain quota: %v %v", changed, err)
	}
}

func TestPostgresReservationContentionAndQuotaReplacement(t *testing.T) {
	f := newReservationFixture(t)
	const competitors = 4
	outcomes := make(chan error, competitors)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range competitors {
		a := f.first
		a.ID, a.SourceID = uuid.NewString(), uuid.NewString()
		wg.Go(func() {
			<-start
			changed, err := reserveInTransaction(t.Context(), f.store, a, reservationIntent(a, 4))
			if err == nil && !changed {
				err = errors.New("new attempt was treated as a replay")
			}
			outcomes <- err
		})
	}
	close(start)
	wg.Wait()
	close(outcomes)
	var winners, exhausted int
	for err := range outcomes {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrReservationQuota):
			exhausted++
		default:
			t.Fatal(err)
		}
	}
	if winners != 1 || exhausted != competitors-1 {
		t.Fatalf("winners=%d exhausted=%d", winners, exhausted)
	}
	if err := pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
		f.policy[0].Limits.Burst[NVIDIAGPU] = 3
		_, _, err := ReplaceQuotaWithin(t.Context(), tx, 1, f.policy)
		return err
	}); !errors.Is(err, ErrQuotaHasActiveReservations) {
		t.Fatalf("policy stranded held GPU: %v", err)
	}
	other := f.first
	other.ID, other.SourceID, other.WorkspaceID = uuid.NewString(), uuid.NewString(), f.other
	if changed, err := reserveInTransaction(t.Context(), f.store, other, reservationIntent(other, 4)); err != nil || !changed {
		t.Fatalf("different workspace lost its allocation: %v %v", changed, err)
	}
}

func TestPostgresReservationRefusesNonOwnerTransaction(t *testing.T) {
	f := newReservationFixture(t)
	a := f.first
	intent := reservationIntent(a, 1)
	if _, err := ReserveWithin(t.Context(), f.store, a, intent); !errors.Is(err, ErrReservationTransaction) {
		t.Fatalf("unbound store accepted: %v", err)
	}
	tx, err := f.pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := ReserveWithin(t.Context(), transaction.PostgreSQLTransaction(tx), a, intent); !errors.Is(err, ErrReservationTransaction) {
		t.Fatalf("stale snapshot isolation accepted: %v", err)
	}
}

func TestPostgresQuotaReplacementWaitsForConcurrentReservation(t *testing.T) {
	f := newReservationFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	hold, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(ctx)
	if changed, err := ReserveWithin(ctx, transaction.PostgreSQLTransaction(hold), f.first, reservationIntent(f.first, 4)); err != nil || !changed {
		t.Fatalf("hold before policy edit: %v %v", changed, err)
	}
	writer := make(chan uint32, 1)
	result := make(chan error, 1)
	f.policy[0].Limits.Burst[NVIDIAGPU] = 3
	go func() {
		result <- pgx.BeginFunc(ctx, f.pool, func(tx pgx.Tx) error {
			writer <- tx.Conn().PgConn().PID()
			_, _, err := ReplaceQuotaWithin(ctx, tx, 1, f.policy)
			return err
		})
	}()
	var pid uint32
	select {
	case pid = <-writer:
	case err := <-result:
		t.Fatalf("policy writer ended before acquiring a connection: %v", err)
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
			t.Fatalf("policy replacement bypassed the held reservation lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrQuotaHasActiveReservations) {
			t.Fatalf("replacement failed to observe the committed reservation: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

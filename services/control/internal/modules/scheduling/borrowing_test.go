package scheduling

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

//go:embed testdata/borrowing.sql
var testBorrowingSQL string

type borrowingFixture struct {
	reservationFixture
	lender  Attempt
	partner Attempt
}

func newBorrowingFixture(t *testing.T) borrowingFixture {
	t.Helper()
	f := newReservationFixture(t)
	partner := newBorrowingAttempt(f.first, uuid.NewString())
	if _, err := f.pool.Exec(t.Context(), testBorrowingSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO workspaces VALUES ($1,$2)", partner.TenantID, partner.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	f.policy[2].Borrowing = true
	delete(f.policy[0].Limits.Guaranteed, NVIDIAGPU)
	for _, index := range []int{1, 2, 3} {
		f.policy[index].Limits.Burst[NVIDIAGPU] = 12
	}
	f.policy = append(f.policy, QuotaNode{
		ID: partner.WorkspaceID, TenantID: partner.TenantID, ParentID: f.policy[2].ID,
		Level: WorkspaceQuota, Limits: Quota{Burst: Resources{NVIDIAGPU: 4}},
	})
	if err := pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
		_, _, err := ReplaceQuotaWithin(t.Context(), tx, 1, f.policy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return borrowingFixture{f, newBorrowingAttempt(f.first, f.other), partner}
}

func newBorrowingAttempt(a Attempt, workspace string) Attempt {
	a.WorkspaceID, a.ID, a.SourceID = workspace, uuid.NewString(), uuid.NewString()
	return a
}

func borrowingIntent(a Attempt, gpu int64) JobIntent {
	return JobIntent{Version: IntentVersion, Kind: a.Kind, SourceID: a.SourceID,
		Groups: []ReplicaGroup{{Name: "worker", Replicas: 1, Requests: Resources{NVIDIAGPU: gpu}}}}
}

func reserveBorrowing(t *testing.T, store transaction.Store, a Attempt, gpu int64) {
	t.Helper()
	if changed, err := reserveInTransaction(t.Context(), store, a, borrowingIntent(a, gpu)); err != nil || !changed {
		t.Fatalf("reserve %s: changed=%v error=%v", a.ID, changed, err)
	}
}

func requestReclaimInTransaction(ctx context.Context, store transaction.Store, a Attempt, intent JobIntent) (ReclaimPlan, error) {
	var plan ReclaimPlan
	err := ownerTransaction(ctx, store, func(ctx context.Context, q transaction.Executor) error {
		var err error
		plan, err = RequestReclaimWithin(ctx, q, a, intent)
		return err
	})
	if err != nil {
		return ReclaimPlan{}, err
	}
	return plan, nil
}

func assertLoan(t *testing.T, f borrowingFixture, attempt, lender string, amount int64) {
	t.Helper()
	var data []byte
	var loans loanAllocation
	if err := f.pool.QueryRow(t.Context(), "SELECT loan_allocations FROM scheduling_reservations WHERE attempt_id=$1", attempt).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &loans); err != nil || len(loans) != 1 || len(loans[lender]) != 1 || loans[lender][NVIDIAGPU] != amount {
		t.Fatalf("incorrect durable loan attribution: %v (%v)", loans, err)
	}
}

func TestPostgresReclaimWaitsForOrderedCommittedReleases(t *testing.T) {
	f := newBorrowingFixture(t)
	old, latest := f.first, f.partner
	reserveBorrowing(t, f.store, old, 1)
	reserveBorrowing(t, f.store, latest, 1)
	assertLoan(t, f, old.ID, f.lender.WorkspaceID, 1)
	assertLoan(t, f, latest.ID, f.lender.WorkspaceID, 1)
	// Invert the timestamps to exercise ordering independent of clock changes.
	if _, err := f.pool.Exec(t.Context(), "UPDATE scheduling_reservations SET reserved_at=reserved_at-interval '1 day' WHERE attempt_id=$1", latest.ID); err != nil {
		t.Fatal(err)
	}

	intent := borrowingIntent(f.lender, 2)
	assertWait := func(err, cause error, code string, action WaitAction) {
		t.Helper()
		want := []WaitReason{{Code: code, Action: action}}
		if !errors.Is(err, cause) || !slices.Equal(WaitReasons(err), want) {
			t.Fatalf("reclaim guidance: %v, %+v, want %v", err, WaitReasons(err), want)
		}
	}
	_, err := reserveInTransaction(t.Context(), f.store, f.lender, intent)
	assertWait(err, ErrReclaimRequired, ErrReclaimRequired.Error(), RequestQuotaReclaim)
	plan, err := requestReclaimInTransaction(t.Context(), f.store, f.lender, intent)
	if err != nil || plan.State != ReclaimPending || !slices.Equal(plan.Victims, []string{latest.ID, old.ID}) {
		t.Fatalf("ordered reclaim: %+v, %v", plan, err)
	}
	if _, err := requestReclaimInTransaction(t.Context(), f.store, f.lender, borrowingIntent(f.lender, 1)); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("changed reclaim replay: %v", err)
	}
	if _, err := reserveInTransaction(t.Context(), f.store, f.lender, intent); !errors.Is(err, ErrReclaimPending) {
		t.Fatalf("plan itself freed quota: %v", err)
	}
	rolledBack := errors.New("rollback owner stop")
	err = ownerTransaction(t.Context(), f.store, func(ctx context.Context, q transaction.Executor) error {
		changed, err := ReleaseWithin(ctx, q, latest)
		if err != nil || !changed {
			t.Fatalf("release before owner rollback: %v %v", changed, err)
		}
		return rolledBack
	})
	if !errors.Is(err, rolledBack) {
		t.Fatal(err)
	}
	_, err = reserveInTransaction(t.Context(), f.store, f.lender, intent)
	assertWait(err, ErrReclaimPending, "SCHEDULING_RECLAIM_RELEASE_PENDING", WaitForReclaimRelease)
	if changed, err := releaseInTransaction(t.Context(), f.store, latest); err != nil || !changed {
		t.Fatalf("release newest victim: %v %v", changed, err)
	}
	replay, err := requestReclaimInTransaction(t.Context(), f.store, f.lender, intent)
	if err != nil || replay.State != plan.State || !slices.Equal(replay.Victims, plan.Victims) {
		t.Fatalf("reclaim changed after a victim stopped: %+v, %v", replay, err)
	}
	other := newBorrowingAttempt(f.partner, f.partner.WorkspaceID)
	_, err = reserveInTransaction(t.Context(), f.store, other, borrowingIntent(other, 1))
	assertWait(err, ErrBorrowingUnavailable, ErrBorrowingUnavailable.Error(), ReviewBorrowableQuota)
	later := newBorrowingAttempt(f.lender, f.lender.WorkspaceID)
	_, err = reserveInTransaction(t.Context(), f.store, later, borrowingIntent(later, 1))
	assertWait(err, ErrReclaimPending, ErrReclaimPending.Error(), WaitForWorkspaceClaim)
	_, err = requestReclaimInTransaction(t.Context(), f.store, later, borrowingIntent(later, 1))
	assertWait(err, ErrReclaimPending, ErrReclaimPending.Error(), WaitForWorkspaceClaim)
	if _, err := reserveInTransaction(t.Context(), f.store, f.lender, intent); !errors.Is(err, ErrReclaimPending) {
		t.Fatalf("partial release admitted whole request: %v", err)
	}
	if changed, err := releaseInTransaction(t.Context(), f.store, old); err != nil || !changed {
		t.Fatalf("release older victim: %v %v", changed, err)
	}

	// Simulate missing evidence in a transaction that rolls back on rejection.
	err = ownerTransaction(t.Context(), f.store, func(ctx context.Context, q transaction.Executor) error {
		if _, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL,
			"DELETE FROM scheduling_reservations WHERE attempt_id=$1", old.ID)); err != nil {
			return err
		}
		_, err := ReserveWithin(ctx, q, f.lender, intent)
		return err
	})
	if !errors.Is(err, ErrReclaimPending) {
		t.Fatalf("missing release record admitted the claimant: %v", err)
	}

	f.policy[2].Borrowing = false
	err = pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
		_, _, err := ReplaceQuotaWithin(t.Context(), tx, 2, f.policy)
		return err
	})
	if !errors.Is(err, ErrBorrowingPolicyInUse) {
		t.Fatalf("policy changed with pending claim: %v", err)
	}
	reserveBorrowing(t, f.store, f.lender, 2)
	replay, err = requestReclaimInTransaction(t.Context(), f.store, f.lender, intent)
	if err != nil || replay.State != ReclaimAdmitted || !slices.Equal(replay.Victims, plan.Victims) {
		t.Fatalf("reclaim did not settle with admission: %+v, %v", replay, err)
	}
	if changed, err := releaseInTransaction(t.Context(), f.store, latest); err != nil || changed {
		t.Fatalf("stale victim release affected replacement: %v %v", changed, err)
	}
	if changed, err := reserveInTransaction(t.Context(), f.store, f.lender, intent); err != nil || changed {
		t.Fatalf("claimant reservation replay: %v %v", changed, err)
	}
}

func TestPostgresConcurrentBorrowersCannotDoubleLend(t *testing.T) {
	f := newBorrowingFixture(t)
	out := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, a := range []Attempt{f.first, f.partner} {
		wg.Go(func() {
			<-start
			_, err := reserveInTransaction(t.Context(), f.store, a, borrowingIntent(a, 2))
			out <- err
		})
	}
	close(start)
	wg.Wait()
	close(out)
	var successes, unavailable int
	for err := range out {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrBorrowingUnavailable):
			unavailable++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || unavailable != 1 {
		t.Fatalf("successes=%d unavailable=%d", successes, unavailable)
	}
	tooLarge := newBorrowingAttempt(f.partner, f.partner.WorkspaceID)
	if _, err := reserveInTransaction(t.Context(), f.store, tooLarge, borrowingIntent(tooLarge, 5)); !errors.Is(err, ErrReservationQuota) {
		t.Fatalf("borrowing bypassed hard ceiling: %v", err)
	}
}

func TestPostgresCancelReclaimPreservesVictimHold(t *testing.T) {
	f := newBorrowingFixture(t)
	reserveBorrowing(t, f.store, f.first, 1)
	intent := borrowingIntent(f.lender, 2)
	if _, err := requestReclaimInTransaction(t.Context(), f.store, f.lender, intent); err != nil {
		t.Fatal(err)
	}
	for _, wantChanged := range []bool{true, false} {
		if err := ownerTransaction(t.Context(), f.store, func(ctx context.Context, q transaction.Executor) error {
			changed, err := CancelReclaimWithin(ctx, q, f.lender)
			if err == nil && changed != wantChanged {
				t.Fatalf("cancel changed=%v want %v", changed, wantChanged)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := requestReclaimInTransaction(t.Context(), f.store, f.lender, intent)
	if err != nil || plan.State != ReclaimCancelled {
		t.Fatalf("cancelled request was recreated: %+v, %v", plan, err)
	}
	if changed, err := reserveInTransaction(t.Context(), f.store, f.first, borrowingIntent(f.first, 1)); err != nil || changed {
		t.Fatalf("cancellation released the victim: %v, %v", changed, err)
	}
	reserveBorrowing(t, f.store, f.partner, 1)
	if _, err := reserveInTransaction(t.Context(), f.store, f.lender, intent); !errors.Is(err, ErrReclaimRequired) {
		t.Fatalf("cancellation pretended to restore quota: %v", err)
	}
}

func TestBorrowingNeedsEveryRequestedDimension(t *testing.T) {
	cohort := map[string]Quota{
		"requester": {},
		"gpu-team":  {Guaranteed: Resources{NVIDIAGPU: 2}},
		"cpu-team":  {Guaranteed: Resources{CPU: 1000}},
	}
	state := cohortUsage{}
	request := Resources{NVIDIAGPU: 2, CPU: 1000}
	loans, err := state.allocate("requester", cohort, request, false)
	if err != nil || loans["gpu-team"][NVIDIAGPU] != 2 || loans["cpu-team"][CPU] != 1000 || len(loans) != 2 {
		t.Fatalf("mixed lending: %v, %v", loans, err)
	}
	state.pending = map[string]bool{"gpu-team": true}
	if loans, err := state.allocate("requester", cohort, request, false); !errors.Is(err, ErrBorrowingUnavailable) || loans != nil {
		t.Fatalf("partial allocation was exposed: %v, %v", loans, err)
	}
}

func TestPostgresBorrowingRequiresLocalHoldsClaimsAndReleases(t *testing.T) {
	f := newBorrowingFixture(t)
	reserveBorrowing(t, f.store, f.first, 1)
	intent := borrowingIntent(f.lender, 2)
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO sites VALUES ('other-site'); INSERT INTO cells VALUES ('other-cell')"); err != nil {
		t.Fatal(err)
	}
	setReservationScope := func(site, cell string) {
		t.Helper()
		if _, err := f.pool.Exec(t.Context(), "UPDATE scheduling_reservations SET site_id=$1,cell_id=$2 WHERE attempt_id=$3", site, cell, f.first.ID); err != nil {
			t.Fatal(err)
		}
	}
	setClaimScope := func(site, cell string) {
		t.Helper()
		if _, err := f.pool.Exec(t.Context(), "UPDATE scheduling_quota_reclaims SET site_id=$1,cell_id=$2 WHERE attempt_id=$3", site, cell, f.lender.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, coordinates := range [][2]string{{"other-site", "default"}, {"default", "other-cell"}} {
		setReservationScope(coordinates[0], coordinates[1])
		if plan, err := requestReclaimInTransaction(t.Context(), f.store, f.lender, intent); !errors.Is(err, ErrQuotaPolicyUnavailable) || plan.State != "" || len(plan.Victims) != 0 {
			t.Fatalf("foreign cohort supplied a plan: %+v, %v", plan, err)
		}
	}
	setReservationScope("default", "default")
	plan, err := requestReclaimInTransaction(t.Context(), f.store, f.lender, intent)
	if err != nil || plan.State != ReclaimPending || !slices.Equal(plan.Victims, []string{f.first.ID}) {
		t.Fatalf("local reclaim: %+v, %v", plan, err)
	}
	for _, coordinates := range [][2]string{{"other-site", "default"}, {"default", "other-cell"}} {
		setClaimScope(coordinates[0], coordinates[1])
		if plan, err := requestReclaimInTransaction(t.Context(), f.store, f.lender, intent); !errors.Is(err, ErrReservationConflict) || plan.State != "" || len(plan.Victims) != 0 {
			t.Fatalf("foreign claim replay: %+v, %v", plan, err)
		}
		if changed, err := reserveInTransaction(t.Context(), f.store, f.lender, intent); changed || !errors.Is(err, ErrReservationConflict) {
			t.Fatalf("foreign claim admitted: %v, %v", changed, err)
		}
		if changed, err := reserveInTransaction(t.Context(), f.store, f.partner, borrowingIntent(f.partner, 1)); changed || !errors.Is(err, ErrQuotaPolicyUnavailable) {
			t.Fatalf("foreign pending claim supplied cohort state: %v, %v", changed, err)
		}
		err := ownerTransaction(t.Context(), f.store, func(ctx context.Context, q transaction.Executor) error {
			changed, err := CancelReclaimWithin(ctx, q, f.lender)
			if changed {
				t.Fatal("foreign claim cancelled")
			}
			return err
		})
		if !errors.Is(err, ErrReservationConflict) {
			t.Fatalf("foreign claim cancellation: %v", err)
		}
	}
	setClaimScope("default", "default")
	if changed, err := releaseInTransaction(t.Context(), f.store, f.first); err != nil || !changed {
		t.Fatalf("release local victim: %v, %v", changed, err)
	}
	for _, coordinates := range [][2]string{{"other-site", "default"}, {"default", "other-cell"}} {
		setReservationScope(coordinates[0], coordinates[1])
		if changed, err := reserveInTransaction(t.Context(), f.store, f.lender, intent); changed || !errors.Is(err, ErrReclaimPending) {
			t.Fatalf("foreign victim satisfied positive release evidence: %v, %v", changed, err)
		}
	}
	setReservationScope("default", "default")
	reserveBorrowing(t, f.store, f.lender, 2)
	plan, err = requestReclaimInTransaction(t.Context(), f.store, f.lender, intent)
	if err != nil || plan.State != ReclaimAdmitted {
		t.Fatalf("local admitted replay: %+v, %v", plan, err)
	}
	var total, held int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*),count(*) FILTER(WHERE released_at IS NULL) FROM scheduling_reservations").Scan(&total, &held); err != nil || total != 2 || held != 1 {
		t.Fatalf("unexpected final holds: %d, %d, %v", total, held, err)
	}
}

func TestPostgresQuotaReplacementRefusesForeignActiveHold(t *testing.T) {
	f := newReservationFixture(t)
	reserveBorrowing(t, f.store, f.first, 2)
	if _, err := f.pool.Exec(t.Context(), "INSERT INTO sites VALUES ('other-site'); INSERT INTO cells VALUES ('other-cell')"); err != nil {
		t.Fatal(err)
	}
	for _, coordinates := range [][2]string{{"other-site", "default"}, {"default", "other-cell"}} {
		if _, err := f.pool.Exec(t.Context(), "UPDATE scheduling_reservations SET site_id=$1,cell_id=$2 WHERE attempt_id=$3", coordinates[0], coordinates[1], f.first.ID); err != nil {
			t.Fatal(err)
		}
		err := pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
			_, _, err := ReplaceQuotaWithin(t.Context(), tx, 1, f.policy)
			return err
		})
		if !errors.Is(err, ErrQuotaPolicyUnavailable) {
			t.Fatalf("foreign hold allowed quota replacement: %v", err)
		}
		var revision int64
		if err := f.pool.QueryRow(t.Context(), "SELECT revision FROM scheduling_quota_policy").Scan(&revision); err != nil || revision != 1 {
			t.Fatalf("failed replacement changed policy: %d, %v", revision, err)
		}
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE scheduling_reservations SET site_id=local_site_id(),cell_id=local_cell_id() WHERE attempt_id=$1", f.first.ID); err != nil {
		t.Fatal(err)
	}
	if err := pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
		revision, changed, err := ReplaceQuotaWithin(t.Context(), tx, 1, f.policy)
		if err == nil && (revision != 2 || !changed) {
			t.Fatalf("local quota replacement: %d, %v", revision, changed)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

package scheduling

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var submissionSnapshot = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

func estimateSubmission(t *testing.T, f reservationFixture, a Attempt, intent JobIntent, cell CellCapacity) (SubmissionEstimate, error) {
	t.Helper()
	var estimate SubmissionEstimate
	err := pgx.BeginTxFunc(t.Context(), f.pool, submissionSnapshot, func(tx pgx.Tx) error {
		var err error
		estimate, err = EstimateSubmissionWithin(t.Context(), tx, a.TenantID, a.WorkspaceID, intent, cell)
		return err
	})
	if err != nil {
		return SubmissionEstimate{}, err
	}
	return estimate, nil
}

func TestPostgresPreflightUsesOneSnapshotAndRefreshes(t *testing.T) {
	f := newReservationFixture(t)
	if changed, err := reserveInTransaction(t.Context(), f.store, f.first, reservationIntent(f.first, 2)); err != nil || !changed {
		t.Fatalf("reserve competing attempt: %v, %v", changed, err)
	}
	a := newBorrowingAttempt(f.first, f.first.WorkspaceID)
	intent := reservationIntent(a, 4)
	cell := CellCapacity{Total: Resources{CPU: 1000, NVIDIAGPU: 4}}
	if err := pgx.BeginTxFunc(t.Context(), f.pool, submissionSnapshot, func(tx pgx.Tx) error {
		first, err := EstimateSubmissionWithin(t.Context(), tx, a.TenantID, a.WorkspaceID, intent, cell)
		if err != nil {
			return err
		}
		available := int64(2)
		want := SubmissionEstimate{PolicyRevision: 1, Demand: Resources{CPU: 1000, NVIDIAGPU: 4}, Reasons: []WaitReason{
			{Code: string(QuotaInUse), Action: WaitForCapacity, Level: WorkspaceQuota, Resource: NVIDIAGPU,
				Requested: 4, Available: &available},
		}}
		if !reflect.DeepEqual(first, want) {
			t.Fatalf("first estimate: %+v", first)
		}

		f.policy[0].Limits.Burst[NVIDIAGPU] = 5
		f.policy[4].Limits.Burst[NVIDIAGPU] = 3
		if err := pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
			_, _, err := ReplaceQuotaWithin(t.Context(), tx, 1, f.policy)
			return err
		}); err != nil {
			return err
		}
		if changed, err := releaseInTransaction(t.Context(), f.store, f.first); err != nil || !changed {
			t.Fatalf("release competing attempt: %v, %v", changed, err)
		}
		repeated, err := EstimateSubmissionWithin(t.Context(), tx, a.TenantID, a.WorkspaceID, intent, cell)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(repeated, first) {
			t.Fatalf("snapshot changed with concurrent commits: %+v, originally %+v", repeated, first)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	fresh, err := estimateSubmission(t, f, a, intent, cell)
	if err != nil || fresh.PolicyRevision != 2 || len(fresh.Reasons) != 0 {
		t.Fatalf("new snapshot did not observe both commits: %+v, %v", fresh, err)
	}
	smaller := CellCapacity{Total: Resources{CPU: 1000, NVIDIAGPU: 3}}
	fresh, err = estimateSubmission(t, f, a, intent, smaller)
	want := []WaitReason{{Code: string(CellTooSmall), Action: ChangeRequestOrCell, Resource: NVIDIAGPU, Requested: 4}}
	if err != nil || !reflect.DeepEqual(fresh.Reasons, want) {
		t.Fatalf("cell observation: %+v, %v", fresh, err)
	}
	if changed, err := reserveInTransaction(t.Context(), f.store, a, intent); err != nil || !changed {
		t.Fatalf("preview created a hold or blocked the real reserve: %v, %v", changed, err)
	}
}

func TestPostgresPreflightRequiresReadOnlyScopedSnapshot(t *testing.T) {
	f := newReservationFixture(t)
	for _, options := range []pgx.TxOptions{{IsoLevel: pgx.RepeatableRead}, {AccessMode: pgx.ReadOnly}} {
		err := pgx.BeginTxFunc(t.Context(), f.pool, options, func(tx pgx.Tx) error {
			_, err := EstimateSubmissionWithin(t.Context(), tx, f.first.TenantID, f.first.WorkspaceID, JobIntent{}, CellCapacity{})
			return err
		})
		if !errors.Is(err, ErrPreflightSnapshotRequired) {
			t.Fatalf("accepted unsuitable transaction %+v: %v", options, err)
		}
	}
	foreign := f.first
	foreign.TenantID = uuid.NewString()
	absent := foreign
	absent.WorkspaceID = uuid.NewString()
	for _, denied := range []Attempt{foreign, absent} {
		got, err := estimateSubmission(t, f, denied, JobIntent{}, CellCapacity{})
		if !errors.Is(err, ErrQuotaScopeNotFound) || !reflect.DeepEqual(got, SubmissionEstimate{}) {
			t.Fatalf("denied scope returned details: %+v, %v", got, err)
		}
	}
}

func TestPostgresPreflightObservesBorrowingWithoutCreatingAClaim(t *testing.T) {
	f := newBorrowingFixture(t)
	reserveBorrowing(t, f.store, f.first, 2)
	intent := borrowingIntent(f.lender, 2)
	cell := CellCapacity{Total: Resources{NVIDIAGPU: 4}}
	assertReason := func(a Attempt, intent JobIntent, want WaitReason) {
		t.Helper()
		got, err := estimateSubmission(t, f.reservationFixture, a, intent, cell)
		if err != nil || !reflect.DeepEqual(got.Reasons, []WaitReason{want}) {
			t.Fatalf("borrowing preview: %+v, %v", got, err)
		}
	}
	assertReason(f.lender, intent, WaitReason{Code: ErrReclaimRequired.Error(), Action: RequestQuotaReclaim})
	var claims int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM scheduling_quota_reclaims").Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("preview created a claim: %d, %v", claims, err)
	}
	if _, err := requestReclaimInTransaction(t.Context(), f.store, f.lender, intent); err != nil {
		t.Fatal(err)
	}
	next := newBorrowingAttempt(f.lender, f.lender.WorkspaceID)
	assertReason(next, borrowingIntent(next, 1), WaitReason{Code: ErrReclaimPending.Error(), Action: WaitForWorkspaceClaim})
	if changed, err := releaseInTransaction(t.Context(), f.store, f.first); err != nil || !changed {
		t.Fatalf("release victim: %v, %v", changed, err)
	}
	assertReason(next, borrowingIntent(next, 1), WaitReason{Code: ErrReclaimPending.Error(), Action: WaitForWorkspaceClaim})
	reserveBorrowing(t, f.store, f.lender, 2)
}

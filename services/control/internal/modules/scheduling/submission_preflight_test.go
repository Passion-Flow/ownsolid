package scheduling

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var submissionSnapshot = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

func fixtureCellSample(t *testing.T, pool *pgxpool.Pool, total, reserved Resources) CellCapacitySample {
	t.Helper()
	var sample CellCapacitySample
	if err := pool.QueryRow(t.Context(), "SELECT local_site_id(),local_cell_id(),clock_timestamp()").
		Scan(&sample.SiteID, &sample.CellID, &sample.ObservedAt); err != nil {
		t.Fatal(err)
	}
	sample.ValidUntil = sample.ObservedAt.Add(time.Hour)
	sample.Capacity = CellCapacity{Total: total, Reserved: reserved}
	return sample
}

func estimateSubmission(t *testing.T, f reservationFixture, a Attempt, intent JobIntent, cell CellCapacitySample) (SubmissionEstimate, error) {
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
	cell := fixtureCellSample(t, f.pool, Resources{CPU: 1000, NVIDIAGPU: 4}, Resources{CPU: 0, NVIDIAGPU: 0})
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
		if changed, err := releaseInTransaction(t.Context(), f.store, f.first, fixtureReplicaReleases(f.first, reservationIntent(f.first, 2))...); err != nil || !changed {
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
	smaller := fixtureCellSample(t, f.pool, Resources{CPU: 1000, NVIDIAGPU: 3}, Resources{CPU: 0, NVIDIAGPU: 0})
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
			_, err := EstimateSubmissionWithin(t.Context(), tx, f.first.TenantID, f.first.WorkspaceID, JobIntent{}, CellCapacitySample{})
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
		got, err := estimateSubmission(t, f, denied, JobIntent{}, CellCapacitySample{})
		if !errors.Is(err, ErrQuotaScopeNotFound) || !reflect.DeepEqual(got, SubmissionEstimate{}) {
			t.Fatalf("denied scope returned details: %+v, %v", got, err)
		}
	}
}

func TestPostgresPreflightObservesBorrowingWithoutCreatingAClaim(t *testing.T) {
	f := newBorrowingFixture(t)
	reserveBorrowing(t, f.store, f.first, 2)
	intent := borrowingIntent(f.lender, 2)
	cell := fixtureCellSample(t, f.pool, Resources{NVIDIAGPU: 4}, Resources{NVIDIAGPU: 0})
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

func TestPostgresPreflightUsesAuthorizedWorkspaceAndCurrentCell(t *testing.T) {
	accounts := identityfixture.New(t)
	password := identityfixture.Bootstrap(t, accounts)
	actor := identityfixture.Login(t, accounts, "admin@example.invalid", password, "admin")
	scope := actor.Me.Workspace
	authority := &workspaces.Service{DB: accounts.DB, Identity: accounts}
	store, err := accounts.Repository()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.DB.Exec(t.Context(), testQuotaSQL+testReservationSQL); err != nil {
		t.Fatal(err)
	}
	limit := Resources{CPU: 4000, NVIDIAGPU: 3}
	policy := []QuotaNode{
		{ID: "platform", Level: PlatformQuota, Limits: Quota{limit, limit}},
		{ID: scope.TenantID, ParentID: "platform", TenantID: scope.TenantID, Level: TenantQuota, Limits: Quota{limit, limit}},
		{ID: "department", ParentID: scope.TenantID, TenantID: scope.TenantID, Level: DepartmentQuota, Limits: Quota{limit, limit}},
		{ID: scope.ID, ParentID: "department", TenantID: scope.TenantID, Level: WorkspaceQuota, Limits: Quota{limit, limit}},
	}
	if err := pgx.BeginFunc(t.Context(), accounts.DB, func(tx pgx.Tx) error {
		_, _, err := ReplaceQuotaWithin(t.Context(), tx, 0, policy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	intent := JobIntent{Version: IntentVersion, Kind: Training, SourceID: "training-preview", Groups: []ReplicaGroup{
		{Name: "driver", Replicas: 1, Requests: Resources{CPU: 1000}},
		{Name: "workers", Replicas: 2, Requests: Resources{CPU: 1000, NVIDIAGPU: 1}},
	}}
	sample := fixtureCellSample(t, accounts.DB, Resources{CPU: 3000, NVIDIAGPU: 2}, Resources{CPU: 0, NVIDIAGPU: 0})
	estimate := func(actor identity.Auth, selector string, sample CellCapacitySample) (SubmissionEstimate, error) {
		var out SubmissionEstimate
		err := ownerTransaction(t.Context(), store, func(ctx context.Context, q transaction.Executor) error {
			workspace, err := authority.ReadScopeWithin(ctx, q, actor, selector)
			if err != nil {
				return err
			}
			if workspace.Status != "active" || workspace.TenantID != scope.TenantID || workspace.ID != scope.ID {
				t.Fatal("unexpected workspace from the owner authority")
			}
			return pgx.BeginTxFunc(ctx, accounts.DB, submissionSnapshot, func(tx pgx.Tx) error {
				out, err = EstimateSubmissionWithin(ctx, tx, workspace.TenantID, workspace.ID, intent, sample)
				return err
			})
		})
		if err != nil {
			return SubmissionEstimate{}, err
		}
		return out, nil
	}
	if got, err := estimate(actor, scope.ID, sample); err != nil || !reflect.DeepEqual(got, SubmissionEstimate{
		PolicyRevision: 1, Demand: Resources{CPU: 3000, NVIDIAGPU: 2},
	}) {
		t.Fatalf("whole-job fit: %+v, %v", got, err)
	}
	if got, err := estimate(identity.Auth{}, scope.ID, sample); err == nil || !reflect.DeepEqual(got, SubmissionEstimate{}) {
		t.Fatalf("unauthenticated estimate: %+v, %v", got, err)
	}
	if got, err := estimate(actor, uuid.NewString(), sample); err == nil || !reflect.DeepEqual(got, SubmissionEstimate{}) {
		t.Fatalf("unknown workspace estimate: %+v, %v", got, err)
	}

	for _, test := range []struct {
		name   string
		change func(*CellCapacitySample)
	}{
		{"expired", func(s *CellCapacitySample) { s.ValidUntil = s.ObservedAt }},
		{"future", func(s *CellCapacitySample) { s.ObservedAt = s.ObservedAt.Add(time.Minute) }},
		{"undated", func(s *CellCapacitySample) { s.ObservedAt = time.Time{} }},
		{"wrong site", func(s *CellCapacitySample) { s.SiteID = "another-site" }},
		{"wrong cell", func(s *CellCapacitySample) { s.CellID = "another-cell" }},
		{"unavailable", func(s *CellCapacitySample) { *s = CellCapacitySample{} }},
		{"unknown total", func(s *CellCapacitySample) { s.Capacity.Total = Resources{CPU: 3000} }},
		{"unknown reservations", func(s *CellCapacitySample) { s.Capacity.Reserved = Resources{NVIDIAGPU: 0} }},
		{"invalid quantity", func(s *CellCapacitySample) { s.Capacity.Total = Resources{CPU: 3000, NVIDIAGPU: -1} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := sample
			test.change(&invalid)
			got, err := estimate(actor, scope.ID, invalid)
			if !errors.Is(err, ErrCellCapacityUnavailable) || !reflect.DeepEqual(got, SubmissionEstimate{}) {
				t.Fatalf("invalid sample returned an estimate: %+v, %v", got, err)
			}
		})
	}
	for _, test := range []struct {
		total, reserved int64
		reason          LimitReason
		action          WaitAction
	}{
		{0, 0, CellTooSmall, ChangeRequestOrCell},
		{2, 1, CellInUse, WaitForCapacity},
	} {
		limited := sample
		limited.Capacity.Total = Resources{CPU: 3000, NVIDIAGPU: test.total}
		limited.Capacity.Reserved = Resources{CPU: 0, NVIDIAGPU: test.reserved}
		got, err := estimate(actor, scope.ID, limited)
		want := []WaitReason{{Code: string(test.reason), Action: test.action, Resource: NVIDIAGPU, Requested: 2}}
		if err != nil || !reflect.DeepEqual(got.Reasons, want) {
			t.Fatalf("whole-job cell shortfall exposed shared quantities or wrong reason: %+v, %v", got, err)
		}
	}
	var holds int
	if err := accounts.DB.QueryRow(t.Context(), "SELECT count(*) FROM scheduling_reservations").Scan(&holds); err != nil || holds != 0 {
		t.Fatalf("estimate wrote holds: %d, %v", holds, err)
	}
}

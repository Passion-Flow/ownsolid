package scheduling

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func gangTestIntent(a Attempt) JobIntent {
	return JobIntent{Version: IntentVersion, Kind: a.Kind, SourceID: a.SourceID, Groups: []ReplicaGroup{
		{Name: "driver", Replicas: 1, Requests: Resources{CPU: 1000}},
		{Name: "worker", Replicas: 2, Requests: Resources{CPU: 2000, NVIDIAGPU: 1}},
	}}
}

func fixtureReplicaReleases(a Attempt, intent JobIntent) []ReplicaRelease {
	var evidence []ReplicaRelease
	for _, group := range intent.Groups {
		for index := range group.Replicas {
			evidence = append(evidence, ReplicaRelease{a.ID, group.Name, index, ReplicaStopped})
		}
	}
	return evidence
}

func TestPostgresGangReservationRollsBackOwnerHolds(t *testing.T) {
	f := newReservationFixture(t)
	if _, err := f.pool.Exec(t.Context(), `
CREATE TABLE gang_fixture_nodes (id text PRIMARY KEY);
INSERT INTO gang_fixture_nodes VALUES ('driver-node'), ('worker-a'), ('worker-b');
CREATE TABLE gang_fixture_holds (
 node_id text PRIMARY KEY REFERENCES gang_fixture_nodes (id) DEFERRABLE INITIALLY DEFERRED,
 attempt_id uuid NOT NULL
);`); err != nil {
		t.Fatal(err)
	}

	a := f.first
	intent := gangTestIntent(a)
	reserveOnNodes := func(lastNode string) error {
		return pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
			for _, node := range []string{"driver-node", "worker-a", lastNode} {
				if _, err := tx.Exec(t.Context(), "INSERT INTO gang_fixture_holds VALUES ($1,$2)", node, a.ID); err != nil {
					return err
				}
			}
			changed, err := ReserveWithin(t.Context(), transaction.PostgreSQLTransaction(tx), a, intent)
			if err == nil && !changed {
				t.Fatal("new gang was treated as a replay")
			}
			return err
		})
	}
	assertCounts := func(nodes, attempts int) {
		t.Helper()
		var gotNodes, gotAttempts int
		err := f.pool.QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM gang_fixture_holds WHERE attempt_id=$1),
 (SELECT count(*) FROM scheduling_reservations WHERE attempt_id=$1)`, a.ID).Scan(&gotNodes, &gotAttempts)
		if err != nil || gotNodes != nodes || gotAttempts != attempts {
			t.Fatalf("owner holds=%d, scheduling rows=%d, error=%v", gotNodes, gotAttempts, err)
		}
	}

	blocker := newBorrowingAttempt(a, a.WorkspaceID)
	blockerIntent := JobIntent{Version: IntentVersion, Kind: blocker.Kind, SourceID: blocker.SourceID,
		Groups: []ReplicaGroup{{Name: "other", Replicas: 1, Requests: Resources{CPU: 4000}}}}
	if changed, err := reserveInTransaction(t.Context(), f.store, blocker, blockerIntent); err != nil || !changed {
		t.Fatalf("reserve CPU blocker: %v, %v", changed, err)
	}
	err := reserveOnNodes("worker-b")
	reasons := WaitReasons(err)
	if !errors.Is(err, ErrReservationQuota) || len(reasons) != 1 || reasons[0].Resource != CPU {
		t.Fatalf("expected exactly one resource to block the gang: %+v, %v", reasons, err)
	}
	assertCounts(0, 0)
	if changed, err := releaseInTransaction(t.Context(), f.store, blocker); err != nil || !changed {
		t.Fatalf("release CPU blocker: %v, %v", changed, err)
	}

	var pg *pgconn.PgError
	if err := reserveOnNodes("unregistered-node"); !errors.As(err, &pg) || pg.Code != "23503" {
		t.Fatalf("expected the deferred constraint to reject COMMIT: %v", err)
	}
	assertCounts(0, 0)
	if err := reserveOnNodes("worker-b"); err != nil {
		t.Fatal(err)
	}
	assertCounts(3, 1)

	slices.Reverse(intent.Groups)
	if changed, err := reserveInTransaction(t.Context(), f.store, a, intent); err != nil || changed {
		t.Fatalf("exact gang replay: %v, %v", changed, err)
	}
	different := gangTestIntent(a)
	different.Groups[1] = ReplicaGroup{Name: "worker", Replicas: 1, Requests: Resources{CPU: 4000, NVIDIAGPU: 2}}
	if changed, err := reserveInTransaction(t.Context(), f.store, a, different); changed || !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("same totals with different replicas replayed: %v, %v", changed, err)
	}
	assertCounts(3, 1)
	var stored []byte
	var demand Resources
	if err := f.pool.QueryRow(t.Context(), "SELECT demand FROM scheduling_reservations WHERE attempt_id=$1", a.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stored, &demand); err != nil || !maps.Equal(demand, Resources{CPU: 5000, NVIDIAGPU: 2}) {
		t.Fatalf("persisted gang demand: %+v, %v", demand, err)
	}
}

func TestPostgresGangReleaseRequiresEveryExactReplica(t *testing.T) {
	f := newReservationFixture(t)
	a := f.first
	intent := gangTestIntent(a)
	if changed, err := reserveInTransaction(t.Context(), f.store, a, intent); err != nil || !changed {
		t.Fatalf("reserve gang: %v, %v", changed, err)
	}
	evidence := fixtureReplicaReleases(a, intent)
	evidence[0].Outcome = ReplicaNeverDelivered

	foreign := a
	foreign.TenantID = uuid.NewString()
	if changed, err := releaseInTransaction(t.Context(), f.store, foreign, evidence...); changed || !errors.Is(err, ErrReservationNotFound) {
		t.Fatalf("foreign release: %v, %v", changed, err)
	}
	if changed, err := releaseInTransaction(t.Context(), f.store, a); changed || !errors.Is(err, ErrGangReleaseEvidence) {
		t.Fatalf("single-replica path released a gang: %v, %v", changed, err)
	}
	if changed, err := releaseInTransaction(t.Context(), f.store, a, evidence[:2]...); changed || !errors.Is(err, ErrGangReleaseEvidence) {
		t.Fatalf("partial proof released the gang: %v, %v", changed, err)
	}
	for name, change := range map[string]func([]ReplicaRelease){
		"duplicate slot":  func(proofs []ReplicaRelease) { proofs[2] = proofs[1] },
		"unknown group":   func(proofs []ReplicaRelease) { proofs[2].Group = "different" },
		"unknown replica": func(proofs []ReplicaRelease) { proofs[2].Index = 2 },
		"uncertain stop":  func(proofs []ReplicaRelease) { proofs[2].Outcome = "timed_out" },
	} {
		t.Run(name, func(t *testing.T) {
			wrong := slices.Clone(evidence)
			change(wrong)
			if changed, err := releaseInTransaction(t.Context(), f.store, a, wrong...); changed || !errors.Is(err, ErrGangReleaseEvidence) {
				t.Fatalf("invalid evidence released the gang: %v, %v", changed, err)
			}
		})
	}

	rollback := errors.New("owner release rollback")
	err := ownerTransaction(t.Context(), f.store, func(ctx context.Context, q transaction.Executor) error {
		if changed, err := ReleaseGangWithin(ctx, q, a, evidence); err != nil || !changed {
			t.Fatalf("release in the owner's transaction: %v, %v", changed, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	competitor := newBorrowingAttempt(a, a.WorkspaceID)
	if _, err := reserveInTransaction(t.Context(), f.store, competitor, reservationIntent(competitor, 3)); !errors.Is(err, ErrReservationQuota) {
		t.Fatalf("incomplete or rolled back release freed capacity: %v", err)
	}
	for _, wantChanged := range []bool{true, false} {
		if changed, err := releaseInTransaction(t.Context(), f.store, a, evidence...); err != nil || changed != wantChanged {
			t.Fatalf("gang release or replay: %v, %v", changed, err)
		}
	}
	replacement := a
	replacement.ID, replacement.Number = uuid.NewString(), a.Number+1
	if changed, err := reserveInTransaction(t.Context(), f.store, replacement, intent); err != nil || !changed {
		t.Fatalf("reserve replacement gang: %v, %v", changed, err)
	}
	if changed, err := releaseInTransaction(t.Context(), f.store, replacement, evidence...); changed || !errors.Is(err, ErrGangReleaseEvidence) {
		t.Fatalf("old evidence released the replacement: %v, %v", changed, err)
	}
}

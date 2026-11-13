//go:build repository_integration

package scheduling

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/jackc/pgx/v5/pgxpool"
)

func reservationFaultStore(t *testing.T, pool *pgxpool.Pool) (transaction.Store, *commitfault.PlaintextPostgreSQL) {
	t.Helper()
	cfg := pool.Config()
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.TLSConfig != nil {
		t.Skip("the existing wire fault harness requires an isolated plaintext loopback endpoint")
	}
	proxy, err := commitfault.NewPlaintextPostgreSQL(cfg.ConnConfig.Host, cfg.ConnConfig.Port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := proxy.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	cfg.ConnConfig.Port = proxy.Port()
	proxied, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proxied.Close)
	store, err := transaction.FromPostgreSQL(proxied)
	if err != nil {
		t.Fatal(err)
	}
	return store, proxy
}

func assertLostReservationCommit(t *testing.T, proxy *commitfault.PlaintextPostgreSQL, operation string, err error) {
	t.Helper()
	var unknown *transaction.Error
	if !errors.As(err, &unknown) || unknown.Kind != transaction.CommitUnknown {
		t.Fatalf("%s expected unknown commit: %v", operation, err)
	}
	select {
	case observed := <-proxy.Outcomes():
		if !observed.Forwarded || !observed.ServerOK || !observed.Suppressed {
			t.Fatalf("%s proxy did not drop a real server commit acknowledgement", operation)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s proxy did not report the commit result", operation)
	}
}

func TestPostgresGangReservationCommitAcknowledgementLoss(t *testing.T) {
	f := newReservationFixture(t)
	store, proxy := reservationFaultStore(t, f.pool)
	a := f.first
	intent := gangTestIntent(a)
	for _, op := range []struct {
		name  string
		write func(transaction.Store) (bool, error)
	}{
		{"reserve", func(store transaction.Store) (bool, error) {
			return reserveInTransaction(t.Context(), store, a, intent)
		}},
		{"release", func(store transaction.Store) (bool, error) {
			return releaseInTransaction(t.Context(), store, a, fixtureReplicaReleases(a, intent)...)
		}},
	} {
		if err := proxy.Arm(commitfault.DropCommitAcknowledgement); err != nil {
			t.Fatal(err)
		}
		changed, err := op.write(store)
		if changed {
			t.Fatalf("%s exposed an unacknowledged write", op.name)
		}
		assertLostReservationCommit(t, proxy, op.name, err)
		if changed, err := op.write(f.store); err != nil || changed {
			t.Fatalf("%s exact replay after unknown commit: %v %v", op.name, changed, err)
		}
	}
}

func TestPostgresBorrowingAndReclaimCommitAcknowledgementLoss(t *testing.T) {
	f := newBorrowingFixture(t)
	store, proxy := reservationFaultStore(t, f.pool)
	borrower, lender := f.first, f.lender
	if err := proxy.Arm(commitfault.DropCommitAcknowledgement); err != nil {
		t.Fatal(err)
	}
	changed, err := reserveInTransaction(t.Context(), store, borrower, borrowingIntent(borrower, 2))
	if changed {
		t.Fatal("borrow reported an unacknowledged write")
	}
	assertLostReservationCommit(t, proxy, "borrow", err)
	if changed, err := reserveInTransaction(t.Context(), f.store, borrower, borrowingIntent(borrower, 2)); err != nil || changed {
		t.Fatalf("borrow replay after commit loss: %v %v", changed, err)
	}
	assertLoan(t, f, borrower.ID, lender.WorkspaceID, 2)

	intent := borrowingIntent(lender, 2)
	if err := proxy.Arm(commitfault.DropCommitAcknowledgement); err != nil {
		t.Fatal(err)
	}
	plan, err := requestReclaimInTransaction(t.Context(), store, lender, intent)
	if plan.State != "" {
		t.Fatalf("unacknowledged reclaim exposed a plan: %+v", plan)
	}
	assertLostReservationCommit(t, proxy, "request reclaim", err)
	plan, err = requestReclaimInTransaction(t.Context(), f.store, lender, intent)
	if err != nil || plan.State != ReclaimPending || !slices.Equal(plan.Victims, []string{borrower.ID}) {
		t.Fatalf("reclaim replay after commit loss: %+v %v", plan, err)
	}
	if _, err := reserveInTransaction(t.Context(), f.store, lender, intent); !errors.Is(err, ErrReclaimPending) {
		t.Fatalf("committed reclaim released victim: %v", err)
	}

	if err := proxy.Arm(commitfault.DropCommitAcknowledgement); err != nil {
		t.Fatal(err)
	}
	changed, err = releaseInTransaction(t.Context(), store, borrower)
	if changed {
		t.Fatal("release reported an unacknowledged write")
	}
	assertLostReservationCommit(t, proxy, "release borrowed hold", err)
	if changed, err := releaseInTransaction(t.Context(), f.store, borrower); err != nil || changed {
		t.Fatalf("release replay after commit loss: %v %v", changed, err)
	}
	reserveBorrowing(t, f.store, lender, 2)
}

func TestPostgresQueuedReservationCommitAcknowledgementLoss(t *testing.T) {
	f := newQueuedFixture(t)
	store, proxy := reservationFaultStore(t, f.pool)
	fault := f
	fault.store = store
	if err := proxy.Arm(commitfault.DropCommitAcknowledgement); err != nil {
		t.Fatal(err)
	}
	changed, err := fault.admit(t.Context(), f.first, f.read, nil)
	if changed {
		t.Fatal("queued dispatch exposed an unacknowledged commit")
	}
	assertLostReservationCommit(t, proxy, "queued dispatch", err)
	f.assertState(t, f.first, "preparing", 2, 1)
	noRead := func(context.Context, transaction.Executor) ([]QueueEntry, error) {
		t.Fatal("unknown commit replay read the already-dequeued entry")
		return nil, nil
	}
	if changed, err := f.admit(t.Context(), f.first, noRead, nil); err != nil || changed {
		t.Fatalf("queued dispatch replay after unknown commit: %v, %v", changed, err)
	}
	f.assertState(t, f.first, "preparing", 2, 1)
}

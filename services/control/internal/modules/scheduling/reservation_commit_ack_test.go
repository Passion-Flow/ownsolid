//go:build repository_integration

package scheduling

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresReservationCommitAcknowledgementLoss(t *testing.T) {
	f := newReservationFixture(t)
	cfg := f.pool.Config()
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
	a := f.first
	for _, op := range []struct {
		name  string
		write func(transaction.Store) (bool, error)
	}{
		{"reserve", func(store transaction.Store) (bool, error) {
			return reserveInTransaction(t.Context(), store, a, reservationIntent(a, 2))
		}},
		{"release", func(store transaction.Store) (bool, error) {
			return releaseInTransaction(t.Context(), store, a)
		}},
	} {
		if err := proxy.Arm(commitfault.DropCommitAcknowledgement); err != nil {
			t.Fatal(err)
		}
		changed, err := op.write(store)
		var unknown *transaction.Error
		if changed || !errors.As(err, &unknown) || unknown.Kind != transaction.CommitUnknown {
			t.Fatalf("%s exposed an unacknowledged write: %v %v", op.name, changed, err)
		}
		select {
		case observed := <-proxy.Outcomes():
			if !observed.Forwarded || !observed.ServerOK || !observed.Suppressed {
				t.Fatalf("%s proxy did not drop a real server commit acknowledgement", op.name)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s proxy did not report the commit result", op.name)
		}
		if changed, err := op.write(f.store); err != nil || changed {
			t.Fatalf("%s exact replay after unknown commit: %v %v", op.name, changed, err)
		}
	}
}

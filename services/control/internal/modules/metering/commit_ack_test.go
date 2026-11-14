//go:build repository_integration

package metering

import (
	"context"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresLedgerCommitAcknowledgementLoss(t *testing.T) {
	pool := newTestLedger(t)
	cfg := pool.Config()
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.TLSConfig != nil {
		t.Skip("the existing wire fault harness requires an isolated plaintext loopback endpoint")
	}
	scope := addTestScope(t, pool)
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
	if err := proxied.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := proxy.Arm(commitfault.DropCommitAcknowledgement); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	event := Event{Producer: "scheduler/cell", ID: "attempt-c/1", OccurredAt: now, Usage: Usage{GPUMicroseconds: 3000000, Requests: 1}}
	if inserted, err := writeEvent(t.Context(), proxied, scope, event); inserted || err == nil {
		t.Fatalf("lost acknowledgement was reported as a committed insert: %v, %v", inserted, err)
	}
	select {
	case observation := <-proxy.Outcomes():
		if !observation.Forwarded || !observation.ServerOK || !observation.Suppressed {
			t.Fatal("proxy did not observe a successful PostgreSQL COMMIT")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not observe the PostgreSQL COMMIT result")
	}
	if inserted, err := writeEvent(t.Context(), pool, scope, event); inserted || err != nil {
		t.Fatalf("retry of already committed event: %v, %v", inserted, err)
	}
	if got := readTotals(t, pool, scope, now.Add(-time.Hour), now.Add(time.Hour)); got.Requests != "1" || got.GPUMicroseconds != "3000000" {
		t.Fatalf("commit with lost acknowledgement not counted exactly once: %+v", got)
	}
}

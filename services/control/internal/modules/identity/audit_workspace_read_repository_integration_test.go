//go:build repository_integration

package identity_test

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWorkspaceAuditReadCommitUncertainty(t *testing.T) {
	for _, fault := range []struct {
		name      string
		mode      commitfault.Mode
		committed int
	}{
		{"commit_not_forwarded", commitfault.DropBeforeCommit, 0},
		{"actual_server_ack_suppressed", commitfault.DropCommitAcknowledgement, 1},
	} {
		t.Run(fault.name, func(t *testing.T) {
			f := newWorkspaceAuditReadFixture(t)
			config := f.reader.DB.Config()
			if config.ConnConfig.Host != "127.0.0.1" || config.ConnConfig.TLSConfig != nil || len(config.ConnConfig.Fallbacks) != 0 {
				t.Fatal("scoped wire fault requires explicit isolated plaintext PostgreSQL")
			}
			proxy, err := commitfault.NewPlaintextPostgreSQL(config.ConnConfig.Host, config.ConnConfig.Port)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := proxy.Close(ctx); err != nil {
					t.Error("scoped wire proxy did not join", err)
				}
			})
			config.ConnConfig.Port, config.MaxConns = proxy.Port(), 2
			pool, err := pgxpool.NewWithConfig(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			if err := pool.Ping(t.Context()); err != nil {
				t.Fatal("scoped forwarding pool unavailable")
			}
			store, err := transaction.FromPostgreSQL(pool)
			if err != nil {
				t.Fatal(err)
			}
			reader := *f.reader
			reader.DB, reader.Storage = nil, store
			read := func() (identity.WorkspaceAuditList, error) {
				return reader.ListWorkspaceAuditForActor(t.Context(), f.actor, f.space, url.Values{"request_id": {f.seed}})
			}
			if err := proxy.Arm(fault.mode); err != nil {
				t.Fatal(err)
			}
			page, err := read()
			if err == nil || err.Error() != "STORAGE_COMMIT_OUTCOME_UNKNOWN" || len(page.Items) != 0 || page.NextCursor != nil || len(page.AssistantActors) != 0 {
				t.Fatal("uncertain workspace read released protected content", err)
			}
			select {
			case observed := <-proxy.Outcomes():
				if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != (fault.committed == 1) || observed.ServerOK != (fault.committed == 1) {
					t.Fatal("scoped read wire fault was not established")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("scoped read wire outcome unavailable")
			}
			if proxy.Commits() != 1 || f.count(t) != fault.committed {
				t.Fatal("scoped read repeated its append or lost durable uncertainty")
			}
			if page, err := read(); err != nil || len(page.Items) != 1 || proxy.Commits() != 2 || f.count(t) != fault.committed+1 {
				t.Fatal("explicit scoped retry did not create its own access event", err)
			}
		})
	}
}

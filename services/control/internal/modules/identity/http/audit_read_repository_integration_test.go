//go:build repository_integration

package http_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/jackc/pgx/v5/pgxpool"
)

func auditReadForwarding(t *testing.T, source *identity.Service) (*identity.Service, *commitfault.PlaintextPostgreSQL) {
	t.Helper()
	c := source.DB.Config()
	if c.ConnConfig.Host != "127.0.0.1" || c.ConnConfig.TLSConfig != nil || len(c.ConnConfig.Fallbacks) != 0 {
		t.Fatal("audit wire fault requires the explicit isolated plaintext PostgreSQL fixture")
	}
	proxy, err := commitfault.NewPlaintextPostgreSQL(c.ConnConfig.Host, c.ConnConfig.Port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := proxy.Close(ctx); err != nil {
			t.Error("audit read wire cleanup did not join", err)
		}
	})
	c.ConnConfig.Port, c.MaxConns = proxy.Port(), 2
	pool, err := pgxpool.NewWithConfig(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err = pool.Ping(ctx); err != nil {
		t.Fatal("audit read forwarding pool did not become ready")
	}
	store, err := transaction.FromPostgreSQL(pool)
	if err != nil {
		t.Fatal(err)
	}
	copy := *source
	copy.Storage, copy.DB = store, nil
	return &copy, proxy
}

func TestAdminAuditReadCommitUncertaintyHTTPS(t *testing.T) {
	for _, preferred := range []bool{false, true} {
		for _, fault := range []struct {
			name      string
			mode      commitfault.Mode
			committed int
		}{
			{"commit_not_forwarded", commitfault.DropBeforeCommit, 0},
			{"actual_server_ack_suppressed", commitfault.DropCommitAcknowledgement, 1},
		} {
			name := "standard/" + fault.name
			if preferred {
				name = "assistant/" + fault.name
			}
			t.Run(name, func(t *testing.T) {
				f := newAuditReadFixture(t)
				handler, ok := f.server.Config.Handler.(*transport.Handler)
				if !ok {
					t.Fatal("actual identity handler required")
				}
				forwarding, proxy := auditReadForwarding(t, f.service)
				handler.Service = forwarding
				defer func() { handler.Service = f.service }()
				if err := proxy.Arm(fault.mode); err != nil {
					t.Fatal(err)
				}
				var headers map[string]string
				if preferred {
					headers = map[string]string{"X-Ownsolid-Audit-Actor": "true"}
				}
				path := auditReadPath + "?request_id=" + f.seed
				out, err := requestAuditRead(t.Context(), f.admin, path, headers)
				if err != nil || out.status != http.StatusConflict || out.headers.Get("Retry-After") != "" || out.body["code"] != "STORAGE_COMMIT_OUTCOME_UNKNOWN" || out.body["request_id"] != out.headers.Get("X-Request-ID") || out.body["data"] != nil {
					t.Fatal("uncertain audit read exposed a page or lost its safe recovery outcome", err)
				}
				select {
				case observed := <-proxy.Outcomes():
					if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != (fault.committed == 1) || observed.ServerOK != (fault.committed == 1) {
						t.Fatal("actual audit read commit fault was not established")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("actual audit read commit observation missing")
				}
				if proxy.Commits() != 1 || f.count(t) != fault.committed {
					t.Fatal("uncertain read was resent or disagreed with its durable access outcome")
				}
				retry, err := requestAuditRead(t.Context(), f.admin, path, headers)
				if err != nil || retry.status != 200 || f.count(t) != fault.committed+1 || proxy.Commits() != 2 || retry.headers.Get("X-Request-ID") == out.headers.Get("X-Request-ID") {
					t.Fatal("explicit audit read retry did not create its own access event", err)
				}
			})
		}
	}
}

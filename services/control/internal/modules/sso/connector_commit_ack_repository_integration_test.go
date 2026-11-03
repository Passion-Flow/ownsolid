//go:build repository_integration

package sso

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

type connectorWireFault interface {
	Arm(commitfault.Mode) error
	Commits() int32
	Outcomes() <-chan commitfault.Observation
	Close(context.Context) error
}

func connectorFaultStore(t *testing.T, f sourceRepositoryFixture) (transaction.Store, connectorWireFault) {
	t.Helper()
	closeProxy := func(proxy connectorWireFault) {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if e := proxy.Close(ctx); e != nil {
				t.Error("bounded actual wire proxy cleanup", e)
			}
		})
	}
	if f.f.Store.Dialect() == transaction.PostgreSQL {
		configuration := f.f.PostgreSQL.Config()
		if len(configuration.ConnConfig.Fallbacks) != 0 || configuration.ConnConfig.Host != "127.0.0.1" {
			t.Fatal("only the exact isolated loopback PG fixture is allowed; never change transport")
		}
		proxy, e := commitfault.NewPostgreSQL(configuration.ConnConfig.Host, configuration.ConnConfig.Port, configuration.ConnConfig.TLSConfig, os.Getenv("PROJECT_ONE_TEST_PG_TLS_PROXY_CERTIFICATE_FILE"), os.Getenv("PROJECT_ONE_TEST_PG_TLS_PROXY_PRIVATE_KEY_FILE"))
		if e != nil {
			t.Fatal(e)
		}
		closeProxy(proxy)
		proxied := configuration.Copy()
		proxied.ConnConfig.Port, proxied.MaxConns = proxy.Port(), 2
		pool, e := pgxpool.NewWithConfig(f.ctx, proxied)
		if e != nil {
			t.Fatal("actual isolated PostgreSQL forwarding pool", e)
		}
		t.Cleanup(pool.Close)
		if e = pool.Ping(f.ctx); e != nil {
			t.Fatal("actual PostgreSQL forwarding startup", e)
		}
		store, e := transaction.FromPostgreSQL(pool)
		if e != nil {
			t.Fatal(e)
		}
		return store, proxy
	}
	if f.f.Store.Dialect() != transaction.MySQL {
		t.Fatal("explicit actual paired engine required")
	}
	configuration, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
	if e != nil {
		t.Fatal("actual private MySQL forwarding configuration", e)
	}
	proxy, e := commitfault.NewMySQL(configuration)
	if e != nil {
		t.Fatal(e)
	}
	closeProxy(proxy)
	configuration.Port = proxy.Port()
	configuration.Policy.MaximumOpenConnections, configuration.Policy.MaximumIdleConnections = 2, 0
	kernel, observation, e := mysqlstore.Open(f.ctx, configuration)
	if e != nil || !observation.TLSAuthenticated {
		t.Fatal("actual verified TLS MySQL forwarding connector required", e)
	}
	t.Cleanup(func() {
		if e := kernel.Close(); e != nil {
			t.Error("actual MySQL forwarding pool cleanup", e)
		}
	})
	store, e := transaction.FromMySQL(kernel)
	if e != nil {
		t.Fatal(e)
	}
	return store, proxy
}

func TestActualSSOConnectorCommitAcknowledgementLoss(t *testing.T) {
	for _, operation := range []string{"create", "disable"} {
		t.Run(operation, func(t *testing.T) {
			for _, fault := range []struct {
				name      string
				mode      commitfault.Mode
				committed bool
			}{
				{"normal_acknowledged_commit", commitfault.Pass, true},
				{"commit_not_forwarded", commitfault.DropBeforeCommit, false},
				{"actual_server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true},
			} {
				t.Run(fault.name, func(t *testing.T) {
					f := catalogRepositoryFixture(t)
					a := catalogAuth(t, f, f.f.User, "admin", nil)
					input, key := connectorInput(f), uuid.NewString()
					target := input.ID
					update := UpdateConnector{Revision: 1, Name: "Actual wire disable \u4e16\u754c", Reason: "Synthetic controlled network fault"}
					var sourced identity.Auth
					var flow identity.SessionSource
					if operation == "disable" {
						target = f.source.ConnectorID
						sourced = catalogAuth(t, f, f.f.User, "admin", &f.source)
						flow = connectorFlow(t, f, target, "portal", "pending")
					}
					mutate := func() (Connector, error) {
						if operation == "create" {
							return f.s.CreateConnector(f.ctx, a, input, key, uuid.NewString())
						}
						return f.s.UpdateConnector(f.ctx, a, target, update, key, uuid.NewString())
					}
					store, proxy := connectorFaultStore(t, f)
					originalStore, originalDB := f.accounts.Storage, f.accounts.DB
					defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
					f.accounts.Storage, f.accounts.DB = store, nil
					if fault.mode != commitfault.Pass {
						if e := proxy.Arm(fault.mode); e != nil {
							t.Fatal(e)
						}
					}
					out, e := mutate()
					f.accounts.Storage, f.accounts.DB = originalStore, originalDB
					if fault.mode == commitfault.Pass {
						if e != nil || out.ID != target {
							t.Fatal("normal actual acknowledged connector write", e)
						}
					} else {
						if e == nil || e.Error() != string(transaction.CommitUnknown) || out != (Connector{}) {
							t.Fatal("actual lost commit acknowledgement must return zero unknown result", e)
						}
						select {
						case observed := <-proxy.Outcomes():
							if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
								t.Fatal("actual wire observation does not establish selected fault", observed)
							}
						case <-f.ctx.Done():
							t.Fatal("actual commit wire observation missing")
						}
					}
					want := [3]int{}
					if fault.committed {
						want = [3]int{1, 1, 1}
					} else if operation == "disable" {
						want[0] = 1
					}
					if counts := connectorCounts(t, f, target, key); counts != want || proxy.Commits() != 1 {
						t.Fatal("independent actual domain/intent/audit readback or automatic resend", counts, proxy.Commits())
					}
					if operation == "disable" {
						stored, e := selectConnectorWithin(f.ctx, f.f.Store, f.f.Tenant, target)
						version := int64(1)
						if fault.committed {
							version = 2
							if stored.Enabled || stored.Name != update.Name {
								t.Fatal("committed disable missing from independent database")
							}
						}
						if e != nil || stored.Revision != version || stored.AuthVersion != version || !fault.committed && !stored.Enabled {
							t.Fatal("actual disabled revision/auth-version outcome", e)
						}
						assertConnectorAuth(t, f, sourced, !fault.committed)
						var state string
						var erased bool
						if e := f.f.Store.QueryRow(f.ctx, f.f.Statement(`SELECT state,verifier_ciphertext IS NULL FROM sso_flows WHERE id=$1`, `SELECT state,verifier_ciphertext IS NULL FROM sso_flows WHERE id=?`, flow.FlowID)).Scan(&state, &erased); e != nil || fault.committed && (state != "failed" || !erased) || !fault.committed && (state != "pending" || erased) {
							t.Fatal("flow verifier invalidation did not share actual commit", e)
						}
					}
					// This explicit identical-key retry is the operator/user decision.
					// Absence alone is never presented as rollback proof by the product.
					retried, e := mutate()
					if e != nil || retried.ID != target || connectorCounts(t, f, target, key) != ([3]int{1, 1, 1}) {
						t.Fatal("explicit identical intent recovery duplicated or lost state", e)
					}
					if fault.committed && fault.mode == commitfault.Pass && !sameConnector(out, retried) || operation == "disable" && (retried.Revision != 2 || retried.AuthVersion != 2) || operation == "create" && (retried.Revision != 1 || retried.AuthVersion != 1) {
						t.Fatal("explicit recovery advanced an accepted revision twice")
					}
					assertConnectorAuth(t, f, a, true)
					if operation == "disable" {
						assertConnectorAuth(t, f, sourced, false)
					}
					t.Logf("actual engine=%s operation=%s fault_mode=%d initial_commit_commands=1 atomic_resource_intent_audit=1 explicit_identical_retry=1", f.f.Store.Dialect(), operation, fault.mode)
				})
			}
		})
	}
}

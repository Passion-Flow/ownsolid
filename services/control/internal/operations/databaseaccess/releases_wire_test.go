//go:build repository_integration

package databaseaccess

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

func TestM17GuardedMigrationRetainsActualTLSCommitOutcome(t *testing.T) {
	for _, fault := range []struct {
		name      string
		mode      commitfault.Mode
		committed bool
	}{
		{"acknowledged", commitfault.Pass, true},
		{"not-forwarded", commitfault.DropBeforeCommit, false},
		{"acknowledgement-suppressed", commitfault.DropCommitAcknowledgement, true},
	} {
		t.Run(fault.name, func(t *testing.T) {
			f := newActualFixture(t, "portal")
			ctx := t.Context()
			c := f.service.DB.Config().Copy()
			if c.ConnConfig.TLSConfig == nil || c.ConnConfig.TLSConfig.InsecureSkipVerify || len(c.ConnConfig.Fallbacks) != 0 {
				t.Fatal("verified exact TLS required")
			}
			proxy, err := commitfault.NewPostgreSQL(c.ConnConfig.Host, c.ConnConfig.Port, c.ConnConfig.TLSConfig, os.Getenv("PROJECT_ONE_TEST_PG_TLS_PROXY_CERTIFICATE_FILE"), os.Getenv("PROJECT_ONE_TEST_PG_TLS_PROXY_PRIVATE_KEY_FILE"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := proxy.Close(cleanup); err != nil {
					t.Error(err)
				}
			})
			c.ConnConfig.Port = proxy.Port()
			c.MaxConns = 2
			c.MinConns = 0
			p, err := pgxpool.NewWithConfig(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			lease, err := p.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			if _, err = f.lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); err != nil {
				t.Fatal(err)
			}
			var held bool
			if err = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&held); err != nil || !held {
				t.Fatal("original exclusive installation authority", err)
			}
			defer lease.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", database.InstallationLockID)
			input := f.input
			input.connection = input.connection.Copy()
			input.connection.ConnConfig.Port = proxy.Port()
			profile := "releases"
			input.configuration.Server.ApplicationProfile = &profile
			if fault.mode != commitfault.Pass {
				if err = proxy.Arm(fault.mode); err != nil {
					t.Fatal(err)
				}
			}
			err = MigrateWithRuntimeContext(ctx, p, lease, input)
			if fault.mode == commitfault.Pass {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || err.Error() != string(transaction.CommitUnknown) {
					t.Fatal("guarded command erased unknown COMMIT", err)
				}
				select {
				case observed := <-proxy.Outcomes():
					if observed.Forwarded != fault.committed || observed.ServerOK != fault.committed || !observed.Suppressed {
						t.Fatal("actual wire outcome mismatch")
					}
				case <-ctx.Done():
					t.Fatal("missing exact wire observation")
				}
			}
			if proxy.Commits() != 1 {
				t.Fatal("automatic migration replay")
			}
			for i := 0; i < 2; i++ {
				expected := "portal"
				if fault.committed {
					expected = "releases"
				}
				if err = database.Check(ctx, f.service.DB, expected); err != nil {
					t.Fatal("independent original ledger read", err)
				}
			}
		})
	}
}

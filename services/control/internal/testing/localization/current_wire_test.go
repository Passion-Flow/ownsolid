//go:build repository_integration

package localization_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
	"time"
)

func localeWire(t *testing.T, s *identity.Service) (*pgxpool.Pool, *commitfault.PlaintextPostgreSQL) {
	t.Helper()
	c := s.DB.Config().Copy()
	if c.ConnConfig.TLSConfig == nil || c.ConnConfig.TLSConfig.InsecureSkipVerify || len(c.ConnConfig.Fallbacks) != 0 {
		t.Fatal("exact verified TLS required")
	}
	proxy, e := commitfault.NewPostgreSQL(c.ConnConfig.Host, c.ConnConfig.Port, c.ConnConfig.TLSConfig, os.Getenv("PROJECT_ONE_TEST_PG_TLS_PROXY_CERTIFICATE_FILE"), os.Getenv("PROJECT_ONE_TEST_PG_TLS_PROXY_PRIVATE_KEY_FILE"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := proxy.Close(ctx); e != nil {
			t.Error(e)
		}
	})
	c.ConnConfig.Port = proxy.Port()
	c.MaxConns = 2
	c.MinConns = 0
	p, e := pgxpool.NewWithConfig(t.Context(), c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(p.Close)
	if e = p.Ping(t.Context()); e != nil {
		t.Fatal(e)
	}
	return p, proxy
}

func TestM16ActualLocaleAndRestoreCommitTLSOutcomes(t *testing.T) {
	for _, operation := range []string{"profile", "restore"} {
		for _, fault := range []struct {
			name      string
			mode      commitfault.Mode
			committed bool
		}{{"acknowledged", commitfault.Pass, true}, {"commit_not_forwarded", commitfault.DropBeforeCommit, false}, {"server_ack_suppressed", commitfault.DropCommitAcknowledgement, true}} {
			t.Run(operation+"/"+fault.name, func(t *testing.T) {
				s := fixture.New(t)
				policy := s.Config.Policy()
				policy.HTTP.ShutdownSeconds = 20
				s.Config.Runtime = &policy
				password := fixture.Bootstrap(t, s)
				ctx := t.Context()
				_, token, _, e := s.Login(ctx, "admin@example.invalid", password, "portal", "", uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				actor, e := s.Authenticate(ctx, token, "portal")
				if e != nil {
					t.Fatal(e)
				}
				p, proxy := localeWire(t, s)
				clone := *s
				clone.DB = nil
				clone.Storage, e = transaction.FromPostgreSQL(p)
				if e != nil {
					t.Fatal(e)
				}
				if fault.mode != commitfault.Pass {
					if e = proxy.Arm(fault.mode); e != nil {
						t.Fatal(e)
					}
				}
				var result identity.Me
				if operation == "profile" {
					result, e = clone.UpdateMe(ctx, actor, "Wire locale profile", "zh-CN", actor.Me.User.Version, uuid.NewString())
				} else {
					e = recovery.InvalidateRestored(ctx, &clone, "Synthetic current M16 restore wire outcome")
				}
				if fault.mode == commitfault.Pass {
					if e != nil {
						t.Fatal(e)
					}
					if operation == "profile" && result.User.Locale != "zh-CN" {
						t.Fatal("acknowledged result missing")
					}
				} else {
					if e == nil || e.Error() != string(transaction.CommitUnknown) || result.User.ID != "" || result.Workspace != nil {
						t.Fatal("unknown must return no DTO", e)
					}
					select {
					case observation := <-proxy.Outcomes():
						if observation.Mode != fault.mode || !observation.Suppressed || observation.Forwarded != fault.committed || observation.ServerOK != fault.committed {
							t.Fatal("wire mismatch")
						}
					case <-ctx.Done():
						t.Fatal("wire observation absent")
					}
				}
				if proxy.Commits() != 1 {
					t.Fatal("automatic mutation replay", proxy.Commits())
				}
				for read := 0; read < 2; read++ {
					current, e := s.Authenticate(ctx, token, "portal")
					if operation == "restore" && fault.committed {
						if e == nil {
							t.Fatal("restored old session survived")
						}
					} else {
						if e != nil {
							t.Fatal("authenticated direct read", e)
						}
						want := "en"
						version := actor.Me.User.Version
						if operation == "profile" && fault.committed {
							want = "zh-CN"
							version++
						}
						if current.Me.User.ID != actor.Me.User.ID || current.Me.User.Locale != want || current.Me.User.Version != version {
							t.Fatal("direct outcome read mismatch")
						}
					}
					var count int
					action := "account.updated"
					if operation == "restore" {
						action = "operator.auth_invalidated"
					}
					if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action=$1", action).Scan(&count); e != nil || count != map[bool]int{true: 1, false: 0}[fault.committed] {
						t.Fatal("audit outcome mismatch", count, e)
					}
				}
			})
		}
	}
}

func TestM16ActualForwardMigrationCommitTLSOutcomes(t *testing.T) {
	for _, fault := range []struct {
		name      string
		mode      commitfault.Mode
		committed bool
	}{{"acknowledged", commitfault.Pass, true}, {"commit_not_forwarded", commitfault.DropBeforeCommit, false}, {"server_ack_suppressed", commitfault.DropCommitAcknowledgement, true}} {
		t.Run(fault.name, func(t *testing.T) {
			s := fixture.New(t)
			fixture.Bootstrap(t, s)
			p, proxy := localeWire(t, s)
			if fault.mode != commitfault.Pass {
				if e := proxy.Arm(fault.mode); e != nil {
					t.Fatal(e)
				}
			}
			e := database.Migrate(t.Context(), p)
			if fault.mode == commitfault.Pass {
				if e != nil {
					t.Fatal(e)
				}
			} else {
				outcome, ok := transaction.CommitOutcome(e)
				if !ok || outcome != transaction.CommitUnknown {
					t.Fatal("schema unknown lost", e)
				}
				select {
				case observation := <-proxy.Outcomes():
					if observation.Forwarded != fault.committed || observation.ServerOK != fault.committed || !observation.Suppressed {
						t.Fatal("schema wire mismatch")
					}
				case <-t.Context().Done():
					t.Fatal("schema wire absent")
				}
			}
			if proxy.Commits() != 1 {
				t.Fatal("automatic schema replay")
			}
			for read := 0; read < 2; read++ {
				if e = database.Check(t.Context(), s.DB); e != nil {
					t.Fatal("direct ledger read", e)
				}
			}
		})
	}
}

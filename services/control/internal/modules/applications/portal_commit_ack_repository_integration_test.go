//go:build repository_integration

package applications_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/notifications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

type portalWireFault interface {
	Arm(commitfault.Mode) error
	Commits() int32
	Outcomes() <-chan commitfault.Observation
	Close(context.Context) error
}

func portalFaultConnection(t *testing.T, f portalStory) (transaction.Store, portalWireFault) {
	t.Helper()
	closeProxy := func(proxy portalWireFault) {
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

// The real transport determines the outcome first. This wrapper adds only the
// simultaneous cancellation observed by the outer request; it invents no commit.
type portalCancelledOutcomeStore struct{ transaction.Store }

func (s portalCancelledOutcomeStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	e := s.Store.Within(ctx, ro, fn)
	if kind, ok := transaction.CommitOutcome(e); ok && kind == transaction.CommitUnknown {
		return errors.Join(context.DeadlineExceeded, e)
	}
	return e
}

func portalRequestAudits(t *testing.T, g portalStory, request string) int {
	t.Helper()
	var count int
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT count(*) FROM audit_events WHERE request_id=$1", "SELECT COUNT(*) FROM audit_events WHERE request_id=?", request)).Scan(&count); e != nil {
		t.Fatal("independent audit readback", e)
	}
	return count
}

func portalTerminalNotice(t *testing.T, g portalStory) (*notifications.Service, string) {
	t.Helper()
	policy := g.owner.Config.Policy()
	policy.Portal.NotificationMaintenanceSeconds = 3600
	g.owner.Config.Runtime = &policy
	service := &notifications.Service{Identity: g.owner}
	if e := service.Start(g.ctx); e != nil {
		t.Fatal("actual notification owner startup", e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := service.Stop(ctx); e != nil {
			t.Error("notification worker did not join", e)
		}
	})
	workflow := g.draft(t, "Synthetic durable notice source")
	run := uuid.NewString()
	var authority, membership string
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT execution_authority::text FROM workflows WHERE id=$1", "SELECT execution_authority FROM workflows WHERE id=?", workflow.ID)).Scan(&authority); e != nil {
		t.Fatal(e)
	}
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", "SELECT authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", g.f.Tenant, g.f.Workspace, g.f.User)).Scan(&membership); e != nil {
		t.Fatal(e)
	}
	g.exec(t, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at) SELECT $1,tenant_id,workspace_id,id,$2,revision,$3,$4,'portal',draft_graph,draft_sha256,'pending','Synthetic notice question',clock_timestamp()+interval '1 minute' FROM workflows WHERE id=$5`,
		`INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at) SELECT ?,tenant_id,workspace_id,id,?,revision,?,?,'portal',draft_graph,draft_sha256,'pending','Synthetic notice question',TIMESTAMPADD(MINUTE,1,UTC_TIMESTAMP(6)) FROM workflows WHERE id=?`, run, g.f.User, authority, membership, workflow.ID)
	// This is explicitly synthetic terminal input to the real product trigger,
	// not proof of workflow execution or external notification delivery.
	g.exec(t, "UPDATE workflow_runs SET state='failed',error_code='SYNTHETIC_NOTICE_FAILURE',finished_at=clock_timestamp() WHERE id=$1", "UPDATE workflow_runs SET state='failed',error_code='SYNTHETIC_NOTICE_FAILURE',finished_at=UTC_TIMESTAMP(6) WHERE id=?", run)
	var notice string
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT id::text FROM terminal_notifications WHERE run_id=$1", "SELECT id FROM terminal_notifications WHERE run_id=?", run)).Scan(&notice); e != nil {
		t.Fatal("actual terminal trigger required", e)
	}
	return service, notice
}

func TestActualPairedPortalCommitAcknowledgementLoss(t *testing.T) {
	for _, operation := range []string{"preference", "notification"} {
		for _, fault := range []struct {
			name      string
			mode      commitfault.Mode
			committed bool
		}{
			{"acknowledged", commitfault.Pass, true},
			{"not_forwarded", commitfault.DropBeforeCommit, false},
			{"ack_suppressed", commitfault.DropCommitAcknowledgement, true},
		} {
			t.Run(operation+"_"+fault.name, func(t *testing.T) {
				g := portalSetup(t)
				request := uuid.NewString()
				var directNotice *notifications.Service
				var notice string
				if operation == "notification" {
					directNotice, notice = portalTerminalNotice(t, g)
				}
				store, proxy := portalFaultConnection(t, g)
				_, prefs := g.wrapped(portalCancelledOutcomeStore{store})
				// Preference and notification use one selected identity store. The
				// notification service is started before arming so cleanup cannot consume
				// the fault; only its user mutation is allowed through the armed transport.
				var proxiedNotice *notifications.Service
				if operation == "notification" {
					proxiedNotice = &notifications.Service{Identity: prefs.Identity}
					if e := proxiedNotice.Start(g.ctx); e != nil {
						t.Fatal(e)
					}
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
						defer cancel()
						if e := proxiedNotice.Stop(ctx); e != nil {
							t.Error(e)
						}
					})
				}
				beforeCommits := proxy.Commits()
				if fault.mode != commitfault.Pass {
					if e := proxy.Arm(fault.mode); e != nil {
						t.Fatal(e)
					}
				}
				var mutationError error
				if operation == "preference" {
					value, e := prefs.Update(g.ctx, g.a, preferences.UpdateRequest{Theme: preferences.DarkTheme, ExpectedRevision: 1}, request)
					mutationError = e
					if fault.mode == commitfault.Pass {
						if e != nil || value.Theme != preferences.DarkTheme || value.Revision != 2 {
							t.Fatal("acknowledged preference", e)
						}
					} else if value != (preferences.Preference{}) {
						t.Fatal("uncertain preference exposed result")
					}
				} else {
					value, e := proxiedNotice.Read(g.ctx, g.a, notice, request)
					mutationError = e
					if fault.mode == commitfault.Pass {
						if e != nil || value.ReadAt == nil {
							t.Fatal("acknowledged notification", e)
						}
					} else if !reflect.DeepEqual(value, notifications.Notification{}) {
						t.Fatal("uncertain notice exposed result")
					}
				}
				if fault.mode != commitfault.Pass {
					if mutationError == nil || mutationError.Error() != string(transaction.CommitUnknown) || errors.Is(mutationError, context.DeadlineExceeded) {
						t.Fatal("uncertain commit downgraded to cancellation", mutationError)
					}
					select {
					case observed := <-proxy.Outcomes():
						if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
							t.Fatal("actual wire outcome differs", observed)
						}
					case <-g.ctx.Done():
						t.Fatal("wire outcome missing")
					}
				}
				wantAudits := 0
				if fault.committed {
					wantAudits = 1
				}
				if portalRequestAudits(t, g, request) != wantAudits || proxy.Commits()-beforeCommits != 1 {
					t.Fatal("atomic audit or single COMMIT violated")
				}
				// Two explicit reads through independent direct connections never resend
				// the unknown write and never label absent durable state a proved rollback.
				for range 2 {
					if operation == "preference" {
						got, e := g.prefs.Get(g.ctx, g.a)
						want := int64(1)
						theme := preferences.LightTheme
						if fault.committed {
							want = 2
							theme = preferences.DarkTheme
						}
						if e != nil || got.Revision != want || got.Theme != theme {
							t.Fatal("durable preference readback", e)
						}
					} else {
						got, e := directNotice.Get(g.ctx, g.a, notice)
						if e != nil || (got.ReadAt != nil) != fault.committed {
							t.Fatal("durable notice readback", e)
						}
					}
				}
				if fault.committed {
					recoveryRequest := uuid.NewString()
					if operation == "preference" {
						got, e := g.prefs.Update(g.ctx, g.a, preferences.UpdateRequest{Theme: preferences.DarkTheme, ExpectedRevision: 1}, recoveryRequest)
						if e == nil || e.Error() != "PREFERENCE_REVISION_CONFLICT" || got != (preferences.Preference{}) {
							t.Fatal("stale recovery revision changed preference", e)
						}
					} else {
						if got, e := directNotice.Read(g.ctx, g.a, notice, recoveryRequest); e != nil || got.ReadAt == nil {
							t.Fatal("idempotent explicit read reconciliation", e)
						}
					}
					if portalRequestAudits(t, g, recoveryRequest) != 0 {
						t.Fatal("explicit reconciliation duplicated audit")
					}
				}
				// Reconciled state is still subject to current user authority.
				g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
				if operation == "preference" {
					got, e := g.prefs.Get(g.ctx, g.a)
					if e == nil || got != (preferences.Preference{}) {
						t.Fatal("stale actor read durable preference")
					}
				} else {
					got, e := directNotice.Get(g.ctx, g.a, notice)
					if e == nil || !reflect.DeepEqual(got, notifications.Notification{}) {
						t.Fatal("stale actor read durable notice")
					}
				}
				if portalRequestAudits(t, g, request) != wantAudits || proxy.Commits()-beforeCommits != 1 {
					t.Fatal("readback or revoked actor replayed mutation")
				}
				t.Logf("engine=%s operation=%s mode=%d commit_commands=1 initial_audits=%d direct_reconciliation_reads=2", g.f.Store.Dialect(), operation, fault.mode, wantAudits)
			})
		}
	}
}

// This explicitly identified adapter fault changes only the affected-row report
// after the real audit INSERT. The actual transaction must roll back all rows.
type portalAuditCountStore struct{ transaction.Store }
type portalAuditCountExecutor struct {
	transaction.Executor
	writes     int
	auditWrite int
}

func (s portalAuditCountStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		ordinal := 1 // PostgreSQL performs UPDATE RETURNING through QueryRow.
		if q.Dialect() == transaction.MySQL {
			ordinal = 2
		}
		return fn(ctx, &portalAuditCountExecutor{Executor: q, auditWrite: ordinal})
	})
}
func (q *portalAuditCountExecutor) Exec(ctx context.Context, statement transaction.Statement) (int64, error) {
	count, e := q.Executor.Exec(ctx, statement)
	q.writes++
	if e == nil && q.writes == q.auditWrite {
		return 0, nil
	}
	return count, e
}
func TestActualPairedPreferenceAuditCountRollback(t *testing.T) {
	g := portalSetup(t)
	before, e := g.prefs.Get(g.ctx, g.a)
	if e != nil {
		t.Fatal(e)
	}
	request := uuid.NewString()
	_, fault := g.wrapped(portalAuditCountStore{g.f.Store})
	got, e := fault.Update(g.ctx, g.a, preferences.UpdateRequest{Theme: preferences.DarkTheme, ExpectedRevision: before.Revision}, request)
	if e == nil || e.Error() != string(transaction.Unavailable) || got != (preferences.Preference{}) {
		t.Fatal("non-single audit acknowledgement accepted", e)
	}
	after, e := g.prefs.Get(g.ctx, g.a)
	if e != nil || after != before || portalRequestAudits(t, g, request) != 0 {
		t.Fatal("audit count refusal did not roll back preference and audit", e)
	}
	// A fresh acknowledged operation still succeeds after the known rollback.
	got, e = g.prefs.Update(g.ctx, g.a, preferences.UpdateRequest{Theme: preferences.DarkTheme, ExpectedRevision: before.Revision}, request)
	if e != nil || got.Revision != before.Revision+1 || portalRequestAudits(t, g, request) != 1 {
		t.Fatal("ordinary update after known rollback", e)
	}
}

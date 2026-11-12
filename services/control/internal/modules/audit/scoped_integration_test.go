//go:build repository_integration

package audit_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type scopedFixture struct {
	db                                                                  *pgxpool.Pool
	store                                                               transaction.Store
	event                                                               audit.ScopedEvent
	defaultWorkspace, foreignTenant, foreignWorkspace, member, disabled string
}

func newScopedFixture(t *testing.T) scopedFixture {
	t.Helper()
	if os.Getenv("PROJECT_ONE_TEST_DSN") == "" && os.Getenv("PROJECT_ONE_TEST_DSN_FILE") == "" {
		t.Fatal("an explicit private PostgreSQL fixture input is required")
	}
	// Use the production migration gate and its current default local identity.
	db := newAuditFixture(t, 0)
	f := scopedFixture{db: db, event: scopedTestEvent(), defaultWorkspace: uuid.NewString(),
		foreignTenant: uuid.NewString(), foreignWorkspace: uuid.NewString(), member: uuid.NewString(), disabled: uuid.NewString()}
	f.event.ActorUserID, f.event.TenantID, f.event.WorkspaceID = uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.event.SiteID, f.event.CellID, f.event.RequestID = "default", "default", uuid.NewString()
	f.seed(t, `INSERT INTO sites(id,display_name) VALUES('audit-site','Audit'),('foreign-site','Foreign')`)
	f.seed(t, `INSERT INTO cells(id,site_id,display_name,is_local) VALUES('audit-cell','audit-site','Audit',false),('foreign-cell','foreign-site','Foreign',false)`)
	f.seed(t, `INSERT INTO tenants(id,name,status) VALUES($1,'Local','active'),($2,'Foreign','active')`, f.event.TenantID, f.foreignTenant)
	f.seed(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Default','active'),($3,$2,'Exact','disabled'),($4,$5,'Foreign','active')`,
		f.defaultWorkspace, f.event.TenantID, f.event.WorkspaceID, f.foreignWorkspace, f.foreignTenant)
	f.seed(t, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES
        ($1,'scoped-admin@example.invalid','scoped-admin@example.invalid','Admin','en','active',true),
        ($2,'scoped-member@example.invalid','scoped-member@example.invalid','Member','en','active',false),
        ($3,'scoped-disabled@example.invalid','scoped-disabled@example.invalid','Disabled','en','disabled',true)`, f.event.ActorUserID, f.member, f.disabled)
	f.seed(t, `UPDATE installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1`, f.event.TenantID, f.defaultWorkspace, f.event.ActorUserID)
	var err error
	f.store, err = transaction.FromPostgreSQL(db)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f scopedFixture) seed(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}
func (f scopedFixture) append(t *testing.T, event audit.ScopedEvent) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	return f.store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		return audit.WriteScopedWithin(ctx, q, event)
	})
}
func (f scopedFixture) count(t *testing.T, request string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE request_id=$1`, request).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func (f scopedFixture) head(t *testing.T) string {
	t.Helper()
	var value string
	if err := f.db.QueryRow(t.Context(), `SELECT chain_position::text || ':' || encode(hash,'hex') FROM audit_chain_head`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestScopedAuditPostgreSQL(t *testing.T) {
	f := newScopedFixture(t)
	t.Run("exact_scope_and_minimal_payload", func(t *testing.T) {
		e := f.event
		e.TargetType, e.TargetID = "workspace", e.WorkspaceID
		if err := f.append(t, e); err != nil {
			t.Fatal(err)
		}
		var matches bool
		err := f.db.QueryRow(t.Context(), `SELECT actor_user_id=$2 AND tenant_id=$3 AND workspace_id=$4
            AND site_id=$5 AND cell_id=$6 AND audience='admin' AND action='audit.events_read' AND outcome='success'
            AND target_type='workspace' AND target_id=$4 AND reason=$7 AND subject_user_id IS NULL
            AND identifier_digest IS NULL AND metadata='{}'::jsonb AND chain_position>0 AND octet_length(hash)=32
            FROM audit_events WHERE request_id=$1`, e.RequestID, e.ActorUserID, e.TenantID, e.WorkspaceID, e.SiteID, e.CellID, e.Reason).Scan(&matches)
		if err != nil || !matches || f.count(t, e.RequestID) != 1 {
			t.Fatalf("exact scoped event: matches=%t error=%v", matches, err)
		}
		// Disabled workspace history and an Admin without membership are intentional.
		e.RequestID = uuid.NewString()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		if err := f.store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			return audit.WriteWithin(ctx, q, e.ActorUserID, "", "admin", "fixture.installation", "success", "installation", "", "", e.RequestID)
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.db.QueryRow(t.Context(), `SELECT workspace_id=$2 FROM audit_events WHERE request_id=$1`, e.RequestID, f.defaultWorkspace).Scan(&matches); err != nil || !matches {
			t.Fatal("installation writer semantics changed", err)
		}
	})
	t.Run("missing_and_foreign_attribution", func(t *testing.T) {
		cases := []struct {
			name string
			edit func(*audit.ScopedEvent)
		}{
			{"missing actor", func(e *audit.ScopedEvent) { e.ActorUserID = uuid.NewString() }},
			{"non Admin", func(e *audit.ScopedEvent) { e.ActorUserID = f.member }},
			{"inactive Admin", func(e *audit.ScopedEvent) { e.ActorUserID = f.disabled }},
			{"missing tenant", func(e *audit.ScopedEvent) { e.TenantID = uuid.NewString() }},
			{"foreign tenant", func(e *audit.ScopedEvent) { e.TenantID = f.foreignTenant }},
			{"missing workspace", func(e *audit.ScopedEvent) { e.WorkspaceID = uuid.NewString() }},
			{"foreign workspace", func(e *audit.ScopedEvent) { e.WorkspaceID = f.foreignWorkspace }},
			{"foreign tenant workspace", func(e *audit.ScopedEvent) { e.TenantID = f.foreignTenant; e.WorkspaceID = f.foreignWorkspace }},
			{"missing site", func(e *audit.ScopedEvent) { e.SiteID = "missing-site" }},
			{"foreign site", func(e *audit.ScopedEvent) { e.SiteID = "foreign-site" }},
			{"missing cell", func(e *audit.ScopedEvent) { e.CellID = "missing-cell" }},
			{"foreign cell", func(e *audit.ScopedEvent) { e.CellID = "foreign-cell" }},
			{"foreign site cell", func(e *audit.ScopedEvent) { e.SiteID = "foreign-site"; e.CellID = "foreign-cell" }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				e := f.event
				e.RequestID = uuid.NewString()
				tc.edit(&e)
				head := f.head(t)
				if err := f.append(t, e); !errors.Is(err, audit.ErrScopeDenied) {
					t.Fatalf("scope was not uniformly denied: %v", err)
				}
				if f.count(t, e.RequestID) != 0 || f.head(t) != head {
					t.Fatal("denial changed the chain")
				}
			})
		}
		for _, query := range []string{`UPDATE installation_state SET bootstrap_complete=false,tenant_id=NULL,default_workspace_id=NULL,primary_admin_user_id=NULL,bootstrapped_at=NULL WHERE id=1`, `UPDATE cells SET is_local=false WHERE is_local`} {
			head := f.head(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			err := f.store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
				if _, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, query)); err != nil {
					return err
				}
				return audit.WriteScopedWithin(ctx, q, f.event)
			})
			cancel()
			if !errors.Is(err, audit.ErrScopeDenied) || f.head(t) != head {
				t.Fatalf("missing installation/local identity accepted: %v", err)
			}
		}
	})
	t.Run("least_privilege_refusal_and_repair", func(t *testing.T) {
		var schema string
		if err := f.db.QueryRow(t.Context(), `SELECT current_schema()`).Scan(&schema); err != nil {
			t.Fatal(err)
		}
		name := "audit_scoped_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		role := pgx.Identifier{name}.Sanitize()
		f.seed(t, "CREATE ROLE "+role+" NOLOGIN")
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := f.db.Exec(ctx, "DROP OWNED BY "+role+"; DROP ROLE "+role); err != nil {
				t.Error("owned role cleanup", err)
			}
		})
		for _, statement := range []string{
			"GRANT USAGE ON SCHEMA " + pgx.Identifier{schema}.Sanitize() + " TO " + role,
			"GRANT SELECT(id,status,is_platform_admin) ON users TO " + role,
			"GRANT SELECT(id,tenant_id) ON workspaces TO " + role,
			"GRANT SELECT(id,bootstrap_complete,tenant_id) ON installation_state TO " + role,
			"GRANT SELECT(id,site_id,is_local) ON cells TO " + role,
			"GRANT INSERT ON audit_events TO " + role,
		} {
			f.seed(t, statement)
		}
		e := f.event
		e.RequestID = uuid.NewString()
		appendAsRole := func() error {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			tx, err := f.db.Begin(ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback(ctx)
			if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+role); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "SAVEPOINT private_head_probe"); err != nil {
				return err
			}
			var position int64
			denied := tx.QueryRow(ctx, `SELECT chain_position FROM audit_chain_head`).Scan(&position)
			var pgerr *pgconn.PgError
			if !errors.As(denied, &pgerr) || pgerr.Code != "42501" {
				t.Fatalf("private head readable or wrong refusal: %v", denied)
			}
			if _, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT private_head_probe"); err != nil {
				return err
			}
			if err = audit.WriteScopedWithin(ctx, transaction.PostgreSQLTransaction(tx), e); err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
		if err := appendAsRole(); err != nil {
			t.Fatal("least privilege append", err)
		}
		e.RequestID = uuid.NewString()
		head := f.head(t)
		f.seed(t, "REVOKE INSERT ON audit_events FROM "+role)
		if err := appendAsRole(); err == nil || f.count(t, e.RequestID) != 0 || f.head(t) != head {
			t.Fatal("refused append released success or changed chain", err)
		}
		f.seed(t, "GRANT INSERT ON audit_events TO "+role)
		if err := appendAsRole(); err != nil || f.count(t, e.RequestID) != 1 {
			t.Fatal("explicit repaired retry", err)
		}
	})
	t.Run("rollback_and_cancel_before_commit", func(t *testing.T) {
		for _, cancelAfter := range []bool{false, true} {
			e := f.event
			e.RequestID = uuid.NewString()
			head := f.head(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			rollback := errors.New("fixture rollback")
			err := f.store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
				if err := audit.WriteScopedWithin(ctx, q, e); err != nil {
					return err
				}
				if cancelAfter {
					cancel()
					return nil
				}
				return rollback
			})
			cancel()
			if err == nil || f.count(t, e.RequestID) != 0 || f.head(t) != head {
				t.Fatalf("rollback/cancel persisted an event: %v", err)
			}
		}
	})
	t.Run("cancel_blocked_append", func(t *testing.T) { scopedBlockedCancellation(t, f) })
	t.Run("concurrent_committed_chain", func(t *testing.T) {
		const writers = 12
		start := make(chan struct{})
		failures := make(chan error, writers)
		var wg sync.WaitGroup
		before := f.head(t)
		var countBefore int
		if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM audit_events`).Scan(&countBefore); err != nil {
			t.Fatal(err)
		}
		for range writers {
			wg.Go(func() { <-start; e := f.event; e.RequestID = uuid.NewString(); failures <- f.append(t, e) })
		}
		close(start)
		wg.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		var broken int
		err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM (
            SELECT chain_position,prev_hash,lag(hash) OVER(ORDER BY chain_position) AS prior,
                lag(chain_position) OVER(ORDER BY chain_position) AS prior_position FROM audit_events
        ) e WHERE prior_position IS NOT NULL AND (chain_position<>prior_position+1 OR prev_hash<>prior)`).Scan(&broken)
		if err != nil || broken != 0 || f.head(t) == before {
			t.Fatalf("concurrent chain continuity: %d %v", broken, err)
		}
		var countAfter int
		if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM audit_events`).Scan(&countAfter); err != nil || countAfter-countBefore != writers {
			t.Fatalf("concurrent append count: %d error=%v", countAfter-countBefore, err)
		}
	})
	t.Run("real_commit_uncertainty", func(t *testing.T) { scopedCommitUncertainty(t, f) })
	t.Run("operator_seeded_nondefault_cell_at_empty_genesis", func(t *testing.T) {
		// The installation gate intentionally supports default/default until
		// ARC-202. This disposable, empty-genesis SQL fixture tests attribution
		// against non-default database metadata, not a non-default installation.
		other := newScopedFixture(t)
		var empty bool
		err := other.db.QueryRow(t.Context(), `SELECT chain_position=0 AND hash=decode(repeat('0',64),'hex')
            AND NOT EXISTS(SELECT 1 FROM audit_events) FROM audit_chain_head`).Scan(&empty)
		if err != nil || !empty {
			t.Fatal("non-default fixture must not rewrite retained history", err)
		}
		other.seed(t, `UPDATE cells SET is_local=false WHERE is_local`)
		other.seed(t, `UPDATE cells SET is_local=true WHERE id='audit-cell'`)
		other.seed(t, `UPDATE audit_chain_head SET site_id='audit-site',cell_id='audit-cell' WHERE chain_position=0`)
		e := other.event
		e.SiteID, e.CellID, e.RequestID = "audit-site", "audit-cell", uuid.NewString()
		if err := other.append(t, e); err != nil {
			t.Fatal("non-default SQL attribution", err)
		}
		var matches bool
		err = other.db.QueryRow(t.Context(), `SELECT site_id=$2 AND cell_id=$3 AND workspace_id=$4 FROM audit_events WHERE request_id=$1`, e.RequestID, e.SiteID, e.CellID, e.WorkspaceID).Scan(&matches)
		if err != nil || !matches {
			t.Fatal("writer substituted default attribution", err)
		}
		e.SiteID, e.CellID, e.RequestID = "default", "default", uuid.NewString()
		if err := other.append(t, e); !errors.Is(err, audit.ErrScopeDenied) || other.count(t, e.RequestID) != 0 {
			t.Fatal("nonlocal defaults were accepted", err)
		}
	})
}

func scopedBlockedCancellation(t *testing.T, f scopedFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	locker, err := f.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Rollback(ctx)
	if _, err = locker.Exec(ctx, `LOCK TABLE audit_events IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = tx.Rollback(cleanup)
	}()
	call, stop := context.WithCancel(ctx)
	defer stop()
	e := f.event
	e.RequestID = uuid.NewString()
	head := f.head(t)
	finished := make(chan error, 1)
	go func() { finished <- audit.WriteScopedWithin(call, transaction.PostgreSQLTransaction(tx), e) }()
	joined := false
	defer func() {
		stop()
		_ = locker.Rollback(ctx)
		if !joined {
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Error("cancelled append did not join")
			}
		}
	}()
	for {
		var waiting bool
		if err = f.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND NOT granted)`, tx.Conn().PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-finished:
			joined = true
			t.Fatalf("append did not block: %v", err)
		case <-ctx.Done():
			t.Fatal("append lock observation deadline")
		case <-time.After(10 * time.Millisecond):
		}
	}
	stop()
	select {
	case err := <-finished:
		joined = true
		if err == nil {
			t.Fatal("blocked cancellation succeeded")
		}
	case <-ctx.Done():
		t.Fatal("append cancellation deadline")
	}
	if f.count(t, e.RequestID) != 0 || f.head(t) != head {
		t.Fatal("cancelled append changed chain")
	}
}

func scopedCommitUncertainty(t *testing.T, f scopedFixture) {
	t.Helper()
	for _, tc := range []struct {
		name    string
		mode    commitfault.Mode
		durable int
	}{
		{"COMMIT_not_forwarded", commitfault.DropBeforeCommit, 0},
		{"server_ACK_suppressed", commitfault.DropCommitAcknowledgement, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			config := f.db.Config().Copy()
			if config.ConnConfig.TLSConfig != nil || len(config.ConnConfig.Fallbacks) != 0 {
				t.Fatal("this fixture requires its explicit isolated loopback plaintext transport")
			}
			proxy, err := commitfault.NewPlaintextPostgreSQL(config.ConnConfig.Host, config.ConnConfig.Port)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
				defer done()
				if err := proxy.Close(cleanup); err != nil {
					t.Error("wire proxy did not join", err)
				}
			}()
			config.ConnConfig.Port = proxy.Port()
			config.MaxConns = 1
			peer, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			if err = peer.Ping(ctx); err != nil {
				t.Fatal(err)
			}
			store, err := transaction.FromPostgreSQL(peer)
			if err != nil {
				t.Fatal(err)
			}
			if err = proxy.Arm(tc.mode); err != nil {
				t.Fatal(err)
			}
			e := f.event
			e.RequestID = uuid.NewString()
			calls := 0
			err = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
				calls++
				return audit.WriteScopedWithin(ctx, q, e)
			})
			var uncertain *transaction.Error
			if !errors.As(err, &uncertain) || uncertain.Kind != transaction.CommitUnknown || calls != 1 || proxy.Commits() != 1 {
				t.Fatalf("unknown commit was lost or retried: error=%v callbacks=%d commits=%d", err, calls, proxy.Commits())
			}
			select {
			case got := <-proxy.Outcomes():
				if got.Mode != tc.mode || !got.Suppressed || got.Forwarded != (tc.durable == 1) || got.ServerOK != (tc.durable == 1) {
					t.Fatal("wire observation disagrees with requested fault", got)
				}
			case <-ctx.Done():
				t.Fatal("wire outcome missing")
			}
			if f.count(t, e.RequestID) != tc.durable {
				t.Fatal("durable event count disagrees with actual wire outcome")
			}
			e.RequestID = uuid.NewString()
			if err = f.append(t, e); err != nil || f.count(t, e.RequestID) != 1 || proxy.Commits() != 1 {
				t.Fatal("explicit new read retry", err)
			}
		})
	}
}

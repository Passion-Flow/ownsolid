//go:build repository_integration

package notifications

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

type notificationStory struct {
	f                               repositoryfixture.Fixture
	ctx                             context.Context
	owner                           *identity.Service
	s                               *Service
	a                               identity.Auth
	workflow, authority, membership string
}

func notificationSetup(t *testing.T) notificationStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	t.Cleanup(cancel)
	owner := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		owner.Storage = nil
		owner.DB = f.PostgreSQL
	}
	policy := owner.Config.Policy()
	policy.Portal.NotificationRetentionDays = 1
	policy.Portal.NotificationMaintenanceBatch = 2
	policy.Portal.NotificationMaintenanceSeconds = 3600
	owner.Config.Runtime = &policy
	g := notificationStory{f: f, ctx: ctx, owner: owner, workflow: uuid.NewString(), authority: uuid.NewString()}
	g.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')", f.User, f.Tenant, f.Workspace)
	g.a = g.session(t, f.User, "portal")
	g.s = &Service{DB: f.PostgreSQL, Identity: owner}
	// The actual product schema/trigger is mandatory. No test-only table or fake
	// terminal producer can turn missing MySQL workflow migrations into support.
	if e := g.s.Start(ctx); e != nil {
		t.Fatal("ACTUAL_PRODUCT_NOTIFICATION_SCHEMA_AND_SELECTED_START_REQUIRED", e)
	}
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := g.s.Stop(stop); e != nil {
			t.Error(e)
		}
	})
	if e := f.Store.QueryRow(ctx, g.sql("SELECT authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", "SELECT authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", f.Tenant, f.Workspace, f.User)).Scan(&g.membership); e != nil {
		t.Fatal(e)
	}
	g.exec(t, `INSERT INTO workflows(id,tenant_id,workspace_id,name,name_key,execution_authority,draft_graph,draft_sha256,created_by) VALUES($1,$2,$3,'Synthetic notification provenance',$4,$5,'{}'::jsonb,$6,$7)`, `INSERT INTO workflows(id,tenant_id,workspace_id,name,name_key,execution_authority,draft_graph,draft_sha256,created_by) VALUES(?,?,?,'Synthetic notification provenance',?,?,CAST('{}' AS JSON),?,?)`, g.workflow, f.Tenant, f.Workspace, g.workflow, g.authority, strings.Repeat("a", 64), f.User)
	return g
}
func (g notificationStory) session(t *testing.T, id, audience string) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = g.owner.IssueSessionWithin(ctx, q, id, 1, audience, "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := g.owner.Authenticate(g.ctx, issued.Token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func (g notificationStory) sql(pg, my string, args ...any) transaction.Statement {
	return g.f.Statement(pg, my, args...)
}
func (g notificationStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.sql(pg, my, args...)); e != nil {
		t.Fatal("actual synthetic notification provenance fixture", e)
	}
}
func (g notificationStory) count(t *testing.T, pg, my string, args ...any) int {
	t.Helper()
	var n int
	if e := g.f.Store.QueryRow(g.ctx, g.sql(pg, my, args...)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func (g notificationStory) pending(t *testing.T, conversation bool) (string, *string) {
	t.Helper()
	id := uuid.NewString()
	var c, v *string
	var draft, turn *int
	if conversation {
		version, chat := uuid.NewString(), uuid.NewString()
		v = &version
		c = &chat
		one := 1
		turn = &one
		g.exec(t, `INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) VALUES($1,$2,$3,$4,1,1,'{}'::jsonb,$5,$6)`, `INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) VALUES(?,?,?,?,1,1,CAST('{}' AS JSON),?,?)`, version, g.f.Tenant, g.f.Workspace, g.workflow, strings.Repeat("a", 64), g.f.User)
		g.exec(t, `INSERT INTO workflow_conversations(id,tenant_id,workspace_id,workflow_id,version_id,owner_id) VALUES($1,$2,$3,$4,$5,$6)`, `INSERT INTO workflow_conversations(id,tenant_id,workspace_id,workflow_id,version_id,owner_id) VALUES(?,?,?,?,?,?)`, chat, g.f.Tenant, g.f.Workspace, g.workflow, version, g.f.User)
	} else {
		one := 1
		draft = &one
	}
	g.exec(t, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,conversation_id,version_id,turn_number,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'portal','{}'::jsonb,$12,'pending','PRIVATE_NOTICE_QUESTION',clock_timestamp()+interval '1 minute')`, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,conversation_id,version_id,turn_number,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,'portal',CAST('{}' AS JSON),?,'pending','PRIVATE_NOTICE_QUESTION',TIMESTAMPADD(MINUTE,1,UTC_TIMESTAMP(6)))`, id, g.f.Tenant, g.f.Workspace, g.workflow, g.f.User, c, v, turn, draft, g.authority, g.membership, strings.Repeat("a", 64))
	return id, c
}
func (g notificationStory) finish(t *testing.T, id, state string, expired bool) string {
	t.Helper()
	pg := `UPDATE workflow_runs SET state=$1::text,error_code=CASE WHEN $2::text='succeeded' THEN NULL ELSE 'SYNTHETIC_NOTICE_FAILURE' END,finished_at=clock_timestamp(),answer=CASE WHEN $3::text='succeeded' THEN 'PRIVATE_NOTICE_ANSWER' ELSE NULL END,evidence=CASE WHEN $4::text='succeeded' THEN '{}'::jsonb ELSE NULL END,alias_snapshot=CASE WHEN $5::text='succeeded' THEN '{}'::jsonb ELSE NULL END,usage=CASE WHEN $6::text='succeeded' THEN '{}'::jsonb ELSE NULL END WHERE id=$7`
	my := `UPDATE workflow_runs SET state=?,error_code=CASE WHEN ?='succeeded' THEN NULL ELSE 'SYNTHETIC_NOTICE_FAILURE' END,finished_at=UTC_TIMESTAMP(6),answer=CASE WHEN ?='succeeded' THEN 'PRIVATE_NOTICE_ANSWER' ELSE NULL END,evidence=CASE WHEN ?='succeeded' THEN CAST('{}' AS JSON) ELSE NULL END,alias_snapshot=CASE WHEN ?='succeeded' THEN CAST('{}' AS JSON) ELSE NULL END,usage=CASE WHEN ?='succeeded' THEN CAST('{}' AS JSON) ELSE NULL END WHERE id=?`
	if expired {
		pg = strings.Replace(pg, "finished_at=clock_timestamp()", "finished_at=clock_timestamp()-interval '2 days'", 1)
		my = strings.Replace(my, "finished_at=UTC_TIMESTAMP(6)", "finished_at=TIMESTAMPADD(DAY,-2,UTC_TIMESTAMP(6))", 1)
	}
	g.exec(t, pg, my, state, state, state, state, state, state, id)
	if expired {
		g.exec(t, "DELETE FROM terminal_notifications WHERE run_id=$1", "DELETE FROM terminal_notifications WHERE run_id=?", id)
		g.exec(t, `INSERT INTO terminal_notifications(id,tenant_id,workspace_id,workflow_id,run_id,recipient_id,outcome,error_code,occurred_at) SELECT $1,tenant_id,workspace_id,workflow_id,id,owner_id,state,error_code,finished_at FROM workflow_runs WHERE id=$2`, `INSERT INTO terminal_notifications(id,tenant_id,workspace_id,workflow_id,run_id,recipient_id,outcome,error_code,occurred_at) SELECT ?,tenant_id,workspace_id,workflow_id,id,owner_id,state,error_code,finished_at FROM workflow_runs WHERE id=?`, uuid.NewString(), id)
	}
	var notice string
	if e := g.f.Store.QueryRow(g.ctx, g.sql("SELECT id::text FROM terminal_notifications WHERE run_id=$1", "SELECT id FROM terminal_notifications WHERE run_id=?", id)).Scan(&notice); e != nil {
		t.Fatal("real terminal transition trigger required", e)
	}
	return notice
}
func (g notificationStory) auditFailure(t *testing.T, action string) func() {
	t.Helper()
	g.exec(t, "CREATE FUNCTION explicit_notification_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='"+action+"' THEN RAISE EXCEPTION 'explicit notification audit failure'; END IF; RETURN NEW; END;$$", "CREATE TRIGGER explicit_notification_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action='"+action+"' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='explicit notification audit failure'; END IF; END")
	if g.f.Store.Dialect() == transaction.PostgreSQL {
		g.exec(t, "CREATE TRIGGER explicit_notification_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION explicit_notification_audit_failure()", "")
	}
	dropped := false
	drop := func() {
		if !dropped {
			g.exec(t, "DROP TRIGGER explicit_notification_audit_failure ON audit_events", "DROP TRIGGER explicit_notification_audit_failure")
			dropped = true
		}
	}
	t.Cleanup(drop)
	return drop
}

type notificationOutcomeStore struct {
	transaction.Store
	unknown bool
}

func (s *notificationOutcomeStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	e := s.Store.Within(ctx, ro, fn)
	if e == nil && s.unknown {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return e
}
func (g notificationStory) wrapped(store transaction.Store) *Service {
	owner := *g.owner
	owner.DB = nil
	owner.Storage = store
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	// Share only lifecycle references, never copy the live service mutex or start
	// another worker. The original test owner alone stops and joins its goroutine.
	return &Service{Identity: &owner, lifecycle: g.s.lifecycle, cancel: g.s.cancel, done: g.s.done, closing: g.s.closing, maintenanceFailed: g.s.maintenanceFailed}
}
func notificationZero(t *testing.T, n Notification, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code || !reflect.DeepEqual(n, Notification{}) {
		t.Fatal("expected zero notification", code, e)
	}
}
func TestActualPairedNotificationRuntime(t *testing.T) {
	t.Run("recipient_private_configuration_UTC_pages_and_strict_cursor", func(t *testing.T) {
		g := notificationSetup(t)
		for _, state := range []string{"succeeded", "failed", "cancelled", "interrupted"} {
			id, _ := g.pending(t, false)
			g.finish(t, id, state, false)
		}
		c, e := g.s.Configuration(g.ctx, g.a)
		if e != nil || !c.Available || c.UnreadCount != 4 || c.RetentionDays != 1 {
			t.Fatal("typed private unread", e)
		}
		first, e := g.s.List(g.ctx, g.a, url.Values{"limit": {"1"}})
		if e != nil || len(first.Items) != 1 || first.NextCursor == nil {
			t.Fatal(e)
		}
		second, e := g.s.List(g.ctx, g.a, url.Values{"limit": {"1"}, "cursor": {*first.NextCursor}})
		if e != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.Items[0].OccurredAt.Location() != time.UTC {
			t.Fatal("UTC exact keyset", e)
		}
		raw, _ := json.Marshal(first)
		for _, secret := range []string{"PRIVATE_NOTICE", "question", "answer", "recipient_id", "owner_id", "graph_snapshot"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("private notice projection", secret)
			}
		}
		for _, values := range []url.Values{{"limit": {""}}, {"cursor": {""}}, {"limit": {"1", "2"}}, {"unknown": {"value"}}} {
			p, e := g.s.List(g.ctx, g.a, values)
			if e == nil || len(p.Items) != 0 || p.NextCursor != nil {
				t.Fatal("invalid query projected", e)
			}
		}
	})
	t.Run("current_membership_workspace_and_deleted_conversation_visibility", func(t *testing.T) {
		g := notificationSetup(t)
		id, conversation := g.pending(t, true)
		notice := g.finish(t, id, "failed", false)
		if _, e := g.s.Get(g.ctx, g.a, notice); e != nil {
			t.Fatal(e)
		}
		other := uuid.NewString()
		email := other + "@example.invalid"
		g.exec(t, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Other real notice recipient','en','active',true)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Other real notice recipient','en','active',true)", other, email, email)
		g.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'viewer')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'viewer')", other, g.f.Tenant, g.f.Workspace)
		otherAdmin := g.session(t, other, "admin")
		foreign, e := g.s.Get(g.ctx, otherAdmin, notice)
		notificationZero(t, foreign, e, "NOTIFICATION_NOT_FOUND")
		page, e := g.s.List(g.ctx, otherAdmin, url.Values{})
		if e != nil || len(page.Items) != 0 {
			t.Fatal("Admin status bypassed recipient privacy", e)
		}

		g.exec(t, "UPDATE workflow_conversations SET deleted_at=clock_timestamp() WHERE id=$1", "UPDATE workflow_conversations SET deleted_at=UTC_TIMESTAMP(6) WHERE id=?", *conversation)
		n, e := g.s.Get(g.ctx, g.a, notice)
		notificationZero(t, n, e, "NOTIFICATION_NOT_FOUND")
		id, _ = g.pending(t, false)
		notice = g.finish(t, id, "failed", false)
		g.exec(t, "UPDATE workspaces SET status='disabled' WHERE id=$1", "UPDATE workspaces SET status='disabled' WHERE id=?", g.f.Workspace)
		n, e = g.s.Read(g.ctx, g.a, notice, uuid.NewString())
		notificationZero(t, n, e, "NOTIFICATION_NOT_FOUND")
		g.exec(t, "UPDATE workspaces SET status='active' WHERE id=$1", "UPDATE workspaces SET status='active' WHERE id=?", g.f.Workspace)
		g.exec(t, "DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", "DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?", g.f.Workspace, g.f.User)
		n, e = g.s.Read(g.ctx, g.a, notice, uuid.NewString())
		notificationZero(t, n, e, "NOTIFICATION_NOT_FOUND")
	})
	t.Run("atomic_read_audit_rollback_and_concurrent_exact_replay", func(t *testing.T) {
		g := notificationSetup(t)
		id, _ := g.pending(t, false)
		notice := g.finish(t, id, "failed", false)
		drop := g.auditFailure(t, "notification.read")
		n, e := g.s.Read(g.ctx, g.a, notice, uuid.NewString())
		notificationZero(t, n, e, "STORAGE_UNAVAILABLE")
		drop()
		n, e = g.s.Get(g.ctx, g.a, notice)
		if e != nil || n.ReadAt != nil {
			t.Fatal("audit rollback changed read time", e)
		}
		var group sync.WaitGroup
		results := make(chan Notification, 2)
		failures := make(chan error, 2)
		for i := 0; i < 2; i++ {
			group.Add(1)
			go func() {
				defer group.Done()
				n, e := g.s.Read(g.ctx, g.a, notice, uuid.NewString())
				results <- n
				failures <- e
			}()
		}
		group.Wait()
		close(results)
		close(failures)
		for e := range failures {
			if e != nil {
				t.Fatal(e)
			}
		}
		var first *time.Time
		for n := range results {
			if n.ReadAt == nil {
				t.Fatal("missing accepted read time")
			}
			if first != nil && !first.Equal(*n.ReadAt) {
				t.Fatal("read time rewritten")
			}
			first = n.ReadAt
		}
		if g.count(t, "SELECT count(*) FROM audit_events WHERE action='notification.read' AND target_id=$1", "SELECT COUNT(*) FROM audit_events WHERE action='notification.read' AND target_id=?", notice) != 1 {
			t.Fatal("duplicate audit")
		}
	})
	t.Run("unknown_read_page_configuration_zero_and_explicit_reconciliation", func(t *testing.T) {
		g := notificationSetup(t)
		id, _ := g.pending(t, false)
		notice := g.finish(t, id, "failed", false)
		x := g.wrapped(&notificationOutcomeStore{Store: g.f.Store, unknown: true})
		n, e := x.Read(g.ctx, g.a, notice, uuid.NewString())
		notificationZero(t, n, e, string(transaction.CommitUnknown))
		n, e = g.s.Get(g.ctx, g.a, notice)
		if e != nil || n.ReadAt == nil {
			t.Fatal("durable unknown read not independently recovered", e)
		}
		n, e = x.Get(g.ctx, g.a, notice)
		notificationZero(t, n, e, string(transaction.CommitUnknown))
		p, e := x.List(g.ctx, g.a, url.Values{})
		if e == nil || e.Error() != string(transaction.CommitUnknown) || len(p.Items) != 0 || p.NextCursor != nil {
			t.Fatal("unknown page leaked", e)
		}
		c, e := x.Configuration(g.ctx, g.a)
		if e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(c, Configuration{}) {
			t.Fatal("unknown config leaked", e)
		}
	})
	t.Run("bounded_retention_atomic_audit_unknown_zero_and_restart", func(t *testing.T) {
		g := notificationSetup(t)
		for i := 0; i < 3; i++ {
			id, _ := g.pending(t, false)
			g.finish(t, id, "failed", true)
		}
		p, e := g.s.List(g.ctx, g.a, url.Values{})
		if e != nil || len(p.Items) != 0 {
			t.Fatal("expired notices visible", e)
		}
		drop := g.auditFailure(t, "notification.retention")
		count, e := g.s.Cleanup(g.ctx)
		if e == nil || count != 0 {
			t.Fatal("retention audit reported deletion", e)
		}
		drop()
		if g.count(t, "SELECT count(*) FROM terminal_notifications", "SELECT COUNT(*) FROM terminal_notifications") != 3 {
			t.Fatal("audit lost expired data")
		}
		x := g.wrapped(&notificationOutcomeStore{Store: g.f.Store, unknown: true})
		count, e = x.Cleanup(g.ctx)
		if e == nil || e.Error() != string(transaction.CommitUnknown) || count != 0 {
			t.Fatal("unknown cleanup projected count", e)
		}
		if g.count(t, "SELECT count(*) FROM terminal_notifications", "SELECT COUNT(*) FROM terminal_notifications") != 1 {
			t.Fatal("durable bounded deletion mismatch")
		}
		if e = g.s.Stop(g.ctx); e != nil {
			t.Fatal(e)
		}
		if g.s.Readiness() == nil {
			t.Fatal("stopped owner ready")
		}
		if e = g.s.Start(g.ctx); e != nil {
			t.Fatal(e)
		}
		if g.count(t, "SELECT count(*) FROM terminal_notifications", "SELECT COUNT(*) FROM terminal_notifications") != 0 || g.count(t, "SELECT count(*) FROM workflow_runs", "SELECT COUNT(*) FROM workflow_runs") != 3 {
			t.Fatal("restart lost/replayed source runs")
		}
	})
	t.Run("provenance_and_missing_product_schema_fail_closed", func(t *testing.T) {
		g := notificationSetup(t)
		id, _ := g.pending(t, false)
		g.finish(t, id, "failed", false)
		if e := g.s.VerifyStored(g.ctx); e != nil {
			t.Fatal(e)
		}
		g.exec(t, "UPDATE workflow_runs SET error_code='SYNTHETIC_CHANGED_PROVENANCE' WHERE id=$1", "UPDATE workflow_runs SET error_code='SYNTHETIC_CHANGED_PROVENANCE' WHERE id=?", id)
		if e := g.s.VerifyStored(g.ctx); e == nil || e.Error() != "NOTIFICATION_PROVENANCE_INVALID" {
			t.Fatal("changed run provenance accepted", e)
		}
		if e := g.s.Stop(g.ctx); e != nil {
			t.Fatal(e)
		}
		g.exec(t, "ALTER TABLE terminal_notifications RENAME TO explicit_missing_terminal_notifications", "RENAME TABLE terminal_notifications TO explicit_missing_terminal_notifications")
		if e := g.s.Start(g.ctx); e == nil || g.s.Available() {
			t.Fatal("missing schema created ready owner", e)
		}
		g.exec(t, "ALTER TABLE explicit_missing_terminal_notifications RENAME TO terminal_notifications", "RENAME TABLE explicit_missing_terminal_notifications TO terminal_notifications")
	})
	t.Run("current_epoch_context_recipient_and_store_coherence", func(t *testing.T) {
		g := notificationSetup(t)
		id, _ := g.pending(t, false)
		notice := g.finish(t, id, "failed", false)
		x := g.wrapped(g.f.Store)
		x.DB = &pgxpool.Pool{}
		n, e := x.Read(g.ctx, g.a, notice, uuid.NewString())
		notificationZero(t, n, e, "NOTIFICATION_STORAGE_MISMATCH")
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		n, e = g.s.Read(ctx, g.a, notice, uuid.NewString())
		notificationZero(t, n, e, string(transaction.Cancelled))
		// The authenticated DTO cannot substitute another recipient ID for this session.
		forged := g.a
		forged.Me.User.ID = uuid.NewString()
		n, e = g.s.Get(g.ctx, forged, notice)
		if e == nil || !reflect.DeepEqual(n, Notification{}) {
			t.Fatal("forged recipient accepted", e)
		}
		g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
		n, e = g.s.Read(g.ctx, g.a, notice, uuid.NewString())
		if e == nil || !reflect.DeepEqual(n, Notification{}) {
			t.Fatal("stale epoch accepted", e)
		}
	})
	t.Run("maintenance_readiness_failure_recovery_and_joined_stop", func(t *testing.T) {
		g := notificationSetup(t)
		if e := g.s.Stop(g.ctx); e != nil {
			t.Fatal(e)
		}
		policy := g.owner.Config.Policy()
		policy.Portal.NotificationMaintenanceSeconds = 1
		g.owner.Config.Runtime = &policy
		if e := g.s.Start(g.ctx); e != nil {
			t.Fatal(e)
		}
		if e := g.s.Start(g.ctx); e == nil || e.Error() != "NOTIFICATION_ALREADY_STARTED" {
			t.Fatal("duplicate maintenance owner", e)
		}
		id, _ := g.pending(t, false)
		g.finish(t, id, "failed", true)
		drop := g.auditFailure(t, "notification.retention")
		deadline := time.Now().Add(4 * time.Second)
		for g.s.Readiness() == nil && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if g.s.Readiness() == nil {
			t.Fatal("maintenance audit failure stayed ready")
		}
		drop()
		deadline = time.Now().Add(4 * time.Second)
		for g.s.Readiness() != nil && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if e := g.s.Readiness(); e != nil {
			t.Fatal("maintenance did not recover", e)
		}
		if e := g.s.Stop(g.ctx); e != nil {
			t.Fatal("owner did not join", e)
		}
	})
}

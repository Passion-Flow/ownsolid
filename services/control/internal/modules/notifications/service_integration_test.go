//go:build portal_database_integration

package notifications

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

type noticeFixture struct {
	service       *Service
	portal, admin identity.Auth
	workflow      workflows.Workflow
}

func newNoticeFixture(t *testing.T) noticeFixture {
	t.Helper()
	fixture.PreflightOwnedPostgres(t)
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	portal := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	account.Config.Workflows.Enabled = true
	policy := account.Config.Policy()
	policy.Portal.NotificationRetentionDays = 1
	policy.Portal.NotificationMaintenanceBatch = 2
	policy.Portal.NotificationMaintenanceSeconds = 3600
	account.Config.Runtime = &policy
	ws := &workspaces.Service{DB: account.DB, Identity: account}
	engine := &workflows.Service{DB: account.DB, Identity: account, Workspaces: ws}
	draft, e := engine.Create(context.Background(), portal, workflows.CreateRequest{WorkspaceID: portal.Me.Workspace.ID, Name: "Notice SQL transition fixture", Reason: "Synthetic SQL provenance and rollback tests"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	service := &Service{DB: account.DB, Identity: account}
	if e = service.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := service.Stop(ctx); e != nil {
			t.Error(e)
		}
	})
	return noticeFixture{service, portal, admin, draft}
}

// These rows test real SQL constraints/transactions with explicit synthetic run
// metadata. The separately tagged Portal workflow test executes an actual tool.
func (f noticeFixture) pending(t *testing.T) string {
	t.Helper()
	id := uuid.NewString()
	tag, e := f.service.DB.Exec(context.Background(), `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at)
 SELECT $1,f.tenant_id,f.workspace_id,f.id,$2,f.revision,f.execution_authority,m.authority_id,'portal',f.draft_graph,f.draft_sha256,'pending','PRIVATE_SYNTHETIC_QUESTION_NOT_NOTICE_CONTENT',clock_timestamp()+interval '1 minute'
 FROM workflows f JOIN workspace_memberships m ON m.tenant_id=f.tenant_id AND m.workspace_id=f.workspace_id AND m.user_id=$2 WHERE f.id=$3`, id, f.portal.Me.User.ID, f.workflow.ID)
	if e != nil || tag.RowsAffected() != 1 {
		t.Fatal("synthetic pending SQL fixture", e)
	}
	return id
}
func (f noticeFixture) finish(t *testing.T, id, state string) {
	t.Helper()
	_, e := f.service.DB.Exec(context.Background(), `UPDATE workflow_runs SET state=$2::text,error_code=CASE WHEN $2::text='succeeded' THEN NULL ELSE 'SYNTHETIC_TERMINAL_TEST' END,finished_at=clock_timestamp(),answer=CASE WHEN $2::text='succeeded' THEN 'PRIVATE_SYNTHETIC_ANSWER_NOT_NOTICE_CONTENT' ELSE NULL END,evidence=CASE WHEN $2::text='succeeded' THEN '{}'::jsonb ELSE NULL END,alias_snapshot=CASE WHEN $2::text='succeeded' THEN '{}'::jsonb ELSE NULL END,usage=CASE WHEN $2::text='succeeded' THEN '{}'::jsonb ELSE NULL END WHERE id=$1`, id, state)
	if e != nil {
		t.Fatal("actual SQL terminal transition", e)
	}
}
func noticeCount(t *testing.T, s *Service, query string, args ...any) int {
	t.Helper()
	var n int
	if e := s.DB.QueryRow(context.Background(), query, args...).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestM15ActualTerminalNoticeAllStatesRecipientReadConcurrencyAndAuditAtomicity(t *testing.T) {
	f := newNoticeFixture(t)
	s := f.service
	ctx := context.Background()
	for _, state := range []string{"succeeded", "failed", "cancelled", "interrupted"} {
		t.Run(state, func(t *testing.T) {
			id := f.pending(t)
			if noticeCount(t, s, "SELECT count(*) FROM terminal_notifications WHERE run_id=$1", id) != 0 {
				t.Fatal("pending notice fabricated")
			}
			f.finish(t, id, state)
			if _, e := s.DB.Exec(ctx, "UPDATE workflow_runs SET state=state WHERE id=$1", id); e != nil {
				t.Fatal(e)
			}
			if noticeCount(t, s, "SELECT count(*) FROM terminal_notifications WHERE run_id=$1 AND outcome=$2", id, state) != 1 {
				t.Fatal("missing or duplicate terminal notice")
			}
		})
	}
	page, e := s.List(ctx, f.portal, url.Values{"limit": {"1"}})
	if e != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal("actual first page", e)
	}
	notice := page.Items[0]
	next, e := s.List(ctx, f.portal, url.Values{"limit": {"1"}, "cursor": {*page.NextCursor}})
	if e != nil || len(next.Items) != 1 || next.Items[0].ID == notice.ID {
		t.Fatal("actual next page", e)
	}
	raw, _ := json.Marshal(page)
	for _, secret := range []string{"PRIVATE_SYNTHETIC", "question", "answer", "prompt", "tool_result", "recipient_id", "owner_id"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("notice leaked private field", secret)
		}
	}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION notice_read_reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='notification.read' THEN RAISE EXCEPTION 'SYNTHETIC_NOTICE_READ_AUDIT_REFUSAL';END IF;RETURN NEW;END;$$;CREATE TRIGGER notice_read_reject_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION notice_read_reject_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Read(ctx, f.portal, notice.ID, uuid.NewString()); e == nil {
		t.Fatal("read audit failure accepted")
	}
	current, e := s.Get(ctx, f.portal, notice.ID)
	if e != nil || current.ReadAt != nil {
		t.Fatal("failed audit changed read state", e)
	}
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER notice_read_reject_audit ON audit_events;DROP FUNCTION notice_read_reject_audit()"); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan Notification, 2)
	failures := make(chan error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			n, e := s.Read(ctx, f.portal, notice.ID, uuid.NewString())
			results <- n
			failures <- e
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	var at *time.Time
	for n := range results {
		if n.ReadAt == nil {
			t.Fatal("missing committed read time")
		}
		if at != nil && !at.Equal(*n.ReadAt) {
			t.Fatal("repeated read rewrote time")
		}
		at = n.ReadAt
	}
	if noticeCount(t, s, "SELECT count(*) FROM audit_events WHERE action='notification.read' AND target_id=$1", notice.ID) != 1 {
		t.Fatal("duplicate read audit")
	}
	if _, e = s.DB.Exec(ctx, "UPDATE terminal_notifications SET read_at=NULL WHERE id=$1", notice.ID); e == nil || !strings.Contains(e.Error(), "NOTIFICATION_READ_TIME_IMMUTABLE") {
		t.Fatal("read state reset", e)
	}
	config, e := s.Configuration(ctx, f.portal)
	if e != nil || config.UnreadCount != 3 || config.RetentionDays != 1 {
		t.Fatal("actual unread and nondefault policy", e, config)
	}
	// Current caller identity is never a request-body recipient override.
	email := uuid.NewString() + "@example.invalid"
	invitation, e := s.Identity.Invite(ctx, f.admin, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	password := fixture.Password()
	if e = s.Identity.Register(ctx, fixture.InvitationToken(t, s.Identity, invitation.ID), "Other notice recipient", password, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	other := fixture.Login(t, s.Identity, email, password, "portal")
	foreign, e := s.List(ctx, other, url.Values{})
	if e != nil || len(foreign.Items) != 0 {
		t.Fatal("other recipient list", e)
	}
	if _, e = s.Read(ctx, other, notice.ID, uuid.NewString()); e == nil || e.Error() != "NOTIFICATION_NOT_FOUND" {
		t.Fatal("other recipient changed notice", e)
	}
	if _, e = s.List(ctx, other, url.Values{"cursor": {*page.NextCursor}}); e == nil || e.Error() != "INVALID_CURSOR" {
		t.Fatal("other recipient cursor", e)
	}
	if e = s.VerifyStored(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestM15ActualNoticeTerminalTransactionRollbackProvenanceAndParentDeletion(t *testing.T) {
	f := newNoticeFixture(t)
	s := f.service
	ctx := context.Background()
	id := f.pending(t)
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, "UPDATE workflow_runs SET state='failed',error_code='SYNTHETIC_ROLLBACK',finished_at=clock_timestamp() WHERE id=$1", id); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = tx.QueryRow(ctx, "SELECT count(*) FROM terminal_notifications WHERE run_id=$1", id).Scan(&count); e != nil || count != 1 {
		t.Fatal("trigger did not share transaction", e)
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	if noticeCount(t, s, "SELECT count(*) FROM terminal_notifications WHERE run_id=$1", id) != 0 || noticeCount(t, s, "SELECT count(*) FROM workflow_runs WHERE id=$1 AND state='pending'", id) != 1 {
		t.Fatal("rolled back terminal produced notice")
	}
	// A compatible FK alone cannot attest a still-pending run's terminal outcome.
	_, e = s.DB.Exec(ctx, `INSERT INTO terminal_notifications(tenant_id,workspace_id,workflow_id,run_id,recipient_id,outcome,error_code) SELECT tenant_id,workspace_id,workflow_id,id,owner_id,'failed','SYNTHETIC_FORGED_TERMINAL' FROM workflow_runs WHERE id=$1`, id)
	if e == nil || !strings.Contains(e.Error(), "NOTIFICATION_RUN_PROVENANCE_REQUIRED") {
		t.Fatal("forged terminal notice", e)
	}
	f.finish(t, id, "failed")
	var notification string
	if e = s.DB.QueryRow(ctx, "SELECT id::text FROM terminal_notifications WHERE run_id=$1", id).Scan(&notification); e != nil {
		t.Fatal(e)
	}
	for _, statement := range []string{"UPDATE terminal_notifications SET recipient_id='" + uuid.NewString() + "' WHERE id=$1", "UPDATE terminal_notifications SET outcome='cancelled' WHERE id=$1"} {
		if _, e = s.DB.Exec(ctx, statement, notification); e == nil || !strings.Contains(e.Error(), "NOTIFICATION_PROVENANCE_IMMUTABLE") {
			t.Fatal("mutable provenance", e)
		}
	}
	if _, e = s.DB.Exec(ctx, "DELETE FROM workflow_runs WHERE id=$1", id); e != nil {
		t.Fatal(e)
	}
	if noticeCount(t, s, "SELECT count(*) FROM terminal_notifications WHERE id=$1", notification) != 0 {
		t.Fatal("deleted run left notice")
	}
}

func TestM15ActualNoticeBoundedRetentionAuditRollbackRestartAndReadiness(t *testing.T) {
	f := newNoticeFixture(t)
	s := f.service
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		id := f.pending(t)
		// Explicit historical SQL fixtures exercise age policy without falsifying a
		// product clock or changing immutable notice timestamps after creation.
		if _, e := s.DB.Exec(ctx, "UPDATE workflow_runs SET state='failed',error_code='SYNTHETIC_EXPIRED',finished_at=clock_timestamp()-interval '2 days' WHERE id=$1", id); e != nil {
			t.Fatal(e)
		}
		if _, e := s.DB.Exec(ctx, "DELETE FROM terminal_notifications WHERE run_id=$1", id); e != nil {
			t.Fatal(e)
		}
		if _, e := s.DB.Exec(ctx, `INSERT INTO terminal_notifications(tenant_id,workspace_id,workflow_id,run_id,recipient_id,outcome,error_code,occurred_at) SELECT tenant_id,workspace_id,workflow_id,id,owner_id,state,error_code,finished_at FROM workflow_runs WHERE id=$1`, id); e != nil {
			t.Fatal(e)
		}
	}
	page, e := s.List(ctx, f.portal, url.Values{})
	if e != nil || len(page.Items) != 0 {
		t.Fatal("expired notices visible", e)
	}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION notice_retention_reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='notification.retention' THEN RAISE EXCEPTION 'SYNTHETIC_NOTICE_RETENTION_AUDIT_REFUSAL';END IF;RETURN NEW;END;$$;CREATE TRIGGER notice_retention_reject_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION notice_retention_reject_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Cleanup(ctx); e == nil {
		t.Fatal("failed retention audit committed")
	}
	if noticeCount(t, s, "SELECT count(*) FROM terminal_notifications") != 3 {
		t.Fatal("retention rollback lost data")
	}
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER notice_retention_reject_audit ON audit_events;DROP FUNCTION notice_retention_reject_audit()"); e != nil {
		t.Fatal(e)
	}
	count, e := s.Cleanup(ctx)
	if e != nil || count != 2 || noticeCount(t, s, "SELECT count(*) FROM terminal_notifications") != 1 {
		t.Fatal("nondefault bounded batch", count, e)
	}
	stop, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if e = s.Stop(stop); e != nil {
		t.Fatal(e)
	}
	if e = s.Readiness(); e == nil {
		t.Fatal("stopped service ready")
	}
	if e = s.Start(ctx); e != nil {
		t.Fatal("restart bounded maintenance", e)
	}
	if noticeCount(t, s, "SELECT count(*) FROM terminal_notifications") != 0 || noticeCount(t, s, "SELECT count(*) FROM workflow_runs") != 3 {
		t.Fatal("restart replayed or lost run")
	}
	if e = s.Start(ctx); e == nil || e.Error() != "NOTIFICATION_ALREADY_STARTED" {
		t.Fatal("duplicate maintenance owner", e)
	}
	if e = s.Readiness(); e != nil {
		t.Fatal(e)
	}
}

func TestM15ActualNoticeMaintenanceFailureReadinessRecoveryAndJoinedStop(t *testing.T) {
	f := newNoticeFixture(t)
	s := f.service
	ctx := context.Background()
	stop, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if e := s.Stop(stop); e != nil {
		t.Fatal(e)
	}
	policy := s.Identity.Config.Policy()
	policy.Portal.NotificationMaintenanceSeconds = 1
	s.Identity.Config.Runtime = &policy
	if e := s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	id := f.pending(t)
	f.finish(t, id, "failed")
	if _, e := s.DB.Exec(ctx, "DELETE FROM terminal_notifications WHERE run_id=$1", id); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(ctx, `INSERT INTO terminal_notifications(tenant_id,workspace_id,workflow_id,run_id,recipient_id,outcome,error_code,occurred_at) SELECT tenant_id,workspace_id,workflow_id,id,owner_id,state,error_code,clock_timestamp()-interval '2 days' FROM workflow_runs WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(ctx, `CREATE FUNCTION notice_worker_reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='notification.retention' THEN RAISE EXCEPTION 'SYNTHETIC_NOTICE_WORKER_AUDIT_REFUSAL';END IF;RETURN NEW;END;$$;CREATE TRIGGER notice_worker_reject_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION notice_worker_reject_audit()`); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(4 * time.Second)
	for s.Readiness() == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if e := s.Readiness(); e == nil || e.Error() != "NOTIFICATION_UNAVAILABLE" {
		t.Fatal("maintenance failure not reflected in readiness", e)
	}
	if noticeCount(t, s, "SELECT count(*) FROM terminal_notifications WHERE run_id=$1", id) != 1 {
		t.Fatal("failed background audit deleted notice")
	}
	if _, e := s.DB.Exec(ctx, "DROP TRIGGER notice_worker_reject_audit ON audit_events;DROP FUNCTION notice_worker_reject_audit()"); e != nil {
		t.Fatal(e)
	}
	deadline = time.Now().Add(4 * time.Second)
	for s.Readiness() != nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if e := s.Readiness(); e != nil {
		t.Fatal("background owner did not recover", e)
	}
	if noticeCount(t, s, "SELECT count(*) FROM terminal_notifications WHERE run_id=$1", id) != 0 {
		t.Fatal("recovered owner did not delete eligible notice")
	}
	final, finish := context.WithTimeout(ctx, 3*time.Second)
	defer finish()
	if e := s.Stop(final); e != nil {
		t.Fatal("joined maintenance stop", e)
	}
}

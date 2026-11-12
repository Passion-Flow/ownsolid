package identity_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type workspaceAuditReadFixture struct {
	service, reader *identity.Service
	actor           identity.Auth
	tenant, space   string
	seed            string
}

func newWorkspaceAuditReadFixture(t *testing.T) workspaceAuditReadFixture {
	t.Helper()
	s := identityfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	password := identityfixture.Password()
	if err := s.Bootstrap(ctx, "workspace-audit@example.invalid", "Workspace audit Admin", password, "Audit tenant", "Default"); err != nil {
		t.Fatal(err)
	}
	var tenant, actor string
	if err := s.DB.QueryRow(ctx, `SELECT tenant_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1`).Scan(&tenant, &actor); err != nil {
		t.Fatal(err)
	}
	space, seed := uuid.NewString(), uuid.NewString()
	if _, err := s.DB.Exec(ctx, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Disabled audit history','disabled')`, space, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,request_id,reason,metadata,identifier_digest)
VALUES($1,clock_timestamp(),$2,$3,$4,'admin','workspace.updated','success',$5,'Visible operator reason','{"protected":"workspace-private-sentinel"}',decode(repeat('ab',32),'hex'))`, uuid.NewString(), actor, tenant, space, seed); err != nil {
		t.Fatal(err)
	}
	admin := identityfixture.Login(t, s, "workspace-audit@example.invalid", password, "admin")
	return workspaceAuditReadFixture{s, auditListReader(t, ctx, s), admin, tenant, space, seed}
}

func (f workspaceAuditReadFixture) count(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.service.DB.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action='audit.events_read' AND tenant_id=$1 AND workspace_id=$2`, f.tenant, f.space).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (f workspaceAuditReadFixture) read(ctx context.Context) (identity.WorkspaceAuditList, error) {
	return f.reader.ListWorkspaceAuditForActor(ctx, f.actor, f.space, url.Values{"request_id": {f.seed}, "limit": {"7"}})
}

func TestWorkspaceAuditReadAccounting(t *testing.T) {
	f := newWorkspaceAuditReadFixture(t)
	page, err := f.read(t.Context())
	if err != nil || len(page.Items) != 1 || page.NextCursor != nil || len(page.AssistantActors) != 0 || f.count(t) != 1 {
		t.Fatal("actual workspace read did not commit one access event", err)
	}
	raw, err := json.Marshal(page)
	if err != nil || strings.Contains(string(raw), "workspace-private-sentinel") || strings.Contains(string(raw), "identifier_digest") || page.Items[0]["reason"] != "Visible operator reason" {
		t.Fatal("workspace projection changed its permitted fields or exposed protected content")
	}
	var request, reason string
	var exact bool
	if err := f.service.DB.QueryRow(t.Context(), `SELECT request_id::text,reason,
actor_user_id=$1 AND tenant_id=$2 AND workspace_id=$3 AND audience='admin' AND outcome='success'
AND target_type='workspace' AND target_id=$3 AND subject_user_id IS NULL
AND metadata='{}'::jsonb AND identifier_digest IS NULL AND site_id=(SELECT site_id FROM cells WHERE is_local)
AND cell_id=(SELECT id FROM cells WHERE is_local)
FROM audit_events WHERE action='audit.events_read' AND workspace_id=$3`, f.actor.Me.User.ID, f.tenant, f.space).Scan(&request, &reason, &exact); err != nil || !exact || reason != "workspace_audit_read;limit=7;returned=1;assistant=true" || uuid.Validate(request) != nil || request == f.seed {
		t.Fatal("workspace access event lost exact database scope, actor or bounded selection", err)
	}
	// A known foreign workspace and a forged context claim do not become scope.
	foreignTenant, foreignSpace := uuid.NewString(), uuid.NewString()
	if _, err := f.service.DB.Exec(t.Context(), `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign audit tenant','active')`, foreignTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.DB.Exec(t.Context(), `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign audit workspace','active')`, foreignSpace, foreignTenant); err != nil {
		t.Fatal(err)
	}
	forged := f.actor
	forged.Me.Workspace = &identity.Workspace{ID: foreignSpace, TenantID: foreignTenant, Role: "owner"}
	for _, space := range []string{foreignSpace, uuid.NewString()} {
		out, err := f.reader.ListWorkspaceAuditForActor(t.Context(), forged, space, nil)
		if err == nil || err.Error() != "RESOURCE_NOT_FOUND" || len(out.Items) != 0 || out.NextCursor != nil || len(out.AssistantActors) != 0 || f.count(t) != 1 {
			t.Fatal("foreign or absent scope escaped current workspace authority", err)
		}
	}
	t.Run("concurrent reads", func(t *testing.T) {
		var wg sync.WaitGroup
		failures := make(chan error, 4)
		for range 4 {
			wg.Add(1)
			go func() { defer wg.Done(); _, err := f.read(t.Context()); failures <- err }()
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal("concurrent scoped access did not commit", err)
			}
		}
		if f.count(t) != 5 {
			t.Fatal("concurrent reads lost or duplicated an access event")
		}
	})
}

func TestWorkspaceAuditReadAppendRefusalAndRepair(t *testing.T) {
	f := newWorkspaceAuditReadFixture(t)
	var role string
	if err := f.reader.DB.QueryRow(t.Context(), "SELECT current_user").Scan(&role); err != nil {
		t.Fatal(err)
	}
	quoted := pgx.Identifier{role}.Sanitize()
	if _, err := f.service.DB.Exec(t.Context(), "REVOKE INSERT ON audit_events FROM "+quoted); err != nil {
		t.Fatal(err)
	}
	page, err := f.read(t.Context())
	if err == nil || len(page.Items) != 0 || page.NextCursor != nil || len(page.AssistantActors) != 0 || f.count(t) != 0 {
		t.Fatal("refused workspace append exposed a page")
	}
	if _, err := f.service.DB.Exec(t.Context(), "GRANT INSERT ON audit_events TO "+quoted); err != nil {
		t.Fatal(err)
	}
	page, err = f.read(t.Context())
	if err != nil || len(page.Items) != 1 || f.count(t) != 1 {
		t.Fatal("explicit scoped read did not recover after append repair", err)
	}
}

func TestWorkspaceAuditReadCancellationAtAppend(t *testing.T) {
	f := newWorkspaceAuditReadFixture(t)
	lock, err := f.service.DB.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = lock.Rollback(ctx)
	}()
	// SHARE permits the actual page SELECT and blocks the following INSERT.
	if _, err := lock.Exec(t.Context(), "LOCK TABLE audit_events IN SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		page identity.WorkspaceAuditList
		err  error
	}
	done := make(chan result, 1)
	go func() { page, err := f.read(ctx); done <- result{page, err} }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("scoped read cancellation worker did not join")
		}
	}()
	deadline, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	for {
		var blocked bool
		if err := f.service.DB.QueryRow(deadline, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%INSERT INTO audit_events%')`).Scan(&blocked); err != nil {
			t.Fatal("actual scoped INSERT wait was not observed", err)
		}
		if blocked {
			break
		}
		select {
		case out := <-done:
			done <- out
			t.Fatal("scoped read escaped the held INSERT fence", out.err)
		case <-deadline.Done():
			t.Fatal("scoped INSERT wait observation exceeded its bound")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case out := <-done:
		done <- out
		if out.err == nil || len(out.page.Items) != 0 || out.page.NextCursor != nil || len(out.page.AssistantActors) != 0 || f.count(t) != 0 {
			t.Fatal("canceled scoped append released a page or committed access")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled scoped append did not finish")
	}
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if page, err := f.read(t.Context()); err != nil || len(page.Items) != 1 || f.count(t) != 1 {
		t.Fatal("new explicit scoped read did not recover after cancellation", err)
	}
}

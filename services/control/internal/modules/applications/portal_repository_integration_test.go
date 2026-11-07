//go:build repository_integration

package applications_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	applicationhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences"
	preferencehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/google/uuid"
)

type portalStory struct {
	f        repositoryfixture.Fixture
	ctx      context.Context
	owner    *identity.Service
	apps     *applications.Service
	prefs    *preferences.Service
	a, admin identity.Auth
}

// The shared repository fixture applies the active product catalog exactly once.
// Portal steps 150–194 are already active; replaying old candidate DDL here would
// refuse every current MySQL case before its first business operation.
func portalSetup(t *testing.T) portalStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		t.Fatal(e)
	}
	owner := &identity.Service{Storage: f.Store, HMACKey: key}
	owner.Config.Workflows.Enabled = true
	policy := owner.Config.Policy()
	policy.UI.PageSize = 1
	owner.Config.Runtime = &policy
	g := portalStory{f: f, ctx: ctx, owner: owner}
	g.apps = g.appService(owner)
	g.prefs = &preferences.Service{Identity: owner}
	g.exec(t, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES($1,$2,$3,'owner',$4)`, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES(?,?,?,'owner',?)`, f.User, f.Tenant, f.Workspace, uuid.NewString())
	g.a = g.auth(t, f.User, "portal")
	g.admin = g.auth(t, f.User, "admin")
	return g
}
func (g portalStory) appService(owner *identity.Service) *applications.Service {
	w := &workspaces.Service{Identity: owner}
	flow := &workflows.Service{Identity: owner, Workspaces: w}
	return &applications.Service{Identity: owner, Workspaces: w, Workflows: flow}
}
func (g portalStory) wrapped(store transaction.Store) (*applications.Service, *preferences.Service) {
	owner := &identity.Service{Storage: store, HMACKey: append([]byte{}, g.owner.HMACKey...), Config: g.owner.Config}
	return g.appService(owner), &preferences.Service{Identity: owner}
}
func (g portalStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(pg, my, args...)); e != nil {
		t.Fatal("actual scoped fixture SQL", e)
	}
}
func (g portalStory) auth(t *testing.T, user, audience string) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = g.owner.IssueSessionWithin(ctx, q, user, 1, audience, "", uuid.NewString(), nil)
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
func (g portalStory) other(t *testing.T) identity.Auth {
	t.Helper()
	id := uuid.NewString()
	email := id + "@example.invalid"
	g.exec(t, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Actual private Portal user','en','active',false)`, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Actual private Portal user','en','active',false)`, id, email, email)
	g.exec(t, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES($1,$2,$3,'viewer',$4)`, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES(?,?,?,'viewer',?)`, id, g.f.Tenant, g.f.Workspace, uuid.NewString())
	return g.auth(t, id, "portal")
}
func (g portalStory) draft(t *testing.T, name string) workflows.Workflow {
	t.Helper()
	v, e := g.apps.Workflows.Create(g.ctx, g.a, workflows.CreateRequest{WorkspaceID: g.f.Workspace, Name: name, Reason: "Actual Portal projection source"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return v
}

// An explicit SQL publication/conversation projection envelope. It is not signed
// publication, admitted interpreter/model execution or result-quality evidence.
func (g portalStory) publication(t *testing.T, v workflows.Workflow, number int) string {
	t.Helper()
	id := uuid.NewString()
	g.exec(t, `INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) SELECT $1,tenant_id,workspace_id,id,$2,revision,draft_graph,draft_sha256,created_by FROM workflows WHERE id=$3`, `INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) SELECT ?,tenant_id,workspace_id,id,?,revision,draft_graph,draft_sha256,created_by FROM workflows WHERE id=?`, id, number, v.ID)
	g.exec(t, `UPDATE workflows SET current_version_id=$1 WHERE id=$2`, `UPDATE workflows SET current_version_id=? WHERE id=?`, id, v.ID)
	return id
}
func (g portalStory) conversation(t *testing.T, workflow, version, user string) string {
	t.Helper()
	id := uuid.NewString()
	g.exec(t, `INSERT INTO workflow_conversations(id,tenant_id,workspace_id,workflow_id,version_id,owner_id) VALUES($1,$2,$3,$4,$5,$6)`, `INSERT INTO workflow_conversations(id,tenant_id,workspace_id,workflow_id,version_id,owner_id) VALUES(?,?,?,?,?,?)`, id, g.f.Tenant, g.f.Workspace, workflow, version, user)
	return id
}
func portalWant(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s got %v", code, e)
	}
}
func (g portalStory) audits(t *testing.T) int {
	t.Helper()
	var n int
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT count(*) FROM audit_events WHERE action='preference.updated' AND actor_user_id=$1`, `SELECT count(*) FROM audit_events WHERE action='preference.updated' AND actor_user_id=?`, g.f.User)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func (g portalStory) refuseAudit(t *testing.T) func() {
	t.Helper()
	if g.f.Store.Dialect() == transaction.PostgreSQL {
		g.exec(t, `CREATE FUNCTION portal_lane_refuse_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='preference.updated' THEN RAISE EXCEPTION 'EXPLICIT_PORTAL_AUDIT_REFUSAL';END IF;RETURN NEW;END;$$`, "")
		g.exec(t, `CREATE TRIGGER portal_lane_refuse_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION portal_lane_refuse_audit()`, "")
	} else {
		g.exec(t, "", `CREATE TRIGGER portal_lane_refuse_audit BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action='preference.updated' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='EXPLICIT_PORTAL_AUDIT_REFUSAL';END IF;END`)
	}
	removed := false
	remove := func() {
		if removed {
			return
		}
		removed = true
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sql := `DROP TRIGGER portal_lane_refuse_audit`
		if g.f.Store.Dialect() == transaction.PostgreSQL {
			sql += ` ON audit_events`
		}
		if _, e := g.f.Store.Exec(ctx, transaction.SQL(g.f.Store.Dialect(), sql)); e != nil {
			t.Error(e)
		}
		if g.f.Store.Dialect() == transaction.PostgreSQL {
			if _, e := g.f.Store.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `DROP FUNCTION portal_lane_refuse_audit()`)); e != nil {
				t.Error(e)
			}
		}
	}
	t.Cleanup(remove)
	return remove
}

// Fault instruments always execute actual SQL/store transactions. They add only
// transport/read failure; no row, affected count or successful COMMIT is invented.
type portalFaultStore struct {
	transaction.Store
	unknown, late bool
	calls         atomic.Int64
}

func (s *portalFaultStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	s.calls.Add(1)
	e := s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		if s.late {
			q = portalFaultExecutor{q}
		}
		return fn(ctx, q)
	})
	if e != nil {
		return e
	}
	if s.unknown {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return nil
}

type portalFaultExecutor struct{ transaction.Executor }

func (q portalFaultExecutor) Query(ctx context.Context, s transaction.Statement) (transaction.Rows, error) {
	rows, e := q.Executor.Query(ctx, s)
	if e != nil {
		return nil, e
	}
	return &portalFaultRows{Rows: rows}, nil
}

type portalFaultRows struct {
	transaction.Rows
	scanned int
}

func (r *portalFaultRows) Scan(dest ...any) error {
	if e := r.Rows.Scan(dest...); e != nil {
		return e
	}
	r.scanned++
	if r.scanned == 2 {
		return &transaction.Error{Kind: transaction.Unavailable}
	}
	return nil
}

// Exactly one leaf is selected per fresh actual database by the lane runner.
func TestActualPairedPortalRuntime(t *testing.T) {
	t.Run("publication_visibility_utc_keyset_and_private_projection", func(t *testing.T) {
		g := portalSetup(t)
		hidden := g.draft(t, "Unpublished private graph")
		first := g.draft(t, "Published \u4e16\u754c")
		g.publication(t, first, 1)
		second := g.draft(t, "Second publication")
		g.publication(t, second, 1)
		page, e := g.apps.List(g.ctx, g.a, g.f.Workspace, url.Values{})
		if e != nil || len(page.Items) != 1 || page.NextCursor == nil {
			t.Fatal("typed UI default/keyset", e)
		}
		if page.Items[0].PublishedAt.Location() != time.UTC || page.Items[0].CanExecute || page.Items[0].QuestionBytes != min(g.owner.Config.Policy().Knowledge.QueryBytes, g.owner.Config.Policy().Workflows.QuestionBytes) {
			t.Fatal("UTC/current unavailable projection")
		}
		next, e := g.apps.List(g.ctx, g.a, g.f.Workspace, url.Values{"cursor": {*page.NextCursor}})
		if e != nil || len(next.Items) != 1 || next.Items[0].WorkflowID == page.Items[0].WorkflowID {
			t.Fatal("exact stable continuation", e)
		}
		raw, _ := json.Marshal([]applications.Application{page.Items[0], next.Items[0]})
		for _, field := range []string{"draft_graph", "instructions", "question", "result", "configuration", "private"} {
			if strings.Contains(string(raw), `"`+field+`"`) {
				t.Fatal("private field projected", field)
			}
		}
		for _, item := range append(page.Items, next.Items...) {
			if item.WorkflowID == hidden.ID {
				t.Fatal("unpublished draft visible")
			}
		}
		g.exec(t, `UPDATE workflows SET enabled=false WHERE id=$1`, `UPDATE workflows SET enabled=false WHERE id=?`, page.Items[0].WorkflowID)
		visible, e := g.apps.List(g.ctx, g.a, g.f.Workspace, url.Values{"limit": {"10"}})
		if e != nil || len(visible.Items) != 1 {
			t.Fatal("withdrawn publication visible", e)
		}
	})
	t.Run("conversation_pinned_owner_withdrawal_and_tombstone", func(t *testing.T) {
		g := portalSetup(t)
		other := g.other(t)
		v := g.draft(t, "Pinned initial name")
		old := g.publication(t, v, 1)
		own := g.conversation(t, v.ID, old, g.f.User)
		g.conversation(t, v.ID, old, other.Me.User.ID)
		g.publication(t, v, 2)
		g.exec(t, `UPDATE workflows SET name='Current changed name',name_key='current changed name',enabled=false WHERE id=$1`, `UPDATE workflows SET name='Current changed name',name_key='current changed name',enabled=false WHERE id=?`, v.ID)
		page, e := g.apps.Conversations(g.ctx, g.a, g.f.Workspace, url.Values{})
		if e != nil || len(page.Items) != 1 || page.Items[0].ID != own || page.Items[0].VersionID != old || page.Items[0].VersionNumber != 1 || page.Items[0].WorkflowName != "Current changed name" || page.Items[0].CanExecute || page.Items[0].UpdatedAt.Location() != time.UTC {
			t.Fatal("private immutable pin/current withdrawal", e)
		}
		foreign, e := g.apps.Conversations(g.ctx, other, g.f.Workspace, url.Values{})
		if e != nil || len(foreign.Items) != 1 || foreign.Items[0].ID == own {
			t.Fatal("foreign recipient projection", e)
		}
		g.exec(t, `UPDATE workflow_conversations SET deleted_at=clock_timestamp() WHERE id=$1`, `UPDATE workflow_conversations SET deleted_at=UTC_TIMESTAMP(6) WHERE id=?`, own)
		page, e = g.apps.Conversations(g.ctx, g.a, g.f.Workspace, url.Values{})
		if e != nil || len(page.Items) != 0 {
			t.Fatal("tombstone disclosed", e)
		}
	})
	t.Run("current_membership_epoch_tenant_and_cursor_scope", func(t *testing.T) {
		g := portalSetup(t)
		v := g.draft(t, "Current projection")
		version := g.publication(t, v, 1)
		g.conversation(t, v.ID, version, g.f.User)
		second := g.draft(t, "Next projection")
		g.publication(t, second, 1)
		other := g.other(t)
		page, e := g.apps.List(g.ctx, g.a, g.f.Workspace, url.Values{})
		if e != nil || page.NextCursor == nil {
			t.Fatal(e)
		}
		_, e = g.apps.List(g.ctx, other, g.f.Workspace, url.Values{"cursor": {*page.NextCursor}})
		portalWant(t, e, "INVALID_CURSOR")
		g.exec(t, `DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, `DELETE FROM workspace_memberships WHERE user_id=? AND workspace_id=?`, g.f.User, g.f.Workspace)
		denied, e := g.apps.List(g.ctx, g.a, g.f.Workspace, url.Values{})
		if e == nil || len(denied.Items) != 0 || denied.NextCursor != nil {
			t.Fatal("removed membership disclosed")
		}
		// Global preference remains available without inventing a Workspace parameter.
		if _, e = g.prefs.Get(g.ctx, g.a); e != nil {
			t.Fatal("global setting acquired invented membership requirement", e)
		}
		g.exec(t, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?`, other.Me.User.ID)
		denied, e = g.apps.List(g.ctx, other, g.f.Workspace, url.Values{})
		if e == nil || len(denied.Items) != 0 {
			t.Fatal("old actor epoch disclosed")
		}
	})
	// The portal home sends the workspace its switcher selected. A workspace the caller
	// is not a member of answers as if it did not exist, with nothing of it disclosed.
	t.Run("another_workspace_is_not_found", func(t *testing.T) {
		g := portalSetup(t)
		v := g.draft(t, "Own workspace application")
		g.conversation(t, v.ID, g.publication(t, v, 1), g.f.User)
		foreign := uuid.NewString()
		g.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Workspace of someone else','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Workspace of someone else','active')`, foreign, g.f.Tenant)
		apps, e := g.apps.List(g.ctx, g.a, foreign, url.Values{})
		portalWant(t, e, "RESOURCE_NOT_FOUND")
		if len(apps.Items) != 0 || apps.NextCursor != nil {
			t.Fatal("another workspace's applications disclosed")
		}
		conversations, e := g.apps.Conversations(g.ctx, g.a, foreign, url.Values{})
		portalWant(t, e, "RESOURCE_NOT_FOUND")
		if len(conversations.Items) != 0 || conversations.NextCursor != nil {
			t.Fatal("another workspace's conversations disclosed")
		}
		ah := &applicationhttp.Handler{Service: g.apps}
		for _, path := range []string{"/api/v1/applications", "/api/v1/application-conversations"} {
			r := httptest.NewRequest(http.MethodGet, "https://portal.example.invalid"+path+"?workspace_id="+foreign, nil)
			body, _, e := ah.Handle(r, g.a, uuid.NewString())
			portalWant(t, e, "RESOURCE_NOT_FOUND")
			if raw, _ := json.Marshal(body); strings.Contains(string(raw), v.ID) {
				t.Fatal("another workspace's row reached the response", path)
			}
		}
		own, e := g.apps.List(g.ctx, g.a, g.f.Workspace, url.Values{})
		if e != nil || len(own.Items) != 1 || own.Items[0].WorkflowID != v.ID {
			t.Fatal("own workspace no longer listed", e)
		}
	})
	t.Run("one_transaction_configuration_and_unknown_zero_pages", func(t *testing.T) {
		g := portalSetup(t)
		v := g.draft(t, "Actual one transaction")
		version := g.publication(t, v, 1)
		g.conversation(t, v.ID, version, g.f.User)
		store := &portalFaultStore{Store: g.f.Store}
		apps, _ := g.wrapped(store)
		page, e := apps.List(g.ctx, g.a, g.f.Workspace, url.Values{})
		if e != nil || len(page.Items) != 1 || store.calls.Load() != 1 {
			t.Fatal("configuration escaped owning transaction", e, store.calls.Load())
		}
		store.unknown = true
		page, e = apps.List(g.ctx, g.a, g.f.Workspace, url.Values{})
		portalWant(t, e, string(transaction.CommitUnknown))
		if len(page.Items) != 0 || page.NextCursor != nil {
			t.Fatal("unknown page disclosed")
		}
		history, e := apps.Conversations(g.ctx, g.a, g.f.Workspace, url.Values{})
		portalWant(t, e, string(transaction.CommitUnknown))
		if len(history.Items) != 0 || history.NextCursor != nil {
			t.Fatal("unknown private history disclosed")
		}
		configuration, e := apps.Workflows.Configuration(g.ctx, g.a, g.f.Workspace)
		portalWant(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(configuration, workflows.Configuration{}) {
			t.Fatal("unknown public configuration returned projection")
		}
	})
	t.Run("late_actual_row_failure_context_and_mismatched_owner", func(t *testing.T) {
		g := portalSetup(t)
		v := g.draft(t, "Actual partial one")
		version := g.publication(t, v, 1)
		g.conversation(t, v.ID, version, g.f.User)
		g.conversation(t, v.ID, version, g.f.User)
		next := g.draft(t, "Actual partial two")
		g.publication(t, next, 1)
		apps, _ := g.wrapped(&portalFaultStore{Store: g.f.Store, late: true})
		page, e := apps.List(g.ctx, g.a, g.f.Workspace, url.Values{"limit": {"10"}})
		portalWant(t, e, "STORAGE_UNAVAILABLE")
		if len(page.Items) != 0 || page.NextCursor != nil {
			t.Fatal("late real scan exposed earlier row")
		}
		history, e := apps.Conversations(g.ctx, g.a, g.f.Workspace, url.Values{"limit": {"10"}})
		portalWant(t, e, "STORAGE_UNAVAILABLE")
		if len(history.Items) != 0 || history.NextCursor != nil {
			t.Fatal("late history scan exposed earlier row")
		}
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		page, e = g.apps.List(ctx, g.a, g.f.Workspace, url.Values{})
		if e == nil || len(page.Items) != 0 {
			t.Fatal("cancelled read disclosed")
		}
		expired, stop := context.WithDeadline(g.ctx, time.Now().Add(-time.Second))
		defer stop()
		page, e = g.apps.List(expired, g.a, g.f.Workspace, url.Values{})
		if e == nil || len(page.Items) != 0 || page.NextCursor != nil {
			t.Fatal("expired deadline disclosed")
		}
		g.apps.Workflows.Identity = &identity.Service{Storage: g.f.Store}
		page, e = g.apps.List(g.ctx, g.a, g.f.Workspace, url.Values{})
		portalWant(t, e, "APPLICATION_STORAGE_MISMATCH")
		if len(page.Items) != 0 {
			t.Fatal("mismatched owner disclosed")
		}
	})
	t.Run("preference_actual_seeds_dual_entrance_and_persistence", func(t *testing.T) {
		g := portalSetup(t)
		other := g.other(t)
		for _, a := range []identity.Auth{g.a, g.admin, other} {
			p, e := g.prefs.Get(g.ctx, a)
			if e != nil || p.Theme != preferences.SystemTheme || p.Revision != 1 || p.UpdatedAt.Location() != time.UTC {
				t.Fatal("real existing/new user seed", e)
			}
		}
		before := g.audits(t)
		p, e := g.prefs.Update(g.ctx, g.a, preferences.UpdateRequest{Theme: new(preferences.DarkTheme), ExpectedRevision: 1}, uuid.NewString())
		if e != nil || p.Revision != 2 || p.Theme != preferences.DarkTheme {
			t.Fatal(e)
		}
		persisted, e := (&preferences.Service{Identity: g.owner}).Get(g.ctx, g.admin)
		if e != nil || !reflect.DeepEqual(persisted, p) || g.audits(t) != before+1 {
			t.Fatal("dual entrance same recipient persistence", e)
		}
		unchanged, e := g.prefs.Get(g.ctx, other)
		if e != nil || unchanged.Theme != preferences.SystemTheme || unchanged.Revision != 1 {
			t.Fatal("other recipient preference changed", e)
		}
		if e = g.prefs.VerifyStored(g.ctx); e != nil {
			t.Fatal("actual retained integrity", e)
		}
	})
	t.Run("preference_invalid_exact_revision_concurrency_and_privacy", func(t *testing.T) {
		g := portalSetup(t)
		for _, input := range []preferences.UpdateRequest{{Theme: new("false"), ExpectedRevision: 1}, {Theme: new("dark"), ExpectedRevision: 0}, {Theme: new("light"), ExpectedRevision: preferences.MaximumRevision}} {
			p, e := g.prefs.Update(g.ctx, g.a, input, uuid.NewString())
			portalWant(t, e, "VALIDATION_FAILED")
			if !reflect.DeepEqual(p, preferences.Preference{}) {
				t.Fatal("invalid preference projection")
			}
		}
		before := g.audits(t)
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, theme := range []string{preferences.LightTheme, preferences.DarkTheme} {
			wg.Add(1)
			go func(theme string) {
				defer wg.Done()
				<-start
				_, e := g.prefs.Update(g.ctx, g.a, preferences.UpdateRequest{Theme: &theme, ExpectedRevision: 1}, uuid.NewString())
				results <- e
			}(theme)
		}
		close(start)
		wg.Wait()
		close(results)
		passed, conflict := 0, 0
		for e := range results {
			if e == nil {
				passed++
			} else if e.Error() == "PREFERENCE_REVISION_CONFLICT" {
				conflict++
			} else {
				t.Fatal(e)
			}
		}
		p, e := g.prefs.Get(g.ctx, g.a)
		if e != nil || p.Revision != 2 || passed != 1 || conflict != 1 || g.audits(t) != before+1 {
			t.Fatal("real lost revision or audit", e, passed, conflict)
		}
		p, e = g.prefs.Update(g.ctx, g.a, preferences.UpdateRequest{Theme: &p.Theme, ExpectedRevision: p.Revision}, uuid.NewString())
		if e != nil || p.Revision != 3 {
			t.Fatal("same-theme revision behavior changed", e)
		}
	})
	t.Run("preference_audit_rollback_and_missing_seed_integrity", func(t *testing.T) {
		g := portalSetup(t)
		before, e := g.prefs.Get(g.ctx, g.a)
		if e != nil {
			t.Fatal(e)
		}
		audits := g.audits(t)
		remove := g.refuseAudit(t)
		p, e := g.prefs.Update(g.ctx, g.a, preferences.UpdateRequest{Theme: new(preferences.DarkTheme), ExpectedRevision: before.Revision}, uuid.NewString())
		if e == nil || !reflect.DeepEqual(p, preferences.Preference{}) {
			t.Fatal("audit refusal returned value")
		}
		after, e := g.prefs.Get(g.ctx, g.a)
		if e != nil || !reflect.DeepEqual(after, before) || g.audits(t) != audits {
			t.Fatal("audit refusal mutated persisted preference", e)
		}
		remove()
		g.exec(t, `DELETE FROM user_preferences WHERE user_id=$1`, `DELETE FROM user_preferences WHERE user_id=?`, g.f.User)
		p, e = g.prefs.Get(g.ctx, g.a)
		portalWant(t, e, "PREFERENCE_UNAVAILABLE")
		if !reflect.DeepEqual(p, preferences.Preference{}) {
			t.Fatal("missing seed fabricated default")
		}
		portalWant(t, g.prefs.VerifyStored(g.ctx), "PREFERENCE_UNAVAILABLE")
	})
	t.Run("preference_unknown_commit_readback_and_stale_revision", func(t *testing.T) {
		g := portalSetup(t)
		_, bad := g.wrapped(&portalFaultStore{Store: g.f.Store, unknown: true})
		before := g.audits(t)
		p, e := bad.Update(g.ctx, g.a, preferences.UpdateRequest{Theme: new(preferences.DarkTheme), ExpectedRevision: 1}, uuid.NewString())
		portalWant(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(p, preferences.Preference{}) {
			t.Fatal("unknown mutation disclosed result")
		}
		actual, e := g.prefs.Get(g.ctx, g.a)
		if e != nil || actual.Theme != preferences.DarkTheme || actual.Revision != 2 || g.audits(t) != before+1 {
			t.Fatal("exact actual durability readback", e)
		}
		_, e = g.prefs.Update(g.ctx, g.a, preferences.UpdateRequest{Theme: new(preferences.DarkTheme), ExpectedRevision: 1}, uuid.NewString())
		portalWant(t, e, "PREFERENCE_REVISION_CONFLICT")
		p, e = bad.Get(g.ctx, g.a)
		portalWant(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(p, preferences.Preference{}) {
			t.Fatal("unknown read disclosed")
		}
		portalWant(t, bad.VerifyStored(g.ctx), string(transaction.CommitUnknown))
	})
	t.Run("preference_current_actor_tenant_cancellation_and_http_contract", func(t *testing.T) {
		g := portalSetup(t)
		other := g.other(t)
		h := &preferencehttp.Handler{Service: g.prefs}
		for _, body := range []string{`{"theme":"dark"}`, `{"theme":"dark","expected_revision":1,"user_id":"foreign"}`, `{"theme":"dark","theme":"light","expected_revision":1}`} {
			r := httptest.NewRequest(http.MethodPatch, "https://portal.example.invalid/api/v1/preferences", strings.NewReader(body))
			_, _, e := h.Handle(r, g.a, uuid.NewString())
			if e == nil {
				t.Fatal("strict required/unknown/duplicate body admitted")
			}
		}
		ah := &applicationhttp.Handler{Service: g.apps}
		r := httptest.NewRequest(http.MethodGet, "https://portal.example.invalid/api/v1/applications?workspace_id="+g.f.Workspace+"&owner_id=foreign", nil)
		if _, _, e := ah.Handle(r, g.a, uuid.NewString()); e == nil {
			t.Fatal("client recipient query admitted")
		}
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		p, e := g.prefs.Get(ctx, g.a)
		if e == nil || !reflect.DeepEqual(p, preferences.Preference{}) {
			t.Fatal("cancelled preference disclosed")
		}
		expired, stop := context.WithDeadline(g.ctx, time.Now().Add(-time.Second))
		defer stop()
		p, e = g.prefs.Get(expired, g.a)
		if e == nil || !reflect.DeepEqual(p, preferences.Preference{}) {
			t.Fatal("expired preference deadline disclosed")
		}
		g.exec(t, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET status='disabled',auth_epoch=auth_epoch+1 WHERE id=?`, other.Me.User.ID)
		p, e = g.prefs.Get(g.ctx, other)
		if e == nil || !reflect.DeepEqual(p, preferences.Preference{}) {
			t.Fatal("disabled current identity disclosed")
		}
		revoked := g.other(t)
		g.exec(t, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='logout' WHERE id=?`, revoked.SessionID)
		p, e = g.prefs.Get(g.ctx, revoked)
		if e == nil || !reflect.DeepEqual(p, preferences.Preference{}) {
			t.Fatal("revoked current session disclosed")
		}
		g.exec(t, `UPDATE tenants SET status='disabled' WHERE id=$1`, `UPDATE tenants SET status='disabled' WHERE id=?`, g.f.Tenant)
		p, e = g.prefs.Update(g.ctx, g.a, preferences.UpdateRequest{Theme: new(preferences.DarkTheme), ExpectedRevision: 1}, uuid.NewString())
		if e == nil || !reflect.DeepEqual(p, preferences.Preference{}) {
			t.Fatal("inactive installed tenant mutated")
		}
		if e = g.prefs.VerifyStored(g.ctx); e == nil {
			t.Fatal("inactive installed tenant reported ready")
		}
		denied, e := g.apps.List(g.ctx, g.a, g.f.Workspace, url.Values{})
		if e == nil || len(denied.Items) != 0 || denied.NextCursor != nil {
			t.Fatal("inactive installed tenant disclosed application")
		}
	})
}

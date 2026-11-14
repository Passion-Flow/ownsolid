//go:build repository_integration

package workspaces

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type candidateFixture struct {
	fixture                            repositoryfixture.Fixture
	ctx                                context.Context
	service                            *Service
	admin, owner, viewer               identity.Auth
	viewerEmail                        string
	workspace                          Workspace
	first, second                      Note
	grant, role, ownerGrant, ownerRole string
}

func newCandidateFixture(t *testing.T) *candidateFixture {
	t.Helper()
	f := repositoryfixture.New(t)
	if f.PostgreSQL == nil {
		t.Fatal("explicit PostgreSQL fixture required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	accounts := &identity.Service{Storage: f.Store}
	s := &Service{Identity: accounts}
	c := &candidateFixture{fixture: f, ctx: ctx, service: s}
	sqlFile := os.Getenv("IAM_ROLE_CANDIDATE_SQL_FILE")
	raw, e := os.ReadFile(sqlFile)
	if e != nil {
		t.Fatal("candidate SQL file required")
	}
	if _, e = f.PostgreSQL.Exec(ctx, string(raw)); e != nil {
		t.Fatal("candidate schema refused", e)
	}
	issue := func(id, audience string) identity.Auth {
		var issued identity.IssuedSession
		if e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			issued, e = accounts.IssueSessionWithin(ctx, q, id, 1, audience, "", uuid.NewString(), nil)
			return e
		}); e != nil {
			t.Fatal("fixture session refused", e)
		}
		a, e := accounts.Authenticate(ctx, issued.Token, audience)
		if e != nil {
			t.Fatal("fixture authentication refused", e)
		}
		return a
	}
	actor := func() (identity.Auth, string) {
		id, email := uuid.NewString(), "synthetic+"+uuid.NewString()+"@example.invalid"
		if _, e := f.PostgreSQL.Exec(ctx, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$2,'Scoped actor','en','active',false)`, id, email); e != nil {
			t.Fatal("fixture actor refused", e)
		}
		return issue(id, "portal"), email
	}
	c.admin = issue(f.User, "admin")
	c.owner, _ = actor()
	c.viewer, c.viewerEmail = actor()
	c.workspace, e = s.Create(ctx, c.admin, "Scoped role experiment", c.owner.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AddMember(ctx, c.admin, c.workspace.ID, c.viewerEmail, "viewer", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	c.first, e = s.CreateNote(ctx, c.admin, c.workspace.ID, "First note", "Original first", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	c.second, e = s.CreateNote(ctx, c.admin, c.workspace.ID, "Second note", "Original second", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	c.role = c.seedRole(t, "platform admin", candidateNoteUpdate)
	c.grant = c.seedGrant(t, c.viewer.Me.User.ID, c.role, c.first.ID)
	c.ownerRole = c.seedRole(t, "Workspace owner", candidateNoteUpdate, candidateNoteDelete)
	c.ownerGrant = c.seedGrant(t, c.owner.Me.User.ID, c.ownerRole, "")
	return c
}
func (c *candidateFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, e := c.fixture.PostgreSQL.Exec(c.ctx, query, args...); e != nil {
		t.Fatal("candidate fixture statement refused", e)
	}
}
func (c *candidateFixture) seedRole(t *testing.T, name string, actions ...string) string {
	t.Helper()
	id := uuid.NewString()
	c.exec(t, `INSERT INTO iam_role_proto_roles(id,tenant_id,workspace_id,name) VALUES($1,$2,$3,$4)`, id, c.fixture.Tenant, c.workspace.ID, name)
	for _, a := range actions {
		c.exec(t, `INSERT INTO iam_role_proto_actions(tenant_id,workspace_id,role_id,action) VALUES($1,$2,$3,$4)`, c.fixture.Tenant, c.workspace.ID, id, a)
	}
	return id
}
func (c *candidateFixture) seedGrant(t *testing.T, user, role, note string) string {
	t.Helper()
	id := uuid.NewString()
	c.exec(t, `INSERT INTO iam_role_proto_grants(id,tenant_id,workspace_id,user_id,authority_id,role_id,note_id) SELECT $1,$2,$3,$4,m.authority_id,$5,NULLIF($6,'')::uuid FROM workspace_memberships m WHERE m.tenant_id=$2 AND m.workspace_id=$3 AND m.user_id=$4`, id, c.fixture.Tenant, c.workspace.ID, user, role, note)
	return id
}
func candidateWant(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || err.Error() != code {
		t.Fatalf("expected %s, received %v", code, err)
	}
}
func candidateEmpty(t *testing.T, n Note) {
	t.Helper()
	if n.ID != "" || n.Title != "" || n.Version != 0 {
		t.Fatal("refusal returned private note")
	}
}
func (c *candidateFixture) noteVersion(t *testing.T, id string) int64 {
	t.Helper()
	var v int64
	if e := c.fixture.PostgreSQL.QueryRow(c.ctx, `SELECT version FROM workspace_notes WHERE id=$1`, id).Scan(&v); e != nil {
		t.Fatal(e)
	}
	return v
}
func (c *candidateFixture) events(t *testing.T, request string) int {
	t.Helper()
	var n int
	if e := c.fixture.PostgreSQL.QueryRow(c.ctx, `SELECT count(*) FROM audit_events WHERE request_id=$1`, request).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

func TestScopedRoleCandidateCurrentScopeAndLifetime(t *testing.T) {
	c := newCandidateFixture(t)
	s, ctx, w := c.service, c.ctx, c.workspace
	granted, e := s.candidateExplain(ctx, c.viewer, w.ID, c.first.ID, candidateNoteUpdate, uuid.NewString())
	if e != nil || !granted.Allowed || len(granted.Grants) != 1 || granted.Grants[0].GrantID != c.grant || granted.PlatformAuthority {
		t.Fatal("own explain did not use the scoped assignment", e)
	}
	got, e := s.ChangeNote(ctx, c.viewer, w.ID, c.first.ID, "Authorized update", "Scoped body", 1, uuid.NewString(), false)
	if e != nil || got.Version != 2 {
		t.Fatal("explicit viewer grant did not authorize real note update", e)
	}
	for _, v := range []struct {
		note    string
		version int64
		remove  bool
	}{{c.second.ID, 1, false}, {c.first.ID, 2, true}} {
		request := uuid.NewString()
		n, e := s.ChangeNote(ctx, c.viewer, w.ID, v.note, "Denied", "Denied", v.version, request, v.remove)
		candidateWant(t, e, "ACCESS_DENIED")
		candidateEmpty(t, n)
		if c.events(t, request) != 0 {
			t.Fatal("denied write appended audit")
		}
	}
	if c.noteVersion(t, c.second.ID) != 1 {
		t.Fatal("ungranted note changed")
	}
	if _, e = s.candidateSetGrantActive(ctx, c.viewer, w.ID, c.grant, 1, false, uuid.NewString()); e == nil {
		t.Fatal("display role name granted management authority")
	}
	inherited, e := s.candidateExplain(ctx, c.owner, w.ID, c.second.ID, candidateNoteDelete, uuid.NewString())
	if e != nil || !inherited.Allowed || len(inherited.Grants) != 1 || inherited.Grants[0].GrantID != c.ownerGrant {
		t.Fatal("Workspace inheritance failed", e)
	}
	// The actual current scope ignores cached Workspace/role data.
	forged := c.viewer
	forged.Me.Workspace = nil
	n, e := s.ChangeNote(ctx, forged, w.ID, c.first.ID, "Current scope", "Current body", 2, uuid.NewString(), false)
	if e != nil || n.Version != 3 {
		t.Fatal("current scope incorrectly depended on cached Workspace", e)
	}
	foreignTenant, foreignWorkspace, foreignNote, foreignRole, foreignGrant := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	c.exec(t, `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign synthetic','active')`, foreignTenant)
	c.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign Workspace','active')`, foreignWorkspace, foreignTenant)
	c.exec(t, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')`, c.viewer.Me.User.ID, foreignTenant, foreignWorkspace)
	c.exec(t, `INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,text,author_user_id) VALUES($1,$2,$3,'Foreign note','',$4)`, foreignNote, foreignTenant, foreignWorkspace, c.viewer.Me.User.ID)
	c.exec(t, `INSERT INTO iam_role_proto_roles(id,tenant_id,workspace_id,name) VALUES($1,$2,$3,'Foreign role')`, foreignRole, foreignTenant, foreignWorkspace)
	c.exec(t, `INSERT INTO iam_role_proto_actions(tenant_id,workspace_id,role_id,action) VALUES($1,$2,$3,$4)`, foreignTenant, foreignWorkspace, foreignRole, candidateNoteUpdate)
	c.exec(t, `INSERT INTO iam_role_proto_grants(id,tenant_id,workspace_id,user_id,authority_id,role_id) SELECT $1,$2,$3,$4,authority_id,$5 FROM workspace_memberships WHERE tenant_id=$2 AND workspace_id=$3 AND user_id=$4`, foreignGrant, foreignTenant, foreignWorkspace, c.viewer.Me.User.ID, foreignRole)
	for _, pair := range [][2]string{{foreignWorkspace, foreignNote}, {w.ID, foreignNote}} {
		request := uuid.NewString()
		n, e := s.ChangeNote(ctx, c.viewer, pair[0], pair[1], "Foreign", "Foreign", 1, request, false)
		candidateWant(t, e, "RESOURCE_NOT_FOUND")
		candidateEmpty(t, n)
		if c.events(t, request) != 0 {
			t.Fatal("foreign note mutation appended audit")
		}
		out, e := s.candidateExplain(ctx, c.viewer, pair[0], pair[1], candidateNoteUpdate, uuid.NewString())
		candidateWant(t, e, "RESOURCE_NOT_FOUND")
		if out.Allowed || len(out.Grants) != 0 {
			t.Fatal("foreign explain leaked grants")
		}
	}
	for _, pair := range [][2]string{{foreignWorkspace, foreignGrant}, {w.ID, foreignGrant}} {
		out, e := s.candidateSetGrantActive(ctx, c.admin, pair[0], pair[1], 1, false, uuid.NewString())
		candidateWant(t, e, "RESOURCE_NOT_FOUND")
		if out.ID != "" {
			t.Fatal("foreign grant returned data")
		}
	}
	_, e = c.fixture.PostgreSQL.Exec(ctx, `INSERT INTO iam_role_proto_grants(id,tenant_id,workspace_id,user_id,authority_id,role_id,note_id) SELECT $1,$2,$3,$4,authority_id,$5,$6 FROM workspace_memberships WHERE tenant_id=$2 AND workspace_id=$3 AND user_id=$4`, uuid.NewString(), c.fixture.Tenant, w.ID, c.viewer.Me.User.ID, c.role, foreignNote)
	if e == nil {
		t.Fatal("database accepted foreign resource parent")
	}
	_, e = c.fixture.PostgreSQL.Exec(ctx, `INSERT INTO iam_role_proto_actions(tenant_id,workspace_id,role_id,action) VALUES($1,$2,$3,'platform.admin')`, c.fixture.Tenant, w.ID, c.role)
	if e == nil {
		t.Fatal("unknown action admitted")
	}
	if _, e = s.candidateExplain(ctx, c.viewer, w.ID, c.first.ID, "platform.admin", uuid.NewString()); e == nil {
		t.Fatal("unknown explain action admitted")
	}
	var membershipVersion int64
	var oldAuthority string
	if e = c.fixture.PostgreSQL.QueryRow(ctx, `SELECT version,authority_id::text FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2`, w.ID, c.viewer.Me.User.ID).Scan(&membershipVersion, &oldAuthority); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeMember(ctx, c.admin, w.ID, c.viewer.Me.User.ID, "", membershipVersion, "Remove synthetic membership", uuid.NewString(), true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AddMember(ctx, c.admin, w.ID, c.viewerEmail, "viewer", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	var replacement string
	var retained int
	if e = c.fixture.PostgreSQL.QueryRow(ctx, `SELECT authority_id::text FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2`, w.ID, c.viewer.Me.User.ID).Scan(&replacement); e != nil || replacement == oldAuthority {
		t.Fatal("membership lifetime was reused", e)
	}
	if e = c.fixture.PostgreSQL.QueryRow(ctx, `SELECT count(*) FROM iam_role_proto_grants WHERE authority_id=$1`, oldAuthority).Scan(&retained); e != nil || retained != 0 {
		t.Fatal("old grant outlived membership", e)
	}
	n, e = s.ChangeNote(ctx, c.viewer, w.ID, c.first.ID, "No revival", "No revival", 3, uuid.NewString(), false)
	candidateWant(t, e, "ACCESS_DENIED")
	candidateEmpty(t, n)
}

type candidateObservedStore struct {
	transaction.Store
	name    string
	started chan int
}

func (s candidateObservedStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		if _, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT set_config('application_name',$1,true)`, s.name)); e != nil {
			return e
		}
		var pid int
		if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT pg_backend_pid()`)).Scan(&pid); e != nil {
			return e
		}
		select {
		case s.started <- pid:
		default:
		}
		return fn(ctx, q)
	})
}
func (c *candidateFixture) observed(name string) (*Service, <-chan int) {
	started := make(chan int, 1)
	accounts := &identity.Service{Storage: candidateObservedStore{Store: c.fixture.Store, name: name, started: started}, Config: c.service.Identity.Config}
	return &Service{Identity: accounts}, started
}
func candidatePID(t *testing.T, ctx context.Context, started <-chan int) int {
	t.Helper()
	select {
	case pid := <-started:
		return pid
	case <-ctx.Done():
		t.Fatal("transaction did not start")
		return 0
	}
}
func candidateObserveWait(t *testing.T, c *candidateFixture, blocked, holder int) {
	t.Helper()
	conn, e := pgx.ConnectConfig(c.ctx, c.fixture.PostgreSQL.Config().ConnConfig.Copy())
	if e != nil {
		t.Fatal("independent observer unavailable")
	}
	defer conn.Close(context.Background())
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		e = conn.QueryRow(c.ctx, `SELECT COALESCE((SELECT wait_event_type='Lock' AND $2=ANY(pg_blocking_pids(pid)) FROM pg_stat_activity WHERE pid=$1),false)`, blocked, holder).Scan(&waiting)
		if e != nil {
			t.Fatal("server wait observation failed")
		}
		if waiting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expected actual server lock edge was not observed")
}
func candidatePause(t *testing.T, ctx context.Context, key candidatePauseKey) (context.Context, <-chan int, func()) {
	t.Helper()
	entered := make(chan int, 1)
	release := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	t.Cleanup(open)
	pause := candidatePauseFunc(func(ctx context.Context, q transaction.Executor) error {
		var pid int
		if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT pg_backend_pid()`)).Scan(&pid); e != nil {
			return e
		}
		entered <- pid
		select {
		case <-release:
			return ctx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	return context.WithValue(ctx, key, pause), entered, open
}

type candidateNoteResult struct {
	note Note
	err  error
}
type candidateGrantResult struct {
	grant candidateGrant
	err   error
}

func TestScopedRoleCandidateActualWriteGrantOrdering(t *testing.T) {
	for _, grantFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "grant-first", false: "note-first"}[grantFirst], func(t *testing.T) {
			c := newCandidateFixture(t)
			writer, writerStarted := c.observed("iam107-note-writer")
			editor, editorStarted := c.observed("iam107-grant-editor")
			writerRequest, editorRequest := uuid.NewString(), uuid.NewString()
			writes := make(chan candidateNoteResult, 1)
			edits := make(chan candidateGrantResult, 1)
			pauseKey := candidateNotePause
			if grantFirst {
				pauseKey = candidateGrantPause
			}
			paused, entered, release := candidatePause(t, c.ctx, pauseKey)
			var writeCtx, editCtx context.Context = c.ctx, c.ctx
			if grantFirst {
				editCtx = paused
			} else {
				writeCtx = paused
			}
			startWrite := func() {
				go func() {
					n, e := writer.ChangeNote(writeCtx, c.viewer, c.workspace.ID, c.first.ID, "Ordered update", "Ordered body", 1, writerRequest, false)
					writes <- candidateNoteResult{n, e}
				}()
			}
			startEdit := func() {
				go func() {
					g, e := editor.candidateSetGrantActive(editCtx, c.admin, c.workspace.ID, c.grant, 1, false, editorRequest)
					edits <- candidateGrantResult{g, e}
				}()
			}
			if grantFirst {
				startEdit()
			} else {
				startWrite()
			}
			holder := candidatePID(t, c.ctx, entered)
			if grantFirst {
				candidatePID(t, c.ctx, editorStarted)
				startWrite()
				candidateObserveWait(t, c, candidatePID(t, c.ctx, writerStarted), holder)
			} else {
				candidatePID(t, c.ctx, writerStarted)
				startEdit()
				candidateObserveWait(t, c, candidatePID(t, c.ctx, editorStarted), holder)
			}
			release()
			var wr candidateNoteResult
			var ed candidateGrantResult
			select {
			case wr = <-writes:
			case <-c.ctx.Done():
				t.Fatal("writer did not finish")
			}
			select {
			case ed = <-edits:
			case <-c.ctx.Done():
				t.Fatal("grant edit did not finish")
			}
			if ed.err != nil || ed.grant.Active || ed.grant.Version != 2 || c.events(t, editorRequest) != 1 {
				t.Fatal("grant outcome was not exact", ed.err)
			}
			if grantFirst {
				candidateWant(t, wr.err, "ACCESS_DENIED")
				candidateEmpty(t, wr.note)
				if c.noteVersion(t, c.first.ID) != 1 || c.events(t, writerRequest) != 0 {
					t.Fatal("revoked waiter changed note or audit")
				}
			} else {
				if wr.err != nil || wr.note.Version != 2 || c.noteVersion(t, c.first.ID) != 2 || c.events(t, writerRequest) != 1 {
					t.Fatal("winning write was not committed exactly once", wr.err)
				}
			}
			version := c.noteVersion(t, c.first.ID)
			n, e := c.service.ChangeNote(c.ctx, c.viewer, c.workspace.ID, c.first.ID, "Revoked", "Revoked", version, uuid.NewString(), false)
			candidateWant(t, e, "ACCESS_DENIED")
			candidateEmpty(t, n)
		})
	}
}
func TestScopedRoleCandidateRollbackCancellationAndDelegation(t *testing.T) {
	c := newCandidateFixture(t)
	s, ctx, w := c.service, c.ctx, c.workspace
	t.Run("audit-refusal-and-retry", func(t *testing.T) {
		c.exec(t, `CREATE FUNCTION iam_role_proto_audit_refusal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action IN ('workspace.note_updated','workspace.role_grant_changed') THEN RAISE EXCEPTION 'IAM_ROLE_AUDIT_TEST_REFUSAL' USING ERRCODE='23514'; END IF; RETURN NEW; END $$`)
		c.exec(t, `CREATE TRIGGER iam_role_proto_audit_refusal BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION iam_role_proto_audit_refusal()`)
		noteRequest, grantRequest := uuid.NewString(), uuid.NewString()
		n, e := s.ChangeNote(ctx, c.viewer, w.ID, c.first.ID, "Must rollback", "Must rollback", 1, noteRequest, false)
		if e == nil {
			t.Fatal("audit failure returned success")
		}
		candidateEmpty(t, n)
		grant, e := s.candidateSetGrantActive(ctx, c.admin, w.ID, c.grant, 1, false, grantRequest)
		if e == nil || grant.ID != "" {
			t.Fatal("grant audit failure returned result")
		}
		var active bool
		var version int64
		if e = c.fixture.PostgreSQL.QueryRow(ctx, `SELECT active,version FROM iam_role_proto_grants WHERE id=$1`, c.grant).Scan(&active, &version); e != nil || !active || version != 1 || c.noteVersion(t, c.first.ID) != 1 || c.events(t, noteRequest) != 0 || c.events(t, grantRequest) != 0 {
			t.Fatal("failed append did not rollback exact state", e)
		}
		c.exec(t, `DROP TRIGGER iam_role_proto_audit_refusal ON audit_events`)
		c.exec(t, `DROP FUNCTION iam_role_proto_audit_refusal()`)
		n, e = s.ChangeNote(ctx, c.viewer, w.ID, c.first.ID, "Explicit retry", "Explicit retry", 1, uuid.NewString(), false)
		if e != nil || n.Version != 2 {
			t.Fatal("explicit retry failed", e)
		}
	})
	t.Run("cancel-actual-wait", func(t *testing.T) {
		blockerCtx, cancelBlocker := context.WithCancel(ctx)
		defer cancelBlocker()
		paused, entered, release := candidatePause(t, blockerCtx, candidateGrantPause)
		defer release()
		blocker := make(chan error, 1)
		go func() {
			_, e := s.candidateSetGrantActive(paused, c.admin, w.ID, c.grant, 1, false, uuid.NewString())
			blocker <- e
		}()
		holder := candidatePID(t, ctx, entered)
		writer, started := c.observed("iam107-canceled-writer")
		writeCtx, cancelWrite := context.WithCancel(ctx)
		defer cancelWrite()
		request := uuid.NewString()
		done := make(chan candidateNoteResult, 1)
		go func() {
			n, e := writer.ChangeNote(writeCtx, c.viewer, w.ID, c.first.ID, "Canceled", "Canceled", 2, request, false)
			done <- candidateNoteResult{n, e}
		}()
		candidateObserveWait(t, c, candidatePID(t, ctx, started), holder)
		cancelWrite()
		select {
		case r := <-done:
			if r.err == nil {
				t.Fatal("canceled write succeeded")
			}
			candidateEmpty(t, r.note)
		case <-time.After(time.Second):
			t.Fatal("canceled writer did not terminate")
		}
		cancelBlocker()
		release()
		if e := <-blocker; e == nil {
			t.Fatal("canceled blocker committed")
		}
		if c.noteVersion(t, c.first.ID) != 2 || c.events(t, request) != 0 {
			t.Fatal("canceled operation changed note/audit")
		}
		n, e := s.ChangeNote(ctx, c.viewer, w.ID, c.first.ID, "After cancellation", "After cancellation", 2, uuid.NewString(), false)
		if e != nil || n.Version != 3 {
			t.Fatal("retry after cancellation failed", e)
		}
	})
	t.Run("bounded-delegation-and-unknown-action", func(t *testing.T) {
		deletionRole := c.seedRole(t, "Additional deletion", candidateNoteDelete)
		deletionGrant := c.seedGrant(t, c.viewer.Me.User.ID, deletionRole, c.first.ID)
		c.exec(t, `UPDATE iam_role_proto_grants SET active=false WHERE id=$1`, deletionGrant)
		c.exec(t, `DELETE FROM iam_role_proto_actions WHERE role_id=$1 AND action=$2`, c.ownerRole, candidateNoteDelete)
		c.exec(t, `UPDATE iam_role_proto_roles SET version=version+1 WHERE id=$1`, c.ownerRole)
		out, e := s.candidateSetGrantActive(ctx, c.owner, w.ID, deletionGrant, 1, true, uuid.NewString())
		candidateWant(t, e, "ACCESS_DENIED")
		if out.ID != "" {
			t.Fatal("delegation refusal returned grant")
		}
		if _, e = s.candidateSetGrantActive(ctx, c.admin, w.ID, deletionGrant, 1, true, uuid.NewString()); e != nil {
			t.Fatal("authorized explicit grant activation failed", e)
		}
		stale, e := s.candidateSetGrantActive(ctx, c.admin, w.ID, deletionGrant, 1, false, uuid.NewString())
		candidateWant(t, e, "VERSION_CONFLICT")
		if stale.ID != "" {
			t.Fatal("stale edit returned grant")
		}
	})
	t.Run("bounded-grant-provenance", func(t *testing.T) {
		for i := 0; i < candidateMaximumGrants; i++ {
			role := c.seedRole(t, "Bounded role", candidateNoteUpdate)
			c.seedGrant(t, c.viewer.Me.User.ID, role, c.first.ID)
		}
		out, e := s.candidateExplain(ctx, c.viewer, w.ID, c.first.ID, candidateNoteUpdate, uuid.NewString())
		candidateWant(t, e, "STORAGE_UNAVAILABLE")
		if out.Allowed || len(out.Grants) != 0 {
			t.Fatal("capped explain exposed partial allow")
		}
		n, e := s.ChangeNote(ctx, c.viewer, w.ID, c.first.ID, "Capped", "Capped", 3, uuid.NewString(), false)
		candidateWant(t, e, "STORAGE_UNAVAILABLE")
		candidateEmpty(t, n)
	})
}

func TestScopedRoleCandidateCommitUncertainty(t *testing.T) {
	for _, operation := range []string{"note", "grant", "explain"} {
		t.Run(operation, func(t *testing.T) {
			for _, mode := range []commitfault.Mode{commitfault.DropBeforeCommit, commitfault.DropCommitAcknowledgement} {
				t.Run(map[commitfault.Mode]string{commitfault.DropBeforeCommit: "not-forwarded", commitfault.DropCommitAcknowledgement: "ack-suppressed"}[mode], func(t *testing.T) {
					c := newCandidateFixture(t)
					cfg := c.fixture.PostgreSQL.Config()
					if cfg.ConnConfig.TLSConfig != nil || len(cfg.ConnConfig.Fallbacks) != 0 || cfg.ConnConfig.Host != "127.0.0.1" {
						t.Fatal("explicit plaintext loopback fixture required; never downgrade TLS")
					}
					proxy, e := commitfault.NewPlaintextPostgreSQL(cfg.ConnConfig.Host, cfg.ConnConfig.Port)
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
					forwarded := cfg.Copy()
					forwarded.ConnConfig.Port = proxy.Port()
					forwarded.MaxConns = 2
					pool, e := pgxpool.NewWithConfig(c.ctx, forwarded)
					if e != nil {
						t.Fatal("forwarding pool unavailable")
					}
					t.Cleanup(pool.Close)
					if e = pool.Ping(c.ctx); e != nil {
						t.Fatal("forwarding startup refused")
					}
					store, e := transaction.FromPostgreSQL(pool)
					if e != nil {
						t.Fatal(e)
					}
					service := &Service{Identity: &identity.Service{Storage: store, Config: c.service.Identity.Config}}
					request := uuid.NewString()
					if e = proxy.Arm(mode); e != nil {
						t.Fatal(e)
					}
					switch operation {
					case "note":
						out, e := service.ChangeNote(c.ctx, c.viewer, c.workspace.ID, c.first.ID, "Unknown outcome", "Unknown outcome", 1, request, false)
						candidateWant(t, e, string(transaction.CommitUnknown))
						candidateEmpty(t, out)
					case "grant":
						out, e := service.candidateSetGrantActive(c.ctx, c.admin, c.workspace.ID, c.grant, 1, false, request)
						candidateWant(t, e, string(transaction.CommitUnknown))
						if out.ID != "" {
							t.Fatal("unknown grant commit returned data")
						}
					case "explain":
						out, e := service.candidateExplain(c.ctx, c.viewer, c.workspace.ID, c.first.ID, candidateNoteUpdate, request)
						candidateWant(t, e, string(transaction.CommitUnknown))
						if out.Allowed || out.PlatformAuthority || len(out.Grants) != 0 {
							t.Fatal("unknown explain commit returned authority")
						}
					}
					committed := mode == commitfault.DropCommitAcknowledgement
					select {
					case actual := <-proxy.Outcomes():
						if actual.Mode != mode || !actual.Suppressed || actual.Forwarded != committed || actual.ServerOK != committed {
							t.Fatal("wire observation did not prove selected fault")
						}
					case <-c.ctx.Done():
						t.Fatal("wire observation absent")
					}
					expectedNote := int64(1)
					expectedGrant := int64(1)
					expectedActive := true
					expectedEvents := 0
					if committed {
						if operation == "note" {
							expectedNote = 2
							expectedEvents = 1
						}
						if operation == "grant" {
							expectedGrant = 2
							expectedActive = false
							expectedEvents = 1
						}
					}
					var grantVersion int64
					var active bool
					if e = c.fixture.PostgreSQL.QueryRow(c.ctx, `SELECT version,active FROM iam_role_proto_grants WHERE id=$1`, c.grant).Scan(&grantVersion, &active); e != nil {
						t.Fatal(e)
					}
					if c.noteVersion(t, c.first.ID) != expectedNote || grantVersion != expectedGrant || active != expectedActive || c.events(t, request) != expectedEvents || proxy.Commits() != 1 {
						t.Fatal("unknown outcome had duplicate or non-atomic durable effects")
					}
					// The direct readback above precedes a new explicit intent/revision. The
					// failed transport never retries or releases its computed response.
					switch operation {
					case "note":
						out, e := c.service.ChangeNote(c.ctx, c.viewer, c.workspace.ID, c.first.ID, "Explicit new intent", "Explicit new intent", expectedNote, uuid.NewString(), false)
						if e != nil || out.Version != expectedNote+1 {
							t.Fatal("explicit new revision failed", e)
						}
					case "grant":
						out, e := c.service.candidateSetGrantActive(c.ctx, c.admin, c.workspace.ID, c.grant, expectedGrant, false, uuid.NewString())
						if e != nil || out.Version != expectedGrant+1 {
							t.Fatal("explicit new grant revision failed", e)
						}
					case "explain":
						out, e := c.service.candidateExplain(c.ctx, c.viewer, c.workspace.ID, c.first.ID, candidateNoteUpdate, uuid.NewString())
						if e != nil || !out.Allowed {
							t.Fatal("fresh explain retry failed", e)
						}
					}
					if proxy.Commits() != 1 {
						t.Fatal("original unknown transport was retried")
					}
					t.Logf("actual PostgreSQL operation=%s mode=%d commit_commands=1 durable_audit_events=%d", operation, mode, expectedEvents)
				})
			}
		})
	}
}

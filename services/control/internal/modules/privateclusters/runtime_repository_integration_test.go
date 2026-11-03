//go:build repository_integration && privatecluster_integration

package privateclusters

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

type clusterStory struct {
	t   *testing.T
	f   repositoryfixture.Fixture
	ctx context.Context
	s   *Service
	a   identity.Auth
	api *apiFixture
}

func clusterSetup(t *testing.T) *clusterStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	t.Cleanup(cancel)
	root := os.Getenv("PROJECT_ONE_REPOSITORY_ROOT")
	if !filepath.IsAbs(root) {
		t.Fatal("explicit trimpath-independent repository root required")
	}
	if f.Store.Dialect() == transaction.MySQL {
		// repositoryfixture.New applies and verifies the active complete catalog.
		// Candidate DDL must never turn a missing deployed variant into a passing gate.
		var tables int
		e := f.Store.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('private_cluster_connections','private_cluster_runs','private_cluster_intents')`)).Scan(&tables)
		if e != nil || tables != 3 {
			t.Fatal("active complete MySQL private-cluster catalog required; candidate DDL is not acceptance", e)
		}
	}
	c, e := Load(os.Getenv("PROJECT_ONE_PRIVATE_CLUSTER_TEST_CONFIGURATION"))
	if e != nil {
		t.Fatal("actual protected manufacturer image-proof configuration required", e)
	}
	if len(c.Profiles) != 1 {
		t.Fatal("exact one explicit fixture image profile required")
	}
	i := &identity.Service{Storage: f.Store, HMACKey: make([]byte, 32)}
	if _, e = rand.Read(i.HMACKey); e != nil {
		t.Fatal(e)
	}
	i.Config.Server.AdminOrigin = "https://admin.example.invalid"
	i.Config.Server.PortalOrigin = "https://portal.example.invalid"
	a := clusterAuth(t, ctx, i, f, f.User, "admin")
	key, trust, e := licensewire.GenerateKey("explicit-private-cluster-sql-fixture")
	if e != nil {
		t.Fatal(e)
	}
	l, e := licensing.New(i, trust, licensewire.DefaultPolicy(), func() time.Time { return time.Now().UTC() })
	if e != nil {
		t.Fatal(e)
	}
	i.BusinessAdmission = l
	state, e := l.StatusAdmin(ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Explicit synthetic M28 SQL license", DeploymentID: state.DeploymentID, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), GraceUntil: now.Add(2 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
	artifact, e := licensewire.Issue(claims, key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = l.Import(ctx, a, licensing.ImportInput{Artifact: string(artifact), Reason: "Actual signed synthetic fixture license"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	p := c.Profiles[0]
	p.WorkspaceID = f.Workspace
	p.InstallationID = state.DeploymentID
	api, p := newAPIFixture(t, c, p)
	c.Profiles = []Profile{p}
	s, e := New(i, &workspaces.Service{Identity: i}, l, c)
	if e != nil {
		t.Fatal(e)
	}
	remote, e := s.client(ctx, workspaces.Workspace{ID: f.Workspace, TenantID: f.Tenant}, p.ID)
	if e != nil {
		t.Fatal("actual manufacturer approval prerequisite", e)
	}
	api.imageConfig = remote.approval.Asset.ImageConfigDigest
	remote.close()
	return &clusterStory{t, f, ctx, s, a, api}
}
func clusterAuth(t *testing.T, ctx context.Context, i *identity.Service, f repositoryfixture.Fixture, user, audience string) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = i.IssueSessionWithin(ctx, q, user, 1, audience, "", uuid.NewString(), nil)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	a, e := i.Authenticate(ctx, issued.Token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func (g *clusterStory) mutation(revision int64) Mutation {
	return Mutation{WorkspaceID: g.f.Workspace, Key: uuid.NewString(), RequestID: uuid.NewString(), Reason: "Actual selected repository with explicit K8s API double", ExpectedRevision: revision}
}
func (g *clusterStory) bind() Connection {
	g.t.Helper()
	c, e := g.s.Bind(g.ctx, g.a, g.mutation(0), g.s.config.Profiles[0].ID)
	if e != nil {
		g.t.Fatal(e)
	}
	return c
}
func (g *clusterStory) start(c Connection) Run {
	g.t.Helper()
	r, e := g.s.Start(g.ctx, g.a, g.mutation(c.Revision), c.ID)
	if e != nil {
		g.t.Fatal(e)
	}
	return r
}
func (g *clusterStory) action(c Connection, r Run, action string) Run {
	g.t.Helper()
	out, e := g.s.Action(g.ctx, g.a, g.mutation(r.Revision), c.ID, r.ID, action)
	if e != nil {
		g.t.Fatal(action, e)
	}
	return out
}
func (g *clusterStory) count(table string) int {
	g.t.Helper()
	allowed := map[string]bool{"private_cluster_connections": true, "private_cluster_runs": true, "private_cluster_intents": true, "audit_events": true}
	if !allowed[table] {
		g.t.Fatal("exact owned table required")
	}
	var n int
	if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(g.f.Store.Dialect(), "SELECT COUNT(*) FROM "+table)).Scan(&n); e != nil {
		g.t.Fatal(e)
	}
	return n
}
func (g *clusterStory) exec(pg, my string, args ...any) {
	g.t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(pg, my, args...)); e != nil {
		g.t.Fatal(e)
	}
}

// Only ACK/failure instrumentation is doubled. The transaction, callback,
// constraint failure/rollback and acknowledged COMMIT remain actual database work.
type clusterFaultStore struct {
	transaction.Store
	table   string
	unknown bool
	armed   atomic.Bool
}

func (f *clusterFaultStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	injected := false
	e := f.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		var before, after int
		sql := transaction.SQL(q.Dialect(), "SELECT COUNT(*) FROM "+f.table)
		if e := q.QueryRow(ctx, sql).Scan(&before); e != nil {
			return e
		}
		if e := fn(ctx, q); e != nil {
			return e
		}
		if e := q.QueryRow(ctx, sql).Scan(&after); e != nil {
			return e
		}
		if after > before && f.armed.CompareAndSwap(true, false) {
			injected = true
			if !f.unknown {
				_, e := q.Exec(ctx, transaction.SQL(q.Dialect(), `INSERT INTO audit_events(id,audience,action,outcome,request_id) VALUES('invalid','admin','private_cluster.real_constraint','invalid','invalid')`))
				return e
			}
		}
		return nil
	})
	if e == nil && injected && f.unknown {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return e
}
func TestActualPairedPrivateClusterRuntime(t *testing.T) {
	t.Run("bind_replay_and_current_scope", func(t *testing.T) {
		g := clusterSetup(t)
		m := g.mutation(0)
		c, e := g.s.Bind(g.ctx, g.a, m, g.s.config.Profiles[0].ID)
		if e != nil {
			t.Fatal(e)
		}
		before, audits := g.count("private_cluster_intents"), g.count("audit_events")
		again, e := g.s.Bind(g.ctx, g.a, m, c.ProfileID)
		if e != nil || again.ID != c.ID || g.count("private_cluster_intents") != before || g.count("audit_events") != audits {
			t.Fatal("replay mutated real rows/audit", e)
		}
		m.Reason = "Changed intent"
		if out, e := g.s.Bind(g.ctx, g.a, m, c.ProfileID); e == nil || !reflect.DeepEqual(out, Connection{}) {
			t.Fatal("fingerprint or zero output")
		}
		g.api.mu.Lock()
		g.api.wrongScope = true
		g.api.mu.Unlock()
		if out, e := g.s.Change(g.ctx, g.a, g.mutation(c.Revision), c.ID, "observe"); e == nil || !reflect.DeepEqual(out, Connection{}) {
			t.Fatal("foreign namespace UID observed")
		}
	})
	t.Run("actual_ledger_create_proof_and_cleanup", func(t *testing.T) {
		g := clusterSetup(t)
		c := g.bind()
		r := g.start(c)
		if r.Phase != "observing" || r.JobUID == nil || r.LeaseUID == nil {
			t.Fatal("actual intents and observed double IDs")
		}
		r = g.action(c, r, "reconcile")
		if r.Result == nil || *r.Result != "succeeded" {
			t.Fatal("explicit double proof protocol")
		}
		p, e := g.s.Proof(g.ctx, g.a, g.f.Workspace, c.ID, r.ID)
		if e != nil || p.ObservedImageID != p.ConfigDigest {
			t.Fatal(e)
		}
		g.api.pendingDeletes()
		r = g.action(c, r, "cleanup")
		if r.Phase != "job_delete_pending" {
			t.Fatal("delete acknowledgement became absence")
		}
		r = g.action(c, r, "reconcile")
		if r.Phase != "job_delete_pending" {
			t.Fatal("terminating owned Job became released")
		}
		g.api.physicalAbsence(true)
		r = g.action(c, r, "reconcile")
		if r.Phase != "job_absent" {
			t.Fatal("actual inventory absence")
		}
		r = g.action(c, r, "cleanup")
		if r.Phase != "lease_delete_pending" {
			t.Fatal("lease acknowledgement became absence")
		}
		r = g.action(c, r, "reconcile")
		if r.Phase != "lease_delete_pending" {
			t.Fatal("terminating Lease became released")
		}
		g.api.physicalAbsence(false)
		r = g.action(c, r, "reconcile")
		if r.Phase != "cleaned" || r.Result == nil || *r.Result != "succeeded" {
			t.Fatal("terminal proof lost during cleanup")
		}
		stored, e := g.s.Get(g.ctx, g.a, g.f.Workspace, c.ID)
		if e != nil || stored.ActiveRunID != nil {
			t.Fatal(e)
		}
		if e = g.s.VerifyStored(g.ctx); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("audit_constraint_rolls_back_binding", func(t *testing.T) {
		g := clusterSetup(t)
		before := g.count("audit_events")
		fault := &clusterFaultStore{Store: g.f.Store, table: "private_cluster_connections"}
		fault.armed.Store(true)
		g.s.identity.Storage = fault
		out, e := g.s.Bind(g.ctx, g.a, g.mutation(0), g.s.config.Profiles[0].ID)
		if e == nil || !reflect.DeepEqual(out, Connection{}) || g.count("private_cluster_connections") != 0 || g.count("private_cluster_intents") != 0 || g.count("audit_events") != before {
			t.Fatal("real owning rollback/zero projection", e)
		}
	})
	t.Run("committed_ack_unknown_never_replays_create", func(t *testing.T) {
		g := clusterSetup(t)
		c := g.bind()
		fault := &clusterFaultStore{Store: g.f.Store, table: "private_cluster_runs", unknown: true}
		fault.armed.Store(true)
		g.s.identity.Storage = fault
		m := g.mutation(c.Revision)
		out, e := g.s.Start(g.ctx, g.a, m, c.ID)
		if e == nil || !reflect.DeepEqual(out, Run{}) || g.count("private_cluster_runs") != 1 {
			t.Fatal("real committed ACK unknown/zero projection", e)
		}
		r, e := g.s.Start(g.ctx, g.a, m, c.ID)
		if e != nil || r.Phase != "prepared" || apiCount(g.api, 0) != 0 || apiCount(g.api, 1) != 0 {
			t.Fatal("replay repeated an external mutation", e)
		}
		r = g.action(c, r, "continue")
		if r.Phase != "lease_acquired" {
			t.Fatal("explicit known continuation")
		}
		r = g.action(c, r, "cancel")
		r = g.action(c, r, "cleanup")
		r = g.action(c, r, "reconcile")
		if r.Phase != "cleaned" {
			t.Fatal("explicit original cleanup")
		}
	})
	t.Run("lost_job_response_get_only_adopts_original", func(t *testing.T) {
		g := clusterSetup(t)
		c := g.bind()
		g.api.behavior(true, false, false, false)
		m := g.mutation(c.Revision)
		out, e := g.s.Start(g.ctx, g.a, m, c.ID)
		if e == nil || !reflect.DeepEqual(out, Run{}) {
			t.Fatal("lost HTTP ACK projected success")
		}
		r, e := g.s.Start(g.ctx, g.a, m, c.ID)
		if e != nil || r.Phase != "job_pending" {
			t.Fatal(e)
		}
		_, posts, _ := g.api.counts()
		r = g.action(c, r, "reconcile")
		if r.Result == nil || *r.Result != "succeeded" || apiCount(g.api, 1) != posts {
			t.Fatal("GET-only reconciliation changed effects")
		}
	})
	t.Run("unknown_absent_keeps_capacity", func(t *testing.T) {
		g := clusterSetup(t)
		c := g.bind()
		g.api.behavior(true, true, false, false)
		m := g.mutation(c.Revision)
		_, e := g.s.Start(g.ctx, g.a, m, c.ID)
		if e == nil {
			t.Fatal("unknown absent failed open")
		}
		r, e := g.s.Start(g.ctx, g.a, m, c.ID)
		if e != nil {
			t.Fatal(e)
		}
		_, posts, _ := g.api.counts()
		if out, e := g.s.Action(g.ctx, g.a, g.mutation(r.Revision), c.ID, r.ID, "reconcile"); e == nil || !reflect.DeepEqual(out, Run{}) || apiCount(g.api, 1) != posts {
			t.Fatal("unknown absent reissued Job")
		}
		stored, e := g.s.Get(g.ctx, g.a, g.f.Workspace, c.ID)
		if e != nil || stored.ActiveRunID == nil {
			t.Fatal("uncertain capacity released")
		}
	})
	t.Run("retained_session_deletion_blocks_origin_allows_admin_cleanup", func(t *testing.T) {
		g := clusterSetup(t)
		c := g.bind()
		r := g.start(c)
		g.exec(`DELETE FROM sessions WHERE id=$1`, `DELETE FROM sessions WHERE id=?`, g.a.SessionID)
		if out, e := g.s.Action(g.ctx, g.a, g.mutation(r.Revision), c.ID, r.ID, "reconcile"); e == nil || !reflect.DeepEqual(out, Run{}) {
			t.Fatal("deleted origin revived")
		}
		g.a = clusterAuth(t, g.ctx, g.s.identity, g.f, g.f.User, "admin")
		r = g.action(c, r, "cancel")
		r = g.action(c, r, "reconcile")
		r = g.action(c, r, "cleanup")
		r = g.action(c, r, "reconcile")
		if r.Phase != "cleaned" {
			t.Fatal("current Admin could not clean retained orphan")
		}
	})
	t.Run("restore_fence_and_immutable_constraints", func(t *testing.T) {
		g := clusterSetup(t)
		c := g.bind()
		r := g.start(c)
		if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			return g.s.InvalidateAuthoritiesWithin(ctx, q)
		}); e != nil {
			t.Fatal(e)
		}
		stored, e := g.s.Get(g.ctx, g.a, g.f.Workspace, c.ID)
		if e != nil || stored.State != "restore_fenced" || stored.ActiveRunID == nil {
			t.Fatal("restore lost physical fence")
		}
		if out, e := g.s.Start(g.ctx, g.a, g.mutation(stored.Revision), c.ID); e == nil || !reflect.DeepEqual(out, Run{}) {
			t.Fatal("restore silently revived admission")
		}
		_, e = g.f.Store.Exec(g.ctx, g.f.Statement(`UPDATE private_cluster_runs SET created_session_id=$1,revision=revision+1 WHERE id=$2`, `UPDATE private_cluster_runs SET created_session_id=?,revision=revision+1 WHERE id=?`, uuid.NewString(), r.ID))
		if e == nil {
			t.Fatal("immutable origin schema guard absent")
		}
		if _, e = g.f.Store.Exec(g.ctx, g.f.Statement(`DELETE FROM private_cluster_runs WHERE id=$1`, `DELETE FROM private_cluster_runs WHERE id=?`, r.ID)); e == nil {
			t.Fatal("immutable run history deleted")
		}
		if e = g.s.VerifyStored(g.ctx); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("namespace_concurrency_and_foreign_cleanup_refusal", func(t *testing.T) {
		g := clusterSetup(t)
		c := g.bind()
		var wg sync.WaitGroup
		var success atomic.Int32
		for n := 0; n < 2; n++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, e := g.s.Start(g.ctx, g.a, g.mutation(c.Revision), c.ID); e == nil {
					success.Add(1)
				}
			}()
		}
		wg.Wait()
		if success.Load() != 1 || g.count("private_cluster_runs") != 1 || apiCount(g.api, 1) != 1 {
			t.Fatal("concurrent capacity duplicated")
		}
		stored, e := g.s.Get(g.ctx, g.a, g.f.Workspace, c.ID)
		if e != nil || stored.ActiveRunID == nil {
			t.Fatal(e)
		}
		r, e := g.s.GetRun(g.ctx, g.a, g.f.Workspace, c.ID, *stored.ActiveRunID)
		if e != nil {
			t.Fatal(e)
		}
		g.api.mu.Lock()
		g.api.foreign = true
		g.api.mu.Unlock()
		_, _, before := g.api.counts()
		if _, e = g.s.Action(g.ctx, g.a, g.mutation(r.Revision), c.ID, r.ID, "cancel"); e == nil || apiCount(g.api, 2) != before {
			t.Fatal("foreign resource was deleted")
		}
	})
	t.Run("portal_membership_privacy_and_proof_refusal", func(t *testing.T) {
		g := clusterSetup(t)
		c := g.bind()
		r := g.start(c)
		g.exec(`INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'viewer')`, `INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES(?,?,?,'viewer')`, g.f.Tenant, g.f.Workspace, g.f.User)
		portal := clusterAuth(t, g.ctx, g.s.identity, g.f, g.f.User, "portal")
		if _, e := g.s.GetRun(g.ctx, portal, g.f.Workspace, c.ID, r.ID); e != nil {
			t.Fatal(e)
		}
		if out, e := g.s.Action(g.ctx, portal, g.mutation(r.Revision), c.ID, r.ID, "cancel"); e == nil || !reflect.DeepEqual(out, Run{}) {
			t.Fatal("viewer mutated")
		}
		g.api.mu.Lock()
		g.api.wrongProof = true
		g.api.mu.Unlock()
		if out, e := g.s.Action(g.ctx, g.a, g.mutation(r.Revision), c.ID, r.ID, "reconcile"); e == nil || !reflect.DeepEqual(out, Run{}) {
			t.Fatal("wrong Pod proof accepted")
		}
		b, _ := json.Marshal(r)
		if string(b) == "" {
			t.Fatal("retained projection")
		}
	})
}

func apiCount(f *apiFixture, index int) int { a, b, c := f.counts(); return []int{a, b, c}[index] }

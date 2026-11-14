//go:build repository_integration

package plugins

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

type pairedPluginStory struct {
	f             repositoryfixture.Fixture
	ctx           context.Context
	owner         *identity.Service
	s             *Service
	admin, portal identity.Auth
	private       ed25519.PrivateKey
}

func pairedPluginSetup(t *testing.T) pairedPluginStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	t.Cleanup(cancel)
	public, private, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := wire.KeyID(public)
	owner := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		owner.Storage = nil
		owner.DB = f.PostgreSQL
	}
	owner.Config.Plugins = config.PluginConfiguration{Enabled: true, TrustedKeys: []config.PluginTrustKey{{KeyID: id, PublicKey: base64.StdEncoding.EncodeToString(public)}}}
	p := owner.Config.Policy()
	p.Plugins.MaintenanceSeconds = 3600
	p.Plugins.MaintenanceBatch = 2
	owner.Config.Runtime = &p
	w := &workspaces.Service{DB: f.PostgreSQL, Identity: owner}
	s := &Service{DB: f.PostgreSQL, Identity: owner, Workspaces: w, Models: &models.Service{DB: f.PostgreSQL, Identity: owner, Workspaces: w}}
	g := pairedPluginStory{f: f, ctx: ctx, owner: owner, s: s, private: private}
	g.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')", f.User, f.Tenant, f.Workspace)
	g.admin = g.session(t, "admin")
	g.portal = g.session(t, "portal")
	if e = s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		if e := s.Stop(ctx); e != nil {
			t.Error(e)
		}
	})
	// Actual migration14 is mandatory, including plugin immutable provenance and
	// the workflow result/proof extension. Missing MySQL tables must fail setup.
	if _, e = f.Store.Exec(ctx, g.sql("SELECT id FROM plugin_versions WHERE false", "SELECT id FROM plugin_versions WHERE false")); e != nil {
		t.Fatal("ACTUAL_PRODUCT_PLUGIN_SCHEMA_REQUIRED", e)
	}
	return g
}
func (g pairedPluginStory) sql(pg, my string, args ...any) transaction.Statement {
	return g.f.Statement(pg, my, args...)
}
func (g pairedPluginStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.sql(pg, my, args...)); e != nil {
		t.Fatal("actual isolated plugin fixture", e)
	}
}
func (g pairedPluginStory) count(t *testing.T, pg, my string, args ...any) int {
	t.Helper()
	var n int
	if e := g.f.Store.QueryRow(g.ctx, g.sql(pg, my, args...)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func (g pairedPluginStory) session(t *testing.T, audience string) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = g.owner.IssueSessionWithin(ctx, q, g.f.User, 1, audience, "", uuid.NewString(), nil)
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
func (g pairedPluginStory) input(t *testing.T, release string) ImportRequest {
	t.Helper()
	raw, e := wire.Sign(wire.Manifest{PluginID: wire.PluginID, Version: release, ABI: wire.ABI, Executor: wire.Executor, Capabilities: []string{wire.Capability}, License: wire.License}, g.private, g.owner.Config.Policy().Plugins.PackageLimits())
	if e != nil {
		t.Fatal(e)
	}
	return ImportRequest{Capsule: base64.StdEncoding.EncodeToString(raw), SHA256: wire.Digest(raw), Reason: "Actual synthetic signed plugin"}
}
func (g pairedPluginStory) installed(t *testing.T, release string) (Version, Grant) {
	t.Helper()
	v, e := g.s.Import(g.ctx, g.admin, g.input(t, release), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	v, e = g.s.Install(g.ctx, g.admin, v.ID, RevisionReason{v.Revision, "Install exact signed version"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	grant, e := g.s.Grant(g.ctx, g.admin, g.f.Workspace, GrantRequest{v.ID, "Grant exact installed version"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return v, grant
}
func pairedPluginCode(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatal("expected", code, "received", e)
	}
}
func pairedPluginVersionZero(t *testing.T, v Version, e error, code string) {
	t.Helper()
	pairedPluginCode(t, e, code)
	if !reflect.DeepEqual(v, Version{}) {
		t.Fatal("failed or unknown mutation projected version")
	}
}
func (g pairedPluginStory) invoke(s *Service, a identity.Auth, version, run string) (*CompletedModelList, error) {
	var out *CompletedModelList
	e := s.storageTransaction(g.ctx, func(ctx context.Context, q transaction.Executor) error {
		var e error
		out, e = s.ExecuteModelListWithin(ctx, q, a, g.f.Workspace, version, run, "PRIVATE_PLUGIN_QUESTION")
		if e != nil {
			return e
		}
		return s.RecheckCompletedModelListWithin(ctx, q, a, g.f.Workspace, version, run, "PRIVATE_PLUGIN_QUESTION", out)
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
func (g pairedPluginStory) recheck(version, run string, c *CompletedModelList, stored bool) error {
	return g.s.storageTransaction(g.ctx, func(ctx context.Context, q transaction.Executor) error {
		if stored {
			result, _ := json.Marshal(c.Result())
			return g.s.RecheckStoredModelListWithin(ctx, q, g.portal, g.f.Workspace, version, c.StoredProof(), result)
		}
		return g.s.RecheckCompletedModelListWithin(ctx, q, g.portal, g.f.Workspace, version, run, "PRIVATE_PLUGIN_QUESTION", c)
	})
}
func (g pairedPluginStory) model(t *testing.T, name string) string {
	t.Helper()
	id := uuid.NewString()
	raw, _ := json.Marshal(agent.ApprovedModel())
	g.exec(t, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,$4,'qwen3-0.6b',$5,$6::jsonb,'ready',$7)`, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,?,'qwen3-0.6b',?,CAST(? AS JSON),'ready',?)`, id, g.f.Tenant, g.f.Workspace, name, agent.ModelCatalogDigest(), raw, g.f.User)
	return id
}
func (g pairedPluginStory) auditFailure(t *testing.T, action string) func() {
	t.Helper()
	g.exec(t, "CREATE FUNCTION paired_plugin_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='"+action+"' THEN RAISE EXCEPTION 'explicit plugin audit failure'; END IF; RETURN NEW; END;$$", "CREATE TRIGGER paired_plugin_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action='"+action+"' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='explicit plugin audit failure'; END IF; END")
	if g.f.Store.Dialect() == transaction.PostgreSQL {
		g.exec(t, "CREATE TRIGGER paired_plugin_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION paired_plugin_audit_failure()", "")
	}
	dropped := false
	drop := func() {
		if !dropped {
			g.exec(t, "DROP TRIGGER paired_plugin_audit_failure ON audit_events", "DROP TRIGGER paired_plugin_audit_failure")
			dropped = true
		}
	}
	t.Cleanup(drop)
	return drop
}

type pairedPluginOutcomeStore struct {
	transaction.Store
	unknown bool
}

func (s *pairedPluginOutcomeStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	e := s.Store.Within(ctx, ro, fn)
	if e == nil && s.unknown {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return e
}

type pairedPluginMaintenanceStore struct {
	transaction.Store
	mu     sync.Mutex
	failed bool
}

func (s *pairedPluginMaintenanceStore) setFailure(v bool) { s.mu.Lock(); s.failed = v; s.mu.Unlock() }
func (s *pairedPluginMaintenanceStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	s.mu.Lock()
	failed := s.failed
	s.mu.Unlock()
	if failed {
		return failure("STORAGE_UNAVAILABLE")
	}
	return s.Store.Within(ctx, ro, fn)
}
func pairedPluginAwait(t *testing.T, s *Service, available bool) {
	t.Helper()
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if s.available() == available {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("bounded maintenance readiness transition")
		case <-tick.C:
		}
	}
}
func (g pairedPluginStory) wrapped(store transaction.Store) *Service {
	owner := *g.owner
	owner.DB = nil
	owner.Storage = store
	w := &workspaces.Service{Identity: &owner}
	m := &models.Service{Identity: &owner, Workspaces: w}
	g.s.lifecycleMu.Lock()
	defer g.s.lifecycleMu.Unlock()
	// No live mutex copy or second worker. The original owner alone joins it.
	return &Service{Identity: &owner, Workspaces: w, Models: m, lifecycleContext: g.s.lifecycleContext, stop: g.s.stop, joined: g.s.joined, closing: g.s.closing, maintenanceError: g.s.maintenanceError}
}
func (g pairedPluginStory) workflow(t *testing.T, version string) (string, string) {
	t.Helper()
	id, pub := uuid.NewString(), uuid.NewString()
	authority := uuid.NewString()
	// Explicit synthetic schema2 graph/row envelope; these tests exercise real
	// plugin publication/reference SQL, not workflow execution or model inference.
	start, tool, end := uuid.NewString(), uuid.NewString(), uuid.NewString()
	graph := []byte(`{"schema_version":2,"nodes":[{"id":"` + start + `","kind":"start","position":{"x":0,"y":0},"configuration":{}},{"id":"` + tool + `","kind":"tool","position":{"x":1,"y":0},"configuration":{"plugin_version_id":"` + version + `"}},{"id":"` + end + `","kind":"end","position":{"x":2,"y":0},"configuration":{}}],"edges":[{"id":"` + uuid.NewString() + `","source":"` + start + `","target":"` + tool + `"},{"id":"` + uuid.NewString() + `","source":"` + tool + `","target":"` + end + `"}]}`)
	digest := wire.Digest(graph)
	g.exec(t, `INSERT INTO workflows(id,tenant_id,workspace_id,name,name_key,execution_authority,draft_graph,draft_sha256,created_by) VALUES($1,$2,$3,'Synthetic plugin references',$1::text,$4,$5::jsonb,$6,$7)`, `INSERT INTO workflows(id,tenant_id,workspace_id,name,name_key,execution_authority,draft_graph,draft_sha256,created_by) VALUES(?,?,?,'Synthetic plugin references',?,?,CAST(? AS JSON),?,?)`, g.workflowArgs(id, authority, graph, digest)...)
	g.exec(t, `INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) VALUES($1,$2,$3,$4,1,1,$5::jsonb,$6,$7)`, `INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) VALUES(?,?,?,?,1,1,CAST(? AS JSON),?,?)`, pub, g.f.Tenant, g.f.Workspace, id, graph, digest, g.f.User)
	if e := g.s.storageTransaction(g.ctx, func(ctx context.Context, q transaction.Executor) error {
		return g.s.RecordPublicationWithin(ctx, q, g.portal, g.f.Workspace, version, id, pub)
	}); e != nil {
		t.Fatal(e)
	}
	chat := uuid.NewString()
	g.exec(t, `INSERT INTO workflow_conversations(id,tenant_id,workspace_id,workflow_id,version_id,owner_id) VALUES($1,$2,$3,$4,$5,$6)`, `INSERT INTO workflow_conversations(id,tenant_id,workspace_id,workflow_id,version_id,owner_id) VALUES(?,?,?,?,?,?)`, chat, g.f.Tenant, g.f.Workspace, id, pub, g.f.User)
	var membership string
	if e := g.f.Store.QueryRow(g.ctx, g.sql("SELECT authority_id::text FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", "SELECT authority_id FROM workspace_memberships WHERE workspace_id=? AND user_id=?", g.f.Workspace, g.f.User)).Scan(&membership); e != nil {
		t.Fatal(e)
	}
	g.exec(t, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at,result_kind) VALUES($1,$2,$3,$4,$5,1,$6,$7,'portal',$8::jsonb,$9,'pending','PRIVATE_REFERENCE_QUESTION',clock_timestamp()+interval '1 minute','model_list')`, `INSERT INTO workflow_runs(id,tenant_id,workspace_id,workflow_id,owner_id,draft_revision,execution_authority,membership_authority,requested_audience,graph_snapshot,graph_sha256,state,question,deadline_at,result_kind) VALUES(?,?,?,?,?,1,?,?,'portal',CAST(? AS JSON),?,'pending','PRIVATE_REFERENCE_QUESTION',TIMESTAMPADD(MINUTE,1,UTC_TIMESTAMP(6)),'model_list')`, uuid.NewString(), g.f.Tenant, g.f.Workspace, id, g.f.User, authority, membership, graph, digest)
	return id, pub
}
func (g pairedPluginStory) workflowArgs(id, authority string, graph []byte, digest string) []any {
	if g.f.Store.Dialect() == transaction.MySQL {
		return []any{id, g.f.Tenant, g.f.Workspace, id, authority, graph, digest, g.f.User}
	}
	return []any{id, g.f.Tenant, g.f.Workspace, authority, graph, digest, g.f.User}
}

func TestActualPairedPluginRuntime(t *testing.T) {
	t.Run("signed_import_exact_replay_conflict_and_current_trust", func(t *testing.T) {
		g := pairedPluginSetup(t)
		input := g.input(t, "1.0.0")
		key := uuid.NewString()
		v, e := g.s.Import(g.ctx, g.admin, input, key, uuid.NewString())
		if e != nil || !v.Trusted || v.State != ImportedState || v.Available {
			t.Fatal(e)
		}
		replay, e := g.s.Import(g.ctx, g.admin, input, key, uuid.NewString())
		if e != nil || replay.ID != v.ID {
			t.Fatal(e)
		}
		input.Reason = "Changed intent"
		out, e := g.s.Import(g.ctx, g.admin, input, key, uuid.NewString())
		pairedPluginVersionZero(t, out, e, "IDEMPOTENCY_CONFLICT")
		if n := g.count(t, "SELECT COUNT(*) FROM plugin_versions", "SELECT COUNT(*) FROM plugin_versions"); n != 1 {
			t.Fatal(n)
		}
		input.Capsule += "="
		out, e = g.s.Import(g.ctx, g.admin, input, uuid.NewString(), uuid.NewString())
		if e == nil || !reflect.DeepEqual(out, Version{}) {
			t.Fatal("invalid package projected")
		}
		g.owner.Config.Plugins.TrustedKeys = nil
		out, e = g.s.Install(g.ctx, g.admin, v.ID, RevisionReason{1, "Trust removed"}, uuid.NewString(), uuid.NewString())
		if e == nil || out.ID != "" {
			t.Fatal("removed key installed")
		}
	})
	t.Run("install_exact_grants_upgrade_UTC_pages_and_strict_cursor", func(t *testing.T) {
		g := pairedPluginSetup(t)
		v, first := g.installed(t, "1.0.0")
		_, second := g.installed(t, "1.1.0")
		p, e := g.s.List(g.ctx, g.admin, url.Values{"limit": {"1"}})
		if e != nil || len(p.Items) != 1 || p.NextCursor == nil || p.Items[0].CreatedAt.Location() != time.UTC {
			t.Fatal(e)
		}
		next, e := g.s.List(g.ctx, g.admin, url.Values{"limit": {"1"}, "cursor": {*p.NextCursor}})
		if e != nil || len(next.Items) != 1 || next.Items[0].ID == p.Items[0].ID {
			t.Fatal(e)
		}
		grants, e := g.s.Grants(g.ctx, g.portal, g.f.Workspace, url.Values{})
		if e != nil || len(grants.Items) != 2 || first.PluginVersionID == second.PluginVersionID {
			t.Fatal(e)
		}
		c, e := g.s.Configuration(g.ctx, g.admin)
		if e != nil || !c.CanManage || len(c.TrustedKeyIDs) != 1 {
			t.Fatal(e)
		}
		for _, q := range []url.Values{{"cursor": {""}}, {"limit": {""}}, {"unknown": {"1"}}, {"limit": {"1", "2"}}} {
			p, e := g.s.List(g.ctx, g.admin, q)
			if e == nil || !reflect.DeepEqual(p, Page[Version]{}) {
				t.Fatal("strict page refusal")
			}
		}
		_, e = g.s.Grants(g.ctx, g.portal, uuid.NewString(), url.Values{})
		if e == nil {
			t.Fatal("cross-workspace grant discovery")
		}
		out, e := g.s.Get(g.ctx, g.portal, v.ID)
		pairedPluginVersionZero(t, out, e, "ACCESS_DENIED")
	})
	t.Run("real_model_directory_empty_safe_projection_and_borrowed_bounds", func(t *testing.T) {
		g := pairedPluginSetup(t)
		v, _ := g.installed(t, "1.0.0")
		c, e := g.invoke(g.s, g.portal, v.ID, uuid.NewString())
		if e != nil || c == nil || len(c.Result().Items) != 0 {
			t.Fatal("real empty directory", e)
		}
		id := g.model(t, "Synthetic \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629")
		c, e = g.invoke(g.s, g.portal, v.ID, uuid.NewString())
		if e != nil || len(c.Result().Items) != 1 || c.Result().Items[0].ID != id {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(c.Result())
		for _, secret := range []string{"manifest", "artifact", "storage", "PRIVATE_PLUGIN_QUESTION"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("unsafe result", secret)
			}
		}
		opaque, _ := json.Marshal(c)
		if string(opaque) != "{}" {
			t.Fatal("opaque capability serialized")
		}
		// One approved catalog per ready Workspace is a real schema invariant. Do
		// not invent a second approved base or bypass derived publication guards.
		e = g.s.storageTransaction(g.ctx, func(ctx context.Context, q transaction.Executor) error {
			items, e := g.s.Models.ReadyReferencesWithin(ctx, q, g.portal, g.f.Workspace, 0)
			if items != nil {
				t.Fatal("invalid bound projected models")
			}
			return e
		})
		pairedPluginCode(t, e, "VALIDATION_FAILED")
		pairedPluginCode(t, g.s.CheckToolWithin(g.ctx, g.f.Store, g.portal, g.f.Workspace, v.ID), "PLUGIN_TRANSACTION_REQUIRED")

	})
	t.Run("completed_capability_identity_token_run_and_model_revalidation", func(t *testing.T) {
		g := pairedPluginSetup(t)
		v, _ := g.installed(t, "1.0.0")
		model := g.model(t, "Reference")
		run := uuid.NewString()
		c, e := g.invoke(g.s, g.portal, v.ID, run)
		if e != nil {
			t.Fatal(e)
		}
		if e = g.recheck(v.ID, run, c, false); e != nil {
			t.Fatal(e)
		}
		pairedPluginCode(t, g.recheck(v.ID, uuid.NewString(), c, false), "PLUGIN_AUTHORITY_INVALID")
		changed := *c
		changed.tokenFingerprint[0] ^= 1
		pairedPluginCode(t, g.recheck(v.ID, run, &changed, false), "PLUGIN_AUTHORITY_INVALID")
		g.exec(t, "UPDATE model_versions SET state='deleted' WHERE id=$1", "UPDATE model_versions SET state='deleted' WHERE id=?", model)
		pairedPluginCode(t, g.recheck(v.ID, run, c, false), "PLUGIN_MODEL_REFERENCE_CHANGED")
		pairedPluginCode(t, g.recheck(v.ID, run, c, true), "PLUGIN_MODEL_REFERENCE_CHANGED")
	})
	t.Run("grant_revocation_fences_completion_and_reviewed_history_reenable", func(t *testing.T) {
		g := pairedPluginSetup(t)
		v, grant := g.installed(t, "1.0.0")
		run := uuid.NewString()
		c, e := g.invoke(g.s, g.portal, v.ID, run)
		if e != nil {
			t.Fatal(e)
		}
		grant, e = g.s.UpdateGrant(g.ctx, g.admin, g.f.Workspace, grant.ID, GrantUpdate{false, grant.Revision, "Revoke current authority"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		pairedPluginCode(t, g.recheck(v.ID, run, c, false), "PLUGIN_GRANT_REVOKED")
		pairedPluginCode(t, g.recheck(v.ID, run, c, true), "PLUGIN_GRANT_REVOKED")
		_, e = g.s.UpdateGrant(g.ctx, g.admin, g.f.Workspace, grant.ID, GrantUpdate{true, grant.Revision, "Explicit reviewed historical visibility"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		pairedPluginCode(t, g.recheck(v.ID, run, c, false), "PLUGIN_AUTHORITY_CHANGED")
		if e = g.recheck(v.ID, run, c, true); e != nil {
			t.Fatal("history without reinvocation", e)
		}
	})
	t.Run("actual_publication_reference_digest_uninstall_and_no_regrant", func(t *testing.T) {
		g := pairedPluginSetup(t)
		v, _ := g.installed(t, "1.0.0")
		before, e := g.s.References(g.ctx, g.admin, v.ID)
		if e != nil || before.Count != 0 {
			t.Fatal(e)
		}
		g.workflow(t, v.ID)
		refs, e := g.s.References(g.ctx, g.admin, v.ID)
		if e != nil || refs.Count != 4 {
			t.Fatal("actual four reference kinds", refs.Count, e)
		}
		raw, _ := json.Marshal(refs)
		if strings.Contains(string(raw), "PRIVATE_REFERENCE_QUESTION") {
			t.Fatal("private reference leak")
		}
		out, e := g.s.Uninstall(g.ctx, g.admin, v.ID, UninstallRequest{v.Revision, before.SHA256, true, "Stale preview"}, uuid.NewString(), uuid.NewString())
		pairedPluginVersionZero(t, out, e, "PLUGIN_REFERENCES_CHANGED")
		out, e = g.s.Uninstall(g.ctx, g.admin, v.ID, UninstallRequest{v.Revision, refs.SHA256, false, "Need referenced confirmation"}, uuid.NewString(), uuid.NewString())
		pairedPluginVersionZero(t, out, e, "PLUGIN_REFERENCED")
		out, e = g.s.Uninstall(g.ctx, g.admin, v.ID, UninstallRequest{v.Revision, refs.SHA256, true, "Explicit referenced uninstall"}, uuid.NewString(), uuid.NewString())
		if e != nil || out.State != UninstalledState {
			t.Fatal(e)
		}
		out, e = g.s.Install(g.ctx, g.admin, v.ID, RevisionReason{out.Revision, "Explicit reinstall"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		c, e := g.invoke(g.s, g.portal, v.ID, uuid.NewString())
		pairedPluginCode(t, e, "PLUGIN_GRANT_REVOKED")
		if c != nil {
			t.Fatal("reinstallation regranted")
		}
		if n := g.count(t, "SELECT COUNT(*) FROM workflow_plugin_references", "SELECT COUNT(*) FROM workflow_plugin_references"); n != 1 {
			t.Fatal("publication provenance deleted", n)
		}
	})
	t.Run("audit_failure_rolls_back_package_intent_grant_and_invocation", func(t *testing.T) {
		g := pairedPluginSetup(t)
		drop := g.auditFailure(t, "plugin.imported")
		out, e := g.s.Import(g.ctx, g.admin, g.input(t, "1.0.0"), uuid.NewString(), uuid.NewString())
		pairedPluginVersionZero(t, out, e, "STORAGE_UNAVAILABLE")
		if g.count(t, "SELECT COUNT(*) FROM plugin_versions", "SELECT COUNT(*) FROM plugin_versions") != 0 || g.count(t, "SELECT COUNT(*) FROM plugin_mutation_intents", "SELECT COUNT(*) FROM plugin_mutation_intents") != 0 {
			t.Fatal("failed import persisted")
		}
		drop()
		v, grant := g.installed(t, "1.0.0")
		drop = g.auditFailure(t, "plugin.grant_updated")
		got, e := g.s.UpdateGrant(g.ctx, g.admin, g.f.Workspace, grant.ID, GrantUpdate{false, grant.Revision, "Fault rollback"}, uuid.NewString(), uuid.NewString())
		if e == nil || !reflect.DeepEqual(got, Grant{}) {
			t.Fatal("failed grant projected")
		}
		drop()
		c, e := g.invoke(g.s, g.portal, v.ID, uuid.NewString())
		if e != nil || c == nil {
			t.Fatal("rollback lost grant", e)
		}
		drop = g.auditFailure(t, "plugin.invoked")
		c, e = g.invoke(g.s, g.portal, v.ID, uuid.NewString())
		pairedPluginCode(t, e, "STORAGE_UNAVAILABLE")
		if c != nil {
			t.Fatal("failed invocation capability")
		}
		drop()
	})
	t.Run("unknown_commit_zero_projections_and_exact_intent_reconciliation", func(t *testing.T) {
		g := pairedPluginSetup(t)
		unknown := g.wrapped(&pairedPluginOutcomeStore{Store: g.f.Store, unknown: true})
		input := g.input(t, "1.0.0")
		key := uuid.NewString()
		v, e := unknown.Import(g.ctx, g.admin, input, key, uuid.NewString())
		pairedPluginVersionZero(t, v, e, string(transaction.CommitUnknown))
		v, e = g.s.Import(g.ctx, g.admin, input, key, uuid.NewString())
		if e != nil || v.ID == "" || g.count(t, "SELECT COUNT(*) FROM plugin_versions", "SELECT COUNT(*) FROM plugin_versions") != 1 {
			t.Fatal("exact committed intent readback", e)
		}
		got, e := unknown.Get(g.ctx, g.admin, v.ID)
		pairedPluginVersionZero(t, got, e, string(transaction.CommitUnknown))
		c, e := unknown.Configuration(g.ctx, g.admin)
		pairedPluginCode(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(c, Configuration{}) {
			t.Fatal("unknown configuration")
		}
		page, e := unknown.List(g.ctx, g.admin, url.Values{})
		pairedPluginCode(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(page, Page[Version]{}) {
			t.Fatal("unknown page")
		}
		v, e = g.s.Install(g.ctx, g.admin, v.ID, RevisionReason{1, "Actual installation"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		grant, e := g.s.Grant(g.ctx, g.admin, g.f.Workspace, GrantRequest{v.ID, "Actual grant"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		completed, err := g.invoke(unknown, g.portal, v.ID, uuid.NewString())
		pairedPluginCode(t, err, string(transaction.CommitUnknown))
		if completed != nil {
			t.Fatal("outer unknown transaction published capability")
		}
		gotGrant, e := unknown.UpdateGrant(g.ctx, g.admin, g.f.Workspace, grant.ID, GrantUpdate{false, grant.Revision, "Unknown revoke"}, uuid.NewString(), uuid.NewString())
		pairedPluginCode(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(gotGrant, Grant{}) {
			t.Fatal("unknown grant")
		}
		refs, e := unknown.References(g.ctx, g.admin, v.ID)
		pairedPluginCode(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(refs, References{}) {
			t.Fatal("unknown references")
		}
	})
	t.Run("current_member_epoch_disabled_scope_context_and_store_coherence", func(t *testing.T) {
		g := pairedPluginSetup(t)
		v, _ := g.installed(t, "1.0.0")
		g.exec(t, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=? AND user_id=?", g.f.Workspace, g.f.User)
		if c, e := g.invoke(g.s, g.portal, v.ID, uuid.NewString()); e != nil || c == nil {
			t.Fatal("current viewer read-only tool", e)
		}
		g.exec(t, "DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", "DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?", g.f.Workspace, g.f.User)
		if c, e := g.invoke(g.s, g.portal, v.ID, uuid.NewString()); e == nil || c != nil {
			t.Fatal("removed membership invocation")
		}
		g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
		got, e := g.s.Get(g.ctx, g.admin, v.ID)
		pairedPluginVersionZero(t, got, e, "AUTH_SESSION_EXPIRED")
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		got, e = g.s.Get(ctx, g.admin, v.ID)
		if e == nil || got.ID != "" {
			t.Fatal("cancelled metadata")
		}
		mismatch := g.wrapped(g.f.Store)
		mismatch.Workspaces = g.s.Workspaces
		got, e = mismatch.Get(g.ctx, g.admin, v.ID)
		pairedPluginVersionZero(t, got, e, "PLUGIN_IDENTITY_REQUIRED")
	})
	t.Run("concurrent_exact_import_and_grant_revision_have_single_winner", func(t *testing.T) {
		g := pairedPluginSetup(t)
		input := g.input(t, "1.0.0")
		key := uuid.NewString()
		results := make(chan Version, 2)
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				v, e := g.s.Import(g.ctx, g.admin, input, key, uuid.NewString())
				results <- v
				errs <- e
			}()
		}
		wg.Wait()
		close(results)
		close(errs)
		var id string
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		for v := range results {
			if id != "" && id != v.ID {
				t.Fatal("duplicate exact import")
			}
			id = v.ID
		}
		v, e := g.s.Install(g.ctx, g.admin, id, RevisionReason{1, "Concurrent fixture"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		grant, e := g.s.Grant(g.ctx, g.admin, g.f.Workspace, GrantRequest{v.ID, "Current grant"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		outcomes := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := g.s.UpdateGrant(g.ctx, g.admin, g.f.Workspace, grant.ID, GrantUpdate{false, grant.Revision, "Concurrent revoke"}, uuid.NewString(), uuid.NewString())
				outcomes <- e
			}()
		}
		wg.Wait()
		close(outcomes)
		success, conflict := 0, 0
		for e := range outcomes {
			if e == nil {
				success++
			} else if e.Error() == "PLUGIN_REVISION_CONFLICT" {
				conflict++
			} else {
				t.Fatal(e)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatal(success, conflict)
		}
	})
	t.Run("immutable_provenance_configured_limits_and_restore_no_regrant", func(t *testing.T) {
		g := pairedPluginSetup(t)
		v, grant := g.installed(t, "1.0.0")
		if _, e := g.f.Store.Exec(g.ctx, g.sql("UPDATE plugin_versions SET capsule_sha256=$1 WHERE id=$2", "UPDATE plugin_versions SET capsule_sha256=? WHERE id=?", strings.Repeat("a", 64), v.ID)); e == nil {
			t.Fatal("immutable provenance bypass")
		}
		p := g.owner.Config.Policy()
		p.Plugins.MaximumVersions = 1
		g.owner.Config.Runtime = &p
		out, e := g.s.Import(g.ctx, g.admin, g.input(t, "1.1.0"), uuid.NewString(), uuid.NewString())
		pairedPluginVersionZero(t, out, e, "PLUGIN_LIMIT_EXCEEDED")
		run := uuid.NewString()
		c, e := g.invoke(g.s, g.portal, v.ID, run)
		if e != nil {
			t.Fatal(e)
		}
		sentinel := failure("PLUGIN_EXPLICIT_ROLLBACK")
		e = g.s.storageTransaction(g.ctx, func(ctx context.Context, q transaction.Executor) error {
			if e := InvalidateAuthoritiesWithin(ctx, q); e != nil {
				return e
			}
			return sentinel
		})
		pairedPluginCode(t, e, sentinel.Error())
		if e = g.recheck(v.ID, run, c, false); e != nil {
			t.Fatal("rollback changed grant", e)
		}
		if e = g.s.storageTransaction(g.ctx, InvalidateAuthoritiesWithin); e != nil {
			t.Fatal(e)
		}
		pairedPluginCode(t, g.recheck(v.ID, run, c, true), "PLUGIN_GRANT_REVOKED")
		_, e = g.s.UpdateGrant(g.ctx, g.admin, g.f.Workspace, grant.ID, GrantUpdate{true, grant.Revision, "Stale restored revision"}, uuid.NewString(), uuid.NewString())
		pairedPluginCode(t, e, "PLUGIN_REVISION_CONFLICT")
	})
	t.Run("bounded_retention_audit_unknown_zero_and_joined_lifecycle", func(t *testing.T) {
		g := pairedPluginSetup(t)
		g.installed(t, "1.0.0")
		age := g.owner.Config.Policy().Identity.IdempotencySeconds + 1
		g.exec(t, "UPDATE plugin_mutation_intents SET created_at=clock_timestamp()-make_interval(secs=>$1)", "UPDATE plugin_mutation_intents SET created_at=TIMESTAMPADD(SECOND,-?,UTC_TIMESTAMP(6))", age)
		drop := g.auditFailure(t, "plugin.intent_retention")
		n, e := g.s.Cleanup(g.ctx)
		pairedPluginCode(t, e, "STORAGE_UNAVAILABLE")
		if n != 0 || g.count(t, "SELECT COUNT(*) FROM plugin_mutation_intents", "SELECT COUNT(*) FROM plugin_mutation_intents") != 3 {
			t.Fatal("retention rollback")
		}
		drop()
		unknown := g.wrapped(&pairedPluginOutcomeStore{Store: g.f.Store, unknown: true})
		n, e = unknown.Cleanup(g.ctx)
		pairedPluginCode(t, e, string(transaction.CommitUnknown))
		if n != 0 || g.count(t, "SELECT COUNT(*) FROM plugin_mutation_intents", "SELECT COUNT(*) FROM plugin_mutation_intents") != 1 {
			t.Fatal("unknown bounded deletion")
		}
		n, e = g.s.Cleanup(g.ctx)
		if e != nil || n != 1 {
			t.Fatal("explicit remaining cleanup", n, e)
		}
		if e = g.s.VerifyStored(g.ctx); e != nil {
			t.Fatal(e)
		}
		pairedPluginCode(t, g.s.Start(g.ctx), "PLUGIN_ALREADY_STARTED")
		stop, done := context.WithTimeout(g.ctx, time.Second)
		defer done()
		if e = g.s.Stop(stop); e != nil {
			t.Fatal(e)
		}
		if g.s.available() {
			t.Fatal("stopped owner available")
		}
		if e = g.s.Start(g.ctx); e != nil {
			t.Fatal("explicit restart", e)
		}
		if g.count(t, "SELECT COUNT(*) FROM plugin_versions", "SELECT COUNT(*) FROM plugin_versions") != 1 {
			t.Fatal("retention removed provenance")
		}
	})
	t.Run("maintenance_failure_readiness_recovery_and_joined_stop", func(t *testing.T) {
		g := pairedPluginSetup(t)
		ctx, cancel := context.WithTimeout(g.ctx, 3*time.Second)
		defer cancel()
		if e := g.s.Stop(ctx); e != nil {
			t.Fatal(e)
		}
		store := &pairedPluginMaintenanceStore{Store: g.f.Store, failed: true}
		s := g.wrapped(store)
		s.stop = nil
		s.joined = nil
		s.lifecycleContext = nil
		s.closing = false
		policy := s.Identity.Config.Policy()
		policy.Plugins.MaintenanceSeconds = 1
		s.Identity.Config.Runtime = &policy
		if e := s.Start(g.ctx); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
			defer done()
			if e := s.Stop(ctx); e != nil {
				t.Error(e)
			}
		})
		pairedPluginAwait(t, s, false)
		pairedPluginCode(t, s.VerifyStored(g.ctx), "PLUGIN_UNAVAILABLE")
		store.setFailure(false)
		pairedPluginAwait(t, s, true)
		if e := s.VerifyStored(g.ctx); e != nil {
			t.Fatal("actual maintenance recovery", e)
		}
		if e := s.Stop(g.ctx); e != nil {
			t.Fatal(e)
		}
		if s.available() {
			t.Fatal("unjoined maintenance owner")
		}
	})

}

//go:build repository_integration && registry_integration

package imageregistry

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/retention"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/registryfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/google/uuid"
)

type registryStory struct {
	f      repositoryfixture.Fixture
	s      *Service
	ctx    context.Context
	a      identity.Auth
	asset  string
	t      *testing.T
	native *registryfixture.Native
}

func registrySetup(t *testing.T) *registryStory {
	t.Helper()
	c, e := Load(os.Getenv("PROJECT_ONE_REGISTRY_TEST_CONFIGURATION"))
	if e != nil {
		t.Fatal("real private Distribution/signed release config required", e)
	}
	g := registrySetupWithConfig(t, c)
	images, e := g.s.ReleaseImages(g.ctx, g.a, g.f.Workspace)
	if e != nil {
		t.Fatal("actual complete signed release required", e)
	}
	asset := ""
	for _, image := range images {
		if image.ManifestDigest == wire.ApprovedInferenceRecipe().ImageManifestDigest && image.ConfigDigest == wire.ApprovedInferenceRecipe().ImageConfigDigest {
			asset = image.ID
		}
	}
	if asset == "" {
		t.Fatal("actual approved M06 image absent from actual signed release")
	}
	g.asset = asset
	return g
}

func registrySetupWithConfig(t *testing.T, c Config) *registryStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Hour)
	t.Cleanup(cancel)
	root := os.Getenv("PROJECT_ONE_REPOSITORY_ROOT")
	if !filepath.IsAbs(root) {
		t.Fatal("explicit trimpath-independent actual repository root required")
	}
	if f.Store.Dialect() == transaction.MySQL {
		for _, name := range []string{"01_pins.sql", "02_blobs.sql", "03_imports.sql", "04_deployment_refs.sql", "05_grants.sql", "06_offline_refs.sql", "07_mutations.sql", "08_pin_guard.sql", "09_grant_guard.sql", "10_deployment_ref_guard.sql", "11_offline_ref_guard.sql"} {
			raw, e := os.ReadFile(filepath.Join(root, "services/control/migrations/mysql/candidates/image-registry", name))
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.Store.Exec(ctx, transaction.SQL(transaction.MySQL, string(raw))); e != nil {
				t.Fatal("actual unactivated source candidate refused", name, e)
			}
		}
	}

	native := registryfixture.New(t, os.Getenv("PROJECT_ONE_REGISTRY_NATIVE_BINARY"), os.Getenv("PROJECT_ONE_REGISTRY_NATIVE_SHA256"), c.RequestSeconds, 512)
	c.Endpoint = native.Endpoint
	c.CAFile = native.CAFile
	c.CredentialFile = native.CredentialFile
	c.AllowedCIDRs = []string{"127.0.0.0/8"}
	c.StoreRoot = t.TempDir()
	if e := os.Chmod(c.StoreRoot, 0700); e != nil {
		t.Fatal(e)
	}
	i := &identity.Service{Storage: f.Store, HMACKey: make([]byte, 32)}
	if _, e := rand.Read(i.HMACKey); e != nil {
		t.Fatal(e)
	}
	i.Config.Server.AdminOrigin = "https://admin.example.invalid"
	i.Config.Server.PortalOrigin = "https://portal.example.invalid"
	w := &workspaces.Service{Identity: i}
	n := &nodes.Service{Identity: i, Workspaces: w}
	m := &models.Service{Identity: i, Workspaces: w, RootDirectory: t.TempDir()}
	d := &deployments.Service{Nodes: n, Models: m}
	n.Inference = d
	var issued identity.IssuedSession
	if e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = i.IssueSessionWithin(ctx, q, f.User, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := i.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	private, trust, e := licensewire.GenerateKey("explicit-registry-sql-fixture")
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
	claims := licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Explicit synthetic registry SQL license", DeploymentID: state.DeploymentID, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(8 * time.Hour).Format(time.RFC3339), GraceUntil: now.Add(9 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
	artifact, e := licensewire.Issue(claims, private)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = l.Import(ctx, a, licensing.ImportInput{Artifact: string(artifact), Reason: "Actual signed synthetic fixture license"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	s, e := New(i, w, n, d, l, c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	return &registryStory{f: f, s: s, ctx: ctx, a: a, t: t, native: native}
}

func (g *registryStory) mutation() Mutation {
	return Mutation{WorkspaceID: g.f.Workspace, Reason: "Actual isolated registry acceptance", Key: uuid.NewString(), RequestID: uuid.NewString()}
}
func (g *registryStory) pin() Pin {
	g.t.Helper()
	p, e := g.s.Import(g.ctx, g.a, g.mutation(), g.asset, true)
	if e != nil || p.State != "ready" {
		g.t.Fatal("actual source/OCI/blob/manifest import refused", e)
	}
	return p
}
func (g *registryStory) count(table string) int {
	g.t.Helper()
	allowed := map[string]bool{"deployments": true, "node_tasks": true, "gpu_leases": true, "image_registry_pins": true, "image_registry_mutations": true, "image_registry_offline_refs": true, "image_registry_grants": true, "audit_events": true}
	if !allowed[table] {
		g.t.Fatal("owned actual table required")
	}
	var n int
	if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(g.f.Store.Dialect(), "SELECT COUNT(*) FROM "+table)).Scan(&n); e != nil {
		g.t.Fatal(e)
	}
	return n
}
func (g *registryStory) exec(pg, my string, args ...any) {
	g.t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(pg, my, args...)); e != nil {
		g.t.Fatal(e)
	}
}

// This instruments only the acknowledgement/failure boundary around the actual
// transaction. The database, business callback, audit and COMMIT remain real.
type registryFaultStore struct {
	transaction.Store
	table   string
	unknown bool
	armed   atomic.Bool
}

func (f *registryFaultStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
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
				_, e := q.Exec(ctx, transaction.SQL(q.Dialect(), `INSERT INTO audit_events(id,audience,action,outcome,request_id) VALUES('invalid','admin','registry.real_constraint','invalid','invalid')`))
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
func TestActualPairedRegistryRuntime(t *testing.T) {
	t.Run("import_actual_oci_and_idempotence", func(t *testing.T) {
		g := registrySetup(t)
		m := g.mutation()
		p, e := g.s.Import(g.ctx, g.a, m, g.asset, true)
		if e != nil || p.State != "ready" {
			t.Fatal(e)
		}
		before, audits := g.count("image_registry_pins"), g.count("audit_events")
		again, e := g.s.Import(g.ctx, g.a, m, g.asset, true)
		if e != nil || again.ID != p.ID || g.count("image_registry_pins") != before || g.count("audit_events") != audits {
			t.Fatal("replay repeated actual mutation", e)
		}
		image, e := readCached(g.ctx, g.s.config, p.ID)
		if e != nil || cachedMatches(p, image) != nil {
			t.Fatal("actual private CAS invalid", e)
		}
		for _, d := range image.Descriptors {
			if e = g.s.remote.Verify(g.ctx, repository(p.tenant, p.WorkspaceID, p.ID), d); e != nil {
				t.Fatal("actual registry bytes not retained", e)
			}
		}
		if e = g.s.VerifyStored(g.ctx); e != nil {
			t.Fatal(e)
		}
		if out, e := g.s.Import(g.ctx, g.a, m, g.asset, false); e == nil || !reflect.DeepEqual(out, Pin{}) {
			t.Fatal("license acknowledgement bypass")
		}
	})
	t.Run("offline_ranges_and_reference_protection", func(t *testing.T) {
		g := registrySetup(t)
		p := g.pin()
		hold, e := g.s.CreateOfflineReference(g.ctx, g.a, g.mutation(), p.ID)
		if e != nil {
			t.Fatal(e)
		}

		g.s.config.MaximumReferences = 1
		before := g.count("image_registry_offline_refs")
		if id, e := g.s.CreateOfflineReference(g.ctx, g.a, g.mutation(), p.ID); e == nil || id != "" || g.count("image_registry_offline_refs") != before {
			t.Fatal("retained reference capacity/audit did not roll back", e)
		}
		m, e := g.s.OfflineManifest(g.ctx, g.a, g.f.Workspace, p.ID, hold)
		if e != nil {
			t.Fatal(e)
		}
		h := sha256.New()
		for offset := int64(0); offset < m.Image.ArchiveBytes; {
			n := min(wire.RegistryMaximumRangeBytes, m.Image.ArchiveBytes-offset)
			raw, sha, e := g.s.OfflineRange(g.ctx, g.a, g.f.Workspace, p.ID, hold, offset, n)
			if e != nil || int64(len(raw)) != n || digestBytes(raw) != sha {
				t.Fatal("actual range refused", e)
			}
			h.Write(raw)
			offset += n
		}
		if hex.EncodeToString(h.Sum(nil)) != m.Image.ArchiveSHA256 {
			t.Fatal("complete archive digest mismatch")
		}
		if out, e := g.s.Remove(g.ctx, g.a, g.mutation(), p.ID); e == nil || !reflect.DeepEqual(out, Pin{}) {
			t.Fatal("retained snapshot removed")
		}
		if e = g.s.ReleaseOfflineReference(g.ctx, g.a, g.mutation(), p.ID, hold); e != nil {
			t.Fatal(e)
		}
		deleted, e := g.s.Remove(g.ctx, g.a, g.mutation(), p.ID)
		if e != nil || deleted.State != "deleted" {
			t.Fatal(e)
		}
		if _, e = os.Stat(filepath.Join(g.s.config.StoreRoot, p.ID)); !os.IsNotExist(e) {
			t.Fatal("exact private cache not cleaned")
		}
	})
	t.Run("audit_failure_rolls_back_real_hold", func(t *testing.T) {
		g := registrySetup(t)
		p := g.pin()
		fault := &registryFaultStore{Store: g.f.Store, table: "image_registry_offline_refs"}
		fault.armed.Store(true)
		g.s.identity.Storage = fault
		before, audits := g.count("image_registry_offline_refs"), g.count("audit_events")
		out, e := g.s.CreateOfflineReference(g.ctx, g.a, g.mutation(), p.ID)
		if e == nil || out != "" || g.count("image_registry_offline_refs") != before || g.count("audit_events") != audits {
			t.Fatal("actual audit constraint did not roll back", e)
		}
	})
	t.Run("committed_ack_unknown_zero_and_replay", func(t *testing.T) {
		g := registrySetup(t)
		p := g.pin()
		fault := &registryFaultStore{Store: g.f.Store, table: "image_registry_offline_refs", unknown: true}
		fault.armed.Store(true)
		g.s.identity.Storage = fault
		m := g.mutation()
		before, audits := g.count("image_registry_offline_refs"), g.count("audit_events")
		out, e := g.s.CreateOfflineReference(g.ctx, g.a, m, p.ID)
		if e == nil || e.Error() != string(transaction.CommitUnknown) || out != "" {
			t.Fatal("uncertain projection escaped", e)
		}
		if g.count("image_registry_offline_refs") != before+1 || g.count("audit_events") != audits+1 {
			t.Fatal("actual commit absent")
		}
		g.s.identity.Storage = g.f.Store
		out, e = g.s.CreateOfflineReference(g.ctx, g.a, m, p.ID)
		if e != nil || !validID(out) || g.count("image_registry_offline_refs") != before+1 || g.count("audit_events") != audits+1 {
			t.Fatal("independent replay duplicated actual effects", e)
		}
	})
	t.Run("scope_epoch_and_foreign_tenant", func(t *testing.T) {
		g := registrySetup(t)
		p := g.pin()
		foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
		g.exec(`INSERT INTO tenants(id,name,status) VALUES($1,'Foreign registry','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Foreign registry','active')`, foreignTenant)
		g.exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign registry Workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Foreign registry Workspace','active')`, foreignWorkspace, foreignTenant)
		if out, e := g.s.Get(g.ctx, g.a, foreignWorkspace, p.ID); e == nil || !reflect.DeepEqual(out, Pin{}) {
			t.Fatal("foreign pin leaked")
		}
		g.exec(`UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?`, g.f.User)
		if out, e := g.s.Get(g.ctx, g.a, g.f.Workspace, p.ID); e == nil || !reflect.DeepEqual(out, Pin{}) {
			t.Fatal("stale actor leaked pin")
		}
	})
	t.Run("snapshot_fresh_service_restore_and_corruption", func(t *testing.T) {
		g := registrySetup(t)
		p := g.pin()
		parent := t.TempDir()
		os.Chmod(parent, 0700)
		backup := filepath.Join(parent, "snapshot")
		receipt, e := g.s.ExportFiles(g.ctx, g.a, g.f.Workspace, backup)
		if e != nil || receipt.Pins != 1 {
			t.Fatal(e)
		}
		restoredConfig := g.s.config
		restoredConfig.StoreRoot = t.TempDir()
		os.Chmod(restoredConfig.StoreRoot, 0700)
		restored, e := New(g.s.identity, g.s.workspaces, g.s.nodes, g.s.deployments, g.s.license, restoredConfig)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(restored.Close)
		if e = restored.ImportFiles(g.ctx, backup, receipt.SHA256); e != nil {
			t.Fatal(e)
		}
		if e = restored.VerifyStored(g.ctx); e != nil {
			t.Fatal("fresh actual file restore invalid", e)
		}
		if e = g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			return restored.InvalidateAuthoritiesWithin(ctx, q)
		}); e != nil {
			t.Fatal(e)
		}
		file, e := os.OpenFile(filepath.Join(restored.config.StoreRoot, p.ID, "archive.tar"), os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		_, e = file.WriteAt([]byte("corrupt"), 0)
		file.Close()
		if e != nil {
			t.Fatal(e)
		}
		if e = restored.VerifyStored(g.ctx); e == nil {
			t.Fatal("actual retained bytes corruption ignored")
		}
	})
	t.Run("failed_preparation_abandon_discard", func(t *testing.T) {
		g := registrySetup(t)
		g.s.config.MaximumBlobBytes, g.s.config.MaximumExpandedBytes = 1, 1
		m := g.mutation()
		out, e := g.s.Import(g.ctx, g.a, m, g.asset, true)
		if e == nil || !reflect.DeepEqual(out, Pin{}) {
			t.Fatal("preparation exceeded its expansion limit")
		}
		page, e := g.s.List(g.ctx, g.a, g.f.Workspace, "")
		if e != nil || len(page.Items) != 1 || page.Items[0].State != "importing" {
			t.Fatal("real failed intent missing", e)
		}
		g.s.config.MaximumRetainedBytes = g.s.config.MaximumArchiveBytes * 2
		if out, e := g.s.Import(g.ctx, g.a, g.mutation(), g.asset, true); e == nil || e.Error() != "REGISTRY_CAPACITY_REQUIRED" || !reflect.DeepEqual(out, Pin{}) || g.count("image_registry_pins") != 1 {
			t.Fatal("pending preparation reservation was not counted", e)
		}
		p := page.Items[0]
		proof, e := g.s.AbandonPrepared(g.ctx, g.a, g.mutation(), p.ID)
		if e != nil || proof.PinID != p.ID {
			t.Fatal(e)
		}
		if e = g.s.DiscardFailed(g.ctx, g.a, g.mutation(), p.ID, proof.SHA256); e != nil {
			t.Fatal(e)
		}
		p, e = g.s.Get(g.ctx, g.a, g.f.Workspace, p.ID)
		if e != nil || p.State != "deleted" {
			t.Fatal("failed intent did not retain tombstone", e)
		}
	})
	t.Run("actual_stopped_registry_gc_retains_held_digest", func(t *testing.T) {
		g := registrySetup(t)
		p := g.pin()
		if _, e := g.s.CreateOfflineReference(g.ctx, g.a, g.mutation(), p.ID); e != nil {
			t.Fatal(e)
		}
		before, e := g.s.CheckGCProtection(g.ctx, g.a, g.f.Workspace)
		if e != nil {
			t.Fatal(e)
		}
		g.native.GC()
		after, e := g.s.CheckGCProtection(g.ctx, g.a, g.f.Workspace)
		if e != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("actual stopped-registry GC lost protected closure", e)
		}
	})
	t.Run("cache_commit_unknown_requires_explicit_remote_intent", func(t *testing.T) {
		g := registrySetup(t)
		fault := &registryFaultStore{Store: g.f.Store, table: "image_registry_blobs", unknown: true}
		fault.armed.Store(true)
		g.s.identity.Storage = fault
		m := g.mutation()
		if out, e := g.s.Import(g.ctx, g.a, m, g.asset, true); e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(out, Pin{}) {
			t.Fatal("actual cache commit uncertainty exposed pin", e)
		}
		g.s.identity.Storage = g.f.Store
		page, e := g.s.List(g.ctx, g.a, g.f.Workspace, "")
		if e != nil || len(page.Items) != 1 || page.Items[0].ManifestDigest == nil {
			t.Fatal("actual committed cache metadata absent", e)
		}
		p := page.Items[0]
		if out, e := g.s.Reconcile(g.ctx, g.a, g.mutation(), p.ID, false); e == nil || e.Error() != "REGISTRY_RETRY_REQUIRED" || !reflect.DeepEqual(out, Pin{}) {
			t.Fatal("observational reconciliation silently uploaded", e)
		}
		out, e := g.s.Reconcile(g.ctx, g.a, g.mutation(), p.ID, true)
		if e != nil || out.State != "ready" {
			t.Fatal("explicit actual remote upload did not reconcile", e)
		}
	})
	t.Run("immutable_candidate_and_borrowed_transaction_refusal", func(t *testing.T) {
		g := registrySetup(t)
		p := g.pin()
		if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(`UPDATE image_registry_pins SET asset_id='replacement',revision=revision+1 WHERE id=$1`, `UPDATE image_registry_pins SET asset_id='replacement',revision=revision+1 WHERE id=?`, p.ID)); e == nil {
			t.Fatal("immutable source changed")
		}
		if e := g.s.InvalidateAuthoritiesWithin(g.ctx, g.f.Store); e == nil {
			t.Fatal("pool accepted as restore transaction")
		}
		if _, e := g.s.OfflineManifest(g.ctx, g.a, g.f.Workspace, p.ID, uuid.NewString()); e == nil {
			t.Fatal("unheld offline export accepted")
		}

		if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(`INSERT INTO image_registry_mutations(tenant_id,workspace_id,actor_id,operation,idempotency_key,fingerprint,target_id) VALUES($1,$2,$3,'revoke',$4,$5,$6)`, `INSERT INTO image_registry_mutations(tenant_id,workspace_id,actor_id,operation,idempotency_key,fingerprint,target_id) VALUES(?,?,?,'revoke',?,?,?)`, g.f.Tenant, g.f.Workspace, g.f.User, uuid.NewString(), []byte{1}, p.ID)); e == nil {
			t.Fatal("short binary fingerprint accepted by actual candidate schema")
		}
	})
	t.Run("unbound_preparation_restart_and_explicit_discard", func(t *testing.T) {
		g := registrySetup(t)
		key := g.mutation()
		// The asset is signed, but the local expansion limit fails after reservation.
		g.s.config.MaximumBlobBytes, g.s.config.MaximumExpandedBytes = 1, 1
		out, err := g.s.Import(g.ctx, g.a, key, g.asset, true)
		if err == nil || !reflect.DeepEqual(out, Pin{}) {
			t.Fatal("preparation exceeded its expansion limit")
		}
		page, err := g.s.List(g.ctx, g.a, g.f.Workspace, "")
		if err != nil || len(page.Items) != 1 || page.Items[0].State != "importing" {
			t.Fatal("actual committed preparation intent missing", err)
		}
		pin := page.Items[0]
		restartRegistryStory(t, g)
		before := g.count("audit_events")
		if err = g.s.VerifyControlState(g.ctx); err != nil {
			t.Fatal("recognized pending preparation blocked control recovery", err)
		}
		if err = g.s.VerifyStored(g.ctx); err == nil || err.Error() != "REGISTRY_RECOVERY_REQUIRED" {
			t.Fatal("strict snapshot gate admitted unfinished preparation", err)
		}
		if g.count("audit_events") != before {
			t.Fatal("read-only startup mutated audit history")
		}
		proof, err := g.s.AbandonPrepared(g.ctx, g.a, g.mutation(), pin.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = g.s.VerifyControlState(g.ctx); err != nil {
			t.Fatal("reviewable failed preparation blocked control", err)
		}
		if err = g.s.DiscardFailed(g.ctx, g.a, g.mutation(), pin.ID, proof.SHA256); err != nil {
			t.Fatal(err)
		}
		if err = g.s.VerifyStored(g.ctx); err != nil {
			t.Fatal("explicitly discarded preparation did not retain a valid tombstone", err)
		}
	})
	t.Run("bound_unknown_restart_preserves_hold_and_requires_reconcile", func(t *testing.T) {
		g := registrySetup(t)
		pin := g.pin()
		reference, err := g.s.CreateOfflineReference(g.ctx, g.a, g.mutation(), pin.ID)
		if err != nil {
			t.Fatal(err)
		}
		var token string
		if err = g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT owner_token::text FROM image_registry_imports WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3`, `SELECT owner_token FROM image_registry_imports WHERE tenant_id=? AND workspace_id=? AND pin_id=?`, g.f.Tenant, g.f.Workspace, pin.ID)).Scan(&token); err != nil {
			t.Fatal(err)
		}
		// Explicit test fault injection into the real selected transaction. This
		// models retained uncertain state, not a claimed actual network ACK loss.
		g.s.markUnknown(g.ctx, g.a, g.mutation(), pin.ID, token)
		current, err := g.s.Get(g.ctx, g.a, g.f.Workspace, pin.ID)
		if err != nil || current.State != "unknown" {
			t.Fatal("real quarantine state was not established", err)
		}
		restartRegistryStory(t, g)
		before := g.count("audit_events")
		if err = g.s.VerifyControlState(g.ctx); err != nil {
			t.Fatal("held quarantine blocked explicit control recovery", err)
		}
		if err = g.s.VerifyStored(g.ctx); err == nil || err.Error() != "REGISTRY_RECOVERY_REQUIRED" {
			t.Fatal("strict verification admitted quarantine", err)
		}
		if g.count("audit_events") != before {
			t.Fatal("startup performed a mutation")
		}
		manifest, err := g.s.OfflineManifest(g.ctx, g.a, g.f.Workspace, pin.ID, reference)
		if err == nil || !reflect.DeepEqual(manifest, wire.RegistryOfflineManifest{}) {
			t.Fatal("quarantine exposed a usable image manifest")
		}
		raw, digest, err := g.s.OfflineRange(g.ctx, g.a, g.f.Workspace, pin.ID, reference, 0, 1)
		if err == nil || len(raw) != 0 || digest != "" {
			t.Fatal("quarantine exposed image bytes")
		}
		refs, err := g.s.References(g.ctx, g.a, g.f.Workspace, pin.ID)
		if err != nil || len(refs.Offline) != 1 || refs.Offline[0].ReleasedAt != nil {
			t.Fatal("restart released the retained hold", err)
		}
		result, err := g.s.Reconcile(g.ctx, g.a, g.mutation(), pin.ID, false)
		if err != nil || result.State != "ready" {
			t.Fatal("explicit actual remote observation did not reconcile existing content", err)
		}
		if err = g.s.VerifyStored(g.ctx); err != nil {
			t.Fatal(err)
		}
	})

}

// Helpers below use the actual existing approved model import and actual bytes.
// They are called by the separately expensive node acceptance root, never a fake
// model success callback or metadata-only substitute for real model file checks.
func importRegistryModel(t *testing.T, g *registryStory) string {
	t.Helper()
	name := os.Getenv("PROJECT_ONE_REGISTRY_MODEL_ARCHIVE")
	if !filepath.IsAbs(name) {
		t.Fatal("actual frozen M06 model archive required")
	}
	f, e := os.Open(name)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		t.Fatal(e)
	}
	m := g.s.deployments.Models
	input := models.ImportRequest{WorkspaceID: g.f.Workspace, Name: "Actual M24 dependency model", CatalogDigest: wire.ModelCatalogDigest(), ExpectedBytes: info.Size(), LicenseAcknowledged: true, Reason: "Actual registry node fixture model import"}
	job, e := m.StartImport(g.ctx, g.a, input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	buffer := make([]byte, models.ChunkBytes)
	for offset := int64(0); offset < info.Size(); {
		n, e := io.ReadFull(f, buffer)
		if e != nil && e != io.ErrUnexpectedEOF {
			t.Fatal(e)
		}
		sha := sha256.Sum256(buffer[:n])
		if _, e = m.UploadChunk(g.ctx, g.a, job.ID, offset, int64(n), hex.EncodeToString(sha[:]), bytes.NewReader(buffer[:n])); e != nil {
			t.Fatal(e)
		}
		offset += int64(n)
	}
	if _, e = m.QueueImport(g.ctx, g.a, job.ID, "Actual immutable model publication", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	owned, e := m.ProcessNext(g.ctx)
	if e != nil || !owned {
		t.Fatal(e)
	}
	job, e = m.GetImport(g.ctx, g.a, job.ID)
	if e != nil || job.ModelID == nil || job.State != "ready" {
		t.Fatal(e)
	}
	return *job.ModelID
}

func TestActualPairedRegistryNode(t *testing.T) {
	t.Run("current_task_grant_revoke_and_physical_reference", func(t *testing.T) {
		g := registrySetup(t)
		p := g.pin()
		model := importRegistryModel(t, g)
		now := time.Now().UTC()
		gpu := "GPU-" + uuid.NewString()
		handshake := wire.Handshake{ProtocolVersion: wire.InferenceVersion, ExecutionProfile: wire.DockerInferenceProfile, Capabilities: wire.Capabilities(wire.DockerInferenceProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "explicit-synthetic-registry-node", LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: now, AgentVersion: wire.AgentVersion}}
		token, e := g.s.nodes.MintForProfile(g.ctx, g.a, g.f.Workspace, "Synthetic physical registry envelope", handshake.Profile(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		node, e := g.s.nodes.Enroll(g.ctx, wire.Enrollment{JoinToken: token.Token, Handshake: handshake}, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		handshake.Inference = &wire.InferenceObservation{RegistryImageDelivery: true, ProbeErrorCode: wire.InferenceImageMissing, GPUs: []wire.NVIDIAGPU{{UUID: gpu, Name: "Explicit synthetic GPU and loader capability only", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: now}}, ObservedAt: now}
		if _, e = g.s.nodes.Heartbeat(g.ctx, node.NodeID, node.Credential, handshake); e != nil {
			t.Fatal(e)
		}
		beforeDeployment, beforeTask, beforeGPU := g.count("deployments"), g.count("node_tasks"), g.count("gpu_leases")
		if _, ordinaryErr := g.s.deployments.Create(g.ctx, g.a, deployments.CreateRequest{NodeID: node.NodeID, ModelID: model, GPUUUID: gpu, Reason: "Ordinary cold-node image refusal"}, uuid.NewString(), uuid.NewString()); ordinaryErr == nil || ordinaryErr.Error() != wire.InferenceImageMissing || g.count("deployments") != beforeDeployment || g.count("node_tasks") != beforeTask || g.count("gpu_leases") != beforeGPU {
			t.Fatal("ordinary missing image gained deployment effects", ordinaryErr)
		}
		m := g.mutation()
		d, e := g.s.CreateDeployment(g.ctx, g.a, m, p.ID, deployments.CreateRequest{NodeID: node.NodeID, ModelID: model, GPUUUID: gpu, Reason: m.Reason})
		if e != nil {
			t.Fatal("actual bound deployment/model files refused", e)
		}
		claim, e := g.s.nodes.Claim(g.ctx, node.NodeID, node.Credential, uuid.NewString())
		if e != nil || claim.Lease == nil || claim.Lease.Inference == nil || claim.Lease.Inference.RegistryPinID != p.ID {
			t.Fatal(e)
		}
		lease := claim.Lease
		manifest, e := g.s.PullManifest(g.ctx, node.NodeID, node.Credential, lease.TaskID, lease.LeaseToken)
		if e != nil || manifest.PinID != p.ID || manifest.ImageManifestDigest != *p.ManifestDigest {
			t.Fatal(e)
		}
		raw, sha, e := g.s.PullRange(g.ctx, node.NodeID, node.Credential, lease.TaskID, lease.LeaseToken, wire.RegistryRange{GrantID: manifest.GrantID, Offset: 0, Length: min(manifest.ArchiveBytes, wire.RegistryMaximumRangeBytes)})
		if e != nil || digestBytes(raw) != sha {
			t.Fatal(e)
		}
		if _, e = g.s.PullManifest(g.ctx, node.NodeID, "wrong-current-credential", lease.TaskID, lease.LeaseToken); e == nil {
			t.Fatal("stale credential accepted")
		}

		origin := g.a.SessionID
		var fresh identity.IssuedSession
		if e = g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			fresh, e = g.s.identity.IssueSessionWithin(ctx, q, g.f.User, g.a.Epoch, "admin", "", uuid.NewString(), nil)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		g.exec(`UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1`, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='logout' WHERE id=?`, origin)
		if out, e := g.s.PullManifest(g.ctx, node.NodeID, node.Credential, lease.TaskID, lease.LeaseToken); e == nil || !reflect.DeepEqual(out, wire.RegistryManifest{}) {
			t.Fatal("logged-out origin session retained pull authority", e)
		}
		// Age only this actual disposable session for the real retention pass.
		// Grants retain history without holding expired sessions indefinitely.
		g.exec(`UPDATE sessions SET created_at=clock_timestamp()-interval '10 days',last_seen_at=clock_timestamp()-interval '9 days',absolute_expires_at=clock_timestamp()-interval '8 days',revoked_at=clock_timestamp()-interval '8 days' WHERE id=$1`, `UPDATE sessions SET created_at=TIMESTAMPADD(DAY,-10,UTC_TIMESTAMP(6)),last_seen_at=TIMESTAMPADD(DAY,-9,UTC_TIMESTAMP(6)),absolute_expires_at=TIMESTAMPADD(DAY,-8,UTC_TIMESTAMP(6)),revoked_at=TIMESTAMPADD(DAY,-8,UTC_TIMESTAMP(6)) WHERE id=?`, origin)
		grants := g.count("image_registry_grants")
		if e = retention.PassSelected(g.ctx, g.f.Store, config.DefaultRuntimePolicy().Retention); e != nil {
			t.Fatal("actual retention blocked by historical registry session binding", e)
		}
		var sessions int
		if e = g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT COUNT(*) FROM sessions WHERE id=$1`, `SELECT COUNT(*) FROM sessions WHERE id=?`, origin)).Scan(&sessions); e != nil || sessions != 0 || g.count("image_registry_grants") != grants {
			t.Fatal("retention failed exact session deletion/history preservation", e)
		}
		if out, e := g.s.PullManifest(g.ctx, node.NodeID, node.Credential, lease.TaskID, lease.LeaseToken); e == nil || !reflect.DeepEqual(out, wire.RegistryManifest{}) {
			t.Fatal("retained missing origin session allowed pull", e)
		}
		g.a, e = g.s.identity.Authenticate(g.ctx, fresh.Token, "admin")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = g.s.Grant(g.ctx, g.a, g.mutation(), p.ID, d.ID); e != nil {
			t.Fatal("fresh original actor session could not explicitly renew", e)
		}
		if _, e = g.s.Remove(g.ctx, g.a, g.mutation(), p.ID); e == nil {
			t.Fatal("current deployment reference removed")
		}
		if e = g.s.Revoke(g.ctx, g.a, g.mutation(), p.ID); e != nil {
			t.Fatal(e)
		}
		if out, e := g.s.PullManifest(g.ctx, node.NodeID, node.Credential, lease.TaskID, lease.LeaseToken); e == nil || !reflect.DeepEqual(out, wire.RegistryManifest{}) {
			t.Fatal("revoked manifest exposed")
		}
		if _, e = g.s.Grant(g.ctx, g.a, g.mutation(), p.ID, d.ID); e != nil {
			t.Fatal("explicit current task grant renewal refused", e)
		}
		newManifest, e := g.s.PullManifest(g.ctx, node.NodeID, node.Credential, lease.TaskID, lease.LeaseToken)
		if e != nil || newManifest.GrantID == manifest.GrantID {
			t.Fatal("grant history was reused", e)
		}
		c := lease.Inference
		absent := &wire.DeploymentObservation{DeploymentID: c.DeploymentID, ModelID: c.ModelID, CatalogDigest: c.CatalogDigest, GPUUUID: c.GPUUUID, Revision: c.Revision, State: "not_installed"}
		failed := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "failed", ErrorCode: "INFERENCE_IMAGE_UNAVAILABLE", Inference: &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, Deployment: absent, ObservedAt: time.Now().UTC()}}
		// Explicit synthetic physical absence only; no container/proof digest or GPU
		// execution is invented. Actual owning SQL/task/result constraints are exercised.
		if _, e = g.s.nodes.Complete(g.ctx, node.NodeID, node.Credential, lease.TaskID, failed, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		d, e = g.s.deployments.Get(g.ctx, g.a, g.f.Workspace, d.ID)
		if e != nil {
			t.Fatal(e)
		}
		d, e = g.s.deployments.Action(g.ctx, g.a, d.ID, deployments.ActionRequest{Action: "remove", Revision: d.Revision, Reason: "Explicit synthetic physical absence envelope"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		removal, e := g.s.nodes.Claim(g.ctx, node.NodeID, node.Credential, uuid.NewString())
		if e != nil || removal.Lease == nil {
			t.Fatal(e)
		}
		rc := removal.Lease.Inference
		absent.Revision = rc.Revision
		result := wire.Result{LeaseToken: removal.Lease.LeaseToken, Outcome: "success", Inference: &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, Deployment: absent, ObservedAt: time.Now().UTC()}}
		if _, e = g.s.nodes.Complete(g.ctx, node.NodeID, node.Credential, removal.Lease.TaskID, result, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if e = g.s.ReleaseDeploymentReference(g.ctx, g.a, g.mutation(), p.ID, d.ID); e != nil {
			t.Fatal("actual durable removal reference refused", e)
		}
		if out, e := g.s.Remove(g.ctx, g.a, g.mutation(), p.ID); e != nil || out.State != "deleted" {
			t.Fatal(e)
		}
	})
	t.Run("tag_drift_keeps_original_pin", func(t *testing.T) {
		g := registrySetup(t)
		p := g.pin()
		images, e := g.s.ReleaseImages(g.ctx, g.a, g.f.Workspace)
		if e != nil {
			t.Fatal(e)
		}
		alternate := ""
		for _, v := range images {
			if v.ManifestDigest != *p.ManifestDigest {
				alternate = v.ID
				break
			}
		}
		if alternate == "" {
			t.Fatal("actual second signed release image required for real tag drift")
		}
		other, e := g.s.Import(g.ctx, g.a, g.mutation(), alternate, true)
		if e != nil {
			t.Fatal(e)
		}
		image, e := readCached(g.ctx, g.s.config, other.ID)
		if e != nil {
			t.Fatal(e)
		}
		repo := repository(p.tenant, p.WorkspaceID, p.ID)
		for _, d := range image.Descriptors {
			if d.Kind != "manifest" {
				if e = g.s.remote.PutBlob(g.ctx, repo, d, filepath.Join(g.s.config.StoreRoot, other.ID, "blobs", d.Digest[7:])); e != nil {
					t.Fatal(e)
				}
			}
		}
		root, e := privateRoot(g.s.config.StoreRoot)
		if e != nil {
			t.Fatal(e)
		}
		manifest, e := root.ReadFile(other.ID + "/manifest.json")
		root.Close()
		if e != nil {
			t.Fatal(e)
		}
		if e = g.s.remote.PutManifest(g.ctx, repo, image.Descriptors[0], manifest); e != nil {
			t.Fatal(e)
		}
		retained, e := g.s.Get(g.ctx, g.a, g.f.Workspace, p.ID)
		if e != nil || *retained.ManifestDigest != *p.ManifestDigest || *retained.ConfigDigest != *p.ConfigDigest {
			t.Fatal("tag drift changed fixed pin", e)
		}
		if _, e = g.s.CheckGCProtection(g.ctx, g.a, g.f.Workspace); e == nil {
			t.Fatal("GC accepted a drifted retained tag")
		}
		original, e := readCached(g.ctx, g.s.config, p.ID)
		if e != nil {
			t.Fatal(e)
		}
		for _, d := range original.Descriptors {
			if e = g.s.remote.Verify(g.ctx, repo, d); e != nil {
				t.Fatal("original immutable content lost", e)
			}
		}
	})
}

// restartRegistryStory constructs a fresh real component on the same actual
// selected database and retained CAS. No logical DB restore is claimed here.
func restartRegistryStory(t *testing.T, g *registryStory) {
	t.Helper()
	old := g.s
	next, err := New(old.identity, old.workspaces, old.nodes, old.deployments, old.license, old.config)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	g.s = next
	t.Cleanup(next.Close)
}

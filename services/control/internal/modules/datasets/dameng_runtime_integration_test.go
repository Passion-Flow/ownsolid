//go:build repository_integration && cgo && odbc_manager

package datasets_test

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

// The operator supplies an already migrated, exclusively owned EMPTY schema.
// This test neither installs a driver nor creates/drops a database/schema. Failed
// SQL and fixtures remain available for coordinated diagnosis and cleanup.
func nativeDatasetStory(t *testing.T) datasetRuntimeStory {
	t.Helper()
	c, e := dameng.LoadConnectionConfiguration(os.Getenv("PROJECT_ONE_DATASET_DAMENG_CONFIGURATION"), "")
	if e != nil || !regexp.MustCompile(`^M29_DATASET_[A-F0-9]{32}_TEST$`).MatchString(c.Schema) {
		t.Fatal("DATASET_EXCLUSIVE_NATIVE_TEST_SCHEMA_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	t.Cleanup(cancel)
	kernel, _, e := dameng.Open(ctx, c)
	if kernel != nil {
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), time.Duration(c.CleanupSeconds)*time.Second)
			defer stop()
			status, err := kernel.Close(cleanup)
			if err != nil || status.State != "closed" || status.PhysicalOwners != 0 || status.CleanupJobs != 0 {
				t.Error("native owner cleanup pending", err)
			}
		})
	}
	if e != nil {
		t.Fatal("native DM open", e)
	}
	store, e := transaction.FromDameng(kernel)
	if e != nil {
		t.Fatal(e)
	}
	f := repositoryfixture.Fixture{Store: store, Tenant: uuid.NewString(), Workspace: uuid.NewString(), User: uuid.NewString()}
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var complete bool
		if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT bootstrap_complete FROM installation_state WHERE id=1 FOR UPDATE`)).Scan(&complete); e != nil {
			return e
		}
		if complete {
			return &transaction.Error{Kind: transaction.CheckConflict}
		}
		var count int
		if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT (SELECT count(*) FROM users)+(SELECT count(*) FROM datasets) FROM DUAL`)).Scan(&count); e != nil {
			return e
		}
		if count != 0 {
			return &transaction.Error{Kind: transaction.CheckConflict}
		}
		for _, stmt := range []transaction.Statement{
			transaction.SQL(transaction.Dameng, `INSERT INTO tenants(id,name,status) VALUES(?,?,'active')`, f.Tenant, "Synthetic native dataset tenant"),
			transaction.SQL(transaction.Dameng, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,?,'active')`, f.Workspace, f.Tenant, "Synthetic native dataset workspace"),
			transaction.SQL(transaction.Dameng, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,?,'en','active',1)`, f.User, "native-dataset@example.invalid", "native-dataset@example.invalid", "Synthetic native actor"),
			transaction.SQL(transaction.Dameng, `INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role,authority_id) VALUES(?,?,?,'owner',?)`, f.Tenant, f.Workspace, f.User, uuid.NewString()),
			transaction.SQL(transaction.Dameng, `UPDATE installation_state SET bootstrap_complete=1,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=CURRENT_TIMESTAMP(6) WHERE id=1`, f.Tenant, f.Workspace, f.User),
		} {
			if _, e := q.Exec(ctx, stmt); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal("native isolated fixture admission/seed", e)
	}
	account := &identity.Service{Storage: store}
	ws := &workspaces.Service{Identity: account}
	root := filepath.Join(t.TempDir(), "datasets")
	if e = os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e = datasets.InitializeStore(root, f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	files, e := datasets.OpenStore(root, f.Tenant, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := files.Close(); e != nil {
			t.Error(e)
		}
	})
	binary, script := os.Getenv("PROJECT_ONE_DATA_PYTHON"), os.Getenv("PROJECT_ONE_DATA_PROCESSOR_FILE")
	if !filepath.IsAbs(binary) || !filepath.IsAbs(script) || filepath.Clean(script) != script {
		t.Fatal("DATASET_PINNED_REAL_PROCESSOR_REQUIRED")
	}
	account.Config.Datasets = config.DatasetConfiguration{ArtifactRoot: root, PythonExecutable: binary, ProcessorFile: script}
	processor, e := datasets.NewProcessor(account.Config.Datasets, account.Config.Policy().Datasets)
	if e != nil {
		t.Fatal(e)
	}
	g := datasetRuntimeStory{f: f, ctx: ctx, account: account, s: &datasets.Service{Identity: account, Workspaces: ws, Store: files, Processor: processor}, root: root}
	g.admin = g.auth(t, "admin")
	g.portal = g.auth(t, "portal")
	return g
}

func nativeDatasetCount(t *testing.T, g datasetRuntimeStory, sql string, args ...any) int {
	t.Helper()
	var n int
	if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.Dameng, sql, args...)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

func TestActualDamengDatasetLifecycle(t *testing.T) {
	g := nativeDatasetStory(t)
	d, draft := g.upload(t, runtimeRow("native-a", "\u4e16\u754c one")+"{bad}\n"+runtimeRow("native-b", "\u0627\u0644\u0639\u0631\u0628\u064a\u0629 two"))
	g.process(t)
	draft = g.draft(t, d.ID, draft.ID)
	if draft.State != "editing" || draft.TotalRows == nil || *draft.TotalRows != 3 || draft.InvalidRows == nil || *draft.InvalidRows != 1 {
		t.Fatal("native inspection counts")
	}
	page, e := g.s.RowPage(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, url.Values{"limit": {"2"}})
	if e != nil || len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatal("native bounded row page", e)
	}
	next, e := g.s.RowPage(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, url.Values{"limit": {"2"}, "cursor": {*page.NextCursor}})
	if e != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[1].ID {
		t.Fatal("native row cursor", e)
	}
	bad := page.Items[1]
	_, e = g.s.ReplaceRow(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, bad.ID, datasets.RowRequest{ExpectedRevision: draft.Revision, RowRevision: bad.Revision, SourceID: "native-repaired", Prompt: "Repaired \u4e16\u754c", Completion: "Native response", Reason: "Repair synthetic invalid row"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	v := g.publish(t, d, draft)
	if v.Ordinal != 1 || v.Manifest.TotalRows != 3 {
		t.Fatal("native publication counts")
	}
	file, asset, e := g.s.VersionFile(g.ctx, g.portal, g.f.Workspace, d.ID, v.ID, "samples.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	raw, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(raw)) != asset.SizeBytes || !bytes.Contains(raw, []byte("Repaired \u4e16\u754c")) {
		t.Fatal("native immutable export")
	}
	if n := nativeDatasetCount(t, g, `SELECT count(*) FROM dataset_versions WHERE dataset_id=?`, d.ID); n != 1 {
		t.Fatal("native version cardinality", n)
	}
	if n := nativeDatasetCount(t, g, `SELECT count(*) FROM audit_events WHERE action='dataset.version_published' AND target_id=?`, v.ID); n != 1 {
		t.Fatal("native atomic audit", n)
	}
	if n := nativeDatasetCount(t, g, `SELECT count(*) FROM dataset_row_history WHERE draft_id=?`, draft.ID); n != 4 {
		t.Fatal("native row history", n)
	}
	// Each refused statement gets a separate real transaction. Never probe or
	// continue on the physical connection poisoned by its statement error.
	for _, statement := range []transaction.Statement{
		transaction.SQL(transaction.Dameng, `UPDATE dataset_versions SET manifest=? WHERE id=?`, `{"changed":true}`, v.ID),
		transaction.SQL(transaction.Dameng, `UPDATE dataset_versions SET ordinal=ordinal+1 WHERE id=?`, v.ID),
		transaction.SQL(transaction.Dameng, `DELETE FROM dataset_versions WHERE id=?`, v.ID),
		transaction.SQL(transaction.Dameng, `UPDATE dataset_row_history SET reason='changed' WHERE draft_id=?`, draft.ID),
		transaction.SQL(transaction.Dameng, `UPDATE dataset_versions SET retired_at=CURRENT_TIMESTAMP(6),retire_reason=NULL WHERE id=?`, v.ID),
	} {
		e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error { _, e := q.Exec(ctx, statement); return e })
		if e == nil {
			t.Fatal("native immutable/retirement guard allowed mutation")
		}
	}
	if n := nativeDatasetCount(t, g, `SELECT count(*) FROM dataset_versions WHERE id=? AND ordinal=1 AND retired_at IS NULL`, v.ID); n != 1 {
		t.Fatal("refused native mutations changed version", n)
	}
	if e = g.s.VerifyStored(g.ctx); e != nil {
		t.Fatal("native referenced file integrity", e)
	}
	duplicate, e := g.s.Create(g.ctx, g.portal, datasets.CreateRequest{WorkspaceID: g.f.Workspace, Name: d.Name, Origin: "Synthetic", License: "CC0", RightsAsserted: true, Reason: "Native unique conflict"}, uuid.NewString(), uuid.NewString())
	runtimeError(t, e, "DATASET_EXISTS")
	if duplicate.ID != "" {
		t.Fatal("unique conflict exposed projection")
	}
	if n := nativeDatasetCount(t, g, `SELECT count(*) FROM datasets WHERE tenant_id=?`, g.f.Tenant); n != 1 {
		t.Fatal("unique conflict changed dataset count", n)
	}
	// Unknown outcome is injected only after an actual native COMMIT. It checks
	// application reconciliation, not proprietary driver disconnect behavior.
	key := uuid.NewString()
	request := datasets.CreateRequest{WorkspaceID: g.f.Workspace, Name: "Native unknown receipt", Origin: "Synthetic", License: "CC0", RightsAsserted: true, Reason: "Native outcome control"}
	unknown := g.wrapped(runtimeFaultStore{Store: g.f.Store, unknown: true})
	projected, e := unknown.s.Create(g.ctx, g.portal, request, key, uuid.NewString())
	runtimeError(t, e, string(transaction.CommitUnknown))
	if projected.ID != "" {
		t.Fatal("unknown native commit projected success")
	}
	recovered, e := g.s.Create(g.ctx, g.portal, request, key, uuid.NewString())
	if e != nil || recovered.ID == "" {
		t.Fatal("native same-key recovery", e)
	}
	if n := nativeDatasetCount(t, g, `SELECT count(*) FROM datasets WHERE creation_key=?`, key); n != 1 {
		t.Fatal("unknown native replay duplicated", n)
	}
	first, e := g.s.DatasetPage(g.ctx, g.portal, g.f.Workspace, url.Values{"limit": {"1"}})
	if e != nil || len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatal("native timestamp page", e)
	}
	second, e := g.s.DatasetPage(g.ctx, g.portal, g.f.Workspace, url.Values{"limit": {"1"}, "cursor": {*first.NextCursor}})
	if e != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatal("native exact timestamp cursor", e)
	}
	auditBefore := nativeDatasetCount(t, g, `SELECT count(*) FROM audit_events WHERE tenant_id=?`, g.f.Tenant)
	rollback := g.wrapped(runtimeFaultStore{Store: g.f.Store, rollback: true})
	request.Name = "Native rolled back receipt"
	rejected, e := rollback.s.Create(g.ctx, g.portal, request, uuid.NewString(), uuid.NewString())
	runtimeError(t, e, string(transaction.Unavailable))
	if rejected.ID != "" || nativeDatasetCount(t, g, `SELECT count(*) FROM datasets WHERE tenant_id=?`, g.f.Tenant) != 2 || nativeDatasetCount(t, g, `SELECT count(*) FROM audit_events WHERE tenant_id=?`, g.f.Tenant) != auditBefore {
		t.Fatal("native rollback leaked dataset or audit")
	}
	// Restore invalidates an actual queued job without deleting immutable data.
	clone, e := g.s.CloneVersion(g.ctx, g.portal, g.f.Workspace, d.ID, v.ID, "Native restore fixture", uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		return datasets.InvalidateAuthoritiesWithin(ctx, q)
	}); e != nil {
		t.Fatal(e)
	}
	restored := g.draft(t, d.ID, clone.ID)
	if restored.State != "cancelled" {
		t.Fatal("native restore retained authority")
	}
	draftPage, e := g.s.DraftPage(g.ctx, g.portal, g.f.Workspace, d.ID, url.Values{})
	if e != nil || len(draftPage.Items) != 2 {
		t.Fatal("native draft page", e)
	}
	versionPage, e := g.s.VersionPage(g.ctx, g.portal, g.f.Workspace, d.ID, url.Values{})
	if e != nil || len(versionPage.Items) != 1 {
		t.Fatal("native version page", e)
	}
	retired, e := g.s.RetireVersion(g.ctx, g.portal, g.f.Workspace, d.ID, v.ID, "Native retirement", uuid.NewString())
	if e != nil || retired.RetiredAt == nil {
		t.Fatal("native version retirement", e)
	}
	_, e = g.s.PublishedSourceSnapshot(g.ctx, g.portal, g.f.Workspace, d.ID, v.ID, false)
	runtimeError(t, e, "DATASET_RETIRED")
	retained, e := g.s.PublishedSourceSnapshot(g.ctx, g.portal, g.f.Workspace, d.ID, v.ID, true)
	if e != nil || retained.ManifestSHA256 != v.ManifestSHA256 {
		t.Fatal("native retained version changed", e)
	}
	current, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	retiredDataset, e := g.s.Retire(g.ctx, g.portal, g.f.Workspace, d.ID, current.Revision, "Native dataset retirement", uuid.NewString())
	if e != nil || retiredDataset.RetiredAt == nil {
		t.Fatal("native dataset retirement", e)
	}
	secondCurrent, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, recovered.ID)
	if e != nil {
		t.Fatal(e)
	}
	secondRetired, e := g.s.Retire(g.ctx, g.portal, g.f.Workspace, recovered.ID, secondCurrent.Revision, "Second native dataset retirement in same scope", uuid.NewString())
	if e != nil || secondRetired.RetiredAt == nil {
		t.Fatal("native composite unique key blocked second retirement", e)
	}
	if n := nativeDatasetCount(t, g, `SELECT count(*) FROM datasets WHERE tenant_id=? AND workspace_id=? AND retired_at IS NOT NULL AND active_tenant_id IS NULL AND active_workspace_id IS NULL AND active_name_key IS NULL`, g.f.Tenant, g.f.Workspace); n != 2 {
		t.Fatal("retired native unique key must be entirely NULL", n)
	}
	reused, e := g.s.Create(g.ctx, g.portal, datasets.CreateRequest{WorkspaceID: g.f.Workspace, Name: d.Name, Origin: "Synthetic", License: "CC0", RightsAsserted: true, Reason: "Reuse retired dataset name"}, uuid.NewString(), uuid.NewString())
	if e != nil || reused.ID == d.ID {
		t.Fatal("native retired name reuse", e)
	}
}

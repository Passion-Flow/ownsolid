//go:build repository_integration

package datasets_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// Identity/Workspace rows are synthetic isolated fixtures. Dataset rows, jobs,
// histories, immutable versions and file output below are created by actual owners.
// No GPU, native target, model quality or full serving qualification follows.
type datasetRuntimeStory struct {
	f             repositoryfixture.Fixture
	ctx           context.Context
	account       *identity.Service
	s             *datasets.Service
	admin, portal identity.Auth
	root          string
}

func runtimeSetup(t *testing.T, processor bool) datasetRuntimeStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	account := &identity.Service{Storage: f.Store}
	if profile := os.Getenv("PROJECT_ONE_TEST_APPLICATION_PROFILE"); profile != "" {
	}
	if f.PostgreSQL != nil {
		account.Storage = nil
		account.DB = f.PostgreSQL
	}
	ws := &workspaces.Service{DB: f.PostgreSQL, Identity: account}
	root := filepath.Join(t.TempDir(), "datasets")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := datasets.InitializeStore(root, f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := datasets.OpenStore(root, f.Tenant, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := store.Close(); e != nil {
			t.Error(e)
		}
	})
	s := &datasets.Service{DB: f.PostgreSQL, Identity: account, Workspaces: ws, Store: store}
	g := datasetRuntimeStory{f: f, ctx: ctx, account: account, s: s, root: root}
	g.exec(t, "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES(?,?,?,'owner')", f.Tenant, f.Workspace, f.User)
	g.admin = g.auth(t, "admin")
	g.portal = g.auth(t, "portal")
	if processor {
		binary := os.Getenv("PROJECT_ONE_DATA_PYTHON")
		if binary == "" {
			t.Fatal("PROJECT_ONE_DATA_PYTHON must select the pinned actual external CPython")
		}
		script := os.Getenv("PROJECT_ONE_DATA_PROCESSOR_FILE")
		if !filepath.IsAbs(script) || filepath.Clean(script) != script {
			t.Fatal("PROJECT_ONE_DATA_PROCESSOR_FILE must select the validated actual processor source")
		}
		account.Config.Datasets = config.DatasetConfiguration{ArtifactRoot: root, PythonExecutable: binary, ProcessorFile: script}
		s.Processor, e = datasets.NewProcessor(account.Config.Datasets, account.Config.Policy().Datasets)
		if e != nil {
			t.Fatal(e)
		}
	}
	return g
}
func (g datasetRuntimeStory) auth(t *testing.T, audience string) identity.Auth {
	t.Helper()
	var session identity.IssuedSession
	if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		session, e = g.account.IssueSessionWithin(ctx, q, g.f.User, 1, audience, "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := g.account.Authenticate(g.ctx, session.Token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func (g datasetRuntimeStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(pg, my, args...)); e != nil {
		t.Fatal("actual scoped fixture SQL", e)
	}
}
func (g datasetRuntimeStory) count(t *testing.T, table string) int {
	t.Helper()
	allowed := map[string]bool{"datasets": true, "dataset_drafts": true, "dataset_jobs": true, "dataset_rows": true, "dataset_versions": true, "dataset_row_history": true, "audit_events": true}
	if !allowed[table] {
		t.Fatal("unowned count table")
	}
	var n int
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT count(*) FROM "+table+" WHERE tenant_id=$1", "SELECT count(*) FROM "+table+" WHERE tenant_id=?", g.f.Tenant)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func runtimeError(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("want %s, got %v", code, e)
	}
}
func (g datasetRuntimeStory) create(t *testing.T) datasets.Dataset {
	t.Helper()
	d, e := g.s.Create(g.ctx, g.portal, datasets.CreateRequest{WorkspaceID: g.f.Workspace, Name: "Actual runtime \u4e16\u754c " + uuid.NewString(), Origin: "Synthetic isolated JSONL", License: "Synthetic CC0", RightsAsserted: true, Reason: "Actual selected repository acceptance"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func runtimeRow(source, prompt string) string {
	raw, _ := json.Marshal(wire.Record{SourceID: source, Prompt: prompt, Completion: "Synthetic response \u4e16\u754c"})
	return string(raw) + "\n"
}
func (g datasetRuntimeStory) upload(t *testing.T, raw string) (datasets.Dataset, datasets.Draft) {
	t.Helper()
	d := g.create(t)
	sum := sha256.Sum256([]byte(raw))
	draft, e := g.s.CreateDraft(g.ctx, g.portal, g.f.Workspace, d.ID, datasets.DraftRequest{SourceName: "synthetic.jsonl", SourceSHA256: hex.EncodeToString(sum[:]), ExpectedBytes: int64(len(raw)), Reason: "Actual private upload"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	draft, e = g.s.Upload(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, draft.SourceSHA256, draft.ExpectedBytes, strings.NewReader(raw), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if draft.State != "validating" || draft.CurrentJobID == nil || draft.TotalRows != nil {
		t.Fatal("queued import projected as complete", draft)
	}
	return d, draft
}
func (g datasetRuntimeStory) process(t *testing.T) {
	t.Helper()
	owned, e := g.s.ProcessNext(g.ctx)
	if e != nil || !owned {
		t.Fatal("actual processor/selected worker", owned, e)
	}
}
func (g datasetRuntimeStory) draft(t *testing.T, d, draft string) datasets.Draft {
	t.Helper()
	v, e := g.s.GetDraft(g.ctx, g.portal, g.f.Workspace, d, draft)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func (g datasetRuntimeStory) publish(t *testing.T, d datasets.Dataset, draft datasets.Draft) datasets.Version {
	t.Helper()
	draft = g.draft(t, d.ID, draft.ID)
	job, e := g.s.Publish(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, datasets.PublicationRequest{ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Actual immutable publication"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.process(t)
	j, e := g.s.GetJob(g.ctx, g.portal, g.f.Workspace, d.ID, job.ID)
	if e != nil || j.State != "succeeded" || j.AttemptID == nil {
		t.Fatal("publication state", j, e)
	}
	v, e := g.s.GetVersion(g.ctx, g.portal, g.f.Workspace, d.ID, *job.VersionID)
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestActualDatasetRuntimeMetadataAuthorityAndPages(t *testing.T) {
	for _, name := range []string{"create_replay_names", "roles_epoch_scope", "keyset_configuration", "limits_invalid", "concurrent_intents"} {
		t.Run(name, func(t *testing.T) {
			g := runtimeSetup(t, false)
			// Actual session issuance has its own audit entries. Count every later
			// event so replay or conflict cannot hide a duplicated creation audit.
			auditsBefore := g.count(t, "audit_events")
			d := g.create(t)
			switch name {
			case "create_replay_names":
				input := datasets.CreateRequest{WorkspaceID: g.f.Workspace, Name: "Case Sensitive \u4e16\u754c", Origin: "Synthetic", License: "CC0", RightsAsserted: true, Reason: "Replay exact intent"}
				key := uuid.NewString()
				first, e := g.s.Create(g.ctx, g.portal, input, key, uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				replay, e := g.s.Create(g.ctx, g.portal, input, key, uuid.NewString())
				if e != nil || first.ID != replay.ID {
					t.Fatal("exact replay", e)
				}
				input.Reason = "Changed intent"
				v, e := g.s.Create(g.ctx, g.portal, input, key, uuid.NewString())
				runtimeError(t, e, "IDEMPOTENCY_CONFLICT")
				if v.ID != "" {
					t.Fatal("conflict projection")
				}
				input.Name = "case sensitive \u4e16\u754c"
				_, e = g.s.Create(g.ctx, g.portal, input, uuid.NewString(), uuid.NewString())
				runtimeError(t, e, "DATASET_EXISTS")
				if g.count(t, "datasets") != 2 || g.count(t, "audit_events") != auditsBefore+2 {
					t.Fatal("duplicate intent/audit")
				}
			case "roles_epoch_scope":
				g.exec(t, "UPDATE workspace_memberships SET role='viewer' WHERE user_id=$1", "UPDATE workspace_memberships SET role='viewer' WHERE user_id=?", g.f.User)
				v, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, d.ID)
				runtimeError(t, e, "DATASET_NOT_FOUND")
				if v.ID != "" {
					t.Fatal("viewer private dataset leaked")
				}
				_, e = g.s.Create(g.ctx, g.portal, datasets.CreateRequest{WorkspaceID: g.f.Workspace, Name: "Forbidden", Origin: "Synthetic", License: "CC0", RightsAsserted: true, Reason: "Viewer denied"}, uuid.NewString(), uuid.NewString())
				runtimeError(t, e, "ACCESS_DENIED")
				if _, e = g.s.Get(g.ctx, g.admin, g.f.Workspace, d.ID); e != nil {
					t.Fatal("Admin current authority", e)
				}
				if v, e = g.s.Get(g.ctx, g.admin, uuid.NewString(), d.ID); e == nil || v.ID != "" {
					t.Fatal("foreign Workspace projection", e)
				}
				g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
				v, e = g.s.Get(g.ctx, g.admin, g.f.Workspace, d.ID)
				if e == nil || v.ID != "" {
					t.Fatal("stale epoch read", e)
				}
				scope, e := g.s.ResolveDatasetWorkspace(g.ctx, g.admin, d.ID)
				if e == nil || scope != "" {
					t.Fatal("stale Admin routing scope", e)
				}
			case "keyset_configuration":
				g.create(t)
				g.create(t)
				page, e := g.s.DatasetPage(g.ctx, g.portal, g.f.Workspace, url.Values{"limit": {"2"}})
				if e != nil || len(page.Items) != 2 || page.NextCursor == nil {
					t.Fatal("first scoped page", page, e)
				}
				next, e := g.s.DatasetPage(g.ctx, g.portal, g.f.Workspace, url.Values{"limit": {"2"}, "cursor": {*page.NextCursor}})
				if e != nil || len(next.Items) != 1 {
					t.Fatal("next page", e)
				}
				if next.Items[0].ID == page.Items[0].ID || next.Items[0].ID == page.Items[1].ID || next.Items[0].CreatedAt.Location() != time.UTC {
					t.Fatal("keyset duplicate/UTC")
				}
				bad, e := g.s.DatasetPage(g.ctx, g.admin, g.f.Workspace, url.Values{"cursor": {*page.NextCursor}})
				if e == nil || len(bad.Items) != 0 {
					t.Fatal("foreign actor cursor accepted", e)
				}
				c, e := g.s.Configuration(g.ctx, g.portal, g.f.Workspace)
				if e != nil || !c.CanImport || !c.CanPublish || c.StorageAvailable {
					t.Fatal("typed dataset configuration", c, e)
				}
				scope, e := g.s.ResolveDatasetWorkspace(g.ctx, g.admin, d.ID)
				if e != nil || scope != g.f.Workspace {
					t.Fatal("Admin routing", e)
				}
			case "concurrent_intents":
				key := uuid.NewString()
				input := datasets.CreateRequest{WorkspaceID: g.f.Workspace, Name: "Concurrent \u4e16\u754c", Origin: "Synthetic", License: "CC0", RightsAsserted: true, Reason: "Concurrent exact intent"}
				type result struct {
					value datasets.Dataset
					err   error
				}
				results := make(chan result, 2)
				start := make(chan struct{})
				for i := 0; i < 2; i++ {
					go func() {
						<-start
						v, e := g.s.Create(g.ctx, g.portal, input, key, uuid.NewString())
						results <- result{v, e}
					}()
				}
				close(start)
				one, two := <-results, <-results
				if one.err != nil || two.err != nil || one.value.ID == "" || one.value.ID != two.value.ID || g.count(t, "datasets") != 2 || g.count(t, "audit_events") != auditsBefore+2 {
					t.Fatal("concurrent intent/audit cardinality", one.err, two.err)
				}
			case "limits_invalid":
				policy := g.account.Config.Policy()
				policy.Datasets.MaxDatasetsPerWorkspace = 1
				g.account.Config.Runtime = &policy
				_, e := g.s.Create(g.ctx, g.portal, datasets.CreateRequest{WorkspaceID: g.f.Workspace, Name: "Over limit", Origin: "Synthetic", License: "CC0", RightsAsserted: true, Reason: "Bounded limit"}, uuid.NewString(), uuid.NewString())
				runtimeError(t, e, "DATASET_LIMIT_REACHED")
				if _, e = g.s.CreateDraft(g.ctx, g.portal, g.f.Workspace, d.ID, datasets.DraftRequest{SourceName: "../escape", ExpectedBytes: 1, SourceSHA256: strings.Repeat("a", 64), Reason: "Refuse path"}, uuid.NewString(), uuid.NewString()); e == nil {
					t.Fatal("path escaped")
				}
			}
		})
	}
}

func TestActualDatasetRuntimeImportRepairPublishClone(t *testing.T) {
	g := runtimeSetup(t, true)
	d, draft := g.upload(t, runtimeRow("source-a", "one \u4e2d\u6587")+"{bad}\n"+runtimeRow("source-b", "two \u0627\u0644\u0639\u0631\u0628\u064a\u0629"))
	g.process(t)
	draft = g.draft(t, d.ID, draft.ID)
	if draft.State != "editing" || draft.InvalidRows == nil || *draft.InvalidRows != 1 || *draft.TotalRows != 3 {
		t.Fatal("real inspection", draft)
	}
	page, e := g.s.RowPage(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, url.Values{"limit": {"2"}})
	if e != nil || len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatal("row page", e)
	}
	invalid := page.Items[1]
	fixed, e := g.s.ReplaceRow(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, invalid.ID, datasets.RowRequest{ExpectedRevision: draft.Revision, RowRevision: invalid.Revision, SourceID: "repaired", Prompt: "Repaired \u4e16\u754c", Completion: "Valid", Reason: "Actual invalid row repair"}, uuid.NewString())
	if e != nil || fixed.Revision != 2 {
		t.Fatal(e)
	}
	stale, e := g.s.ReplaceRow(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, invalid.ID, datasets.RowRequest{ExpectedRevision: draft.Revision, RowRevision: invalid.Revision, SourceID: "wrong", Prompt: "Wrong", Completion: "Wrong", Reason: "Stale edit refused"}, uuid.NewString())
	runtimeError(t, e, "DATASET_REVISION_CONFLICT")
	if stale.ID != "" {
		t.Fatal("stale row projection")
	}
	draft = g.draft(t, d.ID, draft.ID)
	appended, e := g.s.AppendRow(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, datasets.RowRequest{ExpectedRevision: draft.Revision, SourceID: "temporary", Prompt: "Temporary", Completion: "Temporary", Reason: "Append then delete"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	draft = g.draft(t, d.ID, draft.ID)
	if e = g.s.DeleteRow(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, appended.ID, draft.Revision, appended.Revision, "Delete temporary row", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	draft = g.draft(t, d.ID, draft.ID)
	if *draft.InvalidRows != 0 || *draft.TotalRows != 3 {
		t.Fatal("repaired/deleted counts", draft)
	}
	key := uuid.NewString()
	input := datasets.PublicationRequest{ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Publish actual repaired rows"}
	job, e := g.s.Publish(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, input, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	replay, e := g.s.Publish(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, input, key, uuid.NewString())
	if e != nil || replay.ID != job.ID {
		t.Fatal("publication replay", e)
	}
	g.process(t)
	v, e := g.s.GetVersion(g.ctx, g.portal, g.f.Workspace, d.ID, *job.VersionID)
	if e != nil || v.Ordinal != 1 || v.Manifest.TotalRows != 3 || v.PublishedAt.Location() != time.UTC {
		t.Fatal("immutable version", v, e)
	}
	file, asset, e := g.s.VersionFile(g.ctx, g.portal, g.f.Workspace, d.ID, v.ID, "samples.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(file)
	file.Close()
	if e != nil || int64(len(raw)) != asset.SizeBytes || !bytes.Contains(raw, []byte("Repaired")) {
		t.Fatal("actual immutable export", e)
	}
	if e = g.s.VerifyStored(g.ctx); e != nil {
		t.Fatal("full private integrity", e)
	}
	clone, e := g.s.CloneVersion(g.ctx, g.portal, g.f.Workspace, d.ID, v.ID, "Actual immutable clone", uuid.NewString(), uuid.NewString())
	if e != nil || clone.ParentVersionID == nil || *clone.ParentVersionID != v.ID {
		t.Fatal("clone", e)
	}
	g.process(t)
	clone = g.draft(t, d.ID, clone.ID)
	if clone.State != "editing" || *clone.TotalRows != 3 {
		t.Fatal("clone real import", clone)
	}
	if g.count(t, "dataset_versions") != 1 || g.count(t, "dataset_rows") != 7 || g.count(t, "dataset_row_history") != 9 {
		t.Fatal("real lineage/history counts")
	}
	versions, e := g.s.VersionPage(g.ctx, g.admin, g.f.Workspace, d.ID, url.Values{})
	if e != nil || len(versions.Items) != 1 {
		t.Fatal("version page", e)
	}
	drafts, e := g.s.DraftPage(g.ctx, g.portal, g.f.Workspace, d.ID, url.Values{})
	if e != nil || len(drafts.Items) != 2 {
		t.Fatal("draft page", e)
	}
}

func TestActualDatasetRuntimeBulkInspectionHistory(t *testing.T) {
	g := runtimeSetup(t, true)
	var raw strings.Builder
	for i := 0; i < 130; i++ {
		raw.WriteString(runtimeRow(fmt.Sprintf("group-%d", i), fmt.Sprintf("Prompt %d \u4e16\u754c", i)))
	}
	d, draft := g.upload(t, raw.String())
	g.process(t)
	draft = g.draft(t, d.ID, draft.ID)
	if draft.TotalRows == nil || *draft.TotalRows != 130 || *draft.InvalidRows != 0 || g.count(t, "dataset_rows") != 130 || g.count(t, "dataset_row_history") != 130 {
		t.Fatal("bounded multi-batch row/history persistence", draft)
	}
}

type runtimeFaultStore struct {
	transaction.Store
	rollback, unknown bool
}

func (s runtimeFaultStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	e := s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		if e := fn(ctx, q); e != nil {
			return e
		}
		if s.rollback {
			return &transaction.Error{Kind: transaction.Unavailable}
		}
		return nil
	})
	if e != nil {
		return e
	}
	if s.unknown {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return nil
}
func (g datasetRuntimeStory) wrapped(store transaction.Store) datasetRuntimeStory {
	account := *g.account
	account.DB = nil
	account.Storage = store
	ws := *g.s.Workspaces
	ws.DB = nil
	ws.Identity = &account
	s := *g.s
	s.DB = nil
	s.Identity = &account
	s.Workspaces = &ws
	g.account = &account
	g.s = &s
	return g
}
func TestActualDatasetRuntimeRollbackAndCommitRecovery(t *testing.T) {
	for _, name := range []string{"rollback", "unknown_create", "unknown_reads", "unknown_draft"} {
		t.Run(name, func(t *testing.T) {
			g := runtimeSetup(t, false)
			d := g.create(t)
			before := g.count(t, "audit_events")
			input := datasets.CreateRequest{WorkspaceID: g.f.Workspace, Name: "Fault receipt \u4e16\u754c", Origin: "Synthetic", License: "CC0", RightsAsserted: true, Reason: "Explicit transaction fault control"}
			key := uuid.NewString()
			switch name {
			case "rollback":
				broken := g.wrapped(runtimeFaultStore{Store: g.f.Store, rollback: true})
				v, e := broken.s.Create(g.ctx, g.portal, input, key, uuid.NewString())
				runtimeError(t, e, string(transaction.Unavailable))
				if v.ID != "" || g.count(t, "datasets") != 1 || g.count(t, "audit_events") != before {
					t.Fatal("rollback projected or persisted")
				}
			case "unknown_create":
				unknown := g.wrapped(runtimeFaultStore{Store: g.f.Store, unknown: true})
				v, e := unknown.s.Create(g.ctx, g.portal, input, key, uuid.NewString())
				runtimeError(t, e, string(transaction.CommitUnknown))
				if v.ID != "" {
					t.Fatal("unknown create projection")
				}
				recovered, e := g.s.Create(g.ctx, g.portal, input, key, uuid.NewString())
				if e != nil || recovered.ID == "" || g.count(t, "datasets") != 2 || g.count(t, "audit_events") != before+1 {
					t.Fatal("same-key committed recovery", e)
				}
			case "unknown_reads":
				unknown := g.wrapped(runtimeFaultStore{Store: g.f.Store, unknown: true})
				v, e := unknown.s.Get(g.ctx, g.portal, g.f.Workspace, d.ID)
				runtimeError(t, e, string(transaction.CommitUnknown))
				if !reflect.ValueOf(v).IsZero() {
					t.Fatal("unknown Get projection")
				}
				page, e := unknown.s.DatasetPage(g.ctx, g.portal, g.f.Workspace, url.Values{})
				runtimeError(t, e, string(transaction.CommitUnknown))
				if len(page.Items) != 0 || page.NextCursor != nil {
					t.Fatal("unknown page projection")
				}
				scope, e := unknown.s.ResolveDatasetWorkspace(g.ctx, g.admin, d.ID)
				runtimeError(t, e, string(transaction.CommitUnknown))
				if scope != "" {
					t.Fatal("unknown route projection")
				}
			case "unknown_draft":
				unknown := g.wrapped(runtimeFaultStore{Store: g.f.Store, unknown: true})
				request := datasets.DraftRequest{SourceName: "synthetic.jsonl", SourceSHA256: strings.Repeat("a", 64), ExpectedBytes: 1, Reason: "Recover draft intent"}
				v, e := unknown.s.CreateDraft(g.ctx, g.portal, g.f.Workspace, d.ID, request, key, uuid.NewString())
				runtimeError(t, e, string(transaction.CommitUnknown))
				if v.ID != "" {
					t.Fatal("unknown draft projection")
				}
				recovered, e := g.s.CreateDraft(g.ctx, g.portal, g.f.Workspace, d.ID, request, key, uuid.NewString())
				if e != nil || recovered.ID == "" || g.count(t, "dataset_drafts") != 1 {
					t.Fatal("draft key recovery", e)
				}
			}
		})
	}
}

func TestActualDatasetRuntimeWorkerRecoveryAndControls(t *testing.T) {
	for _, name := range []string{"idle", "cancel_pending", "epoch_revoked", "membership_replaced", "workspace_disabled", "policy_changed", "expired_attempt", "retry_failure", "restore_invalidation"} {
		t.Run(name, func(t *testing.T) {
			g := runtimeSetup(t, true)
			if name == "idle" {
				owned, e := g.s.ProcessNext(g.ctx)
				if e != nil || owned {
					t.Fatal("empty selected queue", owned, e)
				}
				return
			}
			d, draft := g.upload(t, runtimeRow("a", "one")+runtimeRow("b", "two"))
			jobID := *draft.CurrentJobID
			switch name {
			case "cancel_pending":
				job, e := g.s.CancelJob(g.ctx, g.portal, g.f.Workspace, d.ID, jobID, "Actual pending cancellation", uuid.NewString())
				if e != nil || job.State != "cancelled" || job.CompletedAt == nil {
					t.Fatal("pending cancellation", job, e)
				}
				owned, e := g.s.ProcessNext(g.ctx)
				if e != nil || owned {
					t.Fatal("cancelled job executed", owned, e)
				}
				draft, e = g.s.CancelDraft(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, "Cancel terminal draft", uuid.NewString())
				if e != nil || draft.State != "cancelled" {
					t.Fatal(e)
				}
			case "epoch_revoked":
				g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
				g.process(t)
				var state, code string
				if e := g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT state,error_code FROM dataset_jobs WHERE id=$1", "SELECT state,error_code FROM dataset_jobs WHERE id=?", jobID)).Scan(&state, &code); e != nil || state != "failed" || code != "DATASET_ACTOR_UNAVAILABLE" {
					t.Fatal("revoked actor worker", state, code, e)
				}
				if g.count(t, "dataset_rows") != 0 {
					t.Fatal("revoked content accepted")
				}
			case "membership_replaced":
				g.exec(t, "UPDATE workspace_memberships SET authority_id=$1 WHERE user_id=$2", "UPDATE workspace_memberships SET authority_id=? WHERE user_id=?", uuid.NewString(), g.f.User)
				g.process(t)
				job, e := g.s.GetJob(g.ctx, g.admin, g.f.Workspace, d.ID, jobID)
				if e != nil || job.ErrorCode == nil || *job.ErrorCode != "DATASET_ACTOR_UNAVAILABLE" {
					t.Fatal("recreated delegation accepted", job, e)
				}
			case "workspace_disabled":
				g.exec(t, "UPDATE workspaces SET status='disabled' WHERE id=$1", "UPDATE workspaces SET status='disabled' WHERE id=?", g.f.Workspace)
				g.process(t)
				if g.count(t, "dataset_rows") != 0 {
					t.Fatal("inactive Workspace accepted")
				}
			case "policy_changed":
				policy := g.account.Config.Policy()
				policy.Datasets.Limits.MaximumRows--
				g.account.Config.Runtime = &policy
				g.process(t)
				job, e := g.s.GetJob(g.ctx, g.portal, g.f.Workspace, d.ID, jobID)
				if e != nil || job.ErrorCode == nil || *job.ErrorCode != "DATASET_POLICY_CHANGED" {
					t.Fatal("changed policy accepted", job, e)
				}
			case "expired_attempt":
				id := uuid.NewString()
				g.exec(t, "INSERT INTO dataset_job_attempts(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state) VALUES($1,$2,$3,$4,$5,$6,1,'running')", "INSERT INTO dataset_job_attempts(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state) VALUES(?,?,?,?,?,?,1,'running')", g.f.Tenant, g.f.Workspace, d.ID, draft.ID, jobID, id)
				// Explicit interrupted lease fixture; the replacement processor execution is real.
				g.exec(t, "UPDATE dataset_jobs SET state='running',attempt_id=$1,attempt_count=1,lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$2", "UPDATE dataset_jobs SET state='running',attempt_id=?,attempt_count=1,lease_expires_at=TIMESTAMPADD(SECOND,-1,CURRENT_TIMESTAMP(6)) WHERE id=?", id, jobID)
				g.process(t)
				job, e := g.s.GetJob(g.ctx, g.portal, g.f.Workspace, d.ID, jobID)
				if e != nil || job.State != "succeeded" || job.AttemptCount != 2 || job.AttemptID == nil || *job.AttemptID == id {
					t.Fatal("expired attempt recovery", job, e)
				}
				var n int
				if e = g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT count(*) FROM dataset_job_attempts WHERE job_id=$1 AND state='expired'", "SELECT count(*) FROM dataset_job_attempts WHERE job_id=? AND state='expired'", jobID)).Scan(&n); e != nil || n != 1 {
					t.Fatal("expired attempt history", n, e)
				}
			case "retry_failure":
				// The failure program is a declared actual subprocess fault fixture. Recovery
				// executes the real installed processor, never a success/result stub.
				program := filepath.Join(runtimePrivateDir(t), "failure.py")
				if e := os.WriteFile(program, []byte("raise SystemExit(1)\n"), 0600); e != nil {
					t.Fatal(e)
				}
				bad := g.account.Config.Datasets
				bad.ProcessorFile = program
				p, e := datasets.NewProcessor(bad, g.account.Config.Policy().Datasets)
				if e != nil {
					t.Fatal(e)
				}
				g.s.Processor = p
				g.process(t)
				job, e := g.s.GetJob(g.ctx, g.portal, g.f.Workspace, d.ID, jobID)
				if e != nil || job.State != "failed" || !job.Retryable {
					t.Fatal("actual failing processor retry classification", job, e)
				}
				key := uuid.NewString()
				retry, e := g.s.RetryJob(g.ctx, g.portal, g.f.Workspace, d.ID, jobID, "Recover actual processor failure", key, uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				replay, e := g.s.RetryJob(g.ctx, g.portal, g.f.Workspace, d.ID, jobID, "Recover actual processor failure", key, uuid.NewString())
				if e != nil || replay.ID != retry.ID {
					t.Fatal("retry exact replay", e)
				}
				g.s.Processor, e = datasets.NewProcessor(g.account.Config.Datasets, g.account.Config.Policy().Datasets)
				if e != nil {
					t.Fatal(e)
				}
				g.exec(t, "UPDATE dataset_jobs SET not_before=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE dataset_jobs SET not_before=TIMESTAMPADD(SECOND,-1,CURRENT_TIMESTAMP(6)) WHERE id=?", retry.ID)
				g.process(t)
				job, e = g.s.GetJob(g.ctx, g.portal, g.f.Workspace, d.ID, retry.ID)
				if e != nil || job.State != "succeeded" {
					t.Fatal("actual recovered retry", job, e)
				}
			case "restore_invalidation":
				if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
					return datasets.InvalidateAuthoritiesWithin(ctx, q)
				}); e != nil {
					t.Fatal(e)
				}
				job, e := g.s.GetJob(g.ctx, g.portal, g.f.Workspace, d.ID, jobID)
				if e != nil || job.State != "cancelled" || job.ErrorCode == nil || *job.ErrorCode != "DATASET_RESTORE_INVALIDATED" {
					t.Fatal("restore stale authority", job, e)
				}
				owned, e := g.s.ProcessNext(g.ctx)
				if e != nil || owned {
					t.Fatal("restored authority executed", owned, e)
				}
			}
		})
	}
}
func runtimePrivateDir(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if e := os.Chmod(p, 0700); e != nil {
		t.Fatal(e)
	}
	return p
}

func TestActualDatasetRuntimeSnapshotRestartRetirementIntegrity(t *testing.T) {
	g := runtimeSetup(t, true)
	d, draft := g.upload(t, runtimeRow("a", "one")+runtimeRow("b", "two"))
	g.process(t)
	v := g.publish(t, d, draft)
	retired, e := g.s.RetireVersion(g.ctx, g.portal, g.f.Workspace, d.ID, v.ID, "Retain immutable history", uuid.NewString())
	if e != nil || retired.RetiredAt == nil {
		t.Fatal(e)
	}
	clone, e := g.s.CloneVersion(g.ctx, g.portal, g.f.Workspace, d.ID, v.ID, "Refuse retired clone", uuid.NewString(), uuid.NewString())
	runtimeError(t, e, "DATASET_VERSION_RETIRED")
	if clone.ID != "" {
		t.Fatal("retired clone projection")
	}
	destination := filepath.Join(runtimePrivateDir(t), "snapshot")
	if e = g.s.ExportFiles(g.ctx, destination); e != nil {
		t.Fatal("real snapshot export", e)
	}
	if e = g.s.AuditFileOperation(g.ctx, "export", "Actual isolated file snapshot export"); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(destination, "snapshot.json"))
	if e != nil {
		t.Fatal(e)
	}
	var snapshot datasets.FileSnapshot
	if e = json.Unmarshal(raw, &snapshot); e != nil || len(snapshot.References.Versions) != 1 || len(snapshot.Files) < 8 {
		t.Fatal("snapshot reference inventory", e)
	}
	target := runtimePrivateDir(t)
	fresh := &datasets.Service{DB: g.s.DB, Identity: g.account, Workspaces: g.s.Workspaces}
	if e = fresh.ImportFiles(g.ctx, destination, target); e != nil {
		t.Fatal("actual fresh private restore", e)
	}
	store, e := datasets.OpenStore(target, g.f.Tenant, 4096)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	fresh.Store = store
	if store.Namespace() != g.s.Store.Namespace() {
		t.Fatal("restored namespace identity")
	}
	if e = fresh.VerifyStored(g.ctx); e != nil {
		t.Fatal("restored file verification", e)
	}
	// Corruption is tested before mutation of the empty restore destination.
	path := ""
	for _, file := range snapshot.Files {
		if strings.HasSuffix(file.Path, "samples.jsonl") {
			path = filepath.Join(destination, filepath.FromSlash(file.Path))
		}
	}
	if path == "" {
		t.Fatal("real published sample absent")
	}
	original, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	empty := runtimePrivateDir(t)
	fresh.Store = nil
	if e = fresh.ImportFiles(g.ctx, destination, empty); e == nil {
		t.Fatal("corrupt archive accepted")
	}
	entries, e := os.ReadDir(empty)
	if e != nil || len(entries) != 0 {
		t.Fatal("corruption mutated destination", e)
	}
	if e = os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	// Reopen the real configured namespace after its previous owner has exited.
	if e = g.s.Store.Close(); e != nil {
		t.Fatal(e)
	}
	restarted := &datasets.Service{DB: g.s.DB, Identity: g.account, Workspaces: g.s.Workspaces}
	if e = restarted.OpenConfiguredStore(g.ctx); e != nil {
		t.Fatal("real configured restart verification", e)
	}
	defer restarted.Store.Close()
	current, e := restarted.Get(g.ctx, g.portal, g.f.Workspace, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	current, e = restarted.Retire(g.ctx, g.portal, g.f.Workspace, d.ID, current.Revision, "Retire actual dataset and retain history", uuid.NewString())
	if e != nil || current.RetiredAt == nil {
		t.Fatal("dataset retirement", e)
	}
	file, _, e := restarted.VersionFile(g.ctx, g.portal, g.f.Workspace, d.ID, v.ID, "train.jsonl")
	if e != nil {
		t.Fatal("retired immutable export", e)
	}
	file.Close()
	if e = restarted.VerifyStored(g.ctx); e != nil {
		t.Fatal("retired stored integrity", e)
	}
}

func TestActualDatasetRuntimeUploadFailureAndRetirement(t *testing.T) {
	for _, name := range []string{"incomplete_recovery", "retirement_pending", "cancel_draft_upload"} {
		t.Run(name, func(t *testing.T) {
			g := runtimeSetup(t, true)
			raw := runtimeRow("a", "one") + runtimeRow("b", "two")
			sum := sha256.Sum256([]byte(raw))
			d := g.create(t)
			draft, e := g.s.CreateDraft(g.ctx, g.portal, g.f.Workspace, d.ID, datasets.DraftRequest{SourceName: "fixture.jsonl", SourceSHA256: hex.EncodeToString(sum[:]), ExpectedBytes: int64(len(raw)), Reason: "Recover upload source"}, uuid.NewString(), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			if name == "cancel_draft_upload" {
				cancelled, e := g.s.CancelDraft(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, "Cancel unused upload", uuid.NewString())
				if e != nil || cancelled.State != "cancelled" {
					t.Fatal(e)
				}
				return
			}
			if name == "incomplete_recovery" {
				failed, e := g.s.Upload(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, draft.SourceSHA256, draft.ExpectedBytes, strings.NewReader(raw[:10]), uuid.NewString())
				if e == nil || failed.ID != "" {
					t.Fatal("incomplete upload succeeded/projected", e)
				}
				draft = g.draft(t, d.ID, draft.ID)
				if draft.State != "failed" || draft.SourceReceivedAt != nil || g.count(t, "dataset_jobs") != 0 {
					t.Fatal("failed upload left runnable intent", draft)
				}
			}
			draft, e = g.s.Upload(g.ctx, g.portal, g.f.Workspace, d.ID, draft.ID, draft.SourceSHA256, draft.ExpectedBytes, strings.NewReader(raw), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			if name == "retirement_pending" {
				retired, e := g.s.Retire(g.ctx, g.portal, g.f.Workspace, d.ID, d.Revision, "Retire pending import", uuid.NewString())
				if e != nil || retired.RetiredAt == nil {
					t.Fatal(e)
				}
				job, e := g.s.GetJob(g.ctx, g.portal, g.f.Workspace, d.ID, *draft.CurrentJobID)
				if e != nil || job.State != "cancelled" || job.CompletedAt == nil || job.ErrorCode == nil || *job.ErrorCode != "DATASET_RETIRED" {
					t.Fatal("pending retirement assignment semantics", job, e)
				}
				owned, e := g.s.ProcessNext(g.ctx)
				if e != nil || owned {
					t.Fatal("retired queue executed", owned, e)
				}
			} else {
				g.process(t)
				if draft = g.draft(t, d.ID, draft.ID); draft.State != "editing" || *draft.InvalidRows != 0 {
					t.Fatal("recovered upload", draft)
				}
			}
		})
	}
}

func runtimeUnknownWorkerClaim(t *testing.T) {
	g := runtimeSetup(t, true)
	d, draft := g.upload(t, runtimeRow("a", "one")+runtimeRow("b", "two"))
	unknown := g.wrapped(runtimeFaultStore{Store: g.f.Store, unknown: true})
	owned, e := unknown.s.ProcessNext(g.ctx)
	runtimeError(t, e, string(transaction.CommitUnknown))
	if !owned {
		t.Fatal("unknown claim lost candidate")
	}
	if g.count(t, "dataset_rows") != 0 {
		t.Fatal("unknown claim executed processor")
	}
	job, e := g.s.GetJob(g.ctx, g.portal, g.f.Workspace, d.ID, *draft.CurrentJobID)
	if e != nil || job.State != "running" || job.AttemptCount != 1 {
		t.Fatal("uncertain durable claim", job, e)
	}
	entries, e := os.ReadDir(filepath.Join(g.root, "attempts"))
	if e != nil || len(entries) != 0 {
		t.Fatal("unknown claim wrote attempt files", e)
	}
	g.exec(t, "UPDATE dataset_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE dataset_jobs SET lease_expires_at=TIMESTAMPADD(SECOND,-1,CURRENT_TIMESTAMP(6)) WHERE id=?", job.ID)
	g.process(t)
	job, e = g.s.GetJob(g.ctx, g.portal, g.f.Workspace, d.ID, job.ID)
	if e != nil || job.State != "succeeded" || job.AttemptCount != 2 {
		t.Fatal("explicit expired claim recovery", job, e)
	}
	cancelled, cancel := context.WithCancel(g.ctx)
	cancel()
	_, e = g.s.ProcessNext(cancelled)
	if e == nil || !errors.Is(cancelled.Err(), context.Canceled) {
		t.Fatal("cancelled worker context accepted", e)
	}
}

// This decorator injects one pre-execution failure into the real final acceptance
// transaction, after row insertion and before history insertion. No driver/result
// is fabricated; the actual engine must roll back rows and metadata atomically.
type runtimeAcceptanceFailureStore struct {
	transaction.Store
	calls int
}

func (s *runtimeAcceptanceFailureStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	s.calls++
	number := s.calls
	return s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		if number == 2 {
			return fn(ctx, &runtimeAcceptanceFailureExecutor{Executor: q})
		}
		return fn(ctx, q)
	})
}

type runtimeAcceptanceFailureExecutor struct {
	transaction.Executor
	calls int
}

func (q *runtimeAcceptanceFailureExecutor) Exec(ctx context.Context, statement transaction.Statement) (int64, error) {
	q.calls++
	if q.calls == 2 {
		return 0, &transaction.Error{Kind: transaction.Unavailable}
	}
	return q.Executor.Exec(ctx, statement)
}
func TestActualDatasetRuntimeWorkerCommitUncertainty(t *testing.T) {
	t.Run("unknown_claim", runtimeUnknownWorkerClaim)
	t.Run("acceptance_rollback", func(t *testing.T) {
		g := runtimeSetup(t, true)
		d, draft := g.upload(t, runtimeRow("a", "one")+runtimeRow("b", "two"))
		fault := &runtimeAcceptanceFailureStore{Store: g.f.Store}
		broken := g.wrapped(fault)
		owned, e := broken.s.ProcessNext(g.ctx)
		runtimeError(t, e, string(transaction.Unavailable))
		if !owned {
			t.Fatal("acceptance lost candidate")
		}
		if g.count(t, "dataset_rows") != 0 || g.count(t, "dataset_row_history") != 0 {
			t.Fatal("failed acceptance leaked rows/history")
		}
		current := g.draft(t, d.ID, draft.ID)
		if current.State != "validating" || current.TotalRows != nil {
			t.Fatal("failed acceptance published metadata", current)
		}
		job, e := g.s.GetJob(g.ctx, g.portal, g.f.Workspace, d.ID, *draft.CurrentJobID)
		if e != nil || job.State != "running" || job.AttemptCount != 1 {
			t.Fatal("failed acceptance damaged recovery authority", job, e)
		}
		g.exec(t, "UPDATE dataset_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE dataset_jobs SET lease_expires_at=TIMESTAMPADD(SECOND,-1,CURRENT_TIMESTAMP(6)) WHERE id=?", job.ID)
		g.process(t)
		job, e = g.s.GetJob(g.ctx, g.portal, g.f.Workspace, d.ID, job.ID)
		if e != nil || job.State != "succeeded" || job.AttemptCount != 2 || g.count(t, "dataset_rows") != 2 || g.count(t, "dataset_row_history") != 2 {
			t.Fatal("real acceptance rollback recovery", job, e)
		}
	})
}

//go:build repository_integration

package modelpreparations

import (
	"bytes"
	"context"
	"encoding/hex"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/google/uuid"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type holdoutStory struct {
	g          reviewStory
	input      HoldoutRequest
	root, path string
}

func holdoutSetup(t *testing.T) holdoutStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	t.Cleanup(cancel)
	owner := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		owner.Storage = nil
		owner.DB = f.PostgreSQL
	}
	ws := &workspaces.Service{DB: f.PostgreSQL, Identity: owner}
	p := owner.Config.Policy()
	p.ModelLifecycle.ScanConcurrency = 2
	owner.Config.Runtime = &p
	var issued identity.IssuedSession
	if e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = owner.IssueSessionWithin(ctx, q, f.User, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	auth, e := owner.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(t.TempDir(), "datasets")
	if e = os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e = datasets.InitializeStore(root, f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	ds, e := datasets.OpenStore(root, f.Tenant, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := ds.Close(); e != nil {
			t.Error(e)
		}
	})
	dataset := &datasets.Service{DB: f.PostgreSQL, Identity: owner, Workspaces: ws, Store: ds}
	s, e := New(f.PostgreSQL, owner, ws, dataset)
	if e != nil {
		t.Fatal(e)
	}
	g := reviewStory{f: f, ctx: ctx, s: s, a: auth}
	input, path := holdoutSource(t, g, root, nil)
	return holdoutStory{g: g, input: input, root: root, path: path}
}
func (g holdoutStory) reserve(key string) (Holdout, error) {
	return g.g.s.ReserveHoldout(g.g.ctx, g.g.a, g.input, key, uuid.NewString())
}
func holdoutWant(t *testing.T, v Holdout, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code || !reflect.DeepEqual(v, Holdout{}) {
		t.Fatal("zero failed holdout", e, code, v)
	}
}
func (g holdoutStory) counts(t *testing.T, n int) {
	t.Helper()
	var h, a int
	if e := g.g.f.Store.QueryRow(g.g.ctx, g.g.f.Statement(`SELECT COUNT(*) FROM model_holdouts WHERE tenant_id=$1 AND workspace_id=$2`, `SELECT COUNT(*) FROM model_holdouts WHERE tenant_id=? AND workspace_id=?`, g.g.f.Tenant, g.g.f.Workspace)).Scan(&h); e != nil {
		t.Fatal(e)
	}
	if e := g.g.f.Store.QueryRow(g.g.ctx, g.g.f.Statement(`SELECT COUNT(*) FROM audit_events WHERE workspace_id=$1 AND action='model.holdout_reserved' AND target_type='model_holdout'`, `SELECT COUNT(*) FROM audit_events WHERE workspace_id=? AND action='model.holdout_reserved' AND target_type='model_holdout'`, g.g.f.Workspace)).Scan(&a); e != nil || h != n || a != n {
		t.Fatal("atomic holdout/audit", h, a, n, e)
	}
}
func (g holdoutStory) trainingReference(t *testing.T, input HoldoutRequest) {
	t.Helper()
	node, model := uuid.NewString(), uuid.NewString()
	g.g.seed(t, "managed_nodes", map[string]any{"id": node, "name": node, "status": "removed", "credential_hash": nil, "protocol_version": wire.NodeProtocolVersion, "execution_profile": wire.NodeProfile, "capabilities": reviewJSON(reviewRaw(t, agent.Capabilities(agent.DockerModelLifecycleProfile))), "last_heartbeat_at": time.Now().UTC(), "facts": reviewJSON(reviewRaw(t, agent.Facts{OSID: "synthetic", Architecture: "test", Hostname: "Synthetic holdout SQL fixture", MemoryTotalBytes: 1048576, ObservedAt: time.Now().UTC()}))})
	g.g.seed(t, "model_versions", map[string]any{"id": model, "name": "Synthetic holdout base " + model, "model_key": agent.ApprovedModel().ModelKey, "catalog_digest": agent.ModelCatalogDigest(), "manifest": reviewJSON(reviewRaw(t, agent.ApprovedModel())), "state": "ready", "created_by": g.g.f.User})
	g.g.seed(t, "training_jobs", map[string]any{"id": uuid.NewString(), "node_id": node, "current_node_id": node, "model_id": model, "dataset_id": input.DatasetID, "dataset_version_id": input.DatasetVersionID, "gpu_uuid": "GPU-" + uuid.NewString(), "configuration": reviewJSON(`{}`), "configuration_sha256": strings.Repeat("a", 64), "execution_recipe": reviewJSON(`{}`), "dataset_manifest_sha256": input.ExpectedManifestSHA256, "model_catalog_sha256": agent.ModelCatalogDigest(), "created_by": g.g.f.User, "requested_by": g.g.f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Explicit synthetic training-reference envelope", "creation_key": uuid.NewString(), "creation_fingerprint": bytes.Repeat([]byte{8}, 32), "state": "cancelled"})
}
func holdoutSource(t *testing.T, g reviewStory, root string, records []datawire.Record) (HoldoutRequest, string) {
	t.Helper()
	dataset, version, draft, job, attempt := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	fp := bytes.Repeat([]byte{3}, 32)
	var payload []byte
	if records == nil {
		for _, label := range wire.Labels() {
			records = append(records, datawire.Record{SourceID: uuid.NewString(), Prompt: "Disjoint synthetic heldout " + uuid.NewString(), Completion: label})
		}
	}
	for _, record := range records {
		payload = append(payload, reviewRaw(t, record)...)
		payload = append(payload, '\n')
	}

	digest := hash(payload)
	put := func(table string, v map[string]any) { g.seed(t, table, v) }
	j := func(v any) reviewJSON { return reviewJSON(reviewRaw(t, v)) }
	put("datasets", map[string]any{"id": dataset, "name": "Synthetic creator heldout", "name_key": dataset, "origin": "local synthetic rows", "license": "CC0 synthetic fixture", "rights_asserted": true, "created_by": g.f.User, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	put("dataset_drafts", map[string]any{"id": draft, "dataset_id": dataset, "created_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "source_object_id": uuid.NewString(), "source_name": "heldout.jsonl", "source_sha256": digest, "expected_bytes": len(payload), "source_received_at": now, "state": "published", "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	put("dataset_jobs", map[string]any{"id": job, "dataset_id": dataset, "draft_id": draft, "kind": "publish", "requested_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic heldout metadata", "expected_revision": 1, "state": "succeeded", "version_id": version, "input_policy": reviewJSON(`{}`), "split_seed": strings.Repeat("1", 32), "validation_basis_points": 1000, "completed_at": now, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	put("dataset_job_attempts", map[string]any{"id": attempt, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_number": 1, "state": "succeeded", "finished_at": now})
	manifest := datawire.Manifest{SchemaVersion: 1, RowSchema: datawire.RowSchema, ProcessorVersion: datawire.ProcessorVersion, SplitAlgorithm: datawire.SplitAlgorithm, SourceSHA256: digest, Seed: strings.Repeat("1", 32), ValidationBasisPoints: 1000, TotalRows: 3, UniqueRows: 3, SourceGroups: 3, ValidationGroups: 1, TrainRows: 2, ValidationRows: 1, ManifestFile: datawire.Asset{Path: "manifest.json", SizeBytes: 2, SHA256: digest}}
	for _, name := range []string{"samples.jsonl", "train.jsonl", "validation.jsonl", "assignments.jsonl", "duplicates.jsonl"} {
		manifest.Files = append(manifest.Files, datawire.Asset{Path: name, SizeBytes: 0, SHA256: hash(nil)})
	}
	put("dataset_versions", map[string]any{"id": version, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_id": attempt, "ordinal": 1, "published_by": g.f.User, "published_at": now, "raw_source_sha256": digest, "effective_source_sha256": digest, "manifest_sha256": digest, "manifest": j(manifest), "row_mapping_sha256": digest})
	directory := filepath.Join(root, "versions", version, attempt)
	if e := os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(directory, "effective.jsonl")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Write(payload); e != nil {
		t.Fatal(e)
	}
	if e = f.Sync(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	d, e := os.Open(directory)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Sync(); e != nil {
		t.Fatal(e)
	}
	d.Close()
	return HoldoutRequest{WorkspaceID: g.f.Workspace, DatasetID: dataset, DatasetVersionID: version, ExpectedManifestSHA256: digest, Name: "Actual synthetic holdout \u4e16\u754c", Reason: "Actual real-file reservation over synthetic publication metadata"}, path
}
func TestActualPairedHoldoutReservation(t *testing.T) {
	t.Run("actual_source_reservation_fingerprints_and_exact_replay", func(t *testing.T) {
		g := holdoutSetup(t)
		key := uuid.NewString()
		v, e := g.reserve(key)
		if e != nil || v.RowCount != 3 || v.SourceBytes < 1 || v.Labels != (LabelCounts{Account: 1, Billing: 1, Technical: 1}) || v.CreatedAt.Location() != time.UTC {
			t.Fatal(v, e)
		}
		again, e := g.reserve(key)
		if e != nil || !reflect.DeepEqual(v, again) {
			t.Fatal("exact replay", e)
		}
		saved := g.g.s.Datasets
		g.g.s.Datasets = nil
		again, e = g.reserve(key)
		g.g.s.Datasets = saved
		if e != nil || !reflect.DeepEqual(v, again) {
			t.Fatal("replay before dependency", e)
		}
		var groups, prompts int
		for _, q := range []struct {
			table string
			count *int
		}{{"model_holdout_sources", &groups}, {"model_holdout_prompts", &prompts}} {
			if e = g.g.f.Store.QueryRow(g.g.ctx, g.g.f.Statement("SELECT COUNT(*) FROM "+q.table+" WHERE holdout_id=$1", "SELECT COUNT(*) FROM "+q.table+" WHERE holdout_id=?", v.ID)).Scan(q.count); e != nil {
				t.Fatal(e)
			}
		}
		if groups != 3 || prompts != 6 {
			t.Fatal(groups, prompts)
		}
		raw, e := os.ReadFile(g.path)
		if e != nil {
			t.Fatal(e)
		}
		for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
			record, e := datawire.DecodeRecord(line, parserCeilings())
			if e != nil {
				t.Fatal(e)
			}
			for _, prompt := range []string{record.Prompt, wire.InstructionPrompt(record.Prompt)} {
				digest, _ := wire.PromptFingerprint(prompt)
				b, _ := hex.DecodeString(digest)
				var n int
				if e = g.g.f.Store.QueryRow(g.g.ctx, g.g.f.Statement("SELECT COUNT(*) FROM model_holdout_prompts WHERE holdout_id=$1 AND prompt_sha256=$2", "SELECT COUNT(*) FROM model_holdout_prompts WHERE holdout_id=? AND prompt_sha256=?", v.ID, b)).Scan(&n); e != nil || n != 1 {
					t.Fatal("actual canonical prompt reservation", e)
				}
			}
		}
		g.counts(t, 1)
		v, e = g.reserve(uuid.NewString())
		holdoutWant(t, v, e, "MODEL_HOLDOUT_EXISTS")
		bad := g
		bad.input.Reason += " changed"
		v, e = bad.reserve(key)
		holdoutWant(t, v, e, "IDEMPOTENCY_CONFLICT")
	})
	t.Run("current_authority_workspace_and_membership", func(t *testing.T) {
		g := holdoutSetup(t)
		g.g.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'member')", g.g.f.User, g.g.f.Tenant, g.g.f.Workspace)
		var issued identity.IssuedSession
		if e := g.g.f.Store.Within(g.g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			issued, e = g.g.s.Identity.IssueSessionWithin(ctx, q, g.g.f.User, 1, "portal", "", uuid.NewString(), nil)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		portal, e := g.g.s.Identity.Authenticate(g.g.ctx, issued.Token, "portal")
		if e != nil {
			t.Fatal(e)
		}
		v, e := g.g.s.ReserveHoldout(g.g.ctx, portal, g.input, uuid.NewString(), uuid.NewString())
		holdoutWant(t, v, e, "ACCESS_DENIED")
		g.g.exec(t, "UPDATE workspace_memberships SET role='owner',authority_id=$1 WHERE user_id=$2 AND workspace_id=$3", "UPDATE workspace_memberships SET role='owner',authority_id=? WHERE user_id=? AND workspace_id=?", uuid.NewString(), g.g.f.User, g.g.f.Workspace)
		v, e = g.g.s.ReserveHoldout(g.g.ctx, portal, g.input, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal("current owner", e)
		}
		wrong := g
		wrong.input.WorkspaceID = uuid.NewString()
		v, e = wrong.reserve(uuid.NewString())
		holdoutWant(t, v, e, "RESOURCE_NOT_FOUND")
		g.g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.g.f.User)
		v, e = g.reserve(uuid.NewString())
		holdoutWant(t, v, e, "AUTH_SESSION_EXPIRED")
		g.counts(t, 1)
	})
	t.Run("source_labels_duplicate_and_overlap_refusal", func(t *testing.T) {
		g := holdoutSetup(t)
		for _, records := range [][]datawire.Record{{{SourceID: "a", Prompt: "a", Completion: "account"}, {SourceID: "b", Prompt: "b", Completion: "account"}, {SourceID: "c", Prompt: "c", Completion: "technical"}}, {{SourceID: "a", Prompt: "same", Completion: "account"}, {SourceID: "b", Prompt: "same", Completion: "billing"}, {SourceID: "c", Prompt: "c", Completion: "technical"}}} {
			x := g
			x.input, _ = holdoutSource(t, g.g, g.root, records)
			v, e := x.reserve(uuid.NewString())
			holdoutWant(t, v, e, "MODEL_HOLDOUT_LABELS_INVALID")
		}
		g.trainingReference(t, g.input)
		v, e := g.reserve(uuid.NewString())
		holdoutWant(t, v, e, "MODEL_HOLDOUT_OVERLAP")
		g.counts(t, 0)
	})
	t.Run("real_file_corruption_and_retirement", func(t *testing.T) {
		g := holdoutSetup(t)
		original, e := os.ReadFile(g.path)
		if e != nil {
			t.Fatal(e)
		}
		bad := append([]byte{}, original...)
		bad[0] ^= 1
		if e = os.WriteFile(g.path, bad, 0600); e != nil {
			t.Fatal(e)
		}
		v, e := g.reserve(uuid.NewString())
		holdoutWant(t, v, e, "MODEL_HOLDOUT_SOURCE_CHANGED")
		if e = os.WriteFile(g.path, original, 0600); e != nil {
			t.Fatal(e)
		}
		g.g.exec(t, "UPDATE datasets SET retired_at=clock_timestamp(),retire_reason='explicit synthetic retirement' WHERE id=$1", "UPDATE datasets SET retired_at=UTC_TIMESTAMP(6),retire_reason='explicit synthetic retirement' WHERE id=?", g.input.DatasetID)
		v, e = g.reserve(uuid.NewString())
		holdoutWant(t, v, e, "DATASET_RETIRED")
		g.counts(t, 0)
	})
	t.Run("serialized_limits_and_concurrent_reservation", func(t *testing.T) {
		g := holdoutSetup(t)
		p := g.g.s.Identity.Config.Policy()
		p.ModelLifecycle.MaxHoldoutsPerWorkspace = 1
		g.g.s.Identity.Config.Runtime = &p
		key := uuid.NewString()
		var wg sync.WaitGroup
		var v [2]Holdout
		var failures [2]error
		for i := range 2 {
			wg.Add(1)
			go func(i int) { defer wg.Done(); v[i], failures[i] = g.reserve(key) }(i)
		}
		wg.Wait()
		if failures[0] != nil || failures[1] != nil || !reflect.DeepEqual(v[0], v[1]) {
			t.Fatal("serialized exact replay", failures)
		}
		x := g
		x.input, _ = holdoutSource(t, g.g, g.root, nil)
		out, e := x.reserve(uuid.NewString())
		holdoutWant(t, out, e, "MODEL_HOLDOUT_LIMIT_REACHED")
		g.counts(t, 1)
	})
	t.Run("audit_rollback_and_retry", func(t *testing.T) {
		g := holdoutSetup(t)
		g.g.exec(t, `CREATE FUNCTION explicit_holdout_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'explicit test audit failure'; END;$$`, `CREATE TRIGGER explicit_holdout_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='explicit test audit failure'`)
		if g.g.f.Store.Dialect() == transaction.PostgreSQL {
			g.g.exec(t, `CREATE TRIGGER explicit_holdout_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION explicit_holdout_audit_failure()`, "")
		}
		key := uuid.NewString()
		v, e := g.reserve(key)
		holdoutWant(t, v, e, "STORAGE_UNAVAILABLE")
		g.counts(t, 0)
		var n int
		if e = g.g.f.Store.QueryRow(g.g.ctx, g.g.f.Statement("SELECT COUNT(*) FROM model_holdout_prompts WHERE workspace_id=$1", "SELECT COUNT(*) FROM model_holdout_prompts WHERE workspace_id=?", g.g.f.Workspace)).Scan(&n); e != nil || n != 0 {
			t.Fatal("rollback fingerprints", n, e)
		}
		g.g.exec(t, "DROP TRIGGER explicit_holdout_audit_failure ON audit_events", "DROP TRIGGER explicit_holdout_audit_failure")
		if _, e = g.reserve(key); e != nil {
			t.Fatal(e)
		}
		g.counts(t, 1)
	})
	t.Run("unknown_commit_zero_and_same_key_recovery", func(t *testing.T) {
		g := holdoutSetup(t)
		for _, at := range []int{1, 3, 4} {
			x := g
			x.input, _ = holdoutSource(t, g.g, g.root, nil)
			wrapped := &reviewUnknownStore{Store: g.g.f.Store, failAt: at}
			owner := &identity.Service{Storage: wrapped, Config: g.g.s.Identity.Config}
			ws := &workspaces.Service{Identity: owner}
			copyService := *g.g.s
			copyService.DB = nil
			copyService.Identity = owner
			copyService.Workspaces = ws
			dataset := *g.g.s.Datasets.(*datasets.Service)
			dataset.DB = nil
			dataset.Identity = owner
			dataset.Workspaces = ws
			copyService.Datasets = &dataset
			x.g.s = &copyService
			key := uuid.NewString()
			v, e := x.reserve(key)
			holdoutWant(t, v, e, string(transaction.CommitUnknown))
			v, e = x.reserve(key)
			if e != nil || v.ID == "" {
				t.Fatal("same-key durable recovery", at, e)
			}
		}

		g.counts(t, 3)
		wrapped := &reviewUnknownStore{Store: g.g.f.Store, failAt: 1}
		owner := &identity.Service{Storage: wrapped, Config: g.g.s.Identity.Config}
		ws := &workspaces.Service{Identity: owner}
		copyService := *g.g.s
		copyService.DB = nil
		copyService.Identity = owner
		copyService.Workspaces = ws
		page, e := copyService.ListHoldouts(g.g.ctx, g.g.a, g.input.WorkspaceID, url.Values{})
		if e == nil || e.Error() != string(transaction.CommitUnknown) || len(page.Items) != 0 || page.NextCursor != nil {
			t.Fatal("uncertain list cannot leak rows", e, page)
		}
	})
	t.Run("actual_get_list_keyset_and_scope", func(t *testing.T) {
		g := holdoutSetup(t)
		ids := map[string]bool{}
		for range 3 {
			x := g
			x.input, _ = holdoutSource(t, g.g, g.root, nil)
			v, e := x.reserve(uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			ids[v.ID] = true
			read, e := g.g.s.GetHoldout(g.g.ctx, g.g.a, g.input.WorkspaceID, v.ID)
			if e != nil || !reflect.DeepEqual(v, read) {
				t.Fatal("actual detail", e)
			}
		}
		q := url.Values{"limit": {"1"}}
		seen := map[string]bool{}
		var cursor string
		for range 3 {
			page, e := g.g.s.ListHoldouts(g.g.ctx, g.g.a, g.input.WorkspaceID, q)
			if e != nil || len(page.Items) != 1 || !ids[page.Items[0].ID] || seen[page.Items[0].ID] {
				t.Fatal(page, e)
			}
			seen[page.Items[0].ID] = true
			if page.NextCursor != nil {
				cursor = *page.NextCursor
				q.Set("cursor", cursor)
			}
		}
		_, e := g.g.s.ListHoldouts(g.g.ctx, g.g.a, uuid.NewString(), url.Values{"cursor": {cursor}})
		if e == nil {
			t.Fatal("cross Workspace cursor")
		}
		v, e := g.g.s.GetHoldout(g.g.ctx, g.g.a, g.input.WorkspaceID, uuid.NewString())
		holdoutWant(t, v, e, "MODEL_HOLDOUT_NOT_FOUND")
		g.counts(t, 3)
	})
	t.Run("training_reference_limits_and_final_source_fence", func(t *testing.T) {
		g := holdoutSetup(t)
		ds := g.g.s.Datasets.(*datasets.Service)
		g.g.s.Datasets = creatorSourceHook{Service: ds, afterOpen: func() { g.trainingReference(t, g.input) }}
		v, e := g.reserve(uuid.NewString())
		holdoutWant(t, v, e, "MODEL_HOLDOUT_CONCURRENT_TRAINING_RETRY")
		g.g.s.Datasets = ds
		v, e = g.reserve(uuid.NewString())
		holdoutWant(t, v, e, "MODEL_HOLDOUT_OVERLAP")
		p := g.g.s.Identity.Config.Policy()
		p.ModelLifecycle.ScanMaximumVersions = 1
		g.g.s.Identity.Config.Runtime = &p
		v, e = g.reserve(uuid.NewString())
		holdoutWant(t, v, e, "MODEL_HOLDOUT_ANALYSIS_LIMIT")
		p.ModelLifecycle.ScanMaximumVersions = 1000
		g.g.s.Identity.Config.Runtime = &p
		x := g
		x.input, _ = holdoutSource(t, g.g, g.root, nil)
		var once sync.Once
		x.g.s.Datasets = creatorSourceHook{Service: ds, afterOpen: func() {
			once.Do(func() {
				g.g.exec(t, "UPDATE datasets SET retired_at=clock_timestamp(),retire_reason='explicit final source fence' WHERE id=$1", "UPDATE datasets SET retired_at=UTC_TIMESTAMP(6),retire_reason='explicit final source fence' WHERE id=?", x.input.DatasetID)
			})
		}}
		v, e = x.reserve(uuid.NewString())
		holdoutWant(t, v, e, "DATASET_RETIRED")
		g.g.s.Datasets = ds
		g.counts(t, 0)
	})
	t.Run("cancel_deadline_query_and_store_coherence", func(t *testing.T) {
		g := holdoutSetup(t)
		ctx, cancel := context.WithCancel(g.g.ctx)
		cancel()
		v, e := g.g.s.ReserveHoldout(ctx, g.g.a, g.input, uuid.NewString(), uuid.NewString())
		holdoutWant(t, v, e, string(transaction.Cancelled))
		ctx, cancel = context.WithDeadline(g.g.ctx, time.Now().Add(-time.Second))
		defer cancel()
		v, e = g.g.s.ReserveHoldout(ctx, g.g.a, g.input, uuid.NewString(), uuid.NewString())
		holdoutWant(t, v, e, string(transaction.Timeout))
		g.g.exec(t, "ALTER TABLE model_holdouts RENAME TO explicit_holdouts_unavailable", "RENAME TABLE model_holdouts TO explicit_holdouts_unavailable")
		v, e = g.reserve(uuid.NewString())
		holdoutWant(t, v, e, "STORAGE_UNAVAILABLE")
		g.g.exec(t, "ALTER TABLE explicit_holdouts_unavailable RENAME TO model_holdouts", "RENAME TABLE explicit_holdouts_unavailable TO model_holdouts")
		saved := g.g.s.Workspaces
		g.g.s.Workspaces = &workspaces.Service{Identity: &identity.Service{}}
		v, e = g.reserve(uuid.NewString())
		holdoutWant(t, v, e, "MODEL_PREPARATION_IDENTITY_REQUIRED")
		g.g.s.Workspaces = saved
		g.counts(t, 0)
	})
}

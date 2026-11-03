//go:build repository_integration

package modelpreparations

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type creatorStory struct {
	g                         reviewStory
	source                    creatorAdapterStory
	input                     CreateRequest
	trainingRoot, heldoutFile string
}

func creatorStoreDirectory(t *testing.T, root, name string) string {
	t.Helper()
	p := filepath.Join(root, name)
	if e := os.Mkdir(p, 0700); e != nil {
		t.Fatal(e)
	}
	return p
}
func creatorSetup(t *testing.T) creatorStory {
	t.Helper()
	g := reviewSetup(t)
	root := t.TempDir()
	trainingRoot := creatorStoreDirectory(t, root, "training")
	datasetRoot := creatorStoreDirectory(t, root, "datasets")
	modelRoot := creatorStoreDirectory(t, root, "models")
	prepRoot := creatorStoreDirectory(t, root, "preparations")
	if e := training.InitializeStore(trainingRoot, g.f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	resources := trainwire.DefaultResources()
	trainingStore, e := training.OpenStore(trainingRoot, g.f.Tenant, 4096, resources.MaximumOutputBytes, resources.MaximumOutputFiles)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := trainingStore.Close(); e != nil {
			t.Error(e)
		}
	})
	ts := &training.Service{DB: g.f.PostgreSQL, Identity: g.s.Identity, Workspaces: g.s.Workspaces, Store: trainingStore}
	source := creatorAdapterGraph(t, creatorAdapterStory{f: g.f, ctx: g.ctx, owner: g.s.Identity, s: ts, a: g.a, c: trainwire.Command{ModelID: g.model}})
	// Real retained CPU adapter bytes pass through the actual private chunk and
	// publication implementation. Synthetic SQL/physical envelopes grant no GPU proof.
	if e = trainingStore.PrepareUpload(source.descriptor); e != nil {
		t.Fatal(e)
	}
	for _, asset := range source.descriptor.Files {
		raw, e := os.ReadFile(filepath.Join(os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT"), "adapter", asset.Path))
		if e != nil || int64(len(raw)) != asset.SizeBytes || hash(raw) != asset.SHA256 {
			t.Fatal("exact retained CPU adapter bytes", asset.Path, e)
		}
		for offset := int64(0); offset < asset.SizeBytes; offset += trainwire.TransferChunkBytes {
			length := min(trainwire.TransferChunkBytes, asset.SizeBytes-offset)
			part := raw[offset : offset+length]
			if _, e = trainingStore.WriteChunk(g.ctx, source.descriptor, asset.Path, offset, length, hash(part), bytes.NewReader(part)); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e = trainingStore.PreparePublishedFiles(g.ctx, source.descriptor); e != nil {
		t.Fatal(e)
	}
	if e = datasets.InitializeStore(datasetRoot, g.f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	dsStore, e := datasets.OpenStore(datasetRoot, g.f.Tenant, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := dsStore.Close(); e != nil {
			t.Error(e)
		}
	})
	if e = models.InitializeStore(modelRoot, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	if e = InitializeStore(prepRoot, g.f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	prepStore, e := OpenStore(prepRoot, g.f.Tenant, 4096, wire.MaximumModelBytes, wire.MaximumModelFiles)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := prepStore.Close(); e != nil {
			t.Error(e)
		}
	})
	ds := &datasets.Service{DB: g.f.PostgreSQL, Identity: g.s.Identity, Workspaces: g.s.Workspaces, Store: dsStore}
	g.s.Training, g.s.Datasets, g.s.Models, g.s.Store = ts, ds, &models.Service{DB: g.f.PostgreSQL, Identity: g.s.Identity, Workspaces: g.s.Workspaces, RootDirectory: modelRoot}, prepStore
	g.s.Identity.Config.Models.ArtifactRoot = modelRoot
	g.s.Identity.Config.Datasets.ArtifactRoot = datasetRoot
	g.s.Identity.Config.Training.ArtifactRoot = trainingRoot
	digest := strings.Repeat("a", 64)
	g.s.Identity.Config.ModelPreparation = config.ModelPreparationConfiguration{ArtifactRoot: prepRoot, ImageReference: wire.ImageRepository + "@sha256:" + digest, ImageID: "sha256:" + strings.Repeat("b", 64), WorkerSourceSHA256: digest}
	node := uuid.NewString()
	now := time.Now().UTC()
	g.seed(t, "managed_nodes", map[string]any{"id": node, "name": "Synthetic creator target " + node, "status": "active", "credential_hash": bytes.Repeat([]byte{9}, 32), "protocol_version": wire.NodeProtocolVersion, "execution_profile": wire.NodeProfile, "capabilities": reviewJSON(reviewRaw(t, agent.Capabilities(agent.DockerModelLifecycleProfile))), "facts": reviewJSON(reviewRaw(t, agent.Facts{OSID: "synthetic", Architecture: "test", Hostname: "Creator metadata only", MemoryTotalBytes: 1048576, ObservedAt: now})), "last_heartbeat_at": now})
	holdout, heldoutFile := creatorHeldout(t, g, datasetRoot)
	return creatorStory{g: g, source: source, input: CreateRequest{WorkspaceID: g.f.Workspace, TrainingJobID: source.job, HeldoutID: holdout, NodeID: node, GPUUUID: "GPU-" + uuid.NewString(), Configuration: wire.DefaultConfiguration(), Reason: "Actual retained-file creation over explicit synthetic SQL scope \u4e16\u754c"}, trainingRoot: trainingRoot, heldoutFile: heldoutFile}
}

// The three JSONL rows are real synthetic bytes in a separate heldout dataset.
// Publication/holdout SQL metadata is explicit; no processor/reservation/GPU claim.
func creatorHeldout(t *testing.T, g reviewStory, root string) (string, string) {
	t.Helper()
	dataset, version, draft, job, attempt, holdout := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	fp := bytes.Repeat([]byte{3}, 32)
	var payload []byte
	for _, label := range wire.Labels() {
		payload = append(payload, reviewRaw(t, datawire.Record{SourceID: uuid.NewString(), Prompt: "Disjoint synthetic heldout " + uuid.NewString(), Completion: label})...)
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
	put("model_holdouts", map[string]any{"id": holdout, "dataset_id": dataset, "dataset_version_id": version, "name": "Synthetic disjoint heldout", "manifest_sha256": digest, "effective_source_sha256": digest, "row_mapping_sha256": digest, "source_bytes": len(payload), "row_count": 3, "label_counts": j(LabelCounts{Account: 1, Billing: 1, Technical: 1}), "recipe_id": wire.RecipeID, "recipe_sha256": wire.RecipeDigest(), "instruction_sha256": wire.InstructionDigest(), "fingerprint_algorithm": wire.PromptFingerprintAlgorithm, "created_by": g.f.User, "audience": "admin", "reason": "Actual synthetic heldout bytes", "creation_key": uuid.NewString(), "creation_fingerprint": fp, "created_at": now})
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
	return holdout, path
}
func (g creatorStory) create(key string) (Preparation, error) {
	return g.g.s.Create(g.g.ctx, g.g.a, g.input, key, uuid.NewString())
}
func creatorWant(t *testing.T, v Preparation, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code || !reflect.DeepEqual(v, Preparation{}) {
		t.Fatalf("expected zero/%s, got %v zero=%v", code, e, reflect.DeepEqual(v, Preparation{}))
	}
}
func (g creatorStory) counts(t *testing.T, expected int) {
	t.Helper()
	for _, queries := range [][2]string{{"SELECT COUNT(*) FROM model_preparations WHERE training_job_id=$1", "SELECT COUNT(*) FROM model_preparations WHERE training_job_id=?"}, {"SELECT COUNT(*) FROM audit_events a JOIN model_preparations p ON a.target_id=p.id WHERE p.training_job_id=$1 AND a.target_type='model_preparation' AND a.action='model.preparation_created'", "SELECT COUNT(*) FROM audit_events a JOIN model_preparations p ON a.target_id=p.id WHERE p.training_job_id=? AND a.target_type='model_preparation' AND a.action='model.preparation_created'"}} {
		var n int
		if e := g.g.f.Store.QueryRow(g.g.ctx, g.g.f.Statement(queries[0], queries[1], g.source.job)).Scan(&n); e != nil || n != expected {
			t.Fatal("atomic current creator counts", n, expected, e)
		}
	}
}

// Explicit test-only hook after real private-file hashing, outside the business
// transaction. It exercises final authority/recipe fences, never fake IO success.
type creatorSourceHook struct {
	*datasets.Service
	afterOpen func()
}

func (h creatorSourceHook) OpenPublishedSource(ctx context.Context, v datasets.Version, max int64) (*os.File, int64, error) {
	f, n, e := h.Service.OpenPublishedSource(ctx, v, max)
	if e == nil && h.afterOpen != nil {
		h.afterOpen()
	}
	return f, n, e
}
func creatorUnknown(g creatorStory, at int) creatorStory {
	wrapped := &reviewUnknownStore{Store: g.g.f.Store, failAt: at}
	owner := &identity.Service{Storage: wrapped, Config: g.g.s.Identity.Config}
	ws := &workspaces.Service{Identity: owner}
	copyService := *g.g.s
	copyService.DB, copyService.Identity, copyService.Workspaces = nil, owner, ws
	g.g.s = &copyService
	return g
}
func creatorPolicy(t *testing.T, g creatorStory, active, scans int) creatorStory {
	t.Helper()
	p := g.g.s.Identity.Config.Policy()
	p.ModelLifecycle.MaxActivePerWorkspace, p.ModelLifecycle.ScanConcurrency = active, scans
	g.g.s.Identity.Config.Runtime = &p
	old := g.g.s
	s, e := New(g.g.f.PostgreSQL, old.Identity, old.Workspaces, old.Datasets.(*datasets.Service))
	if e != nil {
		t.Fatal(e)
	}
	s.Training, s.Models, s.Nodes, s.Store = old.Training, old.Models, old.Nodes, old.Store
	g.g.s = s
	return g
}
func TestActualPairedPreparationCreator(t *testing.T) {
	t.Run("actual_retained_sources_create_and_exact_replay", func(t *testing.T) {
		g := creatorSetup(t)
		key := uuid.NewString()
		v, e := g.create(key)
		if e != nil || v.State != "queued" || v.Revision != 1 || v.CurrentAttemptID != nil || v.AttemptNumber != 0 || v.NodeID != g.input.NodeID || v.TrainingResultSHA256 != g.source.reportSHA || v.inputs.TrainingIdentitySHA256 != (training.AcceptedAdapter{Command: g.source.c, Descriptor: g.source.descriptor}).IdentitySHA256() || v.CreatedAt.Location() != time.UTC {
			t.Fatal("actual typed queued preparation", v, e)
		}
		again, e := g.create(key)
		if e != nil || !reflect.DeepEqual(v, again) {
			t.Fatal("exact committed creation replay", e)
		}
		savedStore, savedTraining := g.g.s.Store, g.g.s.Training
		g.g.s.Store, g.g.s.Training = nil, nil
		again, e = g.create(key)
		g.g.s.Store, g.g.s.Training = savedStore, savedTraining
		if e != nil || !reflect.DeepEqual(v, again) {
			t.Fatal("replay precedes unavailable source dependency", e)
		}
		g.counts(t, 1)
	})
	t.Run("current_actor_owner_scope_and_membership_lifetime", func(t *testing.T) {
		g := creatorSetup(t)
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
		v, e := g.g.s.Create(g.g.ctx, portal, g.input, uuid.NewString(), uuid.NewString())
		creatorWant(t, v, e, "ACCESS_DENIED")
		authority := uuid.NewString()
		g.g.exec(t, "UPDATE workspace_memberships SET role='owner',authority_id=$1 WHERE user_id=$2 AND workspace_id=$3", "UPDATE workspace_memberships SET role='owner',authority_id=? WHERE user_id=? AND workspace_id=?", authority, g.g.f.User, g.g.f.Workspace)
		v, e = g.g.s.Create(g.g.ctx, portal, g.input, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal("current owner over stale member presentation", e)
		}
		var stored string
		if e = g.g.f.Store.QueryRow(g.g.ctx, g.g.f.Statement("SELECT membership_authority::text FROM model_preparations WHERE id=$1", "SELECT membership_authority FROM model_preparations WHERE id=?", v.ID)).Scan(&stored); e != nil || stored != authority {
			t.Fatal("current membership lifetime persisted", e)
		}
		wrong := g.input
		wrong.WorkspaceID = uuid.NewString()
		v, e = g.g.s.Create(g.g.ctx, g.g.a, wrong, uuid.NewString(), uuid.NewString())
		creatorWant(t, v, e, "RESOURCE_NOT_FOUND")
		g.g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.g.f.User)
		v, e = g.create(uuid.NewString())
		creatorWant(t, v, e, "AUTH_SESSION_EXPIRED")
		g.counts(t, 1)
	})
	t.Run("node_recipe_and_source_binding_refusal", func(t *testing.T) {
		g := creatorSetup(t)
		bad := g.input
		bad.NodeID = g.source.node
		v, e := g.g.s.Create(g.g.ctx, g.g.a, bad, uuid.NewString(), uuid.NewString())
		creatorWant(t, v, e, "NODE_UNAVAILABLE")
		bad = g.input
		bad.HeldoutID = uuid.NewString()
		v, e = g.g.s.Create(g.g.ctx, g.g.a, bad, uuid.NewString(), uuid.NewString())
		creatorWant(t, v, e, "MODEL_HOLDOUT_NOT_FOUND")
		ds := g.g.s.Datasets.(*datasets.Service)
		original := g.g.s.Identity.Config.ModelPreparation.WorkerSourceSHA256
		g.g.s.Datasets = creatorSourceHook{Service: ds, afterOpen: func() { g.g.s.Identity.Config.ModelPreparation.WorkerSourceSHA256 = strings.Repeat("c", 64) }}
		v, e = g.create(uuid.NewString())
		creatorWant(t, v, e, "MODEL_PREPARATION_RECIPE_UNAVAILABLE")
		g.g.s.Identity.Config.ModelPreparation.WorkerSourceSHA256 = original
		g.g.s.Datasets = creatorSourceHook{Service: ds, afterOpen: func() {
			g.g.exec(t, "UPDATE managed_nodes SET status='disabled' WHERE id=$1", "UPDATE managed_nodes SET status='disabled' WHERE id=?", g.input.NodeID)
		}}
		v, e = g.create(uuid.NewString())
		creatorWant(t, v, e, "NODE_UNAVAILABLE")
		g.counts(t, 0)
	})
	t.Run("actual_adapter_and_heldout_file_corruption", func(t *testing.T) {
		g := creatorSetup(t)
		adapter := filepath.Join(g.trainingRoot, "artifacts", g.source.job, g.source.attempt, g.source.artifact, "adapter_model.safetensors")
		original, e := os.ReadFile(adapter)
		if e != nil {
			t.Fatal(e)
		}
		bad := append([]byte{}, original...)
		bad[0] ^= 1
		if e = os.WriteFile(adapter, bad, 0600); e != nil {
			t.Fatal(e)
		}
		v, e := g.create(uuid.NewString())
		creatorWant(t, v, e, "TRAINING_STORAGE_DATA_INVALID")
		g.counts(t, 0)
		if e = os.WriteFile(adapter, original, 0600); e != nil {
			t.Fatal(e)
		}
		original, e = os.ReadFile(g.heldoutFile)
		if e != nil {
			t.Fatal(e)
		}
		bad = append([]byte{}, original...)
		bad[0] ^= 1
		if e = os.WriteFile(g.heldoutFile, bad, 0600); e != nil {
			t.Fatal(e)
		}
		v, e = g.create(uuid.NewString())
		creatorWant(t, v, e, "MODEL_HOLDOUT_SOURCE_CHANGED")
		g.counts(t, 0)
		if e = os.WriteFile(g.heldoutFile, original, 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = g.create(uuid.NewString()); e != nil {
			t.Fatal("actual source repair permits a new intent", e)
		}
		g.counts(t, 1)
	})
	t.Run("serialized_limits_and_concurrent_creation", func(t *testing.T) {
		g := creatorPolicy(t, creatorSetup(t), 1, 2)
		key := uuid.NewString()
		var wg sync.WaitGroup
		var values [2]Preparation
		var failures [2]error
		for i := range 2 {
			wg.Add(1)
			go func(i int) { defer wg.Done(); values[i], failures[i] = g.create(key) }(i)
		}
		wg.Wait()
		if failures[0] != nil || failures[1] != nil || !reflect.DeepEqual(values[0], values[1]) {
			t.Fatal("one exact concurrent creation", failures)
		}
		g.counts(t, 1)
		v, e := g.create(uuid.NewString())
		creatorWant(t, v, e, "MODEL_PREPARATION_LIMIT_REACHED")
		g.counts(t, 1)
	})
	t.Run("audit_rollback_and_retry", func(t *testing.T) {
		g := creatorSetup(t)
		g.g.exec(t, `CREATE FUNCTION explicit_creator_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'explicit creator test audit failure'; END;$$`, `CREATE TRIGGER explicit_creator_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='explicit creator test audit failure'`)
		if g.g.f.Store.Dialect() == transaction.PostgreSQL {
			g.g.exec(t, `CREATE TRIGGER explicit_creator_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION explicit_creator_audit_failure()`, "")
		}
		key := uuid.NewString()
		v, e := g.create(key)
		creatorWant(t, v, e, "STORAGE_UNAVAILABLE")
		g.counts(t, 0)
		g.g.exec(t, "DROP TRIGGER explicit_creator_audit_failure ON audit_events", "DROP TRIGGER explicit_creator_audit_failure")
		if _, e = g.create(key); e != nil {
			t.Fatal("exact retry after rollback", e)
		}
		g.counts(t, 1)
	})
	t.Run("unknown_commit_zero_and_same_key_recovery", func(t *testing.T) {
		g := creatorSetup(t)
		for _, at := range []int{1, 2, 3} {
			x := creatorUnknown(g, at)
			key := uuid.NewString()
			before := at - 1
			v, e := x.create(key)
			creatorWant(t, v, e, string(transaction.CommitUnknown))
			if at == 3 {
				x.counts(t, before+1)
			} else {
				x.counts(t, before)
			}
			v, e = x.create(key)
			if e != nil || v.State != "queued" {
				t.Fatal("same-key current outcome resolution", e)
			}
			x.counts(t, at)
		}
	})
	t.Run("actual_get_list_scope_state_and_keyset", func(t *testing.T) {
		g := creatorSetup(t)
		ids := map[string]bool{}
		for range 3 {
			v, e := g.create(uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			ids[v.ID] = true
			got, e := g.g.s.Get(g.g.ctx, g.g.a, g.g.f.Workspace, v.ID)
			if e != nil || !reflect.DeepEqual(v, got) {
				t.Fatal("exact current scoped detail", e)
			}
		}
		query := url.Values{"limit": {"1"}, "state": {"queued"}}
		seen := map[string]bool{}
		var firstCursor string
		for range 3 {
			page, e := g.g.s.List(g.g.ctx, g.g.a, g.g.f.Workspace, query)
			if e != nil || len(page.Items) != 1 || !ids[page.Items[0].ID] || seen[page.Items[0].ID] {
				t.Fatal("stable state keyset", page, e)
			}
			seen[page.Items[0].ID] = true
			if page.NextCursor != nil {
				query.Set("cursor", *page.NextCursor)
				if firstCursor == "" {
					firstCursor = *page.NextCursor
				}
			} else {
				query.Del("cursor")
			}
		}
		if len(seen) != 3 {
			t.Fatal("keyset lost rows")
		}
		page, e := g.g.s.List(g.g.ctx, g.g.a, g.g.f.Workspace, url.Values{"state": {"awaiting_review"}, "cursor": {firstCursor}})
		if e == nil || e.Error() != "INVALID_CURSOR" || len(page.Items) != 0 {
			t.Fatal("state-bound cursor", e)
		}
		v, e := g.g.s.Get(g.g.ctx, g.g.a, uuid.NewString(), g.g.id)
		creatorWant(t, v, e, "RESOURCE_NOT_FOUND")
		page, e = g.g.s.List(g.g.ctx, g.g.a, uuid.NewString(), url.Values{})
		if e == nil || e.Error() != "RESOURCE_NOT_FOUND" || len(page.Items) != 0 {
			t.Fatal("cross scope list", e)
		}
	})
	t.Run("read_unknown_outcome_and_empty_projection", func(t *testing.T) {
		g := creatorSetup(t)
		v, e := g.create(uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		x := creatorUnknown(g, 1)
		got, e := x.g.s.Get(x.g.ctx, x.g.a, x.g.f.Workspace, v.ID)
		creatorWant(t, got, e, string(transaction.CommitUnknown))
		x = creatorUnknown(g, 1)
		page, e := x.g.s.List(x.g.ctx, x.g.a, x.g.f.Workspace, url.Values{})
		if e == nil || e.Error() != string(transaction.CommitUnknown) || len(page.Items) != 0 || page.NextCursor != nil {
			t.Fatal("unknown read published partial page", e)
		}
		g.counts(t, 1)
	})
	t.Run("cancel_deadline_query_and_store_coherence", func(t *testing.T) {
		g := creatorSetup(t)
		ctx, cancel := context.WithCancel(g.g.ctx)
		cancel()
		v, e := g.g.s.Create(ctx, g.g.a, g.input, uuid.NewString(), uuid.NewString())
		creatorWant(t, v, e, string(transaction.Cancelled))
		ctx, cancel = context.WithDeadline(g.g.ctx, time.Now().Add(-time.Second))
		defer cancel()
		v, e = g.g.s.Create(ctx, g.g.a, g.input, uuid.NewString(), uuid.NewString())
		creatorWant(t, v, e, string(transaction.Timeout))
		other := *g.g.s
		other.Workspaces = &workspaces.Service{Identity: &identity.Service{Storage: g.g.f.Store}}
		v, e = other.Create(g.g.ctx, g.g.a, g.input, uuid.NewString(), uuid.NewString())
		creatorWant(t, v, e, "MODEL_PREPARATION_IDENTITY_REQUIRED")
		g.g.exec(t, "ALTER TABLE model_preparations RENAME TO explicit_missing_creator_table", "RENAME TABLE model_preparations TO explicit_missing_creator_table")
		v, e = g.create(uuid.NewString())
		creatorWant(t, v, e, "STORAGE_UNAVAILABLE")
		g.g.exec(t, "ALTER TABLE explicit_missing_creator_table RENAME TO model_preparations", "RENAME TABLE explicit_missing_creator_table TO model_preparations")
		g.counts(t, 0)
	})
}

type creatorAdapterStory struct {
	f                                             repositoryfixture.Fixture
	ctx                                           context.Context
	owner                                         *identity.Service
	s                                             *training.Service
	a                                             identity.Auth
	job, node, attempt, task, artifact, reportSHA string
	c                                             trainwire.Command
	descriptor                                    training.UploadDescriptor
	stop                                          trainwire.StopReceipt
	raw                                           []byte
}

func (g creatorAdapterStory) seed(t *testing.T, table string, v map[string]any) {
	reviewStory{f: g.f, ctx: g.ctx}.seed(t, table, v)
}
func (g creatorAdapterStory) exec(t *testing.T, pg, my string, args ...any) {
	reviewStory{f: g.f, ctx: g.ctx}.exec(t, pg, my, args...)
}

// Adapted from the accepted-adapter gate's already exercised fixture. Original
// retained CPU files/report are real. Production/physical/SQL envelopes below
// remain explicit test doubles; all constraints stay enabled. No GPU is executed.
func creatorAdapterGraph(t *testing.T, g creatorAdapterStory) creatorAdapterStory {
	t.Helper()
	existingModel := g.c.ModelID
	g.job, g.node, g.attempt, g.task, g.artifact = uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	f := g.f
	resources := trainwire.DefaultResources()
	output := os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT")
	if output == "" || !filepath.IsAbs(output) {
		t.Fatal("EXPLICIT_RETAINED_TRAINING_INPUT_REQUIRED")
	}
	source, e := os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil {
		t.Fatal(e)
	}
	var original trainwire.Result
	if e = json.Unmarshal(source, &original); e != nil || original.Status != "trained" || original.DeviceProfile != trainwire.VerificationProfile {
		t.Fatal("original CPU training report required", e)
	}
	hash := strings.Repeat("a", 64)
	image := "sha256:" + strings.Repeat("b", 64)
	cfg := trainwire.DefaultConfiguration()
	cfg.MaximumSteps = original.GlobalStep
	cfg.CheckpointSteps = min(cfg.CheckpointSteps, cfg.MaximumSteps)
	cfgSHA, e := cfg.Digest()
	if e != nil {
		t.Fatal(e)
	}
	model := existingModel
	if model == "" {
		model = uuid.NewString()
	}
	dataset, version := uuid.NewString(), uuid.NewString()
	c := trainwire.Command{SchemaVersion: 1, Scope: trainwire.Scope{TenantID: f.Tenant, WorkspaceID: f.Workspace, JobID: g.job}, AttemptID: g.attempt, AttemptNumber: 1, ModelID: model, DatasetID: dataset, DatasetVersionID: version, DatasetManifestSHA256: hash, ModelCatalogSHA256: agent.ModelCatalogDigest(), GPUUUID: "GPU-" + uuid.NewString(), Configuration: cfg, ConfigurationSHA256: cfgSHA,
		Recipe: trainwire.ExecutionRecipe{SchemaVersion: 1, WorkerVersion: trainwire.WorkerVersion, ImageReference: "projectone/training@sha256:" + hash, Identity: trainwire.ExecutionIdentity{Profile: trainwire.ProductionProfile, Python: trainwire.PythonVersion, WorkerSourceSHA256: hash, ImageID: &image}, Resources: resources, DataLimits: datawire.Limits{MaximumBytes: 1048576, MaximumRows: 256, MaximumLineBytes: 16384, MaximumTextBytes: 4096, MaximumSourceBytes: 256, MaximumErrors: 10}},
		Inputs: trainwire.InputInventory{Train: datawire.Asset{Path: "train.jsonl", SizeBytes: 128, SHA256: strings.Repeat("d", 64)}, Validation: datawire.Asset{Path: "validation.jsonl", SizeBytes: 128, SHA256: strings.Repeat("e", 64)}}}
	if e := c.Validate(); e != nil {
		t.Fatal("synthetic production command", e)
	}
	g.c = c
	request, e := c.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	var envelope map[string]json.RawMessage
	if e = json.Unmarshal(source, &envelope); e != nil {
		t.Fatal(e)
	}
	set := func(k string, v any) { envelope[k] = reviewRaw(t, v) }
	set("bindings", trainwire.ExpectedBindings(request, agent.ApprovedModel().Repository))
	set("job_id", g.job)
	set("attempt_id", g.attempt)
	set("dataset_version_id", version)
	set("dataset_manifest_sha256", hash)
	set("memory_enforcement", "linux_cgroup_memory_cpu_fsize_wall")
	set("device_profile", trainwire.ProductionProfile)
	raw := reviewRaw(t, envelope)
	result, e := trainwire.DecodeResult(raw, request, agent.ApprovedModel().Repository)
	if e != nil {
		t.Fatal("synthetic envelope around unchanged CPU result", e)
	}
	g.raw = raw
	g.reportSHA = creatorRetainedDigest(raw)
	d := training.UploadDescriptor{SchemaVersion: 1, Scope: c.Scope, UploadID: g.artifact, AttemptID: g.attempt, Kind: "artifact", GlobalStep: result.GlobalStep, ManifestSHA256: g.reportSHA}
	for _, a := range result.Files {
		if strings.HasPrefix(a.Path, "adapter/") {
			a.Path = strings.TrimPrefix(a.Path, "adapter/")
			d.Files = append(d.Files, a)
		}
	}
	if e := d.Validate(resources.MaximumOutputBytes, resources.MaximumOutputFiles); e != nil {
		t.Fatal(e)
	}
	g.descriptor = d
	now := time.Now().UTC()
	cid := strings.Repeat("c", 64)
	commandSHA, e := c.Digest()
	if e != nil {
		t.Fatal(e)
	}
	stop := trainwire.StopReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), JournalID: uuid.NewString(), NodeID: g.node, TaskID: g.task, Scope: c.Scope, AttemptID: g.attempt, AttemptNumber: 1, CommandSHA256: commandSHA, ImageID: image, GPUUUID: c.GPUUUID, ContainerName: trainwire.ContainerName(g.attempt), ContainerID: &cid, Proof: "owned_container_removed", OwnedContainerAbsent: true, OutcomeSHA256: &g.reportSHA, CompletedAt: now}
	if e := stop.ValidateFor(c, g.node, g.task); e != nil {
		t.Fatal(e)
	}
	g.stop = stop
	storedCommand, storedDescriptor := c, d
	storedReport := raw
	attemptOutcome := g.reportSHA
	storage := "artifacts/" + g.job + "/" + g.attempt + "/" + g.artifact
	currentNode := g.node
	finalState, uploadState := "succeeded", "accepted"
	currentAttempt, acceptedArtifact := g.attempt, g.artifact
	stopRaw := reviewRaw(t, stop)
	stopDigest := creatorRetainedDigest(stopRaw)
	fp := bytes.Repeat([]byte{8}, 32)
	draft, job, attempt := uuid.NewString(), uuid.NewString(), uuid.NewString()
	g.seed(t, "datasets", map[string]any{"id": dataset, "name": "Synthetic training provenance", "name_key": dataset, "origin": "synthetic", "license": "synthetic", "rights_asserted": true, "created_by": f.User, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_drafts", map[string]any{"id": draft, "dataset_id": dataset, "created_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "source_object_id": uuid.NewString(), "source_name": "synthetic.jsonl", "source_sha256": hash, "expected_bytes": 1, "source_received_at": now, "state": "published", "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_jobs", map[string]any{"id": job, "dataset_id": dataset, "draft_id": draft, "kind": "publish", "requested_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic provenance", "expected_revision": 1, "state": "succeeded", "version_id": version, "input_policy": reviewJSON(`{}`), "split_seed": strings.Repeat("1", 32), "validation_basis_points": 1000, "completed_at": now, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_job_attempts", map[string]any{"id": attempt, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_number": 1, "state": "succeeded", "finished_at": now})
	g.seed(t, "dataset_versions", map[string]any{"id": version, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_id": attempt, "ordinal": 1, "published_by": f.User, "raw_source_sha256": hash, "effective_source_sha256": hash, "manifest_sha256": hash, "manifest": reviewJSON(`{}`), "row_mapping_sha256": hash})
	if existingModel == "" {
		g.seed(t, "model_versions", map[string]any{"id": model, "name": "Synthetic accepted adapter base", "model_key": "qwen3-0.6b", "catalog_digest": agent.ModelCatalogDigest(), "manifest": reviewJSON(reviewRaw(t, agent.ApprovedModel())), "state": "ready", "created_by": f.User})
	}
	facts := agent.Facts{OSID: "synthetic", Architecture: "test", Hostname: "Accepted adapter metadata only", MemoryTotalBytes: 1048576, ObservedAt: now}
	g.seed(t, "managed_nodes", map[string]any{"id": g.node, "name": "Synthetic retained training node", "status": "removed", "credential_hash": nil, "last_heartbeat_at": now, "protocol_version": agent.AIVersion, "execution_profile": agent.DockerAIProfile, "capabilities": reviewJSON(reviewRaw(t, agent.Capabilities(agent.DockerAIProfile))), "facts": reviewJSON(reviewRaw(t, facts))})
	g.seed(t, "training_jobs", map[string]any{"id": g.job, "node_id": g.node, "current_node_id": currentNode, "model_id": model, "dataset_id": dataset, "dataset_version_id": version, "gpu_uuid": c.GPUUUID, "configuration": reviewJSON(reviewRaw(t, cfg)), "configuration_sha256": cfgSHA, "execution_recipe": reviewJSON(reviewRaw(t, c.Recipe)), "dataset_manifest_sha256": hash, "model_catalog_sha256": agent.ModelCatalogDigest(), "created_by": f.User, "requested_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic accepted source", "creation_key": uuid.NewString(), "creation_fingerprint": fp, "state": "queued"})
	g.seed(t, "node_tasks", map[string]any{"id": g.task, "node_id": g.node, "operation": "training.run.v1", "state": "succeeded", "attempt": 1, "idempotency_key": uuid.NewString(), "requested_by": f.User, "deadline_at": now.Add(time.Minute), "completed_at": now, "payload": reviewJSON(reviewRaw(t, storedCommand))})
	g.seed(t, "training_attempts", map[string]any{"id": g.attempt, "job_id": g.job, "node_id": g.node, "task_id": g.task, "attempt_number": 1, "state": "succeeded", "requested_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "input_inventory": reviewJSON(reviewRaw(t, c.Inputs)), "outcome_sha256": attemptOutcome, "outcome": reviewJSON(raw), "physical_stop_receipt": reviewJSON(stopRaw), "physical_stop_sha256": stopDigest, "physical_stopped_at": now, "finished_at": now})
	g.seed(t, "training_uploads", map[string]any{"id": g.artifact, "job_id": g.job, "attempt_id": g.attempt, "kind": "artifact", "global_step": result.GlobalStep, "manifest_sha256": g.reportSHA, "manifest": reviewJSON(raw), "state": uploadState, "manifest_bytes": storedReport})
	g.seed(t, "training_artifacts", map[string]any{"id": g.artifact, "job_id": g.job, "attempt_id": g.attempt, "manifest_sha256": g.reportSHA, "manifest": reviewJSON(raw), "file_inventory": reviewJSON(reviewRaw(t, storedDescriptor)), "storage_key": storage})
	g.exec(t, "UPDATE training_jobs SET state=$1,attempt_number=1,current_attempt_id=$2,accepted_artifact_id=$3,finished_at=$4 WHERE id=$5", "UPDATE training_jobs SET state=?,attempt_number=1,current_attempt_id=?,accepted_artifact_id=?,finished_at=? WHERE id=?", finalState, currentAttempt, acceptedArtifact, now, g.job)
	return g
}

func creatorRetainedDigest(raw []byte) string { return hash(raw) }

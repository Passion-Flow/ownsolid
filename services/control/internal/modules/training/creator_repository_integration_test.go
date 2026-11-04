//go:build repository_integration

package training_test

import (
	"bytes"
	"context"
	"encoding/hex"
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
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type trainingCreatorStory struct {
	acceptedAdapterStory
	input     training.CreateRequest
	partition string
}

// Only SQL publication/model and enrolled hardware facts are synthetic. Private
// train/validation files, current actor transactions and intent writes are real.
func trainingCreatorSetup(t *testing.T) trainingCreatorStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	t.Cleanup(cancel)
	account := &identity.Service{Storage: f.Store}
	if profile := os.Getenv("PROJECT_ONE_TEST_APPLICATION_PROFILE"); profile == "training" {
	}
	if f.PostgreSQL != nil {
		account.Storage, account.DB = nil, f.PostgreSQL
	}
	ws := &workspaces.Service{DB: f.PostgreSQL, Identity: account}
	n := &nodes.Service{DB: f.PostgreSQL, Identity: account, Workspaces: ws}
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
	ds := &datasets.Service{DB: f.PostgreSQL, Identity: account, Workspaces: ws, Store: store}
	account.Config.Datasets.ArtifactRoot = root
	account.Config.Models.ArtifactRoot = filepath.Join(filepath.Dir(root), "models")
	account.Config.Training = config.TrainingConfiguration{ArtifactRoot: filepath.Join(filepath.Dir(root), "training"), ImageReference: "projectone/training@sha256:" + strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("b", 64), WorkerSourceSHA256: strings.Repeat("c", 64)}
	g := trainingCreatorStory{acceptedAdapterStory: acceptedAdapterStory{f: f, ctx: ctx, owner: account, s: &training.Service{DB: f.PostgreSQL, Identity: account, Workspaces: ws, Nodes: n, Models: &models.Service{DB: f.PostgreSQL, Identity: account, Workspaces: ws}, Datasets: ds}}}
	var issued identity.IssuedSession
	e = f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = account.IssueSessionWithin(ctx, q, f.User, 1, "admin", "", uuid.NewString(), nil)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	g.a, e = account.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	token, e := n.MintForProfile(ctx, g.a, f.Workspace, "Synthetic training creator target", agent.DockerAIProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := agent.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-creator", LogicalCPUs: 4, MemoryTotalBytes: 17179869184, DiskPath: "/", DiskTotalBytes: 107374182400, DiskAvailableBytes: 53687091200, ObservedAt: time.Now().UTC(), AgentVersion: agent.AgentVersion}
	enrolled, e := n.Enroll(ctx, agent.Enrollment{JoinToken: token.Token, Handshake: agent.Handshake{ProtocolVersion: agent.AIVersion, ExecutionProfile: agent.DockerAIProfile, Capabilities: agent.Capabilities(agent.DockerAIProfile), Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	dataset, version, draft, job, attempt, model := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	train := []byte("{\"source_id\":\"synthetic-train\",\"prompt\":\"Train \u4e16\u754c\",\"completion\":\"answer\"}\n")
	validation := []byte("{\"source_id\":\"synthetic-validation\",\"prompt\":\"Validation \u0627\u0644\u0639\u0631\u0628\u064a\u0629\",\"completion\":\"answer\"}\n")
	payload := append(append([]byte{}, train...), validation...)
	digest := acceptedAdapterDigest(payload)
	now := time.Now().UTC().Truncate(time.Microsecond)
	fp := bytes.Repeat([]byte{8}, 32)
	g.seed(t, "datasets", map[string]any{"id": dataset, "name": "Synthetic training creator partitions", "name_key": dataset, "origin": "synthetic", "license": "synthetic", "rights_asserted": true, "created_by": f.User, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_drafts", map[string]any{"id": draft, "dataset_id": dataset, "created_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "source_object_id": uuid.NewString(), "source_name": "synthetic.jsonl", "source_sha256": digest, "expected_bytes": len(payload), "source_received_at": now, "state": "published", "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_jobs", map[string]any{"id": job, "dataset_id": dataset, "draft_id": draft, "kind": "publish", "requested_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic immutable partition provenance", "expected_revision": 1, "state": "succeeded", "version_id": version, "input_policy": acceptedAdapterJSON(`{}`), "split_seed": strings.Repeat("1", 32), "validation_basis_points": 1000, "completed_at": now, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_job_attempts", map[string]any{"id": attempt, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_number": 1, "state": "succeeded", "finished_at": now})
	manifest := datawire.Manifest{SchemaVersion: 1, RowSchema: datawire.RowSchema, ProcessorVersion: datawire.ProcessorVersion, SplitAlgorithm: datawire.SplitAlgorithm, SourceSHA256: digest, Seed: strings.Repeat("1", 32), ValidationBasisPoints: 1000, TotalRows: 2, UniqueRows: 2, SourceGroups: 2, ValidationGroups: 1, TrainRows: 1, ValidationRows: 1, ManifestFile: datawire.Asset{Path: "manifest.json", SizeBytes: 2, SHA256: digest}}
	parts := map[string][]byte{"effective.jsonl": payload, "samples.jsonl": payload, "train.jsonl": train, "validation.jsonl": validation, "assignments.jsonl": nil, "duplicates.jsonl": nil}
	for _, name := range []string{"samples.jsonl", "train.jsonl", "validation.jsonl", "assignments.jsonl", "duplicates.jsonl"} {
		raw := parts[name]
		manifest.Files = append(manifest.Files, datawire.Asset{Path: name, SizeBytes: int64(len(raw)), SHA256: acceptedAdapterDigest(raw)})
	}
	g.seed(t, "dataset_versions", map[string]any{"id": version, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_id": attempt, "ordinal": 1, "published_by": f.User, "published_at": now, "raw_source_sha256": digest, "effective_source_sha256": digest, "manifest_sha256": digest, "manifest": acceptedAdapterJSON(acceptedAdapterRaw(t, manifest)), "row_mapping_sha256": digest})
	directory := filepath.Join(root, "versions", version, attempt)
	if e = os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	for name, raw := range parts {
		p := filepath.Join(directory, name)
		file, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = file.Write(raw); e != nil {
			t.Fatal(e)
		}
		if e = file.Sync(); e != nil {
			t.Fatal(e)
		}
		if e = file.Close(); e != nil {
			t.Fatal(e)
		}
	}
	dir, e := os.Open(directory)
	if e != nil {
		t.Fatal(e)
	}
	if e = dir.Sync(); e != nil {
		t.Fatal(e)
	}
	dir.Close()
	g.partition = filepath.Join(directory, "train.jsonl")
	g.seed(t, "model_versions", map[string]any{"id": model, "name": "Synthetic creator model metadata", "model_key": "qwen3-0.6b", "catalog_digest": agent.ModelCatalogDigest(), "manifest": acceptedAdapterJSON(acceptedAdapterRaw(t, agent.ApprovedModel())), "state": "ready", "created_by": f.User})
	g.input = training.CreateRequest{WorkspaceID: f.Workspace, NodeID: enrolled.NodeID, ModelID: model, DatasetID: dataset, DatasetVersionID: version, GPUUUID: "GPU-" + uuid.NewString(), Configuration: wire.DefaultConfiguration(), Reason: "Actual partition backed intent \u4e16\u754c"}
	return g
}
func (g trainingCreatorStory) create(key string) (training.Job, error) {
	return g.s.Create(g.ctx, g.a, g.input, key, uuid.NewString())
}
func trainingCreatorWant(t *testing.T, v training.Job, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code || !reflect.DeepEqual(v, training.Job{}) {
		t.Fatalf("expected zero/%s observed %v zero=%v", code, e, reflect.DeepEqual(v, training.Job{}))
	}
}
func (g trainingCreatorStory) counts(t *testing.T, want int) {
	t.Helper()
	for _, pair := range [][2]string{{"SELECT COUNT(*) FROM training_jobs WHERE tenant_id=$1 AND workspace_id=$2", "SELECT COUNT(*) FROM training_jobs WHERE tenant_id=? AND workspace_id=?"}, {"SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND workspace_id=$2 AND action='training.created' AND target_type='training_job'", "SELECT COUNT(*) FROM audit_events WHERE tenant_id=? AND workspace_id=? AND action='training.created' AND target_type='training_job'"}} {
		var n int
		if e := g.f.Store.QueryRow(g.ctx, g.statement(pair[0], pair[1], g.f.Tenant, g.f.Workspace)).Scan(&n); e != nil || n != want {
			t.Fatal("atomic training creator counts", n, want, e)
		}
	}
}

// Explicit test-only transaction outcome/between-phase hooks. Real SQL commits,
// files and audit run; no production hook or fabricated successful adapter exists.
type trainingCreatorStore struct {
	transaction.Store
	at, calls int
	before    func()
	unknown   bool
}

func (s *trainingCreatorStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	s.calls++
	if s.calls == s.at && s.before != nil {
		s.before()
	}
	e := s.Store.Within(ctx, readOnly, fn)
	if e == nil && s.calls == s.at && s.unknown {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return e
}
func trainingCreatorWrapped(g trainingCreatorStory, store transaction.Store) trainingCreatorStory {
	account := *g.owner
	account.DB = nil
	account.Storage = store
	ws := &workspaces.Service{Identity: &account}
	s := *g.s
	s.DB = nil
	s.Identity = &account
	s.Workspaces = ws
	n := *s.Nodes
	n.DB = nil
	n.Identity = &account
	n.Workspaces = ws
	s.Nodes = &n
	m := *s.Models
	m.DB = nil
	m.Identity = &account
	m.Workspaces = ws
	s.Models = &m
	d := *s.Datasets
	d.DB = nil
	d.Identity = &account
	d.Workspaces = ws
	s.Datasets = &d
	g.s = &s
	g.owner = &account
	return g
}

func (g trainingCreatorStory) reserveEnvelope(t *testing.T) {
	t.Helper()
	digest := strings.Repeat("d", 64)
	holdout := uuid.NewString()
	g.seed(t, "model_holdouts", map[string]any{"id": holdout, "dataset_id": g.input.DatasetID, "dataset_version_id": g.input.DatasetVersionID, "name": "Explicit synthetic overlapping registry", "manifest_sha256": digest, "effective_source_sha256": digest, "row_mapping_sha256": digest, "source_bytes": 100, "row_count": 3, "label_counts": acceptedAdapterJSON(`{"account":1,"billing":1,"technical":1}`), "recipe_id": lifecycle.RecipeID, "recipe_sha256": lifecycle.RecipeDigest(), "instruction_sha256": lifecycle.InstructionDigest(), "fingerprint_algorithm": lifecycle.PromptFingerprintAlgorithm, "created_by": g.f.User, "audience": "admin", "reason": "Synthetic reservation between actual analysis and commit", "creation_key": uuid.NewString(), "creation_fingerprint": bytes.Repeat([]byte{3}, 32)})
	source, e := hex.DecodeString(acceptedAdapterDigest([]byte("synthetic-train")))
	if e != nil {
		t.Fatal(e)
	}
	g.seed(t, "model_holdout_sources", map[string]any{"holdout_id": holdout, "source_sha256": source})
}
func TestActualPairedTrainingCreator(t *testing.T) {
	t.Run("actual_partition_backed_create_and_exact_replay", func(t *testing.T) {
		g := trainingCreatorSetup(t)
		key := uuid.NewString()
		v, e := g.create(key)
		if e != nil || v.State != "queued" || v.AttemptNumber != 0 || v.CurrentAttemptID != nil {
			t.Fatal("actual queued intent", v, e)
		}
		g.counts(t, 1)
		if e = os.Rename(g.partition, g.partition+".retained"); e != nil {
			t.Fatal(e)
		}
		same, e := g.create(key)
		if e != nil || !reflect.DeepEqual(v, same) {
			t.Fatal("exact replay precedes dependency verification", e)
		}
		changed := g
		changed.input.Reason += " changed"
		failed, e := changed.create(key)
		trainingCreatorWant(t, failed, e, "IDEMPOTENCY_CONFLICT")
		if e = os.Rename(g.partition+".retained", g.partition); e != nil {
			t.Fatal(e)
		}
		g.counts(t, 1)
	})
	t.Run("current_owner_scope_session_and_membership_lifetime", func(t *testing.T) {
		g := trainingCreatorSetup(t)
		g.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'member')", g.f.User, g.f.Tenant, g.f.Workspace)
		var issued identity.IssuedSession
		e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			issued, e = g.owner.IssueSessionWithin(ctx, q, g.f.User, 1, "portal", "", uuid.NewString(), nil)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		portal, e := g.owner.Authenticate(g.ctx, issued.Token, "portal")
		if e != nil {
			t.Fatal(e)
		}
		v, e := g.s.Create(g.ctx, portal, g.input, uuid.NewString(), uuid.NewString())
		trainingCreatorWant(t, v, e, "ACCESS_DENIED")
		authority := uuid.NewString()
		g.exec(t, "UPDATE workspace_memberships SET role='owner',authority_id=$1 WHERE user_id=$2 AND workspace_id=$3", "UPDATE workspace_memberships SET role='owner',authority_id=? WHERE user_id=? AND workspace_id=?", authority, g.f.User, g.f.Workspace)
		v, e = g.s.Create(g.ctx, portal, g.input, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		var saved string
		if e = g.f.Store.QueryRow(g.ctx, g.statement("SELECT membership_authority::text FROM training_jobs WHERE id=$1", "SELECT membership_authority FROM training_jobs WHERE id=?", v.ID)).Scan(&saved); e != nil || saved != authority {
			t.Fatal("current membership lifetime", e)
		}
		foreign := g
		foreign.input.WorkspaceID = uuid.NewString()
		v, e = foreign.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "RESOURCE_NOT_FOUND")
		expired := g.a
		expired.Epoch++
		v, e = g.s.Create(g.ctx, expired, g.input, uuid.NewString(), uuid.NewString())
		trainingCreatorWant(t, v, e, "AUTH_SESSION_EXPIRED")
		g.counts(t, 1)
	})
	t.Run("target_model_recipe_and_source_refusal", func(t *testing.T) {
		g := trainingCreatorSetup(t)
		bad := g
		bad.input.NodeID = uuid.NewString()
		v, e := bad.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "RESOURCE_NOT_FOUND")
		bad = g
		bad.input.ModelID = uuid.NewString()
		v, e = bad.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "RESOURCE_NOT_FOUND")
		bad = g
		bad.input.DatasetVersionID = uuid.NewString()
		v, e = bad.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "DATASET_NOT_FOUND")
		saved := g.owner.Config.Training
		g.owner.Config.Training = config.TrainingConfiguration{}
		v, e = g.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "TRAINING_UNAVAILABLE")
		g.owner.Config.Training = saved
		g.exec(t, "UPDATE managed_nodes SET status='disabled' WHERE id=$1", "UPDATE managed_nodes SET status='disabled' WHERE id=?", g.input.NodeID)
		v, e = g.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "NODE_UNAVAILABLE")
		g.counts(t, 0)
	})
	t.Run("real_partition_corruption_and_final_authority_fences", func(t *testing.T) {
		g := trainingCreatorSetup(t)
		raw, e := os.ReadFile(g.partition)
		if e != nil {
			t.Fatal(e)
		}
		bad := append([]byte{}, raw...)
		bad[0] ^= 1
		if e = os.WriteFile(g.partition, bad, 0600); e != nil {
			t.Fatal(e)
		}
		v, e := g.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "DATASET_FILE_INVALID")
		if e = os.WriteFile(g.partition, raw, 0600); e != nil {
			t.Fatal(e)
		}
		hook := &trainingCreatorStore{Store: g.f.Store, at: 2, before: func() {
			g.exec(t, "UPDATE dataset_versions SET retired_at=clock_timestamp(),retire_reason='During outside-lock scan' WHERE id=$1", "UPDATE dataset_versions SET retired_at=UTC_TIMESTAMP(6),retire_reason='During outside-lock scan' WHERE id=?", g.input.DatasetVersionID)
		}}
		x := trainingCreatorWrapped(g, hook)
		v, e = x.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "DATASET_RETIRED")
		g.counts(t, 0)
		g.exec(t, "UPDATE dataset_versions SET retired_at=NULL,retire_reason=NULL WHERE id=$1", "UPDATE dataset_versions SET retired_at=NULL,retire_reason=NULL WHERE id=?", g.input.DatasetVersionID)
		hook = &trainingCreatorStore{Store: g.f.Store, at: 2, before: func() {
			g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
		}}
		x = trainingCreatorWrapped(g, hook)
		v, e = x.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "AUTH_SESSION_EXPIRED")
		g.exec(t, "UPDATE users SET auth_epoch=1 WHERE id=$1", "UPDATE users SET auth_epoch=1 WHERE id=?", g.f.User)
		hook = &trainingCreatorStore{Store: g.f.Store, at: 2}
		x = trainingCreatorWrapped(g, hook)
		hook.before = func() { x.owner.Config.Training.WorkerSourceSHA256 = strings.Repeat("d", 64) }
		v, e = x.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "TRAINING_RECIPE_UNAVAILABLE")
		guard, e := modelpreparations.New(g.f.PostgreSQL, g.owner, g.s.Workspaces, g.s.Datasets)
		if e != nil {
			t.Fatal(e)
		}
		hook = &trainingCreatorStore{Store: g.f.Store, at: 2, before: func() { g.reserveEnvelope(t) }}
		x = trainingCreatorWrapped(g, hook)
		x.s.Holdouts = guard
		v, e = x.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "MODEL_HOLDOUT_OVERLAP")
		g.s.Holdouts = nil
		v, e = g.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "MODEL_HOLDOUT_GUARD_REQUIRED")
		g.counts(t, 0)
	})
	t.Run("serialized_limits_and_concurrent_creation", func(t *testing.T) {
		g := trainingCreatorSetup(t)
		policy := g.owner.Config.Policy()
		policy.Training.MaxActivePerWorkspace = 1
		g.owner.Config.Runtime = &policy
		key := uuid.NewString()
		var wg sync.WaitGroup
		var values [2]training.Job
		var failures [2]error
		for i := range 2 {
			wg.Add(1)
			go func(i int) { defer wg.Done(); values[i], failures[i] = g.create(key) }(i)
		}
		wg.Wait()
		if failures[0] != nil || failures[1] != nil || !reflect.DeepEqual(values[0], values[1]) {
			t.Fatal("serialized exact intent", failures)
		}
		v, e := g.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "TRAINING_JOB_LIMIT_REACHED")
		g.counts(t, 1)
	})
	t.Run("atomic_audit_rollback_and_retry", func(t *testing.T) {
		g := trainingCreatorSetup(t)
		g.exec(t, `CREATE FUNCTION explicit_training_creator_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'explicit training audit test'; END;$$`, `CREATE TRIGGER explicit_training_creator_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='explicit training audit test'`)
		if g.f.Store.Dialect() == transaction.PostgreSQL {
			g.exec(t, `CREATE TRIGGER explicit_training_creator_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION explicit_training_creator_audit_failure()`, "")
		}
		key := uuid.NewString()
		v, e := g.create(key)
		trainingCreatorWant(t, v, e, "STORAGE_UNAVAILABLE")
		g.counts(t, 0)
		g.exec(t, "DROP TRIGGER explicit_training_creator_audit_failure ON audit_events", "DROP TRIGGER explicit_training_creator_audit_failure")
		if _, e = g.create(key); e != nil {
			t.Fatal("retry exact rolled-back intent", e)
		}
		g.counts(t, 1)
	})
	t.Run("unknown_commit_zero_and_same_key_recovery", func(t *testing.T) {
		g := trainingCreatorSetup(t)
		for _, at := range []int{1, 2} {
			x := trainingCreatorWrapped(g, &trainingCreatorStore{Store: g.f.Store, at: at, unknown: true})
			key := uuid.NewString()
			v, e := x.create(key)
			trainingCreatorWant(t, v, e, string(transaction.CommitUnknown))
			want := at - 1
			if at == 2 {
				want++
			}
			g.counts(t, want)
			v, e = x.create(key)
			if e != nil || v.State != "queued" {
				t.Fatal("same-key durable resolution", e)
			}
			g.counts(t, at)
		}
	})
	t.Run("actual_get_list_UTC_state_and_keyset", func(t *testing.T) {
		g := trainingCreatorSetup(t)
		ids := map[string]bool{}
		for range 3 {
			v, e := g.create(uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			ids[v.ID] = true
			got, e := g.s.Get(g.ctx, g.a, g.f.Workspace, v.ID)
			if e != nil || !reflect.DeepEqual(v, got) || got.CreatedAt.Location() != time.UTC || got.UpdatedAt.Location() != time.UTC {
				t.Fatal("exact UTC detail", e)
			}
		}
		query := url.Values{"limit": {"1"}, "state": {"queued"}}
		seen := map[string]bool{}
		cursor := ""
		for range 3 {
			page, e := g.s.List(g.ctx, g.a, g.f.Workspace, query)
			if e != nil || len(page.Items) != 1 || !ids[page.Items[0].ID] || seen[page.Items[0].ID] {
				t.Fatal("stable keyset", page, e)
			}
			seen[page.Items[0].ID] = true
			if page.NextCursor != nil {
				query.Set("cursor", *page.NextCursor)
				if cursor == "" {
					cursor = *page.NextCursor
				}
			}
		}
		page, e := g.s.List(g.ctx, g.a, g.f.Workspace, url.Values{"state": {"failed"}, "cursor": {cursor}})
		if e == nil || e.Error() != "INVALID_CURSOR" || len(page.Items) != 0 {
			t.Fatal("state scoped cursor", e)
		}
		v, e := g.s.Get(g.ctx, g.a, uuid.NewString(), uuid.NewString())
		trainingCreatorWant(t, v, e, "RESOURCE_NOT_FOUND")
		g.counts(t, 3)
	})
	t.Run("uncertain_reads_and_empty_projection", func(t *testing.T) {
		g := trainingCreatorSetup(t)
		v, e := g.create(uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		x := trainingCreatorWrapped(g, &trainingCreatorStore{Store: g.f.Store, at: 1, unknown: true})
		got, e := x.s.Get(x.ctx, x.a, x.f.Workspace, v.ID)
		trainingCreatorWant(t, got, e, string(transaction.CommitUnknown))
		x = trainingCreatorWrapped(g, &trainingCreatorStore{Store: g.f.Store, at: 1, unknown: true})
		page, e := x.s.List(x.ctx, x.a, x.f.Workspace, url.Values{})
		if e == nil || e.Error() != string(transaction.CommitUnknown) || len(page.Items) != 0 || page.NextCursor != nil {
			t.Fatal("uncertain page leaked rows", e)
		}
		g.counts(t, 1)
	})
	t.Run("cancel_deadline_query_and_store_coherence", func(t *testing.T) {
		g := trainingCreatorSetup(t)
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		v, e := g.s.Create(ctx, g.a, g.input, uuid.NewString(), uuid.NewString())
		trainingCreatorWant(t, v, e, string(transaction.Cancelled))
		ctx, cancel = context.WithDeadline(g.ctx, time.Now().Add(-time.Second))
		defer cancel()
		v, e = g.s.Create(ctx, g.a, g.input, uuid.NewString(), uuid.NewString())
		trainingCreatorWant(t, v, e, string(transaction.Timeout))
		s := *g.s
		s.Workspaces = &workspaces.Service{Identity: &identity.Service{Storage: g.f.Store}}
		v, e = s.Create(g.ctx, g.a, g.input, uuid.NewString(), uuid.NewString())
		trainingCreatorWant(t, v, e, "TRAINING_IDENTITY_REQUIRED")
		g.exec(t, "ALTER TABLE training_jobs RENAME TO explicit_training_creator_missing", "RENAME TABLE training_jobs TO explicit_training_creator_missing")
		v, e = g.create(uuid.NewString())
		trainingCreatorWant(t, v, e, "STORAGE_UNAVAILABLE")
		g.exec(t, "ALTER TABLE explicit_training_creator_missing RENAME TO training_jobs", "RENAME TABLE explicit_training_creator_missing TO training_jobs")
		g.counts(t, 0)
	})
}

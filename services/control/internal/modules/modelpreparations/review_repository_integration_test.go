//go:build repository_integration

package modelpreparations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainwire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/google/uuid"
)

type reviewJSON string
type reviewStory struct {
	f                                                                                   repositoryfixture.Fixture
	ctx                                                                                 context.Context
	s                                                                                   *Service
	a                                                                                   identity.Auth
	model, node, job, trainAttempt, trainArtifact, holdout, id, attempt, task, artifact string
	c                                                                                   wire.Command
	input                                                                               ReviewRequest
}

func reviewRaw(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func (g reviewStory) seed(t *testing.T, table string, values map[string]any) {
	t.Helper()
	values["tenant_id"], values["workspace_id"] = g.f.Tenant, g.f.Workspace
	columns := []string{}
	for k := range values {
		columns = append(columns, k)
	}
	sort.Strings(columns)
	args := []any{}
	marks := []string{}
	for _, k := range columns {
		mark := "?"
		if g.f.Store.Dialect() == transaction.PostgreSQL {
			mark = fmt.Sprintf("$%d", len(args)+1)
		}
		v := values[k]
		if j, ok := v.(reviewJSON); ok {
			v = string(j)
			if g.f.Store.Dialect() == transaction.MySQL {
				mark = "CAST(" + mark + " AS JSON)"
			} else {
				mark += "::jsonb"
			}
		}
		args = append(args, v)
		marks = append(marks, mark)
	}
	_, e := g.f.Store.Exec(g.ctx, transaction.SQL(g.f.Store.Dialect(), "INSERT INTO "+table+"("+strings.Join(columns, ",")+") VALUES("+strings.Join(marks, ",")+")", args...))
	if e != nil {
		t.Fatal("explicit synthetic constrained review fixture", table, e)
	}
}
func (g reviewStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(pg, my, args...)); e != nil {
		t.Fatal(e)
	}
}
func reviewSetup(t *testing.T) reviewStory {
	t.Helper()
	return reviewGraph(t, reviewActorSetup(t), "")
}
func reviewActorSetup(t *testing.T) reviewStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	t.Cleanup(cancel)
	owner := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		owner.Storage, owner.DB = nil, f.PostgreSQL
	}
	ws := &workspaces.Service{DB: f.PostgreSQL, Identity: owner}
	s, e := New(f.PostgreSQL, owner, ws, nil)
	if e != nil {
		t.Fatal(e)
	}
	s.Nodes = &nodes.Service{DB: f.PostgreSQL, Identity: owner, Workspaces: ws}
	g := reviewStory{f: f, ctx: ctx, s: s}
	var issued identity.IssuedSession
	if e = f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = owner.IssueSessionWithin(ctx, q, f.User, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	g.a, e = owner.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	return g
}

func (g reviewStory) seedDatasetVersion(t *testing.T, dataset, digest string, ordinal int, parent *string) (string, string) {
	t.Helper()
	version, draft, job, dsAttempt := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now().UTC()
	fp := bytes.Repeat([]byte{7}, 32)
	if parent == nil {
		g.seed(t, "datasets", map[string]any{"id": dataset, "name": "Synthetic review source " + dataset, "name_key": dataset, "origin": "synthetic", "license": "synthetic", "rights_asserted": true, "created_by": g.f.User, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	}
	g.seed(t, "dataset_drafts", map[string]any{"id": draft, "dataset_id": dataset, "created_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "parent_version_id": parent, "source_object_id": uuid.NewString(), "source_name": "synthetic.jsonl", "source_sha256": digest, "expected_bytes": 512, "source_received_at": now, "state": "published", "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_jobs", map[string]any{"id": job, "dataset_id": dataset, "draft_id": draft, "kind": "publish", "requested_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic provenance", "expected_revision": 1, "state": "succeeded", "version_id": version, "input_policy": reviewJSON(`{}`), "split_seed": strings.Repeat("1", 32), "validation_basis_points": 1000, "completed_at": now, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_job_attempts", map[string]any{"id": dsAttempt, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_number": 1, "state": "succeeded", "finished_at": now})
	g.seed(t, "dataset_versions", map[string]any{"id": version, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_id": dsAttempt, "ordinal": ordinal, "parent_version_id": parent, "published_by": g.f.User, "raw_source_sha256": digest, "effective_source_sha256": digest, "manifest_sha256": digest, "manifest": reviewJSON(`{}`), "row_mapping_sha256": digest})
	return version, dsAttempt
}

// Every command, physical stop and quality report below is an explicitly
// synthetic SQL envelope. No worker, adapter/model file, GPU or human is faked
// into product execution, and every database trigger/constraint remains enabled.
func reviewGraph(t *testing.T, g reviewStory, variant string) reviewStory {
	t.Helper()
	existingModel := g.model
	g.model, g.node, g.job, g.trainAttempt, g.trainArtifact, g.holdout, g.id, g.attempt, g.task, g.artifact = uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if existingModel != "" {
		g.model = existingModel
	}
	digest := strings.Repeat("a", 64)
	image := "sha256:" + strings.Repeat("b", 64)
	now := time.Now().UTC()
	stamp := now.Truncate(time.Microsecond)
	fp := bytes.Repeat([]byte{7}, 32)
	cfg := wire.DefaultConfiguration()
	cfgSHA, _ := cfg.Digest()
	trainCfg := trainwire.DefaultConfiguration()
	trainSHA, _ := trainCfg.Digest()
	limits := datawire.Limits{MaximumBytes: 1048576, MaximumRows: 256, MaximumLineBytes: 16384, MaximumTextBytes: 4096, MaximumSourceBytes: 256, MaximumErrors: 10}
	recipe := wire.ExecutionRecipe{SchemaVersion: 1, WorkerVersion: wire.WorkerVersion, ImageReference: wire.ImageRepository + "@sha256:" + digest, Identity: trainwire.ExecutionIdentity{Profile: trainwire.ProductionProfile, Python: trainwire.PythonVersion, WorkerSourceSHA256: digest, ImageID: &image}, Resources: wire.DefaultResources(), DataLimits: limits, RecipeSHA256: wire.RecipeDigest(), ProcessorSHA256: wire.ProcessorDigest()}
	dataset := uuid.NewString()
	version, dsAttempt := g.seedDatasetVersion(t, dataset, digest, 1, nil)
	holdoutDataset, holdoutVersion, holdoutDigest := dataset, version, digest
	if variant == "lineage" {
		holdoutDataset, holdoutDigest = uuid.NewString(), strings.Repeat("e", 64)
		holdoutVersion, dsAttempt = g.seedDatasetVersion(t, holdoutDataset, holdoutDigest, 1, nil)
	}
	tc := trainwire.Command{SchemaVersion: 1, Scope: trainwire.Scope{TenantID: g.f.Tenant, WorkspaceID: g.f.Workspace, JobID: g.job}, AttemptID: g.trainAttempt, AttemptNumber: 1, ModelID: g.model, DatasetID: dataset, DatasetVersionID: version, DatasetManifestSHA256: digest, ModelCatalogSHA256: agent.ModelCatalogDigest(), GPUUUID: "GPU-" + uuid.NewString(), Configuration: trainCfg, ConfigurationSHA256: trainSHA, Recipe: trainwire.ExecutionRecipe{SchemaVersion: 1, WorkerVersion: trainwire.WorkerVersion, ImageReference: "projectone/training@sha256:" + digest, Identity: recipe.Identity, Resources: trainwire.DefaultResources(), DataLimits: limits}, Inputs: trainwire.InputInventory{Train: datawire.Asset{Path: "train.jsonl", SizeBytes: 128, SHA256: digest}, Validation: datawire.Asset{Path: "validation.jsonl", SizeBytes: 128, SHA256: strings.Repeat("d", 64)}}}
	adapter := training.UploadDescriptor{SchemaVersion: 1, Scope: tc.Scope, UploadID: g.trainArtifact, AttemptID: g.trainAttempt, Kind: "artifact", GlobalStep: 1, ManifestSHA256: digest, Files: []datawire.Asset{{Path: "README.md", SizeBytes: 248, SHA256: digest}, {Path: "adapter_config.json", SizeBytes: 1092, SHA256: digest}, {Path: "adapter_model.safetensors", SizeBytes: 2308432, SHA256: digest}}}
	h := Holdout{ID: g.holdout, WorkspaceID: g.f.Workspace, DatasetID: holdoutDataset, DatasetVersionID: holdoutVersion, Name: "Synthetic heldout", ManifestSHA256: holdoutDigest, EffectiveSourceSHA256: holdoutDigest, RowMappingSHA256: holdoutDigest, SourceBytes: 512, RowCount: 3, Labels: LabelCounts{Account: 1, Billing: 1, Technical: 1}, RecipeID: wire.RecipeID, RecipeSHA256: wire.RecipeDigest(), InstructionSHA256: wire.InstructionDigest(), FingerprintAlgorithm: wire.PromptFingerprintAlgorithm, CreatedBy: g.f.User, Audience: "admin", Reason: "Synthetic SQL provenance", CreatedAt: stamp}
	inputs := preparationInputs{SchemaVersion: 1, TrainingCommand: tc, Adapter: adapter, TrainingIdentitySHA256: (training.AcceptedAdapter{Command: tc, Descriptor: adapter}).IdentitySHA256(), Heldout: h, DatasetAttemptID: dsAttempt}
	if variant == "lineage_conflict" {
		inputs.TrainingCommand.AttemptID = uuid.NewString()
		inputs.Adapter.AttemptID = inputs.TrainingCommand.AttemptID
		inputs.TrainingIdentitySHA256 = (training.AcceptedAdapter{Command: inputs.TrainingCommand, Descriptor: inputs.Adapter}).IdentitySHA256()
	}
	if e := inputs.validate(); e != nil {
		t.Fatal("synthetic typed inputs", e)
	}
	c := wire.Command{SchemaVersion: 1, Scope: wire.Scope{TenantID: g.f.Tenant, WorkspaceID: g.f.Workspace, PreparationID: g.id}, AttemptID: g.attempt, AttemptNumber: 1, ModelID: g.model, ModelCatalogSHA256: wire.ApprovedModelCatalogSHA256, TrainingJobID: g.job, TrainingAttemptID: g.trainAttempt, TrainingConfiguration: trainCfg, TrainingConfigurationSHA256: trainSHA, HeldoutID: g.holdout, HeldoutManifestSHA256: h.ManifestSHA256, GPUUUID: tc.GPUUUID, Configuration: cfg, ConfigurationSHA256: cfgSHA, Recipe: recipe, Inputs: inputs.inventory()}
	if e := c.Validate(); e != nil {
		t.Fatal("synthetic typed preparation command", e)
	}
	g.c = c
	commandSHA, _ := c.Digest()
	report := wire.PreparedResult{Status: "prepared", GatePassed: true, EventCount: 1, PreparationID: g.id, AttemptID: g.attempt, ConfigurationSHA256: cfgSHA, RowCount: 3}
	report.AdapterFiles = []datawire.Asset{}
	report.Cases = []wire.EvaluationCase{}
	report.ModelFiles = []datawire.Asset{}
	report.ChangedTargets = []string{}
	report.Identity = recipe.Identity
	report.Base.Confusion = [][]int64{}
	report.Candidate.Confusion = [][]int64{}
	report.Base.Correct = 3
	report.Candidate.Correct = 3
	if variant == "gate" {
		report.GatePassed = false
	}
	raw := reviewRaw(t, report)
	var roundtrip wire.PreparedResult
	if e := wire.DecodeStrict(raw, &roundtrip); e != nil {
		t.Fatal("explicit synthetic report must retain exact nonnullable wire shape", e)
	}
	reportSHA := hash(raw)
	descriptor := UploadDescriptor{SchemaVersion: 1, Scope: c.Scope, UploadID: g.artifact, AttemptID: g.attempt, ManifestSHA256: reportSHA, Files: wire.ApprovedBaseFiles()}
	for i := range descriptor.Files {
		if descriptor.Files[i].Path == "model.safetensors" {
			descriptor.Files[i].SHA256 = digest
		}
	}
	if e := descriptor.Validate(wire.MaximumModelBytes, wire.MaximumModelFiles); e != nil {
		t.Fatal(e)
	}
	cid := strings.Repeat("c", 64)
	stop := wire.StopReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), JournalID: uuid.NewString(), NodeID: g.node, TaskID: g.task, Scope: c.Scope, AttemptID: g.attempt, AttemptNumber: 1, CommandSHA256: commandSHA, ImageID: image, GPUUUID: c.GPUUUID, ContainerName: wire.ContainerName(g.attempt), ContainerID: &cid, Proof: "owned_container_removed", OwnedContainerAbsent: true, OutcomeSHA256: &reportSHA, CompletedAt: now}
	if e := stop.ValidateFor(c, g.node, g.task); e != nil {
		t.Fatal(e)
	}
	storedCommand := c
	if variant == "command" {
		storedCommand.GPUUUID = "GPU-" + uuid.NewString()
	}
	if variant == "stop" {
		stop.AgentErrorCode = "MODEL_PREPARATION_FAILED"
	}
	put := func(table string, v map[string]any) { g.seed(t, table, v) }
	j := func(v any) reviewJSON { return reviewJSON(reviewRaw(t, v)) }
	if existingModel == "" {
		put("model_versions", map[string]any{"id": g.model, "name": "Synthetic review base " + g.model, "model_key": agent.ApprovedModel().ModelKey, "catalog_digest": agent.ModelCatalogDigest(), "manifest": j(agent.ApprovedModel()), "state": "ready", "created_by": g.f.User})
	}
	put("managed_nodes", map[string]any{"id": g.node, "name": g.node, "status": "removed", "credential_hash": nil, "last_heartbeat_at": now, "protocol_version": wire.NodeProtocolVersion, "execution_profile": wire.NodeProfile, "capabilities": j(agent.Capabilities(agent.DockerModelLifecycleProfile)), "facts": j(agent.Facts{OSID: "synthetic", Architecture: "test", Hostname: "SQL review fixture", MemoryTotalBytes: 1048576, ObservedAt: now})})
	put("training_jobs", map[string]any{"id": g.job, "node_id": g.node, "current_node_id": g.node, "model_id": g.model, "dataset_id": dataset, "dataset_version_id": version, "gpu_uuid": tc.GPUUUID, "configuration": j(trainCfg), "configuration_sha256": trainSHA, "execution_recipe": j(tc.Recipe), "dataset_manifest_sha256": digest, "model_catalog_sha256": agent.ModelCatalogDigest(), "created_by": g.f.User, "requested_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic review provenance", "creation_key": uuid.NewString(), "creation_fingerprint": fp, "state": "cancelled"})
	put("training_attempts", map[string]any{"id": g.trainAttempt, "job_id": g.job, "node_id": g.node, "attempt_number": 1, "state": "cancelled", "requested_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "input_inventory": reviewJSON(`{}`)})
	put("training_uploads", map[string]any{"id": g.trainArtifact, "job_id": g.job, "attempt_id": g.trainAttempt, "kind": "artifact", "global_step": 1, "manifest_sha256": digest, "manifest": reviewJSON(`{}`), "manifest_bytes": []byte(`{}`), "state": "accepted"})
	put("training_artifacts", map[string]any{"id": g.trainArtifact, "job_id": g.job, "attempt_id": g.trainAttempt, "manifest_sha256": digest, "manifest": reviewJSON(`{}`), "file_inventory": j(adapter), "storage_key": "artifacts/" + g.job + "/" + g.trainAttempt + "/" + g.trainArtifact})
	put("model_holdouts", map[string]any{"id": g.holdout, "dataset_id": h.DatasetID, "dataset_version_id": h.DatasetVersionID, "name": h.Name, "manifest_sha256": h.ManifestSHA256, "effective_source_sha256": h.EffectiveSourceSHA256, "row_mapping_sha256": h.RowMappingSHA256, "source_bytes": h.SourceBytes, "row_count": h.RowCount, "label_counts": j(h.Labels), "recipe_id": h.RecipeID, "recipe_sha256": h.RecipeSHA256, "instruction_sha256": h.InstructionSHA256, "fingerprint_algorithm": h.FingerprintAlgorithm, "created_by": g.f.User, "audience": "admin", "reason": h.Reason, "creation_key": uuid.NewString(), "creation_fingerprint": fp, "created_at": stamp})
	put("model_preparations", map[string]any{"id": g.id, "node_id": g.node, "current_node_id": g.node, "model_id": g.model, "training_job_id": g.job, "training_attempt_id": g.trainAttempt, "training_artifact_id": g.trainArtifact, "heldout_id": g.holdout, "gpu_uuid": c.GPUUUID, "input_snapshot": j(inputs), "input_sha256": inputs.digest(), "configuration": j(cfg), "configuration_sha256": cfgSHA, "execution_recipe": j(recipe), "created_by": g.f.User, "requested_by": g.f.User, "creation_audience": "admin", "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic review intent", "creation_key": uuid.NewString(), "creation_fingerprint": fp, "state": "awaiting_review"})
	put("node_tasks", map[string]any{"id": g.task, "node_id": g.node, "operation": agent.ModelPreparationRunOperation, "payload": j(storedCommand), "state": "succeeded", "idempotency_key": uuid.NewString(), "requested_by": g.f.User, "deadline_at": now, "completed_at": now})
	attemptValues := map[string]any{"id": g.attempt, "preparation_id": g.id, "node_id": g.node, "task_id": g.task, "attempt_number": 1, "state": "prepared", "requested_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "command_sha256": commandSHA, "physical_stop_receipt": j(stop), "physical_stop_sha256": hash(reviewRaw(t, stop)), "physical_stopped_at": now, "outcome": j(report), "outcome_bytes": raw, "outcome_sha256": reportSHA, "last_event_sequence": 1}
	if variant == "unstopped" {
		delete(attemptValues, "physical_stopped_at")
		delete(attemptValues, "physical_stop_receipt")
		delete(attemptValues, "physical_stop_sha256")
	}
	put("model_preparation_attempts", attemptValues)
	put("model_preparation_uploads", map[string]any{"id": g.artifact, "preparation_id": g.id, "attempt_id": g.attempt, "manifest_sha256": reportSHA, "manifest": j(report), "manifest_bytes": raw, "descriptor": j(descriptor)})
	put("model_preparation_artifacts", map[string]any{"id": g.artifact, "preparation_id": g.id, "attempt_id": g.attempt, "upload_id": g.artifact, "manifest_sha256": reportSHA, "descriptor": j(descriptor), "report": j(report), "report_bytes": raw, "storage_path": "artifacts/" + g.id + "/" + g.attempt + "/" + g.artifact})
	g.exec(t, "UPDATE model_preparations SET attempt_number=1,current_attempt_id=$1 WHERE id=$2", "UPDATE model_preparations SET attempt_number=1,current_attempt_id=? WHERE id=?", g.attempt, g.id)
	leaseState := "released"
	if variant == "lease" {
		leaseState = "held"
	}
	put("gpu_leases", map[string]any{"id": uuid.NewString(), "node_id": g.node, "gpu_uuid": c.GPUUUID, "model_preparation_id": g.id, "revision": 1, "state": leaseState})
	g.input = ReviewRequest{ExpectedRevision: 1, ArtifactID: g.artifact, ReportSHA256: reportSHA, Decision: "reject", Reason: "Explicit synthetic SQL decision \u4e16\u754c"}
	return g
}
func (g reviewStory) review(input ReviewRequest, key string) (ModelReview, error) {
	return g.s.Review(g.ctx, g.a, g.f.Workspace, g.id, input, key, uuid.NewString())
}
func reviewWant(t *testing.T, v ModelReview, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code || !reflect.DeepEqual(v, ModelReview{}) {
		t.Fatalf("expected zero/%s, got %v zero=%v", code, e, reflect.DeepEqual(v, ModelReview{}))
	}
}
func (g reviewStory) durable(t *testing.T, state string, reviews, audits int) {
	t.Helper()
	var actual string
	var n int
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT state FROM model_preparations WHERE id=$1", "SELECT state FROM model_preparations WHERE id=?", g.id)).Scan(&actual); e != nil || actual != state {
		t.Fatal("durable parent", actual, e)
	}
	for _, check := range []struct {
		pg, my   string
		expected int
	}{{"SELECT COUNT(*) FROM model_preparation_reviews WHERE preparation_id=$1", "SELECT COUNT(*) FROM model_preparation_reviews WHERE preparation_id=?", reviews}, {"SELECT COUNT(*) FROM audit_events WHERE target_type='model_preparation' AND action='model.preparation_review_rejected' AND target_id=$1", "SELECT COUNT(*) FROM audit_events WHERE target_type='model_preparation' AND action='model.preparation_review_rejected' AND target_id=?", audits}, {"SELECT COUNT(*) FROM audit_events WHERE target_type='model_holdout' AND target_id=$1", "SELECT COUNT(*) FROM audit_events WHERE target_type='model_holdout' AND target_id=?", 0}} {
		if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(check.pg, check.my, g.id)).Scan(&n); e != nil || n != check.expected {
			t.Fatal("durable review/audit", n, check.expected, e)
		}
	}
}

// Explicit test-only transport failure after a real completed transaction.
type reviewUnknownStore struct {
	transaction.Store
	count, failAt int
}

func (s *reviewUnknownStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	e := s.Store.Within(ctx, ro, fn)
	s.count++
	if e == nil && s.count == s.failAt {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return e
}
func TestActualPairedPreparationReview(t *testing.T) {
	t.Run("typed_reject_and_exact_replay", func(t *testing.T) {
		g := reviewSetup(t)
		key := uuid.NewString()
		v, e := g.review(g.input, key)
		if e != nil || v.PreparationID != g.id || v.Decision != "reject" || v.ModelID != nil || v.CreatedAt.Location() != time.UTC {
			t.Fatal("typed real decision", v, e)
		}
		again, e := g.review(g.input, key)
		if e != nil || !reflect.DeepEqual(v, again) {
			t.Fatal("exact durable replay", e)
		}
		g.durable(t, "rejected", 1, 1)
	})
	t.Run("scope_current_authority_and_revision", func(t *testing.T) {
		g := reviewSetup(t)
		bad := g.input
		bad.ExpectedRevision++
		v, e := g.review(bad, uuid.NewString())
		reviewWant(t, v, e, "MODEL_PREPARATION_REVISION_CONFLICT")
		a := g.a
		a.Me.Audience = "portal"
		v, e = g.s.Review(g.ctx, a, g.f.Workspace, g.id, g.input, uuid.NewString(), uuid.NewString())
		reviewWant(t, v, e, "ACCESS_DENIED")
		v, e = g.s.Review(g.ctx, g.a, uuid.NewString(), g.id, g.input, uuid.NewString(), uuid.NewString())
		reviewWant(t, v, e, "RESOURCE_NOT_FOUND")
		g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
		v, e = g.review(g.input, uuid.NewString())
		reviewWant(t, v, e, "AUTH_SESSION_EXPIRED")
		g.durable(t, "awaiting_review", 0, 0)
	})
	t.Run("conflicting_intent_and_concurrent_replay", func(t *testing.T) {
		g := reviewSetup(t)
		key := uuid.NewString()
		var wg sync.WaitGroup
		var result [2]ModelReview
		var failures [2]error
		for i := range 2 {
			wg.Add(1)
			go func(i int) { defer wg.Done(); result[i], failures[i] = g.review(g.input, key) }(i)
		}
		wg.Wait()
		if failures[0] != nil || failures[1] != nil || !reflect.DeepEqual(result[0], result[1]) {
			t.Fatal("serialized exact retry", failures)
		}
		changed := g.input
		changed.Reason += " changed"
		v, e := g.review(changed, key)
		reviewWant(t, v, e, "IDEMPOTENCY_CONFLICT")
		g.durable(t, "rejected", 1, 1)
	})
	t.Run("stopped_command_report_and_lease_binding", func(t *testing.T) {
		g := reviewSetup(t)
		for _, c := range []struct{ variant, code string }{{"command", "MODEL_PREPARATION_REVIEW_BINDING_INVALID"}, {"stop", "MODEL_PREPARATION_PHYSICAL_STOP_REQUIRED"}, {"unstopped", "MODEL_PREPARATION_PHYSICAL_STOP_REQUIRED"}, {"lease", "MODEL_PREPARATION_PHYSICAL_STOP_REQUIRED"}, {"gate", "MODEL_PREPARATION_QUALITY_FAILED"}} {
			x := reviewGraph(t, g, c.variant)
			v, e := x.review(x.input, uuid.NewString())
			reviewWant(t, v, e, c.code)
			x.durable(t, "awaiting_review", 0, 0)
		}
	})
	t.Run("retained_sources_and_file_failure", func(t *testing.T) {
		g := reviewSetup(t)
		input := g.input
		input.Decision = "approve"
		input.Name = "Synthetic approval refusal"
		v, e := g.review(input, uuid.NewString())
		reviewWant(t, v, e, "MODEL_PREPARATION_UNAVAILABLE")
		g.durable(t, "awaiting_review", 0, 0)
	})
	t.Run("atomic_audit_failure_and_retry", func(t *testing.T) {
		g := reviewSetup(t)
		g.exec(t, `CREATE FUNCTION explicit_review_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'explicit test audit failure'; END;$$`, `CREATE TRIGGER explicit_review_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='explicit test audit failure'`)
		if g.f.Store.Dialect() == transaction.PostgreSQL {
			g.exec(t, `CREATE TRIGGER explicit_review_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION explicit_review_audit_failure()`, "")
		}
		key := uuid.NewString()
		v, e := g.review(g.input, key)
		reviewWant(t, v, e, "STORAGE_UNAVAILABLE")
		g.durable(t, "awaiting_review", 0, 0)
		g.exec(t, "DROP TRIGGER explicit_review_audit_failure ON audit_events", "DROP TRIGGER explicit_review_audit_failure")
		if _, e = g.review(g.input, key); e != nil {
			t.Fatal(e)
		}
		g.durable(t, "rejected", 1, 1)
	})
	t.Run("uncertain_commit_zero_and_recovery", func(t *testing.T) {
		g := reviewSetup(t)
		for _, at := range []int{1, 2} {
			x := reviewGraph(t, g, "")
			store := &reviewUnknownStore{Store: x.f.Store, failAt: at}
			owner := &identity.Service{Storage: store, Config: x.s.Identity.Config}
			ws := &workspaces.Service{Identity: owner}
			copyService := *x.s
			copyService.DB, copyService.Identity, copyService.Workspaces = nil, owner, ws
			copyNodes := *x.s.Nodes
			copyNodes.DB, copyNodes.Identity, copyNodes.Workspaces = nil, owner, ws
			copyService.Nodes = &copyNodes
			x.s = &copyService
			key := uuid.NewString()
			v, e := x.review(x.input, key)
			reviewWant(t, v, e, string(transaction.CommitUnknown))
			if at == 1 {
				x.durable(t, "awaiting_review", 0, 0)
			} else {
				x.durable(t, "rejected", 1, 1)
			}
			v, e = x.review(x.input, key)
			if e != nil || v.PreparationID != x.id {
				t.Fatal("same key recovery", e)
			}
			x.durable(t, "rejected", 1, 1)
		}
	})
	t.Run("cancellation_deadline_sql_and_store_guards", func(t *testing.T) {
		g := reviewSetup(t)
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		v, e := g.s.Review(ctx, g.a, g.f.Workspace, g.id, g.input, uuid.NewString(), uuid.NewString())
		reviewWant(t, v, e, string(transaction.Cancelled))
		ctx, cancel = context.WithDeadline(g.ctx, time.Now().Add(-time.Second))
		defer cancel()
		v, e = g.s.Review(ctx, g.a, g.f.Workspace, g.id, g.input, uuid.NewString(), uuid.NewString())
		reviewWant(t, v, e, string(transaction.Timeout))
		other := *g.s
		other.Workspaces = &workspaces.Service{Identity: &identity.Service{Storage: g.f.Store}}
		v, e = other.Review(g.ctx, g.a, g.f.Workspace, g.id, g.input, uuid.NewString(), uuid.NewString())
		reviewWant(t, v, e, "MODEL_PREPARATION_IDENTITY_REQUIRED")
		g.exec(t, "ALTER TABLE model_preparation_reviews RENAME TO explicit_missing_review_table", "RENAME TABLE model_preparation_reviews TO explicit_missing_review_table")
		v, e = g.review(g.input, uuid.NewString())
		reviewWant(t, v, e, "STORAGE_UNAVAILABLE")
		g.exec(t, "ALTER TABLE explicit_missing_review_table RENAME TO model_preparation_reviews", "RENAME TABLE explicit_missing_review_table TO model_preparation_reviews")
		g.durable(t, "awaiting_review", 0, 0)
	})
}

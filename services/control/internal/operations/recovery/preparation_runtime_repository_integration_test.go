//go:build repository_integration

package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainwire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type removalJSON string
type preparationRemovalStory struct {
	f                                                                                   repositoryfixture.Fixture
	ctx                                                                                 context.Context
	owner                                                                               *identity.Service
	model, node, job, trainAttempt, trainArtifact, holdout, id, attempt, task, artifact string
	c                                                                                   wire.Command
}
type removalInputs struct {
	SchemaVersion          int                       `json:"schema_version"`
	TrainingCommand        trainwire.Command         `json:"training_command"`
	Adapter                training.UploadDescriptor `json:"adapter"`
	TrainingIdentitySHA256 string                    `json:"training_identity_sha256"`
	Heldout                modelpreparations.Holdout `json:"heldout"`
	DatasetAttemptID       string                    `json:"dataset_attempt_id"`
}

func preparationRemovalHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (i removalInputs) digest() string { raw, _ := json.Marshal(i); return preparationRemovalHash(raw) }
func (i removalInputs) inventory() wire.InputInventory {
	return wire.InputInventory{AdapterFiles: i.Adapter.Files, Heldout: datawire.Asset{Path: "heldout.jsonl", SizeBytes: i.Heldout.SourceBytes, SHA256: i.Heldout.EffectiveSourceSHA256}}
}
func preparationRemovalSetup(t *testing.T) preparationRemovalStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	owner := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		owner.Storage, owner.DB = nil, f.PostgreSQL
	}
	return preparationRemovalGraph(t, preparationRemovalStory{f: f, ctx: ctx, owner: owner}, "")
}
func removalRaw(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func (g preparationRemovalStory) seed(t *testing.T, table string, values map[string]any) {
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
		if j, ok := v.(removalJSON); ok {
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
func (g preparationRemovalStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(pg, my, args...)); e != nil {
		t.Fatal(e)
	}
}

func preparationRemovalGraph(t *testing.T, g preparationRemovalStory, variant string) preparationRemovalStory {
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
	dataset, version, draft, job, dsAttempt := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	tc := trainwire.Command{SchemaVersion: 1, Scope: trainwire.Scope{TenantID: g.f.Tenant, WorkspaceID: g.f.Workspace, JobID: g.job}, AttemptID: g.trainAttempt, AttemptNumber: 1, ModelID: g.model, DatasetID: dataset, DatasetVersionID: version, DatasetManifestSHA256: digest, ModelCatalogSHA256: agent.ModelCatalogDigest(), GPUUUID: "GPU-" + uuid.NewString(), Configuration: trainCfg, ConfigurationSHA256: trainSHA, Recipe: trainwire.ExecutionRecipe{SchemaVersion: 1, WorkerVersion: trainwire.WorkerVersion, ImageReference: "projectone/training@sha256:" + digest, Identity: recipe.Identity, Resources: trainwire.DefaultResources(), DataLimits: limits}, Inputs: trainwire.InputInventory{Train: datawire.Asset{Path: "train.jsonl", SizeBytes: 128, SHA256: digest}, Validation: datawire.Asset{Path: "validation.jsonl", SizeBytes: 128, SHA256: strings.Repeat("d", 64)}}}
	adapter := training.UploadDescriptor{SchemaVersion: 1, Scope: tc.Scope, UploadID: g.trainArtifact, AttemptID: g.trainAttempt, Kind: "artifact", GlobalStep: 1, ManifestSHA256: digest, Files: []datawire.Asset{{Path: "README.md", SizeBytes: 248, SHA256: digest}, {Path: "adapter_config.json", SizeBytes: 1092, SHA256: digest}, {Path: "adapter_model.safetensors", SizeBytes: 2308432, SHA256: digest}}}
	h := modelpreparations.Holdout{ID: g.holdout, WorkspaceID: g.f.Workspace, DatasetID: dataset, DatasetVersionID: version, Name: "Synthetic heldout", ManifestSHA256: digest, EffectiveSourceSHA256: digest, RowMappingSHA256: digest, SourceBytes: 512, RowCount: 3, Labels: modelpreparations.LabelCounts{Account: 1, Billing: 1, Technical: 1}, RecipeID: wire.RecipeID, RecipeSHA256: wire.RecipeDigest(), InstructionSHA256: wire.InstructionDigest(), FingerprintAlgorithm: wire.PromptFingerprintAlgorithm, CreatedBy: g.f.User, Audience: "admin", Reason: "Synthetic SQL provenance", CreatedAt: stamp}
	inputs := removalInputs{SchemaVersion: 1, TrainingCommand: tc, Adapter: adapter, TrainingIdentitySHA256: (training.AcceptedAdapter{Command: tc, Descriptor: adapter}).IdentitySHA256(), Heldout: h, DatasetAttemptID: dsAttempt}

	c := wire.Command{SchemaVersion: 1, Scope: wire.Scope{TenantID: g.f.Tenant, WorkspaceID: g.f.Workspace, PreparationID: g.id}, AttemptID: g.attempt, AttemptNumber: 1, ModelID: g.model, ModelCatalogSHA256: wire.ApprovedModelCatalogSHA256, TrainingJobID: g.job, TrainingAttemptID: g.trainAttempt, TrainingConfiguration: trainCfg, TrainingConfigurationSHA256: trainSHA, HeldoutID: g.holdout, HeldoutManifestSHA256: digest, GPUUUID: tc.GPUUUID, Configuration: cfg, ConfigurationSHA256: cfgSHA, Recipe: recipe, Inputs: inputs.inventory()}
	if e := c.Validate(); e != nil {
		t.Fatal("synthetic typed preparation command", e)
	}
	g.c = c
	commandSHA, _ := c.Digest()
	put := func(table string, v map[string]any) { g.seed(t, table, v) }
	j := func(v any) removalJSON { return removalJSON(removalRaw(t, v)) }
	if existingModel == "" {
		put("model_versions", map[string]any{"id": g.model, "name": "Synthetic review base " + g.model, "model_key": agent.ApprovedModel().ModelKey, "catalog_digest": agent.ModelCatalogDigest(), "manifest": j(agent.ApprovedModel()), "state": "ready", "created_by": g.f.User})
	}
	put("managed_nodes", map[string]any{"id": g.node, "name": g.node, "status": "active", "credential_hash": bytes.Repeat([]byte{9}, 32), "last_heartbeat_at": now, "protocol_version": wire.NodeProtocolVersion, "execution_profile": wire.NodeProfile, "capabilities": j(agent.Capabilities(agent.DockerModelLifecycleProfile)), "facts": j(agent.Facts{OSID: "synthetic", Architecture: "test", Hostname: "SQL review fixture", MemoryTotalBytes: 1048576, ObservedAt: now})})
	put("datasets", map[string]any{"id": dataset, "name": "Synthetic review source", "name_key": dataset, "origin": "synthetic", "license": "synthetic", "rights_asserted": true, "created_by": g.f.User, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	put("dataset_drafts", map[string]any{"id": draft, "dataset_id": dataset, "created_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "source_object_id": uuid.NewString(), "source_name": "synthetic.jsonl", "source_sha256": digest, "expected_bytes": 512, "source_received_at": now, "state": "published", "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	put("dataset_jobs", map[string]any{"id": job, "dataset_id": dataset, "draft_id": draft, "kind": "publish", "requested_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic provenance", "expected_revision": 1, "state": "succeeded", "version_id": version, "input_policy": removalJSON(`{}`), "split_seed": strings.Repeat("1", 32), "validation_basis_points": 1000, "completed_at": now, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	put("dataset_job_attempts", map[string]any{"id": dsAttempt, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_number": 1, "state": "succeeded", "finished_at": now})
	put("dataset_versions", map[string]any{"id": version, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_id": dsAttempt, "ordinal": 1, "published_by": g.f.User, "raw_source_sha256": digest, "effective_source_sha256": digest, "manifest_sha256": digest, "manifest": removalJSON(`{}`), "row_mapping_sha256": digest})
	put("training_jobs", map[string]any{"id": g.job, "node_id": g.node, "current_node_id": g.node, "model_id": g.model, "dataset_id": dataset, "dataset_version_id": version, "gpu_uuid": tc.GPUUUID, "configuration": j(trainCfg), "configuration_sha256": trainSHA, "execution_recipe": j(tc.Recipe), "dataset_manifest_sha256": digest, "model_catalog_sha256": agent.ModelCatalogDigest(), "created_by": g.f.User, "requested_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic review provenance", "creation_key": uuid.NewString(), "creation_fingerprint": fp, "state": "cancelled"})
	put("training_attempts", map[string]any{"id": g.trainAttempt, "job_id": g.job, "node_id": g.node, "attempt_number": 1, "state": "cancelled", "requested_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "input_inventory": removalJSON(`{}`)})
	put("training_uploads", map[string]any{"id": g.trainArtifact, "job_id": g.job, "attempt_id": g.trainAttempt, "kind": "artifact", "global_step": 1, "manifest_sha256": digest, "manifest": removalJSON(`{}`), "manifest_bytes": []byte(`{}`), "state": "accepted"})
	put("training_artifacts", map[string]any{"id": g.trainArtifact, "job_id": g.job, "attempt_id": g.trainAttempt, "manifest_sha256": digest, "manifest": removalJSON(`{}`), "file_inventory": j(adapter), "storage_key": "artifacts/" + g.job + "/" + g.trainAttempt + "/" + g.trainArtifact})
	put("model_holdouts", map[string]any{"id": g.holdout, "dataset_id": dataset, "dataset_version_id": version, "name": h.Name, "manifest_sha256": digest, "effective_source_sha256": digest, "row_mapping_sha256": digest, "source_bytes": h.SourceBytes, "row_count": h.RowCount, "label_counts": j(h.Labels), "recipe_id": h.RecipeID, "recipe_sha256": h.RecipeSHA256, "instruction_sha256": h.InstructionSHA256, "fingerprint_algorithm": h.FingerprintAlgorithm, "created_by": g.f.User, "audience": "admin", "reason": h.Reason, "creation_key": uuid.NewString(), "creation_fingerprint": fp, "created_at": stamp})
	put("model_preparations", map[string]any{"id": g.id, "node_id": g.node, "current_node_id": g.node, "model_id": g.model, "training_job_id": g.job, "training_attempt_id": g.trainAttempt, "training_artifact_id": g.trainArtifact, "heldout_id": g.holdout, "gpu_uuid": c.GPUUUID, "input_snapshot": j(inputs), "input_sha256": inputs.digest(), "configuration": j(cfg), "configuration_sha256": cfgSHA, "execution_recipe": j(recipe), "created_by": g.f.User, "requested_by": g.f.User, "creation_audience": "admin", "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic review intent", "creation_key": uuid.NewString(), "creation_fingerprint": fp, "state": "preparing"})
	put("node_tasks", map[string]any{"id": g.task, "node_id": g.node, "operation": agent.ModelPreparationRunOperation, "payload": j(c), "state": "running", "idempotency_key": uuid.NewString(), "requested_by": g.f.User, "deadline_at": now.Add(time.Hour)})
	attemptValues := map[string]any{"id": g.attempt, "preparation_id": g.id, "node_id": g.node, "task_id": g.task, "attempt_number": 1, "state": "preparing", "requested_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "command_sha256": commandSHA}
	if variant == "unstopped" {
		delete(attemptValues, "physical_stopped_at")
		delete(attemptValues, "physical_stop_receipt")
		delete(attemptValues, "physical_stop_sha256")
	}
	put("model_preparation_attempts", attemptValues)
	g.exec(t, "UPDATE model_preparations SET attempt_number=1,current_attempt_id=$1 WHERE id=$2", "UPDATE model_preparations SET attempt_number=1,current_attempt_id=? WHERE id=?", g.attempt, g.id)
	leaseState := "held"
	if variant == "lease" {
		leaseState = "held"
	}
	put("gpu_leases", map[string]any{"id": uuid.NewString(), "node_id": g.node, "gpu_uuid": c.GPUUUID, "model_preparation_id": g.id, "revision": 1, "state": leaseState})
	return g
}

func preparationRemovalReceipt(t *testing.T, c wire.RemovalChallenge) wire.RemovalReceipt {
	t.Helper()
	now := time.Now().UTC()
	command := c.Command
	stop := wire.StopReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), JournalID: uuid.NewString(), NodeID: c.NodeID, TaskID: c.TaskID, Scope: command.Scope, AttemptID: command.AttemptID, AttemptNumber: command.AttemptNumber, CommandSHA256: c.CommandSHA256, ImageID: *command.Recipe.Identity.ImageID, GPUUUID: command.GPUUUID, ContainerName: wire.ContainerName(command.AttemptID), Proof: "never_created", OwnedContainerAbsent: true, AgentErrorCode: "MODEL_PREPARATION_AGENT_INTERRUPTED", CompletedAt: now}
	r := wire.RemovalReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), Challenge: c, ChallengeSHA256: c.Digest(), Stop: stop, ObservedAt: now}
	if e := r.Validate(time.Now()); e != nil {
		t.Fatal(e)
	}
	return r
}
func removalCount(t *testing.T, g preparationRemovalStory, pg, my string, args ...any) int {
	t.Helper()
	var n int
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(pg, my, args...)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestActualPairedPreparationRemovalRuntime(t *testing.T) {
	t.Run("current_fence_challenge_receipt_release_replay_conflict", func(t *testing.T) {
		g := preparationRemovalSetup(t)
		c, e := PreparePreparationRemoval(g.ctx, g.owner, g.node, g.id, "Explicit isolated synthetic physical challenge")
		if e != nil || c.Command.AttemptID != g.attempt || c.NodeID != g.node {
			t.Fatal("actual selected challenge", e)
		}
		if n := removalCount(t, g, `SELECT count(*) FROM managed_nodes WHERE id=$1 AND status='removed' AND credential_hash IS NULL`, `SELECT count(*) FROM managed_nodes WHERE id=? AND status='removed' AND credential_hash IS NULL`, g.node); n != 1 {
			t.Fatal("credential not fenced")
		}
		if n := removalCount(t, g, `SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='recovery_required'`, `SELECT count(*) FROM gpu_leases WHERE model_preparation_id=? AND state='recovery_required'`, g.id); n != 1 {
			t.Fatal("challenge released physical owner")
		}
		receipt := preparationRemovalReceipt(t, c)
		if e = ReconcilePreparationRemoval(g.ctx, g.owner, receipt, "Explicit positive never-created operator test double"); e != nil {
			t.Fatal(e)
		}
		if e = ReconcilePreparationRemoval(g.ctx, g.owner, receipt, "Explicit exact replay"); e != nil {
			t.Fatal("actual replay", e)
		}
		if n := removalCount(t, g, `SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='released'`, `SELECT count(*) FROM gpu_leases WHERE model_preparation_id=? AND state='released'`, g.id); n != 1 {
			t.Fatal("matching owner not released")
		}
		changed := receipt
		changed.ReceiptID = uuid.NewString()
		if e = ReconcilePreparationRemoval(g.ctx, g.owner, changed, "Changed same challenge must conflict"); e == nil || e.Error() != "IDEMPOTENCY_CONFLICT" {
			t.Fatal("changed receipt replay accepted", e)
		}
	})
	t.Run("removal_audit_rollback_preserves_physical_owner", func(t *testing.T) {
		g := preparationRemovalSetup(t)
		c, e := PreparePreparationRemoval(g.ctx, g.owner, g.node, g.id, "Isolated removal audit rollback")
		if e != nil {
			t.Fatal(e)
		}
		receipt := preparationRemovalReceipt(t, c)
		if g.f.Store.Dialect() == transaction.MySQL {
			g.exec(t, "", `CREATE TRIGGER reject_preparation_removal_audit BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action='model.preparation_removal_reconciled' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SYNTHETIC_REMOVAL_AUDIT_FAILURE'; END IF; END`)
		} else {
			g.exec(t, `CREATE FUNCTION reject_preparation_removal_audit_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_removal_reconciled' THEN RAISE EXCEPTION 'SYNTHETIC_REMOVAL_AUDIT_FAILURE';END IF;RETURN NEW;END;$$`, "")
			g.exec(t, `CREATE TRIGGER reject_preparation_removal_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_preparation_removal_audit_fn()`, "")
		}
		if e = ReconcilePreparationRemoval(g.ctx, g.owner, receipt, "Expected isolated operator audit rollback"); e == nil {
			t.Fatal("failed audit released owner")
		}
		if n := removalCount(t, g, `SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='recovery_required'`, `SELECT count(*) FROM gpu_leases WHERE model_preparation_id=? AND state='recovery_required'`, g.id); n != 1 {
			t.Fatal("GPU mutation escaped rollback")
		}
		if n := removalCount(t, g, `SELECT count(*) FROM model_preparation_attempts WHERE id=$1 AND physical_stopped_at IS NULL`, `SELECT count(*) FROM model_preparation_attempts WHERE id=? AND physical_stopped_at IS NULL`, g.attempt); n != 1 {
			t.Fatal("stop mutation escaped rollback")
		}
		if g.f.Store.Dialect() == transaction.MySQL {
			g.exec(t, "", `DROP TRIGGER reject_preparation_removal_audit`)
		} else {
			g.exec(t, `DROP TRIGGER reject_preparation_removal_audit ON audit_events`, "")
			g.exec(t, `DROP FUNCTION reject_preparation_removal_audit_fn()`, "")
		}
		if e = ReconcilePreparationRemoval(g.ctx, g.owner, receipt, "Explicit retry after isolated audit repair"); e != nil {
			t.Fatal(e)
		}
	})

	t.Run("unknown_challenge_suppressed_and_uncertain_release_recovered", func(t *testing.T) {
		g := preparationRemovalSetup(t)
		// Commit-ack instrumentation wraps the actual owning SQL transaction.
		wrapped := &preparationRemovalUnknown{Store: g.f.Store, failAt: 2}
		owner := &identity.Service{Storage: wrapped, Config: g.owner.Config}
		c, e := PreparePreparationRemoval(g.ctx, owner, g.node, g.id, "Unknown challenge acknowledgement")
		if e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(c, wire.RemovalChallenge{}) {
			t.Fatal("uncertain challenge escaped", e)
		}
		c, e = PreparePreparationRemoval(g.ctx, g.owner, g.node, g.id, "Explicit new challenge after unresolved file outcome")
		if e != nil {
			t.Fatal(e)
		}
		receipt := preparationRemovalReceipt(t, c)
		wrapped = &preparationRemovalUnknown{Store: g.f.Store, failAt: 1}
		owner = &identity.Service{Storage: wrapped, Config: g.owner.Config}
		e = ReconcilePreparationRemoval(g.ctx, owner, receipt, "Unknown release acknowledgement")
		if e == nil || e.Error() != string(transaction.CommitUnknown) {
			t.Fatal("uncertain release not preserved", e)
		}
		if e = ReconcilePreparationRemoval(g.ctx, g.owner, receipt, "Explicit exact release readback"); e != nil {
			t.Fatal(e)
		}
	})
}

type preparationRemovalUnknown struct {
	transaction.Store
	count, failAt int
}

func (s *preparationRemovalUnknown) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	e := s.Store.Within(ctx, ro, fn)
	s.count++
	if e == nil && s.count == s.failAt {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return e
}

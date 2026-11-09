//go:build repository_integration

package training_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/google/uuid"
)

type acceptedAdapterJSON string
type acceptedAdapterStory struct {
	f                                             repositoryfixture.Fixture
	ctx                                           context.Context
	owner                                         *identity.Service
	s                                             *training.Service
	a                                             identity.Auth
	job, node, attempt, task, artifact, reportSHA string
	c                                             wire.Command
	descriptor                                    training.UploadDescriptor
	stop                                          wire.StopReceipt
	raw                                           []byte
	root, token                                   string
}

func acceptedAdapterDigest(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (g acceptedAdapterStory) seed(t *testing.T, table string, values map[string]any) {
	t.Helper()
	values["tenant_id"], values["workspace_id"] = g.f.Tenant, g.f.Workspace
	columns := make([]string, 0, len(values))
	for k := range values {
		columns = append(columns, k)
	}
	sort.Strings(columns)
	var args []any
	var marks []string
	for _, k := range columns {
		mark := "?"
		if g.f.Store.Dialect() == transaction.PostgreSQL {
			mark = fmt.Sprintf("$%d", len(args)+1)
		}
		v := values[k]
		if j, ok := v.(acceptedAdapterJSON); ok {
			v = string(j)
			if g.f.Store.Dialect() == transaction.PostgreSQL {
				mark += "::jsonb"
			} else {
				mark = "CAST(" + mark + " AS JSON)"
			}
		}
		args = append(args, v)
		marks = append(marks, mark)
	}
	sql := "INSERT INTO " + table + "(" + strings.Join(columns, ",") + ") VALUES(" + strings.Join(marks, ",") + ")"
	if _, e := g.f.Store.Exec(g.ctx, transaction.SQL(g.f.Store.Dialect(), sql, args...)); e != nil {
		t.Fatal("synthetic constrained accepted adapter seed", table, e)
	}
}
func (g acceptedAdapterStory) statement(pg, my string, args ...any) transaction.Statement {
	return g.f.Statement(pg, my, args...)
}
func (g acceptedAdapterStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.statement(pg, my, args...)); e != nil {
		t.Fatal("actual synthetic adapter fixture", e)
	}
}
func (g acceptedAdapterStory) within(fn func(context.Context, transaction.Executor) error) error {
	return g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := g.owner.RecheckOrderedWithin(ctx, q, g.a); e != nil {
			return e
		}
		if _, e := g.s.Workspaces.RecheckScopeWithin(ctx, q, g.a, g.f.Workspace); e != nil {
			return e
		}
		return fn(ctx, q)
	})
}
func (g acceptedAdapterStory) read(ctx context.Context, q transaction.Executor) (training.AcceptedAdapter, error) {
	return g.s.AcceptedAdapterWithin(ctx, q, g.f.Tenant, g.f.Workspace, g.job)
}
func acceptedAdapterWant(t *testing.T, v training.AcceptedAdapter, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code || !reflect.DeepEqual(v, training.AcceptedAdapter{}) {
		t.Fatalf("expected zero accepted adapter/%s, got %v, zero=%v", code, e, reflect.DeepEqual(v, training.AcceptedAdapter{}))
	}
}
func acceptedAdapterRaw(t *testing.T, v any) []byte {
	t.Helper()
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

// Actual retained CPU report/assets are independently bound by the harness. This
// fixture explicitly substitutes production identity/physical/SQL envelopes and
// creates no adapter files. It is not training, GPU or quality evidence.
func acceptedAdapterSetup(t *testing.T) acceptedAdapterStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	t.Cleanup(cancel)
	owner := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		owner.Storage, owner.DB = nil, f.PostgreSQL
	}
	root := filepath.Join(t.TempDir(), "accepted-adapter-store")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := training.InitializeStore(root, f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	resources := wire.DefaultResources()
	store, e := training.OpenStore(root, f.Tenant, 4096, resources.MaximumOutputBytes, resources.MaximumOutputFiles)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := store.Close(); e != nil {
			t.Error(e)
		}
	})
	g := acceptedAdapterStory{f: f, ctx: ctx, owner: owner, s: &training.Service{DB: f.PostgreSQL, Identity: owner, Workspaces: &workspaces.Service{DB: f.PostgreSQL, Identity: owner}, Store: store}, job: uuid.NewString(), node: uuid.NewString(), attempt: uuid.NewString(), task: uuid.NewString(), artifact: uuid.NewString()}
	var issued identity.IssuedSession
	if e = f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = owner.IssueSessionWithin(ctx, q, f.User, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	g.root, g.token = root, issued.Token
	g.a, e = owner.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	return acceptedAdapterGraph(t, g, "")
}

func acceptedAdapterGraph(t *testing.T, g acceptedAdapterStory, variant string) acceptedAdapterStory {
	t.Helper()
	existingModel, existingNode := g.c.ModelID, g.node
	g.job, g.node, g.attempt, g.task, g.artifact = uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	f := g.f
	resources := wire.DefaultResources()
	output := os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT")
	if output == "" || !filepath.IsAbs(output) {
		t.Fatal("EXPLICIT_RETAINED_TRAINING_INPUT_REQUIRED")
	}
	source, e := os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil {
		t.Fatal(e)
	}
	var original wire.Result
	if e = json.Unmarshal(source, &original); e != nil || original.Status != "trained" || original.DeviceProfile != wire.VerificationProfile || original.Bindings == nil {
		t.Fatal("original CPU training report required", e)
	}
	hash := strings.Repeat("a", 64)
	image := "sha256:" + strings.Repeat("b", 64)
	cfg := original.Bindings.Configuration
	cfg.Device = wire.ProductionProfile
	cfgSHA, e := cfg.Digest()
	if e != nil {
		t.Fatal(e)
	}
	model := existingModel
	if model == "" {
		model = uuid.NewString()
	}
	dataset, version := uuid.NewString(), uuid.NewString()
	c := wire.Command{SchemaVersion: 1, Scope: wire.Scope{TenantID: f.Tenant, WorkspaceID: f.Workspace, JobID: g.job}, AttemptID: g.attempt, AttemptNumber: 1, ModelID: model, DatasetID: dataset, DatasetVersionID: version, DatasetManifestSHA256: hash, ModelCatalogSHA256: agent.ModelCatalogDigest(), GPUUUID: "GPU-" + uuid.NewString(), Configuration: cfg, ConfigurationSHA256: cfgSHA,
		Recipe: wire.ExecutionRecipe{SchemaVersion: 1, WorkerVersion: wire.WorkerVersion, ImageReference: "projectone/training@sha256:" + hash, Identity: wire.ExecutionIdentity{Profile: wire.ProductionProfile, Python: wire.PythonVersion, WorkerSourceSHA256: hash, ImageID: &image}, Resources: resources, DataLimits: datawire.Limits{MaximumBytes: 1048576, MaximumRows: 256, MaximumLineBytes: 16384, MaximumTextBytes: 4096, MaximumSourceBytes: 256, MaximumErrors: 10}},
		Inputs: wire.InputInventory{Train: datawire.Asset{Path: "train.jsonl", SizeBytes: 128, SHA256: strings.Repeat("d", 64)}, Validation: datawire.Asset{Path: "validation.jsonl", SizeBytes: 128, SHA256: strings.Repeat("e", 64)}}}
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
	set := func(k string, v any) { envelope[k] = acceptedAdapterRaw(t, v) }
	set("bindings", wire.ExpectedBindings(request, agent.ApprovedModel().Repository))
	set("job_id", g.job)
	set("attempt_id", g.attempt)
	set("dataset_version_id", version)
	set("dataset_manifest_sha256", hash)
	set("memory_enforcement", "linux_cgroup_memory_cpu_fsize_wall")
	set("device_profile", wire.ProductionProfile)
	raw := acceptedAdapterRaw(t, envelope)
	if variant == "normalized_result_budget" {
		c.Recipe.Resources.MaximumResultBytes = int64(len(raw))
		g.c = c
		request, e = c.ContainerRequest()
		if e != nil {
			t.Fatal("exact raw result budget", e)
		}
	}
	result, e := wire.DecodeResult(raw, request, agent.ApprovedModel().Repository)
	if e != nil {
		t.Fatal("synthetic envelope around unchanged CPU result", e)
	}
	g.raw = raw
	g.reportSHA = acceptedAdapterDigest(raw)
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
	stop := wire.StopReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), JournalID: uuid.NewString(), NodeID: g.node, TaskID: g.task, Scope: c.Scope, AttemptID: g.attempt, AttemptNumber: 1, CommandSHA256: commandSHA, ImageID: image, GPUUUID: c.GPUUUID, ContainerName: wire.ContainerName(g.attempt), ContainerID: &cid, Proof: "owned_container_removed", OwnedContainerAbsent: true, OutcomeSHA256: &g.reportSHA, CompletedAt: now}
	if e := stop.ValidateFor(c, g.node, g.task); e != nil {
		t.Fatal(e)
	}
	g.stop = stop
	storedCommand, storedDescriptor, storedStop := c, d, stop
	storedDescriptor.Files = append([]datawire.Asset{}, d.Files...)
	storedReport := raw
	storedAttemptInputs := c.Inputs
	attemptOutcome := g.reportSHA
	storage := "artifacts/" + g.job + "/" + g.attempt + "/" + g.artifact
	currentNode := g.node
	finalState, uploadState := "succeeded", "accepted"
	var currentAttempt any = g.attempt
	var acceptedArtifact any = g.artifact
	wrong := strings.Repeat("f", 64)
	switch variant {
	case "", "normalized_result_budget":
	case "terminal_state":
		finalState = "failed"
	case "terminal_attempt":
		currentAttempt = nil
	case "terminal_artifact":
		acceptedArtifact = nil
	case "terminal_upload":
		uploadState = "failed"
	case "command_scope":
		storedCommand.Scope.WorkspaceID = uuid.NewString()
	case "command_gpu":
		storedCommand.GPUUUID = "GPU-" + uuid.NewString()
	case "command_configuration":
		storedCommand.ConfigurationSHA256 = wrong
	case "command_recipe":
		storedCommand.Recipe.WorkerVersion = "invalid"
	case "command_node":
		currentNode = existingNode
	case "descriptor":
		storedDescriptor.Files[0].SHA256 = wrong
	case "storage":
		storage = "artifacts/" + uuid.NewString() + "/" + g.attempt + "/" + g.artifact
	case "report":
		storedReport = []byte(`{}`)
	case "attempt_inputs":
		storedAttemptInputs.Train.SHA256 = wrong
	case "stop_receipt_outcome":
		storedStop.OutcomeSHA256 = &wrong
	case "stop_attempt_outcome":
		attemptOutcome = wrong
	case "stop_agent_error":
		storedStop.AgentErrorCode = "TRAINING_AGENT_EXECUTION_FAILED"
	case "stop_never_created":
		storedStop.Proof = "never_created"
		storedStop.ContainerID = nil
	case "stop_canonical_sha":
	default:
		t.Fatal("unknown explicit source fixture variant")
	}
	if e := storedStop.ValidateFor(c, g.node, g.task); e != nil {
		t.Fatal("stop fixture must satisfy wire shape", e)
	}
	stopRaw := acceptedAdapterRaw(t, storedStop)
	stopDigest := acceptedAdapterDigest(stopRaw)
	if variant == "stop_canonical_sha" {
		stopDigest = wrong
	}
	fp := bytes.Repeat([]byte{8}, 32)
	draft, job, attempt := uuid.NewString(), uuid.NewString(), uuid.NewString()
	g.seed(t, "datasets", map[string]any{"id": dataset, "name": "Synthetic training provenance", "name_key": dataset, "origin": "synthetic", "license": "synthetic", "rights_asserted": true, "created_by": f.User, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_drafts", map[string]any{"id": draft, "dataset_id": dataset, "created_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "source_object_id": uuid.NewString(), "source_name": "synthetic.jsonl", "source_sha256": hash, "expected_bytes": 1, "source_received_at": now, "state": "published", "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_jobs", map[string]any{"id": job, "dataset_id": dataset, "draft_id": draft, "kind": "publish", "requested_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic provenance", "expected_revision": 1, "state": "succeeded", "version_id": version, "input_policy": acceptedAdapterJSON(`{}`), "split_seed": strings.Repeat("1", 32), "validation_basis_points": 1000, "completed_at": now, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_job_attempts", map[string]any{"id": attempt, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_number": 1, "state": "succeeded", "finished_at": now})
	g.seed(t, "dataset_versions", map[string]any{"id": version, "dataset_id": dataset, "draft_id": draft, "job_id": job, "attempt_id": attempt, "ordinal": 1, "published_by": f.User, "raw_source_sha256": hash, "effective_source_sha256": hash, "manifest_sha256": hash, "manifest": acceptedAdapterJSON(`{}`), "row_mapping_sha256": hash})
	if existingModel == "" {
		g.seed(t, "model_versions", map[string]any{"id": model, "name": "Synthetic accepted adapter base", "model_key": "qwen3-0.6b", "catalog_digest": agent.ModelCatalogDigest(), "manifest": acceptedAdapterJSON(acceptedAdapterRaw(t, agent.ApprovedModel())), "state": "ready", "created_by": f.User})
	}
	facts := agent.Facts{OSID: "synthetic", Architecture: "test", Hostname: "Accepted adapter metadata only", MemoryTotalBytes: 1048576, ObservedAt: now}
	g.seed(t, "managed_nodes", map[string]any{"id": g.node, "name": "Synthetic retained training node", "status": "removed", "credential_hash": nil, "last_heartbeat_at": now, "protocol_version": agent.AIVersion, "execution_profile": agent.DockerAIProfile, "capabilities": acceptedAdapterJSON(acceptedAdapterRaw(t, agent.Capabilities(agent.DockerAIProfile))), "facts": acceptedAdapterJSON(acceptedAdapterRaw(t, facts))})
	g.seed(t, "training_jobs", map[string]any{"id": g.job, "node_id": g.node, "current_node_id": currentNode, "model_id": model, "dataset_id": dataset, "dataset_version_id": version, "gpu_uuid": c.GPUUUID, "configuration": acceptedAdapterJSON(acceptedAdapterRaw(t, cfg)), "configuration_sha256": cfgSHA, "execution_recipe": acceptedAdapterJSON(acceptedAdapterRaw(t, c.Recipe)), "dataset_manifest_sha256": hash, "model_catalog_sha256": agent.ModelCatalogDigest(), "created_by": f.User, "requested_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic accepted source", "creation_key": uuid.NewString(), "creation_fingerprint": fp, "state": "queued"})
	g.seed(t, "node_tasks", map[string]any{"id": g.task, "node_id": g.node, "operation": "training.run.v1", "state": "succeeded", "attempt": 1, "idempotency_key": uuid.NewString(), "requested_by": f.User, "deadline_at": now.Add(time.Minute), "completed_at": now, "payload": acceptedAdapterJSON(acceptedAdapterRaw(t, storedCommand))})
	g.seed(t, "training_attempts", map[string]any{"id": g.attempt, "job_id": g.job, "node_id": g.node, "task_id": g.task, "attempt_number": 1, "state": "succeeded", "requested_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "input_inventory": acceptedAdapterJSON(acceptedAdapterRaw(t, storedAttemptInputs)), "outcome_sha256": attemptOutcome, "outcome": acceptedAdapterJSON(raw), "physical_stop_receipt": acceptedAdapterJSON(stopRaw), "physical_stop_sha256": stopDigest, "physical_stopped_at": now, "finished_at": now})
	g.seed(t, "training_uploads", map[string]any{"id": g.artifact, "job_id": g.job, "attempt_id": g.attempt, "kind": "artifact", "global_step": result.GlobalStep, "manifest_sha256": g.reportSHA, "manifest": acceptedAdapterJSON(raw), "state": uploadState, "manifest_bytes": storedReport})
	g.seed(t, "training_artifacts", map[string]any{"id": g.artifact, "job_id": g.job, "attempt_id": g.attempt, "manifest_sha256": g.reportSHA, "manifest": acceptedAdapterJSON(raw), "file_inventory": acceptedAdapterJSON(acceptedAdapterRaw(t, storedDescriptor)), "storage_key": storage})
	g.exec(t, "UPDATE training_jobs SET state=$1,attempt_number=1,current_attempt_id=$2,accepted_artifact_id=$3,finished_at=$4 WHERE id=$5", "UPDATE training_jobs SET state=?,attempt_number=1,current_attempt_id=?,accepted_artifact_id=?,finished_at=? WHERE id=?", finalState, currentAttempt, acceptedArtifact, now, g.job)
	return g
}

type acceptedAdapterWrongDialect struct{ transaction.Executor }

func (q acceptedAdapterWrongDialect) Dialect() transaction.Dialect {
	if q.Executor.Dialect() == transaction.PostgreSQL {
		return transaction.MySQL
	}
	return transaction.PostgreSQL
}

func TestActualBorrowedAcceptedAdapter(t *testing.T) {
	t.Run("typed_accepted_identity", func(t *testing.T) {
		g := acceptedAdapterSetup(t)
		var out training.AcceptedAdapter
		if e := g.within(func(ctx context.Context, q transaction.Executor) error {
			var e error
			out, e = g.read(ctx, q)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		if out.Job.ID != g.job || out.Job.State != "succeeded" || !reflect.DeepEqual(out.Command, g.c) || !reflect.DeepEqual(out.Descriptor, g.descriptor) || out.Job.CreatedAt.Location() != time.UTC || out.Job.FinishedAt == nil || out.Job.FinishedAt.Location() != time.UTC {
			t.Fatal("typed accepted source changed")
		}
		changed := out
		changed.Job.Reason = "presentation only"
		if changed.IdentitySHA256() != out.IdentitySHA256() {
			t.Fatal("immutable identity includes mutable presentation")
		}
		if g.f.PostgreSQL != nil {
			tx, e := g.f.PostgreSQL.Begin(g.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback(g.ctx)
			original, e := g.s.AcceptedAdapterInTransaction(g.ctx, tx, g.f.Tenant, g.f.Workspace, g.job)
			if e != nil || !reflect.DeepEqual(original, out) {
				t.Fatal("original PG source wrapper changed", e)
			}
		}
	})
	t.Run("exact_scope_and_job_isolation", func(t *testing.T) {
		g := acceptedAdapterSetup(t)
		for _, v := range [][3]string{{uuid.NewString(), g.f.Workspace, g.job}, {g.f.Tenant, uuid.NewString(), g.job}, {g.f.Tenant, g.f.Workspace, uuid.NewString()}, {g.f.Tenant, g.f.Workspace, "bad"}} {
			if e := g.within(func(ctx context.Context, q transaction.Executor) error {
				out, e := g.s.AcceptedAdapterWithin(ctx, q, v[0], v[1], v[2])
				acceptedAdapterWant(t, out, e, "TRAINING_NOT_FOUND")
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
	})
	t.Run("terminal_current_attempt_and_artifact_refusal", func(t *testing.T) {
		base := acceptedAdapterSetup(t)
		for _, variant := range []string{"terminal_state", "terminal_attempt", "terminal_artifact", "terminal_upload"} {
			g := acceptedAdapterGraph(t, base, variant)
			if e := g.within(func(ctx context.Context, q transaction.Executor) error {
				out, e := g.read(ctx, q)
				acceptedAdapterWant(t, out, e, "TRAINING_RESULT_UNAVAILABLE")
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
	})
	t.Run("command_configuration_recipe_and_node_corruption", func(t *testing.T) {
		base := acceptedAdapterSetup(t)
		for _, variant := range []string{"command_scope", "command_gpu", "command_configuration", "command_recipe", "command_node"} {
			g := acceptedAdapterGraph(t, base, variant)
			if e := g.within(func(ctx context.Context, q transaction.Executor) error {
				out, e := g.read(ctx, q)
				acceptedAdapterWant(t, out, e, "TRAINING_DATA_INVALID")
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
	})
	t.Run("descriptor_report_storage_and_file_set_corruption", func(t *testing.T) {
		base := acceptedAdapterSetup(t)
		for _, variant := range []string{"descriptor", "storage", "report", "attempt_inputs"} {
			g := acceptedAdapterGraph(t, base, variant)
			if e := g.within(func(ctx context.Context, q transaction.Executor) error {
				out, e := g.read(ctx, q)
				acceptedAdapterWant(t, out, e, "TRAINING_DATA_INVALID")
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
	})
	t.Run("physical_stop_outcome_and_agent_integrity", func(t *testing.T) {
		base := acceptedAdapterSetup(t)
		for _, variant := range []string{"stop_receipt_outcome", "stop_attempt_outcome", "stop_agent_error", "stop_never_created", "stop_canonical_sha"} {
			g := acceptedAdapterGraph(t, base, variant)
			if e := g.within(func(ctx context.Context, q transaction.Executor) error {
				out, e := g.read(ctx, q)
				acceptedAdapterWant(t, out, e, "TRAINING_DATA_INVALID")
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
	})
	t.Run("actual_missing_adapter_file_failure", func(t *testing.T) {
		g := acceptedAdapterSetup(t)
		var out training.AcceptedAdapter
		if e := g.within(func(ctx context.Context, q transaction.Executor) error {
			var e error
			out, e = g.read(ctx, q)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		if e := g.s.VerifyAcceptedAdapter(g.ctx, out); e == nil {
			t.Fatal("SQL metadata falsely proved adapter file existence")
		}
		file, _, e := g.s.OpenAcceptedAdapterAsset(g.ctx, out, "adapter_model.safetensors")
		if e == nil || file != nil {
			if file != nil {
				file.Close()
			}
			t.Fatal("missing accepted file escaped", e)
		}
	})
	t.Run("actual_job_artifact_and_task_row_fences", func(t *testing.T) {
		g := acceptedAdapterSetup(t)
		for table, id := range map[string]string{"training_jobs": g.job, "training_artifacts": g.artifact, "training_uploads": g.artifact, "training_attempts": g.attempt, "node_tasks": g.task} {
			if e := g.within(func(ctx context.Context, q transaction.Executor) error {
				if _, e := g.read(ctx, q); e != nil {
					return e
				}
				blocked, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
				start := time.Now()
				e := g.f.Store.Within(blocked, false, func(ctx context.Context, writer transaction.Executor) error {
					var locked string
					return writer.QueryRow(ctx, g.statement("SELECT id::text FROM "+table+" WHERE id=$1 FOR UPDATE", "SELECT id FROM "+table+" WHERE id=? FOR UPDATE", id)).Scan(&locked)
				})
				if e == nil || time.Since(start) < 150*time.Millisecond {
					t.Fatal("accepted source graph row fence missing", table, e)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
	})
	t.Run("owner_rollback_and_reopen", func(t *testing.T) {
		g := acceptedAdapterSetup(t)
		rollback := errors.New("EXPLICIT_TEST_ADAPTER_OWNER_ROLLBACK")
		e := g.within(func(ctx context.Context, q transaction.Executor) error {
			if _, e := g.read(ctx, q); e != nil {
				return e
			}
			if _, e := q.Exec(ctx, g.statement("UPDATE training_jobs SET wait_code='EXPLICIT_TEST_SOURCE_ROLLBACK' WHERE id=$1", "UPDATE training_jobs SET wait_code='EXPLICIT_TEST_SOURCE_ROLLBACK' WHERE id=?", g.job)); e != nil {
				return e
			}
			return rollback
		})
		if e != rollback {
			t.Fatal(e)
		}
		service := *g.s
		if e = g.within(func(ctx context.Context, q transaction.Executor) error {
			out, e := service.AcceptedAdapterWithin(ctx, q, g.f.Tenant, g.f.Workspace, g.job)
			if e != nil || out.Job.WaitCode != nil {
				t.Fatal("owner transaction was committed or source reopen failed", e)
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("nil_mixed_context_and_query_failure", func(t *testing.T) {
		g := acceptedAdapterSetup(t)
		if e := g.within(func(ctx context.Context, q transaction.Executor) error {
			var absent *training.Service
			out, e := absent.AcceptedAdapterWithin(ctx, q, g.f.Tenant, g.f.Workspace, g.job)
			acceptedAdapterWant(t, out, e, "STORAGE_UNAVAILABLE")
			missing := *g.s
			missing.Store = nil
			out, e = missing.AcceptedAdapterWithin(ctx, q, g.f.Tenant, g.f.Workspace, g.job)
			acceptedAdapterWant(t, out, e, "TRAINING_STORAGE_UNAVAILABLE")
			out, e = g.s.AcceptedAdapterWithin(nil, q, g.f.Tenant, g.f.Workspace, g.job)
			acceptedAdapterWant(t, out, e, "STORAGE_UNAVAILABLE")
			out, e = g.s.AcceptedAdapterWithin(ctx, nil, g.f.Tenant, g.f.Workspace, g.job)
			acceptedAdapterWant(t, out, e, "STORAGE_UNAVAILABLE")
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			out, e = g.read(cancelled, q)
			acceptedAdapterWant(t, out, e, "STORAGE_CANCELLED")
			expired, expire := context.WithDeadline(ctx, time.Now().Add(-time.Second))
			defer expire()
			out, e = g.read(expired, q)
			acceptedAdapterWant(t, out, e, "STORAGE_TIMEOUT")
			out, e = g.s.AcceptedAdapterWithin(ctx, acceptedAdapterWrongDialect{q}, g.f.Tenant, g.f.Workspace, g.job)
			acceptedAdapterWant(t, out, e, "TRAINING_STORAGE_MISMATCH")
			incoherent := *g.s
			incoherent.Workspaces = &workspaces.Service{}
			out, e = incoherent.AcceptedAdapterWithin(ctx, q, g.f.Tenant, g.f.Workspace, g.job)
			acceptedAdapterWant(t, out, e, "TRAINING_IDENTITY_REQUIRED")
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		g.exec(t, "ALTER TABLE training_artifacts RENAME TO source_test_retained_artifacts", "RENAME TABLE training_artifacts TO source_test_retained_artifacts")
		defer g.exec(t, "ALTER TABLE source_test_retained_artifacts RENAME TO training_artifacts", "RENAME TABLE source_test_retained_artifacts TO training_artifacts")
		e := g.within(func(ctx context.Context, q transaction.Executor) error {
			out, e := g.read(ctx, q)
			acceptedAdapterWant(t, out, e, "STORAGE_UNAVAILABLE")
			return e
		})
		if e == nil || e.Error() != "STORAGE_UNAVAILABLE" {
			t.Fatal("actual source query failure lost", e)
		}
	})
}

func TestActualAcceptedExperimentRead(t *testing.T) {
	g := acceptedAdapterSetup(t)
	read := func(s *training.Service, workspace string) (training.AcceptedExperiment, error) {
		return s.ReadAcceptedExperiment(g.ctx, g.a, workspace, g.job, uuid.NewString())
	}
	refuse := func(out training.AcceptedExperiment, e error) {
		t.Helper()
		if e == nil || !reflect.DeepEqual(out, training.AcceptedExperiment{}) {
			t.Fatalf("unverified experiment escaped: %v", e)
		}
	}
	refuse(read(g.s, g.f.Workspace))

	// Put real retained adapter bytes in the production file layout. The
	// fixture's command and stop envelope remain synthetic.
	if e := g.s.Store.PrepareUpload(g.descriptor); e != nil {
		t.Fatal(e)
	}
	for _, asset := range g.descriptor.Files {
		path := filepath.Join(os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT"), "adapter", asset.Path)
		raw, e := os.ReadFile(path)
		if e != nil || int64(len(raw)) != asset.SizeBytes {
			t.Fatal("retained adapter", e)
		}
		for offset := int64(0); offset < asset.SizeBytes; offset += wire.TransferChunkBytes {
			chunk := raw[offset:min(offset+wire.TransferChunkBytes, asset.SizeBytes)]
			if _, e := g.s.Store.WriteChunk(g.ctx, g.descriptor, asset.Path, offset, int64(len(chunk)), acceptedAdapterDigest(chunk), bytes.NewReader(chunk)); e != nil {
				t.Fatal(e)
			}
		}
	}
	published, e := g.s.Store.PreparePublishedFiles(g.ctx, g.descriptor)
	if e != nil {
		t.Fatal(e)
	}
	out, e := read(g.s, g.f.Workspace)
	if e != nil || !reflect.DeepEqual(out.Command, g.c) || out.ArtifactID != g.artifact ||
		out.NodeID != g.node || out.ResultSHA256 != g.reportSHA || out.CommandSHA256 != g.stop.CommandSHA256 ||
		out.StopSHA256 != acceptedAdapterDigest(acceptedAdapterRaw(t, g.stop)) ||
		out.ModelRepository != agent.ApprovedModel().Repository || out.ModelRevision != agent.ApprovedModel().Revision ||
		out.GlobalStep != g.descriptor.GlobalStep || out.Packages != wire.PinnedPackages() ||
		!reflect.DeepEqual(out.Files, g.descriptor.Files) {
		t.Fatal("accepted experiment did not retain exact bindings", e)
	}
	other, e := g.s.Workspaces.Create(g.ctx, g.a, "Second experiment workspace", g.f.User, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	refuse(read(g.s, other.ID))
	legacy, e := g.s.Export(g.ctx, g.a, g.f.Workspace, g.job, "manifest.json", uuid.NewString())
	if e != nil || legacy.Manifest == nil || !bytes.Contains(legacy.Manifest.Bytes, []byte("not_established_by_training_output")) {
		t.Fatal("existing adapter export changed", e)
	}

	asset := filepath.Join(g.root, published, "adapter_config.json")
	original, e := os.ReadFile(asset)
	if e != nil {
		t.Fatal(e)
	}
	changed := bytes.Clone(original)
	changed[0] ^= 1
	if e := os.WriteFile(asset, changed, 0600); e != nil {
		t.Fatal(e)
	}
	refuse(read(g.s, g.f.Workspace))
	if e := os.WriteFile(asset, original, 0600); e != nil {
		t.Fatal(e)
	}
	if retried, e := read(g.s, g.f.Workspace); e != nil || !reflect.DeepEqual(out, retried) {
		t.Fatal("repaired original did not yield the same experiment", e)
	}

	wrapped := func(store transaction.Store) *training.Service {
		account := *g.owner
		account.DB, account.Storage = nil, store
		s := *g.s
		s.DB, s.Identity, s.Workspaces = nil, &account, &workspaces.Service{Identity: &account}
		return &s
	}
	uncertain, e := read(wrapped(&trainingCreatorStore{Store: g.f.Store, at: 2, unknown: true}), g.f.Workspace)
	refuse(uncertain, e)
	if e.Error() != string(transaction.CommitUnknown) {
		t.Fatal("expected unknown commit", e)
	}
	revoked, e := read(wrapped(&trainingCreatorStore{Store: g.f.Store, at: 2, before: func() {
		if e := g.owner.Logout(g.ctx, g.token, "admin", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}}), g.f.Workspace)
	refuse(revoked, e)
	if !strings.HasPrefix(e.Error(), "AUTH_") {
		t.Fatal("expected final session refusal", e)
	}
}

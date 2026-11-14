//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/teststorage"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	lifecyclewire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// Real approved CPU tensor/checkpoint bytes and actual publication/acceptance/stop transactions are used.
// Production result bindings, base registry metadata, node/GPU/container observations are explicit test doubles.
// No preparation worker or physical NVIDIA computation runs in this admission fixture.
func acceptedTrainingForPreparation(t *testing.T) (scenario, agentwire.Lease) {
	t.Helper()
	f, l, checkpoint, files, receipt := actualCheckpointIntent(t)
	ctx := context.Background()
	uploadActualFiles(t, f, l, checkpoint, files)
	if _, e := f.s.FinishUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, checkpoint.ID); e != nil {
		t.Fatal(e)
	}
	output := os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT")
	raw, e := os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil {
		t.Fatal(e)
	}
	var result map[string]json.RawMessage
	if e = json.Unmarshal(raw, &result); e != nil {
		t.Fatal(e)
	}
	set := func(key string, value any) {
		encoded, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		result[key] = encoded
	}
	request, e := l.Training.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	set("bindings", wire.ExpectedBindings(request, agentwire.ApprovedModel().Repository))
	set("job_id", request.JobID)
	set("attempt_id", request.AttemptID)
	set("dataset_version_id", request.DatasetVersionID)
	set("dataset_manifest_sha256", request.DatasetManifestSHA256)
	set("memory_enforcement", "linux_cgroup_memory_cpu_fsize_wall")
	set("device_profile", wire.ProductionProfile)
	set("event_count", 3)
	var trainRows, validationRows int64
	if e = f.s.DB.QueryRow(ctx, "SELECT (manifest->>'train_rows')::bigint,(manifest->>'validation_rows')::bigint FROM dataset_versions WHERE id=$1", f.input.DatasetVersionID).Scan(&trainRows, &validationRows); e != nil {
		t.Fatal(e)
	}
	set("train_rows", trainRows)
	set("validation_rows", validationRows)
	set("checkpoints", []wire.Checkpoint{{GlobalStep: 1, Path: "checkpoint-1", ReceiptSHA256: digest(receipt)}})
	var assets []datawire.Asset
	if e = json.Unmarshal(result["files"], &assets); e != nil {
		t.Fatal(e)
	}
	for index := range assets {
		if assets[index].Path == "checkpoints/checkpoint-1/projectone-checkpoint.json" {
			assets[index].SizeBytes = int64(len(receipt))
			assets[index].SHA256 = digest(receipt)
		}
	}
	set("files", assets)
	raw, e = json.Marshal(result)
	if e != nil {
		t.Fatal(e)
	}
	artifact, e := f.s.BeginUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, "artifact", raw)
	if e != nil {
		t.Fatal(e)
	}
	adapterFiles := map[string][]byte{}
	for _, asset := range artifact.Files {
		adapterFiles[asset.Path], e = os.ReadFile(filepath.Join(output, "adapter", asset.Path))
		if e != nil {
			t.Fatal(e)
		}
	}
	uploadActualFiles(t, f, l, artifact, adapterFiles)
	if _, e = f.s.FinishUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, artifact.ID); e != nil {
		t.Fatal(e)
	}
	stop := stopFixture(t, f, l)
	stop.Proof = "owned_container_removed"
	container := strings.Repeat("a", 64)
	stop.ContainerID = &container
	sha := digest(raw)
	stop.OutcomeSHA256 = &sha
	if _, e = f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, stop, nil); e != nil {
		t.Fatal(e)
	}
	f.s.Identity.Config.Models.ArtifactRoot = f.s.Models.RootDirectory
	return f, l
}

func publishedPreparationHoldout(t *testing.T, f scenario) datasets.Version {
	t.Helper()
	ctx := context.Background()
	ds := f.s.Datasets
	workspace := f.input.WorkspaceID
	parent, e := ds.Create(ctx, f.portal, datasets.CreateRequest{WorkspaceID: workspace, Name: "Independent preparation evaluation", Origin: "Actual local synthetic routing source", License: "CC0 synthetic fixture", RightsAsserted: true, Reason: "Immutable preparation input"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var source bytes.Buffer
	for _, label := range []string{"account", "billing", "technical"} {
		raw, _ := json.Marshal(datawire.Record{SourceID: uuid.NewString(), Prompt: "Independent routing " + label + " " + uuid.NewString(), Completion: label})
		source.Write(raw)
		source.WriteByte('\n')
	}
	draft, e := ds.CreateDraft(ctx, f.portal, workspace, parent.ID, datasets.DraftRequest{SourceName: "heldout.jsonl", ExpectedBytes: int64(source.Len()), SourceSHA256: digest(source.Bytes()), Reason: "Actual native heldout publication"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ds.Upload(ctx, f.portal, workspace, parent.ID, draft.ID, draft.SourceSHA256, draft.ExpectedBytes, &source, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if worked, e := ds.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("actual heldout inspection", e)
	}
	draft, e = ds.GetDraft(ctx, f.portal, workspace, parent.ID, draft.ID)
	if e != nil {
		t.Fatal(e)
	}
	job, e := ds.Publish(ctx, f.portal, workspace, parent.ID, draft.ID, datasets.PublicationRequest{ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Actual immutable heldout split"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if worked, e := ds.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("actual heldout publication", e)
	}
	job, e = ds.GetJob(ctx, f.portal, workspace, parent.ID, job.ID)
	if e != nil || job.State != "succeeded" || job.VersionID == nil {
		t.Fatal("heldout completion", e)
	}
	version, e := ds.GetVersion(ctx, f.portal, workspace, parent.ID, *job.VersionID)
	if e != nil {
		t.Fatal(e)
	}
	return version
}

type preparationScenario struct {
	f       scenario
	l       agentwire.Lease
	service *modelpreparations.Service
	input   modelpreparations.CreateRequest
	root    string
	store   *modelpreparations.Store
	version datasets.Version
	node    agentwire.Enrolled
}

func newPreparationScenario(t *testing.T) preparationScenario {
	t.Helper()
	f, l := acceptedTrainingForPreparation(t)
	ctx := context.Background()
	manifest := os.Getenv("PROJECT_ONE_MODEL_LIFECYCLE_IMAGE_MANIFEST")
	raw, e := os.ReadFile(manifest)
	if e != nil {
		t.Fatal("actual lifecycle image receipt required", e)
	}
	var image struct {
		Status    string `json:"status"`
		Reference string `json:"image_reference"`
		ID        string `json:"image_id"`
		Source    string `json:"worker_source_sha256"`
	}
	if e = json.Unmarshal(raw, &image); e != nil || image.Status != "passed_actual_linux_amd64_lifecycle_image_cpu_import_only" {
		t.Fatal("passed actual image receipt required", e)
	}
	root := filepath.Join(t.TempDir(), "preparations")
	if e = os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	f.s.Identity.Config.ModelPreparation = config.ModelPreparationConfiguration{ArtifactRoot: root, ImageReference: image.Reference, ImageID: image.ID, WorkerSourceSHA256: image.Source}
	if e = f.s.Identity.Config.ModelPreparation.Validate(f.s.Identity.Config.Models.ArtifactRoot, f.s.Identity.Config.Datasets.ArtifactRoot, f.s.Identity.Config.Training.ArtifactRoot); e != nil {
		t.Fatal(e)
	}
	if e = modelpreparations.InitializeStore(root, f.admin.Me.Workspace.TenantID, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := modelpreparations.OpenStore(root, f.admin.Me.Workspace.TenantID, 4096, lifecyclewire.MaximumModelBytes, lifecyclewire.MaximumModelFiles)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { store.Close() })
	service, e := modelpreparations.New(f.s.DB, f.s.Identity, f.s.Workspaces, f.s.Datasets)
	if e != nil {
		t.Fatal(e)
	}
	service.Training = f.s
	service.Nodes = f.s.Nodes
	service.Models = f.s.Models
	service.Inference = f.s.Inference
	f.s.Nodes.Preparations = service
	service.Store = store
	f.s.Holdouts = service
	version := publishedPreparationHoldout(t, f)
	heldout, e := service.ReserveHoldout(ctx, f.portal, modelpreparations.HoldoutRequest{WorkspaceID: version.WorkspaceID, DatasetID: version.DatasetID, DatasetVersionID: version.ID, ExpectedManifestSHA256: version.ManifestSHA256, Name: "Preparation routing evaluation", Reason: "Actual independent immutable source"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	join, e := f.s.Nodes.MintForProfile(ctx, f.admin, f.input.WorkspaceID, "Synthetic preparation authority", agentwire.DockerModelLifecycleProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := agentwire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-preparation-node", LogicalCPUs: 4, MemoryTotalBytes: 17179869184, DiskPath: "/", DiskTotalBytes: 107374182400, DiskAvailableBytes: 53687091200, ObservedAt: time.Now().UTC(), AgentVersion: agentwire.AgentVersion}
	node, e := f.s.Nodes.Enroll(ctx, agentwire.Enrollment{JoinToken: join.Token, Handshake: agentwire.Handshake{ProtocolVersion: agentwire.ModelLifecycleVersion, ExecutionProfile: agentwire.DockerModelLifecycleProfile, Capabilities: agentwire.Capabilities(agentwire.DockerModelLifecycleProfile), Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	input := modelpreparations.CreateRequest{WorkspaceID: f.input.WorkspaceID, TrainingJobID: l.Training.Scope.JobID, HeldoutID: heldout.ID, NodeID: node.NodeID, GPUUUID: f.input.GPUUUID, Configuration: lifecyclewire.DefaultConfiguration(), Reason: "Actual durable source-bound preparation intent"}
	teststorage.Observe(t, "model", f.s.Models.RootDirectory)
	teststorage.Observe(t, "preparation", root)
	return preparationScenario{f: f, l: l, service: service, input: input, root: root, store: store, version: version, node: node}
}

func TestActualCpuModelPreparationAdmissionImmutableReplayCancellationAndRestart(t *testing.T) {
	g := newPreparationScenario(t)
	f, l, service, input, root, store := g.f, g.l, g.service, g.input, g.root, g.store
	ctx := context.Background()
	key := uuid.NewString()
	created, e := service.Create(ctx, f.portal, input, key, uuid.NewString())
	if e != nil || created.State != "queued" || created.TrainingAttemptID != l.Training.AttemptID || created.InputSHA256 == "" {
		t.Fatal("actual immutable preparation admission", e)
	}
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='held'") != 0 || count(t, f, "SELECT count(*) FROM node_tasks WHERE operation='model.prepare.v1'") != 0 {
		t.Fatal("queued preparation reserved GPU or fabricated a task")
	}
	configured := f.s.Identity.Config.ModelPreparation
	f.s.Identity.Config.ModelPreparation = config.ModelPreparationConfiguration{}
	replay, e := service.Create(ctx, f.portal, input, key, uuid.NewString())
	if e != nil || replay.ID != created.ID {
		t.Fatal("disabled new-job config broke exact replay", e)
	}
	_, e = service.Create(ctx, f.portal, input, uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_PREPARATION_UNAVAILABLE")
	f.s.Identity.Config.ModelPreparation = configured
	changed := input
	changed.Configuration.MinimumAccuracyBasisPoints = 3000
	_, e = service.Create(ctx, f.portal, changed, key, uuid.NewString())
	expected(t, e, "IDEMPOTENCY_CONFLICT")
	if _, e = f.s.DB.Exec(ctx, "UPDATE model_preparations SET configuration='{}' WHERE id=$1", created.ID); e == nil {
		t.Fatal("immutable preparation inputs modified")
	}
	if _, e = f.s.DB.Exec(ctx, "UPDATE model_preparations SET creation_audience='admin' WHERE id=$1", created.ID); e == nil {
		t.Fatal("creation replay audience modified")
	}
	policy := f.s.Identity.Config.Policy()
	policy.ModelLifecycle.MaxActivePerWorkspace = 1
	f.s.Identity.Config.Runtime = &policy
	_, e = service.Create(ctx, f.portal, input, uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_PREPARATION_LIMIT_REACHED")
	if _, e = f.s.DB.Exec(ctx, `CREATE FUNCTION reject_preparation_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_cancel_requested' THEN RAISE EXCEPTION 'SYNTHETIC_PREPARATION_AUDIT_FAILURE'; END IF;RETURN NEW;END;$$;CREATE TRIGGER reject_preparation_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_preparation_audit()`); e != nil {
		t.Fatal(e)
	}
	action := modelpreparations.ActionRequest{ExpectedRevision: created.Revision, Reason: "Cancel queued preparation without a physical owner"}
	if _, e = service.Cancel(ctx, f.portal, input.WorkspaceID, created.ID, action, uuid.NewString()); e == nil {
		t.Fatal("audit failure committed cancellation")
	}
	unchanged, e := service.Get(ctx, f.portal, input.WorkspaceID, created.ID)
	if e != nil || unchanged.State != "queued" || unchanged.Revision != created.Revision || unchanged.CancelRequestedAt != nil {
		t.Fatal("audit rollback changed intent", e)
	}
	if _, e = f.s.DB.Exec(ctx, "DROP TRIGGER reject_preparation_audit ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", input.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	_, e = service.Cancel(ctx, f.portal, input.WorkspaceID, created.ID, action, uuid.NewString())
	expected(t, e, "ACCESS_DENIED")
	if _, e = service.Get(ctx, f.portal, input.WorkspaceID, created.ID); e != nil {
		t.Fatal("current viewer read", e)
	}
	if _, e = f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='owner' WHERE workspace_id=$1 AND user_id=$2", input.WorkspaceID, f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	stale := action
	stale.ExpectedRevision++
	_, e = service.Cancel(ctx, f.portal, input.WorkspaceID, created.ID, stale, uuid.NewString())
	expected(t, e, "MODEL_PREPARATION_REVISION_CONFLICT")
	cancelled, e := service.Cancel(ctx, f.portal, input.WorkspaceID, created.ID, action, uuid.NewString())
	if e != nil || cancelled.State != "cancelled" || cancelled.FinishedAt == nil {
		t.Fatal("actual queued cancellation", e)
	}
	second, e := service.Create(ctx, f.portal, changed, uuid.NewString(), uuid.NewString())
	if e != nil || second.ID == created.ID || second.ConfigurationSHA256 == created.ConfigurationSHA256 || second.InputSHA256 != created.InputSHA256 {
		t.Fatal("separate threshold intent", e)
	}
	page, e := service.List(ctx, f.portal, input.WorkspaceID, url.Values{"limit": {"1"}})
	if e != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal("actual keyset page", e)
	}
	if _, e = service.List(ctx, f.admin, input.WorkspaceID, url.Values{"cursor": {*page.NextCursor}}); e == nil {
		t.Fatal("foreign-audience cursor accepted")
	}
	if e = store.Close(); e != nil {
		t.Fatal(e)
	}
	opened, e := modelpreparations.OpenStore(root, f.admin.Me.Workspace.TenantID, 4096, lifecyclewire.MaximumModelBytes, lifecyclewire.MaximumModelFiles)
	if e != nil {
		t.Fatal(e)
	}
	defer opened.Close()
	restarted, e := modelpreparations.New(f.s.DB, f.s.Identity, f.s.Workspaces, f.s.Datasets)
	if e != nil {
		t.Fatal(e)
	}
	restarted.Store = opened
	restarted.Training = f.s
	restarted.Nodes = f.s.Nodes
	restarted.Models = f.s.Models
	persisted, e := restarted.Get(ctx, f.portal, input.WorkspaceID, second.ID)
	if e != nil || persisted.InputSHA256 != second.InputSHA256 || persisted.State != "queued" {
		t.Fatal("durable restart", e)
	}
	if count(t, f, "SELECT count(*) FROM model_preparations") != 2 || count(t, f, "SELECT count(*) FROM audit_events WHERE action='model.preparation_created'") != 2 || count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='held'") != 0 {
		t.Fatal("failed admission/audit had an effect or restart reserved GPU")
	}
}

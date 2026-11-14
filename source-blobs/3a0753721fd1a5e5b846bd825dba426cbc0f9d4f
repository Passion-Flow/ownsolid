package browser_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func browserDigest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// configureTrainingBrowser uses actual CPython data processing and recorded approved CPU files.
// Node/GPU/container reports and production bindings are explicit test doubles; no Docker/CUDA compute is claimed.
func configureTrainingBrowser(t *testing.T, h *transport.Handler, manifest map[string]any) func() {
	t.Helper()
	ctx := context.Background()
	s := h.Service
	output := os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT")
	base := os.Getenv("PROJECT_ONE_APPROVED_MODEL_DIRECTORY")
	if output == "" || base == "" {
		t.Fatal("actual approved CPU files required for training browser fixture")
	}
	admin := fixture.Login(t, s, "admin@example.invalid", manifest["admin_password"].(string), "admin")
	actor := fixture.Login(t, s, "admin@example.invalid", manifest["admin_password"].(string), "portal")
	workspace := actor.Me.Workspace.ID
	tenant := actor.Me.Workspace.TenantID
	modelRoot := t.TempDir()
	os.Chmod(modelRoot, 0700)
	model := uuid.NewString()
	if e := os.MkdirAll(filepath.Join(modelRoot, "objects", model), 0700); e != nil {
		t.Fatal(e)
	}
	configBytes, e := os.ReadFile(filepath.Join(base, "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(modelRoot, "objects", model, "config.json"), configBytes, 0444); e != nil {
		t.Fatal(e)
	}
	// Immutable installed model assets have an exact mode, independent of the private harness umask.
	if e = os.Chmod(filepath.Join(modelRoot, "objects", model, "config.json"), 0444); e != nil {
		t.Fatal(e)
	}
	h.Models.Service.RootDirectory = modelRoot
	s.Config.Models.ArtifactRoot = modelRoot
	catalog, _ := json.Marshal(agentwire.ApprovedModel())
	if _, e = s.DB.Exec(ctx, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Approved CPU file fixture base','qwen3-0.6b',$4,$5,'ready',$6)", model, tenant, workspace, agentwire.ModelCatalogDigest(), catalog, admin.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	dataset, e := h.Datasets.Service.Create(ctx, actor, datasets.CreateRequest{WorkspaceID: workspace, Name: "Synthetic training browser corpus", Origin: "Local synthetic records for UI contract verification", License: "CC0 synthetic fixture", RightsAsserted: true, Reason: "Isolated training browser fixture"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var source bytes.Buffer
	for i := 0; i < 10; i++ {
		b, _ := json.Marshal(datawire.Record{SourceID: uuid.NewString(), Prompt: "Synthetic browser training prompt " + uuid.NewString(), Completion: "Synthetic browser completion"})
		source.Write(b)
		source.WriteByte('\n')
	}
	draft, e := h.Datasets.Service.CreateDraft(ctx, actor, workspace, dataset.ID, datasets.DraftRequest{SourceName: "synthetic-training.jsonl", ExpectedBytes: int64(source.Len()), SourceSHA256: browserDigest(source.Bytes()), Reason: "Real native processor fixture input"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.Datasets.Service.Upload(ctx, actor, workspace, dataset.ID, draft.ID, draft.SourceSHA256, draft.ExpectedBytes, &source, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if worked, e := h.Datasets.Service.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("actual browser dataset inspection", e)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		draft, e = h.Datasets.Service.GetDraft(ctx, actor, workspace, dataset.ID, draft.ID)
		if e != nil {
			t.Fatal(e)
		}
		if draft.State == "editing" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native browser dataset import did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	publication, e := h.Datasets.Service.Publish(ctx, actor, workspace, dataset.ID, draft.ID, datasets.PublicationRequest{ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Actual immutable native browser split"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if worked, e := h.Datasets.Service.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("actual browser immutable split", e)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		publication, e = h.Datasets.Service.GetJob(ctx, actor, workspace, dataset.ID, publication.ID)
		if e != nil {
			t.Fatal(e)
		}
		if publication.State == "succeeded" && publication.VersionID != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native browser dataset publication did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	version, e := h.Datasets.Service.GetVersion(ctx, actor, workspace, dataset.ID, *publication.VersionID)
	if e != nil {
		t.Fatal(e)
	}
	join, e := h.Nodes.Service.MintForProfile(ctx, admin, workspace, "Explicit synthetic training reporter", agentwire.DockerAIProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := agentwire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-training-browser", LogicalCPUs: 4, MemoryTotalBytes: 17179869184, DiskPath: "/", DiskTotalBytes: 107374182400, DiskAvailableBytes: 53687091200, ObservedAt: time.Now().UTC(), AgentVersion: agentwire.AgentVersion}
	node, e := h.Nodes.Service.Enroll(ctx, agentwire.Enrollment{JoinToken: join.Token, Handshake: agentwire.Handshake{ProtocolVersion: agentwire.AIVersion, ExecutionProfile: agentwire.DockerAIProfile, Capabilities: agentwire.Capabilities(agentwire.DockerAIProfile), Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	gpu := "GPU-" + uuid.NewString()
	observation := agentwire.InferenceObservation{GPUs: []agentwire.NVIDIAGPU{{UUID: gpu, Name: "Explicit synthetic NVIDIA browser observation", DriverVersion: "580.1.0", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8.0, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}}, ObservedAt: time.Now().UTC()}
	obs, _ := json.Marshal(observation)
	if _, e = s.DB.Exec(ctx, "INSERT INTO inference_nodes(tenant_id,workspace_id,node_id,observation) VALUES($1,$2,$3,$4)", tenant, workspace, node.NodeID, obs); e != nil {
		t.Fatal(e)
	}
	trainingRoot := t.TempDir()
	os.Chmod(trainingRoot, 0700)
	s.Config.Training = config.TrainingConfiguration{ArtifactRoot: trainingRoot, ImageReference: "projectone/training@sha256:" + strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("b", 64), WorkerSourceSHA256: strings.Repeat("c", 64)}
	policy := s.Config.Policy()
	policy.Training.PollMilliseconds = 100
	s.Config.Runtime = &policy
	if e = training.InitializeStore(trainingRoot, tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := training.OpenStore(trainingRoot, tenant, 4096, wire.MaximumCheckpointBytes, wire.MaximumCheckpointFiles)
	if e != nil {
		t.Fatal(e)
	}
	h.Training.Store = store
	releaseFile := filepath.Join(manifest["output"].(string), "release-discovery-fixture")
	manifest["training_workspace"] = workspace
	manifest["training_release_file"] = releaseFile
	// A genuine queued discovery task makes DispatchNext persist NODE_BUSY with no GPU lease.
	// Only its normal authenticated claim/result releases this task; no physical reservation is evicted.
	if _, e = s.DB.Exec(ctx, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at) VALUES($1,$2,$3,$4,'collect_facts.v1','pending',$5,$6,clock_timestamp()+interval '5 minutes')`, uuid.NewString(), tenant, workspace, node.NodeID, uuid.NewString(), admin.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	manifest["training_node_id"] = node.NodeID
	manifest["model_id"] = model
	manifest["dataset_id"] = dataset.ID
	manifest["dataset_version_id"] = version.ID
	manifest["training_gpu_uuid"] = gpu
	manifest["training_fixture_scope"] = "Actual Go/React/PostgreSQL/native data processing and recorded approved CPU files. Explicit synthetic GPU/container/node production reports, no Docker/CUDA execution."
	worker, stop := context.WithCancel(context.Background())
	var group sync.WaitGroup
	group.Add(2)
	go func() { defer group.Done(); h.Training.Run(worker) }()
	go func() {
		defer group.Done()
		for worker.Err() == nil {
			_, _ = s.DB.Exec(worker, "UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp() WHERE id=$1", node.NodeID)
			_, _ = s.DB.Exec(worker, "UPDATE inference_nodes SET received_at=clock_timestamp() WHERE node_id=$1", node.NodeID)
			if _, e := os.Stat(releaseFile); os.IsNotExist(e) {
				select {
				case <-worker.Done():
					return
				case <-time.After(100 * time.Millisecond):
					continue
				}
			} else if e != nil {
				t.Error("training release fixture read")
				return
			}
			claim, e := h.Nodes.Service.Claim(worker, node.NodeID, node.Credential, uuid.NewString())
			if e != nil {
				if worker.Err() != nil {
					return
				}
				// Dispatch may publish after the training probe but before the generic probe.
				// Retry a fresh authority-bound claim; the generic path issued no lease.
				if e.Error() == "TRAINING_AUTHORITY_REQUIRED" {
					select {
					case <-worker.Done():
						return
					case <-time.After(100 * time.Millisecond):
						continue
					}
				}
				t.Errorf("synthetic training browser claim failed: %v", e)
				return
			}
			if claim.Lease != nil {
				if claim.Lease.Operation == agentwire.CollectFacts {
					facts.ObservedAt = time.Now().UTC()
					if _, e = h.Nodes.Service.Complete(worker, node.NodeID, node.Credential, claim.Lease.TaskID, agentwire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Facts: &facts}, uuid.NewString()); e != nil {
						t.Error("discovery fixture completion", e)
						return
					}
					continue
				}
				if claim.Lease.Training == nil {
					t.Error("unexpected non-training browser task")
					return
				}
				if e = reportRecordedTraining(worker, h, node.NodeID, node.Credential, *claim.Lease, output, version.Manifest.TrainRows, version.Manifest.ValidationRows); e != nil {
					if worker.Err() != nil {
						return
					}
					t.Errorf("synthetic recorded training report failed: %s", e.Error())
					return
				}
			}
			select {
			case <-worker.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}()
	return func() { stop(); group.Wait(); store.Close() }
}

func reportRecordedTraining(ctx context.Context, h *transport.Handler, node, credential string, l agentwire.Lease, output string, trainRows, validationRows int64) error {
	command := *l.Training
	request, e := command.ContainerRequest()
	if e != nil {
		return e
	}
	bindings := wire.ExpectedBindings(request, agentwire.ApprovedModel().Repository)
	event := func(sequence, step int64, kind string, extra map[string]any) []byte {
		v := map[string]any{"sequence": sequence, "job_id": command.Scope.JobID, "attempt_id": command.AttemptID, "global_step": step, "kind": kind}
		for key, value := range extra {
			v[key] = value
		}
		raw, _ := json.Marshal(v)
		return raw
	}
	phase := "training"
	phaseStep := int64(0)
	if command.Resume != nil {
		phase = "finalizing_checkpoint"
		phaseStep = command.Resume.GlobalStep
	}
	values := [][]byte{event(1, 0, "phase", map[string]any{"phase": "loading_model"}), event(2, phaseStep, "phase", map[string]any{"phase": phase})}
	if _, e = h.Training.RecordEvents(ctx, node, credential, l.TaskID, l.LeaseToken, values); e != nil {
		return e
	}
	if command.Resume == nil {
		raw, e := os.ReadFile(filepath.Join(output, "checkpoints/checkpoint-1/projectone-checkpoint.json"))
		if e != nil {
			return e
		}
		var receipt wire.CheckpointReceipt
		if e = json.Unmarshal(raw, &receipt); e != nil {
			return e
		}
		receipt.Bindings = bindings
		raw, _ = json.Marshal(receipt)
		if _, e = h.Training.RecordEvents(ctx, node, credential, l.TaskID, l.LeaseToken, [][]byte{event(3, 1, "metric", map[string]any{"values": map[string]any{"loss": 1.0}}), event(4, 1, "checkpoint", map[string]any{"path": "checkpoint-1", "receipt_sha256": browserDigest(raw)})}); e != nil {
			return e
		}
		if e = sendRecordedUpload(ctx, h, node, credential, l, "checkpoint", raw, filepath.Join(output, "checkpoints/checkpoint-1")); e != nil {
			return e
		}
		for ctx.Err() == nil {
			if _, e = h.Nodes.Service.Renew(ctx, node, credential, l.TaskID, l.LeaseToken); e != nil {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		return reportSyntheticStop(ctx, h, node, credential, l, nil)
	}
	// Read real private accepted optimizer bytes through the current resumed machine range before returning a recorded adapter.
	for _, asset := range command.Resume.Files {
		if asset.Path == "optimizer.pt" {
			download, e := h.Training.InputFile(ctx, node, credential, l.TaskID, wire.InputFileRequest{LeaseToken: l.LeaseToken, Kind: "checkpoint", Asset: asset.Path, Length: min(wire.TransferChunkBytes, asset.SizeBytes)})
			if e != nil {
				return e
			}
			_, e = io.Copy(io.Discard, io.NewSectionReader(download.File, download.Offset, download.Size))
			download.File.Close()
			if e != nil {
				return e
			}
		}
	}
	raw, e := os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil {
		return e
	}
	var result map[string]json.RawMessage
	if e = json.Unmarshal(raw, &result); e != nil {
		return e
	}
	set := func(key string, v any) { result[key], _ = json.Marshal(v) }
	set("job_id", command.Scope.JobID)
	set("attempt_id", command.AttemptID)
	set("bindings", bindings)
	set("dataset_version_id", command.DatasetVersionID)
	set("dataset_manifest_sha256", command.DatasetManifestSHA256)
	set("memory_enforcement", "linux_cgroup_memory_cpu_fsize_wall")
	set("device_profile", wire.ProductionProfile)
	set("event_count", 2)
	set("train_rows", trainRows)
	set("validation_rows", validationRows)
	set("resume_from_step", command.Resume.GlobalStep)
	set("parent_checkpoint_receipt_sha256", command.Resume.ReceiptSHA256)
	set("loss_kind", "saved_checkpoint_step_loss")
	set("metrics", []wire.Metric{})
	set("checkpoints", []wire.Checkpoint{})
	var assets []datawire.Asset
	json.Unmarshal(result["files"], &assets)
	filtered := []datawire.Asset{}
	for _, asset := range assets {
		if strings.HasPrefix(asset.Path, "adapter/") {
			filtered = append(filtered, asset)
		}
	}
	set("files", filtered)
	raw, _ = json.Marshal(result)
	if e = sendRecordedUpload(ctx, h, node, credential, l, "artifact", raw, filepath.Join(output, "adapter")); e != nil {
		return e
	}
	sha := browserDigest(raw)
	return reportSyntheticStop(ctx, h, node, credential, l, &sha)
}

func sendRecordedUpload(ctx context.Context, h *transport.Handler, node, credential string, l agentwire.Lease, kind string, metadata []byte, directory string) error {
	u, e := h.Training.BeginUpload(ctx, node, credential, l.TaskID, l.LeaseToken, kind, metadata)
	if e != nil {
		return e
	}
	for _, asset := range u.Files {
		raw := metadata
		if asset.Path != "projectone-checkpoint.json" {
			raw, e = os.ReadFile(filepath.Join(directory, asset.Path))
			if e != nil {
				return e
			}
		}
		for offset := int64(0); offset < int64(len(raw)); offset += wire.TransferChunkBytes {
			part := raw[offset:min(offset+wire.TransferChunkBytes, int64(len(raw)))]
			if _, e = h.Training.UploadChunk(ctx, node, credential, l.TaskID, l.LeaseToken, u.ID, wire.ChunkRequest{Asset: asset.Path, Offset: offset, Length: int64(len(part)), SHA256: browserDigest(part)}, bytes.NewReader(part)); e != nil {
				return e
			}
		}
	}
	_, e = h.Training.FinishUpload(ctx, node, credential, l.TaskID, l.LeaseToken, u.ID)
	return e
}

func reportSyntheticStop(ctx context.Context, h *transport.Handler, node, credential string, l agentwire.Lease, outcome *string) error {
	sha, e := l.Training.Digest()
	if e != nil {
		return e
	}
	container := strings.Repeat("e", 64)
	receipt := wire.StopReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), JournalID: uuid.NewString(), NodeID: node, TaskID: l.TaskID, Scope: l.Training.Scope, AttemptID: l.Training.AttemptID, AttemptNumber: l.Training.AttemptNumber, CommandSHA256: sha, ImageID: *l.Training.Recipe.Identity.ImageID, GPUUUID: l.Training.GPUUUID, ContainerName: wire.ContainerName(l.Training.AttemptID), ContainerID: &container, Proof: "owned_container_removed", OwnedContainerAbsent: true, OutcomeSHA256: outcome, CompletedAt: time.Now().UTC()}
	_, e = h.Training.RecordStop(ctx, node, credential, l.TaskID, l.LeaseToken, receipt, nil)
	return e
}

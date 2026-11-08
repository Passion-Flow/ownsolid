package training_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
)

type publicationCostFixture struct {
	*costRun
	base, task      string
	lease           agentwire.Lease
	checkpoint      []byte
	checkpointFiles map[string][]byte
	adapterFiles    map[string][]byte
}

func newPublicationCostFixture(t *testing.T, peers int) publicationCostFixture {
	t.Helper()
	var shape training.BaseShape
	run, base := newCostRun(t, peers, func(f *scenario) {
		f.input.Configuration.Rank = 4
		f.input.Configuration.MaximumSteps = 1
		f.input.Configuration.CheckpointSteps = 1
		config, err := os.ReadFile("../../../../../tests/fixtures/models/qwen3-config.json")
		if err != nil {
			t.Fatal(err)
		}
		approved, ok := agentwire.ModelFileForName("config.json")
		if !ok || digest(config) != approved.SHA256 || json.Unmarshal(config, &shape) != nil || shape.Validate() != nil {
			t.Fatal("publication fixture must match the frozen approved base shape")
		}
		root := filepath.Join(t.TempDir(), "model-root")
		directory := filepath.Join(root, "objects", f.input.ModelID)
		if err = os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "config.json")
		if err = os.WriteFile(path, config, 0444); err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(path, 0444); err != nil {
			t.Fatal(err)
		}
		f.s.Identity.Config.Models.ArtifactRoot = root
		f.s.Models.RootDirectory = root
	})
	// Seeding peer history can outlast the fixture node's liveness window.
	node, err := run.f.s.Nodes.Get(t.Context(), run.f.admin, "", run.f.input.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	node.Facts.ObservedAt = time.Now().UTC()
	run.client.Call("POST", base+"/heartbeat", agentwire.Handshake{
		ProtocolVersion: node.ProtocolVersion, ExecutionProfile: node.ExecutionProfile,
		Capabilities: node.Capabilities, Facts: node.Facts,
	}, 200, "")
	if err = run.flush(); err != nil {
		t.Fatal(err)
	}
	dispatched(t, run.f)
	l := run.claim(base, 0)
	if l.Training == nil {
		t.Fatal("publication fixture received no training lease")
	}
	task := "/tasks/" + l.TaskID
	run.client.Call("POST", base+task+"/"+agentwire.TaskAcknowledgementsPath,
		agentwire.TaskAcknowledgementRequest{Sequence: l.Sequence, LeaseToken: l.LeaseToken}, 200, "")
	request, err := l.Training.ContainerRequest()
	if err != nil {
		t.Fatal(err)
	}
	model := agentwire.ApprovedModel()
	adapter, err := os.ReadFile("../../../../../api/training/v1/adapter-template.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = wire.ValidateAdapterConfiguration(adapter, request.Configuration, model.Repository); err != nil {
		t.Fatal(err)
	}
	// These finite F32 matrices and opaque checkpoint objects are synthetic test
	// bytes. The control plane still verifies every published byte and contract.
	tensors := syntheticAdapterTensors(t, shape, request.Configuration, "")
	checkpointFiles := map[string][]byte{
		"adapter_model.safetensors": tensors,
		"adapter_config.json":       adapter,
		"optimizer.pt":              []byte("synthetic optimizer"),
		"scheduler.pt":              []byte("synthetic scheduler"),
		"rng_state.pth":             []byte("synthetic RNG"),
		"trainer_state.json": []byte(`{
 "best_global_step":null,"best_metric":null,"best_model_checkpoint":null,
 "epoch":1,"eval_steps":1,"global_step":1,"is_hyper_param_search":false,
 "is_local_process_zero":true,"is_world_process_zero":true,"log_history":[{"step":1,"loss":1}],
 "logging_steps":1,"max_steps":1,"num_input_tokens_seen":0,"num_train_epochs":1,
 "save_steps":1,"stateful_callbacks":{"TrainerControl":{"args":{"should_epoch_stop":false,
 "should_evaluate":false,"should_log":false,"should_save":false,"should_training_stop":false},
 "attributes":{}}},"total_flos":0,"train_batch_size":1,"trial_name":null,"trial_params":null
}`),
	}
	checkpoint := publicationJSON(t, wire.CheckpointReceipt{
		SchemaVersion: 1, GlobalStep: 1, Bindings: wire.ExpectedBindings(request, model.Repository),
		Files: publicationAssets("", checkpointFiles),
	})
	checkpointFiles["projectone-checkpoint.json"] = checkpoint
	events := prelude(*l.Training)
	events = append(events, eventFixture(*l.Training, 3, 1, "checkpoint", map[string]any{
		"path": "checkpoint-1", "receipt_sha256": digest(checkpoint),
	}))
	batch := make([]json.RawMessage, 0, len(events))
	for _, event := range events {
		batch = append(batch, event)
	}
	run.client.Call("POST", base+task+"/training-events",
		map[string]any{"lease_token": l.LeaseToken, "events": batch}, 200, "TrainingEventBatchResponse")
	return publicationCostFixture{
		costRun: run, base: base, task: task, lease: l,
		checkpoint: checkpoint, checkpointFiles: checkpointFiles,
		adapterFiles: map[string][]byte{
			"adapter_model.safetensors": tensors, "adapter_config.json": adapter, "README.md": wire.AdapterReadme(model.Repository),
		},
	}
}

func publicationJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func publicationAssets(prefix string, files map[string][]byte) []datawire.Asset {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	assets := make([]datawire.Asset, 0, len(names))
	for _, name := range names {
		raw := files[name]
		assets = append(assets, datawire.Asset{Path: prefix + name, SizeBytes: int64(len(raw)), SHA256: digest(raw)})
	}
	return assets
}

func (p publicationCostFixture) artifact() []byte {
	p.t.Helper()
	command := p.lease.Training
	request, err := command.ContainerRequest()
	if err != nil {
		p.t.Fatal(err)
	}
	var train, validation int64
	err = p.f.s.DB.QueryRow(p.t.Context(), "SELECT (manifest->>'train_rows')::bigint,(manifest->>'validation_rows')::bigint FROM dataset_versions WHERE id=$1", command.DatasetVersionID).Scan(&train, &validation)
	if err != nil {
		p.t.Fatal(err)
	}
	files := append(publicationAssets("adapter/", p.adapterFiles), publicationAssets("checkpoints/checkpoint-1/", p.checkpointFiles)...)
	return publicationJSON(p.t, map[string]any{
		"schema_version": 1, "worker_version": wire.WorkerVersion, "status": "trained",
		"job_id": command.Scope.JobID, "attempt_id": command.AttemptID,
		"dataset_version_id": command.DatasetVersionID, "dataset_manifest_sha256": command.DatasetManifestSHA256,
		"memory_enforcement": "linux_cgroup_memory_cpu_fsize_wall", "device_profile": wire.ProductionProfile,
		"event_count": 3, "global_step": 1, "training_loss": 1, "loss_kind": "trainer_reported_training_loss",
		"resume_from_step": 0, "parent_checkpoint_receipt_sha256": nil, "adapter_changed": true, "base_parameters_frozen": true,
		"bindings": wire.ExpectedBindings(request, agentwire.ApprovedModel().Repository), "metrics": []wire.Metric{},
		"checkpoints": []wire.Checkpoint{{GlobalStep: 1, Path: "checkpoint-1", ReceiptSHA256: digest(p.checkpoint)}},
		"files":       files, "train_rows": train, "validation_rows": validation,
	})
}

func (p publicationCostFixture) stage(kind string, metadata []byte, files map[string][]byte, measureName string) string {
	p.t.Helper()
	path := p.task + "/training-uploads"
	var id string
	begin := func() {
		p.t.Helper()
		reply := p.client.Call("POST", p.base+path, map[string]any{
			"lease_token": p.lease.LeaseToken, "kind": kind, "metadata": json.RawMessage(metadata),
		}, 201, "TrainingUploadResponse")
		id, _ = reply["id"].(string)
		if !datawire.ValidID(id) || reply["attempt_id"] != p.lease.Training.AttemptID {
			p.t.Fatal("upload did not identify the exact training attempt")
		}
	}
	if measureName == "" {
		begin()
	} else {
		p.measure(measureName, agentwire.CallUpload, begin)
	}
	for _, asset := range publicationAssets("", files) {
		raw := files[asset.Path]
		for offset := int64(0); offset < asset.SizeBytes; offset += wire.TransferChunkBytes {
			chunk := raw[offset:min(offset+wire.TransferChunkBytes, asset.SizeBytes)]
			p.post(path+"/"+id+"/chunks", map[string]string{
				"Content-Type": "application/octet-stream", "X-Training-Lease-Token": p.lease.LeaseToken,
				"X-Training-Asset": asset.Path, "X-Training-SHA256": digest(chunk), "X-Training-Offset": strconv.FormatInt(offset, 10),
			}, chunk, 200)
		}
	}
	return id
}

func (p publicationCostFixture) finish(id, kind string, duplicate bool) {
	p.t.Helper()
	result := p.client.Call("POST", p.base+p.task+"/training-uploads/"+id+"/finish",
		map[string]any{"lease_token": p.lease.LeaseToken}, 200, "TrainingOutputAcceptanceResponse")
	if result["upload_id"] != id || result["reference_id"] != id || result["kind"] != kind ||
		result["global_step"] != float64(1) || result["duplicate"] != duplicate {
		p.t.Fatalf("unexpected %s acceptance: %+v", kind, result)
	}
}

func measurePublicationFleet(t *testing.T, peers int) map[string]exchangeCost {
	t.Helper()
	p := newPublicationCostFixture(t, peers)
	checkpoint := p.stage("checkpoint", p.checkpoint, p.checkpointFiles, "")
	p.measure("checkpoint finish", agentwire.CallUpload, func() { p.finish(checkpoint, "checkpoint", false) })
	p.measure("checkpoint finish replay", agentwire.CallUpload, func() { p.finish(checkpoint, "checkpoint", true) })
	metadata := p.artifact()
	artifact := p.stage("artifact", metadata, p.adapterFiles, "artifact upload begin")
	p.measure("artifact finish", agentwire.CallUpload, func() { p.finish(artifact, "artifact", false) })
	p.measure("artifact finish replay", agentwire.CallUpload, func() { p.finish(artifact, "artifact", true) })

	command := p.lease.Training
	if count(t, p.f, "SELECT count(*) FROM training_checkpoints WHERE id=$1 AND attempt_id=$2 AND receipt_sha256=$3", checkpoint, command.AttemptID, digest(p.checkpoint)) != 1 ||
		count(t, p.f, "SELECT count(*) FROM training_artifacts WHERE id=$1 AND attempt_id=$2 AND manifest_sha256=$3", artifact, command.AttemptID, digest(metadata)) != 1 ||
		count(t, p.f, "SELECT count(*) FROM training_uploads WHERE id IN ($1,$2) AND state='accepted' AND active_chunk_id IS NULL", checkpoint, artifact) != 2 ||
		count(t, p.f, `SELECT count(*) FROM audit_events WHERE target_id=$1 AND actor_user_id=$2 AND audience='portal'
 AND action IN ('training.checkpoint_accepted','training.artifact_accepted')`, command.Scope.JobID, p.f.portal.Me.User.ID) != 2 ||
		count(t, p.f, "SELECT count(*) FROM training_jobs WHERE id=$1 AND state='running' AND accepted_artifact_id IS NULL", command.Scope.JobID) != 1 ||
		count(t, p.f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='held'", command.Scope.JobID) != 1 {
		t.Fatal("publication lost a reference, duplicated audit or changed physical ownership")
	}

	receipt := stopFixture(t, p.f, p.lease)
	container, result := digest([]byte("synthetic stopped container")), digest(metadata)
	receipt.Proof, receipt.ContainerID, receipt.OutcomeSHA256 = "owned_container_removed", &container, &result
	p.measure("artifact stop", agentwire.CallStop, func() {
		reply := p.client.Call("POST", p.base+p.task+"/training-stops",
			wire.StopSubmission{LeaseToken: p.lease.LeaseToken, Receipt: receipt, Outcome: json.RawMessage("null")}, 200, "TrainingStopAcceptanceResponse")
		if reply["state"] != "succeeded" || reply["duplicate"] != false || reply["gpu_released"] != true ||
			reply["attempt_id"] != command.AttemptID || reply["receipt_id"] != receipt.ReceiptID {
			t.Fatalf("artifact did not settle through exact stop: %+v", reply)
		}
	})
	if count(t, p.f, "SELECT count(*) FROM training_jobs WHERE id=$1 AND state='succeeded' AND accepted_artifact_id=$2", command.Scope.JobID, artifact) != 1 ||
		count(t, p.f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='released'", command.Scope.JobID) != 1 {
		t.Fatal("exact stop did not commit the published artifact and physical release")
	}
	return p.measured
}

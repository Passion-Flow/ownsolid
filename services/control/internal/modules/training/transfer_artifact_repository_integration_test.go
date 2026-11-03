//go:build repository_integration && training_artifact_integration

package training_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// Actual retained CPU tensor/optimizer/RNG files are unchanged. Scope, physical
// NVIDIA/device reports and row-count envelopes are explicitly synthetic. These
// tests establish SQL/file workflows, never training on the synthetic partitions.
func trainingRuntimeCheckpoint(t *testing.T) (trainingRuntimeStory, training.Upload, map[string][]byte, []byte) {
	t.Helper()
	return trainingRuntimeCheckpointUsing(t, trainingRuntimeSetup)
}
func trainingRuntimeCheckpointUsing(t *testing.T, setup func(*testing.T, *wire.Configuration, bool) trainingRuntimeStory) (trainingRuntimeStory, training.Upload, map[string][]byte, []byte) {
	t.Helper()
	output, base := os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT"), os.Getenv("PROJECT_ONE_APPROVED_MODEL_DIRECTORY")
	if !filepath.IsAbs(output) || !filepath.IsAbs(base) {
		t.Fatal("EXPLICIT_RETAINED_CPU_OUTPUT_AND_MODEL_REQUIRED")
	}
	raw, e := os.ReadFile(filepath.Join(filepath.Dir(output), "request.json"))
	if e != nil {
		t.Fatal(e)
	}
	request, e := wire.DecodeRequest(raw)
	if e != nil {
		t.Fatal(e)
	}
	g := setup(t, &request.Configuration, true)
	modelRoot := g.owner.Config.Models.ArtifactRoot
	if e = os.MkdirAll(filepath.Join(modelRoot, "objects", g.input.ModelID), 0700); e != nil {
		t.Fatal(e)
	}
	configuration, e := os.ReadFile(filepath.Join(base, "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(modelRoot, "objects", g.input.ModelID, "config.json"), configuration, 0444); e != nil {
		t.Fatal(e)
	}
	// Immutable installed model assets have an exact mode, independent of the private harness umask.
	if e = os.Chmod(filepath.Join(modelRoot, "objects", g.input.ModelID, "config.json"), 0444); e != nil {
		t.Fatal(e)
	}
	g.s.Models.RootDirectory = modelRoot
	directory := filepath.Join(output, "checkpoints", "checkpoint-1")
	raw, e = os.ReadFile(filepath.Join(directory, "projectone-checkpoint.json"))
	if e != nil {
		t.Fatal(e)
	}
	var receipt wire.CheckpointReceipt
	if e = json.Unmarshal(raw, &receipt); e != nil {
		t.Fatal(e)
	}
	production, e := g.lease.Training.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	receipt.Bindings = wire.ExpectedBindings(production, agent.ApprovedModel().Repository)
	files := map[string][]byte{}
	for _, asset := range receipt.Files {
		b, e := os.ReadFile(filepath.Join(directory, asset.Path))
		if e != nil || int64(len(b)) != asset.SizeBytes || acceptedAdapterDigest(b) != asset.SHA256 {
			t.Fatal("retained CPU checkpoint file changed", asset.Path, e)
		}
		files[asset.Path] = b
	}
	raw = acceptedAdapterRaw(t, receipt)
	files["projectone-checkpoint.json"] = raw
	events := append(prelude(*g.lease.Training), eventFixture(*g.lease.Training, 3, 1, "checkpoint", map[string]any{"path": "checkpoint-1", "receipt_sha256": acceptedAdapterDigest(raw)}))
	if _, e = g.s.RecordEvents(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, events); e != nil {
		t.Fatal(e)
	}
	upload, e := g.s.BeginUpload(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, "checkpoint", raw)
	if e != nil {
		t.Fatal(e)
	}
	return g, upload, files, raw
}
func (g trainingRuntimeStory) upload(t *testing.T, u training.Upload, files map[string][]byte) {
	t.Helper()
	for _, asset := range u.Files {
		raw, ok := files[asset.Path]
		if !ok || int64(len(raw)) != asset.SizeBytes || acceptedAdapterDigest(raw) != asset.SHA256 {
			t.Fatal("actual upload inventory", asset.Path)
		}
		for offset := int64(0); offset < int64(len(raw)); offset += wire.TransferChunkBytes {
			part := raw[offset:min(offset+wire.TransferChunkBytes, int64(len(raw)))]
			input := training.ChunkRequest{Asset: asset.Path, Offset: offset, Length: int64(len(part)), SHA256: acceptedAdapterDigest(part)}
			if _, e := g.s.UploadChunk(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, u.ID, input, bytes.NewReader(part)); e != nil {
				t.Fatal("actual paired CPU file chunk", asset.Path, e)
			}
		}
	}
}
func (g trainingRuntimeStory) finish(id string) (training.OutputAcceptance, error) {
	return g.s.FinishUpload(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, id)
}
func (g trainingRuntimeStory) artifact(t *testing.T, checkpoint []byte) (training.Upload, map[string][]byte, []byte) {
	t.Helper()
	output := os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT")
	raw, e := os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil {
		t.Fatal(e)
	}
	var result map[string]json.RawMessage
	if e = json.Unmarshal(raw, &result); e != nil {
		t.Fatal(e)
	}
	set := func(key string, v any) { result[key] = acceptedAdapterRaw(t, v) }
	request, e := g.lease.Training.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	set("bindings", wire.ExpectedBindings(request, agent.ApprovedModel().Repository))
	set("job_id", request.JobID)
	set("attempt_id", request.AttemptID)
	set("dataset_version_id", request.DatasetVersionID)
	set("dataset_manifest_sha256", request.DatasetManifestSHA256)
	set("memory_enforcement", "linux_cgroup_memory_cpu_fsize_wall")
	set("device_profile", wire.ProductionProfile)
	set("train_rows", 1)
	set("validation_rows", 1)
	sequence := int64(3)
	if request.Configuration.MaximumSteps > 1 {
		sequence++
		event := eventFixture(*g.lease.Training, sequence, request.Configuration.MaximumSteps, "metric", map[string]any{"values": map[string]any{"loss": 1.0}})
		if _, e = g.s.RecordEvent(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, event); e != nil {
			t.Fatal(e)
		}
	}
	set("event_count", sequence)
	var checkpoints []wire.Checkpoint
	if e = json.Unmarshal(result["checkpoints"], &checkpoints); e != nil {
		t.Fatal(e)
	}
	for i := range checkpoints {
		if checkpoints[i].GlobalStep == 1 {
			checkpoints[i].ReceiptSHA256 = acceptedAdapterDigest(checkpoint)
		}
	}
	set("checkpoints", checkpoints)
	var assets []datawire.Asset
	if e = json.Unmarshal(result["files"], &assets); e != nil {
		t.Fatal(e)
	}
	for i := range assets {
		if assets[i].Path == "checkpoints/checkpoint-1/projectone-checkpoint.json" {
			assets[i].SizeBytes = int64(len(checkpoint))
			assets[i].SHA256 = acceptedAdapterDigest(checkpoint)
		}
	}
	set("files", assets)
	raw = acceptedAdapterRaw(t, result)
	u, e := g.s.BeginUpload(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, "artifact", raw)
	if e != nil {
		t.Fatal("synthetic envelope with real retained CPU adapter", e)
	}
	files := map[string][]byte{}
	for _, asset := range u.Files {
		files[asset.Path], e = os.ReadFile(filepath.Join(output, "adapter", asset.Path))
		if e != nil {
			t.Fatal(e)
		}
	}
	return u, files, raw
}
func TestActualPairedTrainingOutputTransfer(t *testing.T) {
	t.Run("chunk_rollback_ticket_expiry_and_unknown_publication_zero", func(t *testing.T) {
		g, u, files, _ := trainingRuntimeCheckpoint(t)
		asset := u.Files[0]
		part := files[asset.Path][:min(wire.TransferChunkBytes, asset.SizeBytes)]
		input := training.ChunkRequest{Asset: asset.Path, Length: int64(len(part)), SHA256: acceptedAdapterDigest(part)}
		drop := g.auditFailure(t, "training.upload_chunk_accepted")
		got, e := g.s.UploadChunk(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, u.ID, input, bytes.NewReader(part))
		if e == nil || !reflect.DeepEqual(got, training.ChunkAcceptance{}) {
			t.Fatal("chunk audit projected", e)
		}
		drop()
		if g.count(t, "SELECT count(*) FROM training_upload_chunks WHERE upload_id=$1", "SELECT COUNT(*) FROM training_upload_chunks WHERE upload_id=?", u.ID) != 0 {
			t.Fatal("chunk survived failed audit")
		}
		x := g.wrapped(&trainingCreatorStore{Store: g.f.Store, at: 4, before: func() {
			g.exec(t, "UPDATE training_uploads SET active_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE training_uploads SET active_expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", u.ID)
		}})
		got, e = x.s.UploadChunk(x.ctx, x.input.NodeID, x.credential, x.lease.TaskID, x.lease.LeaseToken, u.ID, input, bytes.NewReader(part))
		if e == nil || e.Error() != "TRAINING_UPLOAD_TICKET_STALE" || !reflect.DeepEqual(got, training.ChunkAcceptance{}) {
			t.Fatal("expired ticket accepted", e)
		}
		g.upload(t, u, files)
		drop = g.auditFailure(t, "training.checkpoint_accepted")
		accepted, e := g.finish(u.ID)
		if e == nil || !reflect.DeepEqual(accepted, training.OutputAcceptance{}) {
			t.Fatal("checkpoint audit projected", e)
		}
		drop()
		if g.count(t, "SELECT count(*) FROM training_checkpoints WHERE id=$1", "SELECT COUNT(*) FROM training_checkpoints WHERE id=?", u.ID) != 0 {
			t.Fatal("failed audit retained reference")
		}
		x = g.wrapped(&trainingCreatorStore{Store: g.f.Store, at: 6, unknown: true})
		accepted, e = x.finish(u.ID)
		if e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(accepted, training.OutputAcceptance{}) {
			t.Fatal("unknown publication projected", e)
		}
		accepted, e = g.finish(u.ID)
		if e != nil || !accepted.Duplicate || accepted.ReferenceID != u.ID {
			t.Fatal("explicit publication reconciliation", e)
		}
	})
	t.Run("accepted_checkpoint_integrity_stopped_recovery_and_private_resume", func(t *testing.T) {
		g, u, files, _ := trainingRuntimeCheckpoint(t)
		g.upload(t, u, files)
		if _, e := g.finish(u.ID); e != nil {
			t.Fatal(e)
		}
		if e := g.s.VerifyStored(g.ctx); e != nil {
			t.Fatal(e)
		}
		receipt := g.stopReceipt(t)
		receipt.Proof = "owned_container_removed"
		container := strings.Repeat("c", 64)
		receipt.ContainerID = &container
		if _, e := g.s.RecordStop(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil); e != nil {
			t.Fatal(e)
		}
		current, e := g.s.Get(g.ctx, g.a, g.f.Workspace, g.job.ID)
		if e != nil {
			t.Fatal(e)
		}
		recovery := training.RecoveryRequest{ExpectedRevision: current.Revision, Reason: "Actual immutable CPU checkpoint SQL recovery", NodeID: g.input.NodeID, Mode: "checkpoint", CheckpointID: &u.ID}
		optimizer := filepath.Join(g.owner.Config.Training.ArtifactRoot, "checkpoints", g.job.ID, g.lease.Training.AttemptID, u.ID, "optimizer.pt")
		corrupt := append([]byte{}, files["optimizer.pt"]...)
		corrupt[0] ^= 1
		if e = os.WriteFile(optimizer, corrupt, 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = g.s.Recover(g.ctx, g.a, g.f.Workspace, g.job.ID, recovery, uuid.NewString(), uuid.NewString()); e == nil {
			t.Fatal("corrupt checkpoint recovered")
		}
		if e = os.WriteFile(optimizer, files["optimizer.pt"], 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = g.s.Recover(g.ctx, g.a, g.f.Workspace, g.job.ID, recovery, uuid.NewString(), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if worked, e := g.s.DispatchNext(g.ctx); e != nil || !worked {
			t.Fatal(e)
		}
		g.claim(t)
		if g.lease.Training.Resume == nil || g.lease.Training.Resume.CheckpointID != u.ID {
			t.Fatal("checkpoint missing from next command")
		}
		request := wire.InputFileRequest{LeaseToken: g.lease.LeaseToken, Kind: "checkpoint", Asset: "optimizer.pt", Length: min(wire.TransferChunkBytes, int64(len(files["optimizer.pt"])))}
		d, e := g.s.InputFile(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, request)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(io.NewSectionReader(d.File, d.Offset, d.Size))
		d.File.Close()
		if e != nil || !bytes.Equal(raw, files["optimizer.pt"][:request.Length]) || d.SHA256 != acceptedAdapterDigest(raw) {
			t.Fatal("checkpoint range", e)
		}
	})
	t.Run("actual_adapter_requires_positive_stop_then_export_and_snapshot", func(t *testing.T) {
		g, checkpoint, files, receipt := trainingRuntimeCheckpoint(t)
		g.upload(t, checkpoint, files)
		if _, e := g.finish(checkpoint.ID); e != nil {
			t.Fatal(e)
		}
		artifact, adapterFiles, outcome := g.artifact(t, receipt)
		g.upload(t, artifact, adapterFiles)
		if _, e := g.finish(artifact.ID); e != nil {
			t.Fatal(e)
		}
		current, e := g.s.Get(g.ctx, g.a, g.f.Workspace, g.job.ID)
		if e != nil || current.State == "succeeded" {
			t.Fatal("publication invented physical stop", e)
		}
		stop := g.stopReceipt(t)
		stop.Proof = "owned_container_removed"
		container := strings.Repeat("d", 64)
		stop.ContainerID = &container
		sha := acceptedAdapterDigest(outcome)
		stop.OutcomeSHA256 = &sha
		accepted, e := g.s.RecordStop(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, stop, outcome)
		if e != nil || accepted.State != "succeeded" || !accepted.GPUReleased {
			t.Fatal("verified artifact with exact stop", e)
		}
		exported, e := g.s.Export(g.ctx, g.a, g.f.Workspace, g.job.ID, "manifest.json", uuid.NewString())
		if e != nil || exported.Manifest == nil || !bytes.Contains(exported.Manifest.Bytes, []byte("not_established_by_training_output")) {
			t.Fatal("manufacturer provenance", e)
		}
		x := g.wrapped(&trainingCreatorStore{Store: g.f.Store, at: 2, unknown: true})
		exported, e = x.s.Export(x.ctx, x.a, x.f.Workspace, x.job.ID, "adapter_model.safetensors", uuid.NewString())
		if e == nil || e.Error() != string(transaction.CommitUnknown) || exported.File != nil || exported.Manifest != nil {
			t.Fatal("unknown export leaked file", e)
		}
		destination := filepath.Join(t.TempDir(), "retained-snapshot")
		if e = g.s.ExportFiles(g.ctx, destination); e != nil {
			t.Fatal("paired accepted-files snapshot", e)
		}
		if e = g.s.AuditStoreOperation(g.ctx, "export", "Protected stopped runtime snapshot audit"); e != nil {
			t.Fatal(e)
		}
		if e = g.s.Store.Close(); e != nil {
			t.Fatal(e)
		}
		g.s.Store = nil
		restored := filepath.Join(t.TempDir(), "restored")
		if e = os.Mkdir(restored, 0700); e != nil {
			t.Fatal(e)
		}
		if e = g.s.ImportFiles(g.ctx, destination, restored); e != nil {
			t.Fatal("paired snapshot import/readback", e)
		}
		store, e := training.OpenStore(restored, g.f.Tenant, 4096, wire.MaximumCheckpointBytes, wire.MaximumCheckpointFiles)
		if e != nil {
			t.Fatal(e)
		}
		defer store.Close()
		g.s.Store = store
		if e = g.s.VerifyStored(g.ctx); e != nil {
			t.Fatal("restored accepted bytes", e)
		}
	})
}

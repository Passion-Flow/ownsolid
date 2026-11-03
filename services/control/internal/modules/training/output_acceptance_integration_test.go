//go:build training_artifact_integration

package training_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// Physical adapter/checkpoint files are actual approved-model CPU outputs. GPU node reports,
// scope/binding envelopes and production execution claims below are explicitly synthetic test inputs.
// This exercises real control/storage transactions and does not attest NVIDIA computation.
func actualCheckpointIntent(t *testing.T) (scenario, agentwire.Lease, training.Upload, map[string][]byte, []byte) {
	return actualCheckpointWithFault(t, nil)
}

func TestActualCpuAcceptedCheckpointRecoveryFreezesNewAttemptAndPrivateRanges(t *testing.T) {
	f, l, u, files, _ := actualCheckpointIntent(t)
	ctx := context.Background()
	uploadActualFiles(t, f, l, u, files)
	if _, e := f.s.FinishUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, u.ID); e != nil {
		t.Fatal(e)
	}
	if e := f.s.VerifyStored(ctx); e != nil {
		t.Fatal("actual accepted checkpoint readiness", e)
	}
	policy := f.s.Identity.Config.Policy()
	policy.Training.Resources.MaximumOutputBytes = 1048576
	f.s.Identity.Config.Runtime = &policy
	f.s.Identity.Config.Models.ArtifactRoot = f.s.Models.RootDirectory
	if e := f.s.Store.Close(); e != nil {
		t.Fatal(e)
	}
	f.s.Store = nil
	if e := f.s.OpenConfiguredStore(ctx); e != nil {
		t.Fatal("new-job budget invalidated retained accepted checkpoint", e)
	}
	reopened := f.s.Store
	t.Cleanup(func() { reopened.Close() })
	// Restore the manufactured recipe policy before exercising explicit compatible recovery below.
	policy.Training.Resources.MaximumOutputBytes = l.Training.Recipe.Resources.MaximumOutputBytes
	f.s.Identity.Config.Runtime = &policy
	if _, e := f.s.DB.Exec(ctx, "UPDATE training_uploads SET state='uploading' WHERE id=$1", u.ID); e == nil {
		t.Fatal("accepted upload regressed to mutable state")
	}
	stop := stopFixture(t, f, l)
	stop.Proof = "owned_container_removed"
	container := strings.Repeat("e", 64)
	stop.ContainerID = &container
	if _, e := f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, stop, nil); e != nil {
		t.Fatal(e)
	}
	j, e := f.s.Get(ctx, f.portal, f.input.WorkspaceID, l.Training.Scope.JobID)
	if e != nil {
		t.Fatal(e)
	}
	input := training.RecoveryRequest{ExpectedRevision: j.Revision, Reason: "Explicit recovery from accepted actual CPU bytes with synthetic production bindings", NodeID: f.input.NodeID, Mode: "checkpoint", CheckpointID: &u.ID}
	key := uuid.NewString()
	optimizerPath := filepath.Join(f.s.Identity.Config.Training.ArtifactRoot, "checkpoints", j.ID, l.Training.AttemptID, u.ID, "optimizer.pt")
	corrupt := append([]byte{}, files["optimizer.pt"]...)
	corrupt[0] ^= 1
	if e = os.WriteFile(optimizerPath, corrupt, 0600); e != nil {
		t.Fatal(e)
	}
	if e = f.s.VerifyStored(ctx); e == nil {
		t.Fatal("corrupt accepted optimizer passed readiness")
	}
	_, e = f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, input, key, uuid.NewString())
	expected(t, e, "TRAINING_STORAGE_DATA_INVALID")
	if count(t, f, "SELECT count(*) FROM training_recoveries") != 0 {
		t.Fatal("corrupt checkpoint silently fell back to restart")
	}
	if e = os.WriteFile(optimizerPath, files["optimizer.pt"], 0600); e != nil {
		t.Fatal(e)
	}
	queued, e := f.s.Recover(ctx, f.portal, f.input.WorkspaceID, j.ID, input, key, uuid.NewString())
	if e != nil || queued.PendingRecoveryID == nil || queued.State != "queued" {
		t.Fatal("checkpoint recovery intent", e)
	}
	_, e = f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, stop, nil)
	expected(t, e, "TRAINING_ATTEMPT_STALE")
	if worked, e := f.s.DispatchNext(ctx); e != nil || !worked {
		t.Fatal("checkpoint recovery dispatch", e)
	}
	next := claimed(t, f)
	if next.Training.Resume == nil || next.Training.Resume.CheckpointID != u.ID || next.Training.Resume.GlobalStep != 1 || next.Training.AttemptNumber != 2 {
		t.Fatal("accepted checkpoint not frozen into new attempt")
	}
	asset := datawire.Asset{}
	for _, a := range next.Training.Resume.Files {
		if a.Path == "optimizer.pt" {
			asset = a
		}
	}
	rangeRequest := wire.InputFileRequest{LeaseToken: next.LeaseToken, Kind: "checkpoint", Asset: asset.Path, Length: min(wire.TransferChunkBytes, asset.SizeBytes)}
	download, e := f.s.InputFile(ctx, f.input.NodeID, f.credential, next.TaskID, rangeRequest)
	if e != nil {
		t.Fatal("actual accepted private optimizer range", e)
	}
	raw, e := io.ReadAll(io.NewSectionReader(download.File, download.Offset, download.Size))
	download.File.Close()
	if e != nil || !bytes.Equal(raw, files[asset.Path][:rangeRequest.Length]) || digest(raw) != download.SHA256 {
		t.Fatal("accepted checkpoint transfer bytes/hash", e)
	}
	rangeRequest.LeaseToken = l.LeaseToken
	_, e = f.s.InputFile(ctx, f.input.NodeID, f.credential, l.TaskID, rangeRequest)
	expected(t, e, "AGENT_LEASE_STALE")
	_, e = f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, stop, nil)
	expected(t, e, "TRAINING_ATTEMPT_STALE")
	if count(t, f, "SELECT revision FROM gpu_leases WHERE state='held'") != 2 {
		t.Fatal("old stop affected new physical owner")
	}
	if _, e = f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	rangeRequest.LeaseToken = next.LeaseToken
	_, e = f.s.InputFile(ctx, f.input.NodeID, f.credential, next.TaskID, rangeRequest)
	expected(t, e, "ACCESS_DENIED")
}
func actualCheckpointWithFault(t *testing.T, mutate func(map[string][]byte)) (scenario, agentwire.Lease, training.Upload, map[string][]byte, []byte) {
	t.Helper()
	output := os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT")
	base := os.Getenv("PROJECT_ONE_APPROVED_MODEL_DIRECTORY")
	if output == "" || base == "" {
		t.Fatal("actual approved CPU output required")
	}
	raw, e := os.ReadFile(filepath.Join(filepath.Dir(output), "request.json"))
	if e != nil {
		t.Fatal(e)
	}
	request, e := wire.DecodeRequest(raw)
	if e != nil {
		t.Fatal(e)
	}
	f := setup(t)
	attachStore(t, f)
	f.input.Configuration = request.Configuration
	f.input.Configuration.Device = wire.ProductionProfile
	dispatched(t, f)
	l := claimed(t, f)
	root := filepath.Join(t.TempDir(), "model-root")
	if e = os.MkdirAll(filepath.Join(root, "objects", f.input.ModelID), 0700); e != nil {
		t.Fatal(e)
	}
	config, e := os.ReadFile(filepath.Join(base, "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "objects", f.input.ModelID, "config.json"), config, 0444); e != nil {
		t.Fatal(e)
	}
	// Immutable installed model assets have an exact mode, independent of the private harness umask.
	if e = os.Chmod(filepath.Join(root, "objects", f.input.ModelID, "config.json"), 0444); e != nil {
		t.Fatal(e)
	}
	f.s.Models.RootDirectory = root
	directory := filepath.Join(output, "checkpoints", "checkpoint-1")
	raw, e = os.ReadFile(filepath.Join(directory, "projectone-checkpoint.json"))
	if e != nil {
		t.Fatal(e)
	}
	var receipt wire.CheckpointReceipt
	if e = json.Unmarshal(raw, &receipt); e != nil {
		t.Fatal(e)
	}
	production, e := l.Training.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	receipt.Bindings = wire.ExpectedBindings(production, agentwire.ApprovedModel().Repository)
	files := map[string][]byte{}
	for _, asset := range receipt.Files {
		b, e := os.ReadFile(filepath.Join(directory, asset.Path))
		if e != nil || int64(len(b)) != asset.SizeBytes || digest(b) != asset.SHA256 {
			t.Fatal("actual CPU checkpoint file changed", e)
		}
		files[asset.Path] = b
	}
	if mutate != nil {
		mutate(files)
		for i := range receipt.Files {
			b := files[receipt.Files[i].Path]
			receipt.Files[i].SizeBytes = int64(len(b))
			receipt.Files[i].SHA256 = digest(b)
		}
	}
	raw, e = json.Marshal(receipt)
	if e != nil {
		t.Fatal(e)
	}
	files["projectone-checkpoint.json"] = raw
	announce(t, f, l, raw)
	u, e := f.s.BeginUpload(context.Background(), f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, "checkpoint", raw)
	if e != nil {
		t.Fatal(e)
	}
	return f, l, u, files, raw
}

func TestActualCpuCheckpointRehashedSemanticFaultsCannotBecomeAcceptedReferences(t *testing.T) {
	faults := map[string]func(map[string][]byte){
		"trainer_null_integer": func(files map[string][]byte) {
			var v map[string]any
			json.Unmarshal(files["trainer_state.json"], &v)
			v["num_input_tokens_seen"] = nil
			files["trainer_state.json"], _ = json.Marshal(v)
		},
		"private_path_readme": func(files map[string][]byte) {
			files["README.md"] = []byte("Base model: /private/synthetic-test-path\n")
		},
		"trainer_step": func(files map[string][]byte) {
			var v map[string]any
			json.Unmarshal(files["trainer_state.json"], &v)
			v["global_step"] = 0
			files["trainer_state.json"], _ = json.Marshal(v)
		},
		"trainer_duplicate": func(files map[string][]byte) {
			files["trainer_state.json"] = append([]byte(`{"global_step":1,`), files["trainer_state.json"][1:]...)
		},
		"trainer_foreign_callback": func(files map[string][]byte) {
			var v map[string]any
			json.Unmarshal(files["trainer_state.json"], &v)
			v["stateful_callbacks"] = map[string]any{"UntrustedCallback": map[string]any{}}
			files["trainer_state.json"], _ = json.Marshal(v)
		},
		"adapter_custom_code": func(files map[string][]byte) {
			var v map[string]any
			json.Unmarshal(files["adapter_config.json"], &v)
			v["auto_mapping"] = map[string]any{"parent_library": "untrusted"}
			files["adapter_config.json"], _ = json.Marshal(v)
		},
		"tensor_nonfinite": func(files map[string][]byte) {
			raw := append([]byte{}, files["adapter_model.safetensors"]...)
			copy(raw[len(raw)-4:], []byte{0, 0, 128, 127})
			files["adapter_model.safetensors"] = raw
		},
	}
	for name, fault := range faults {
		t.Run(name, func(t *testing.T) {
			f, l, u, files, _ := actualCheckpointWithFault(t, fault)
			uploadActualFiles(t, f, l, u, files)
			_, e := f.s.FinishUpload(context.Background(), f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, u.ID)
			if e == nil {
				t.Fatal("rehashed semantic corruption accepted")
			}
			if count(t, f, "SELECT count(*) FROM training_checkpoints") != 0 || count(t, f, "SELECT count(*) FROM training_artifacts") != 0 || count(t, f, "SELECT count(*) FROM training_uploads WHERE state='accepted'") != 0 {
				t.Fatal("invalid semantic files acquired an accepted reference")
			}
		})
	}
}

func uploadActualFiles(t *testing.T, f scenario, l agentwire.Lease, u training.Upload, files map[string][]byte) {
	t.Helper()
	for _, asset := range u.Files {
		raw, ok := files[asset.Path]
		if !ok || int64(len(raw)) != asset.SizeBytes || digest(raw) != asset.SHA256 {
			t.Fatal("test bytes differ from exact upload inventory", asset.Path)
		}
		for offset := int64(0); offset < int64(len(raw)); offset += wire.TransferChunkBytes {
			part := raw[offset:min(offset+wire.TransferChunkBytes, int64(len(raw)))]
			input := training.ChunkRequest{Asset: asset.Path, Offset: offset, Length: int64(len(part)), SHA256: digest(part)}
			if _, e := f.s.UploadChunk(context.Background(), f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, u.ID, input, bytes.NewReader(part)); e != nil {
				diagnostic := training.StorageDiagnostic(e)
				t.Fatalf("actual CPU bytes upload asset=%s code=%s storage_operation=%s errno=%d", asset.Path, e.Error(), diagnostic.Operation, diagnostic.Errno)
			}
		}
	}
}

func TestActualCpuCheckpointAcceptanceAuditRollbackRetryReplayAndArtifactStillNeedsStop(t *testing.T) {
	f, l, u, files, receipt := actualCheckpointIntent(t)
	ctx := context.Background()
	finish := func(id string) (training.OutputAcceptance, error) {
		return f.s.FinishUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, id)
	}
	_, e := finish(u.ID)
	expected(t, e, "TRAINING_UPLOAD_INCOMPLETE")
	uploadActualFiles(t, f, l, u, files)
	if _, e = f.s.DB.Exec(ctx, `CREATE FUNCTION reject_training_acceptance() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='training.checkpoint_accepted' THEN RAISE EXCEPTION 'SYNTHETIC_ACCEPTANCE_AUDIT_FAILURE'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_training_acceptance BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_training_acceptance()`); e != nil {
		t.Fatal(e)
	}
	if _, e = finish(u.ID); e == nil {
		t.Fatal("acceptance audit failure reported success")
	}
	if count(t, f, "SELECT count(*) FROM training_checkpoints") != 0 || count(t, f, "SELECT count(*) FROM training_uploads WHERE state='accepted'") != 0 {
		t.Fatal("reference survived audit rollback")
	}
	if _, e = os.Stat(filepath.Join(f.s.Identity.Config.Training.ArtifactRoot, "checkpoints", l.Training.Scope.JobID, l.Training.AttemptID, u.ID, "publication.json")); e != nil {
		t.Fatal("unreferenced exact files not retained", e)
	}
	if _, e = f.s.DB.Exec(ctx, "DROP TRIGGER reject_training_acceptance ON audit_events"); e != nil {
		t.Fatal(e)
	}
	first, e := finish(u.ID)
	if e != nil || first.ReferenceID != u.ID || first.Duplicate {
		t.Fatal("actual checkpoint acceptance retry", e)
	}
	again, e := finish(u.ID)
	if e != nil || !again.Duplicate {
		t.Fatal("accepted checkpoint replay", e)
	}
	if count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.checkpoint_accepted'") != 1 {
		t.Fatal("checkpoint acceptance audit duplicated")
	}
	// Adapt only metadata to this isolated business scope, retaining original CPU tensor/optimizer bytes.
	output := os.Getenv("PROJECT_ONE_ACTUAL_TRAINING_OUTPUT")
	raw, e := os.ReadFile(filepath.Join(output, "result.json"))
	if e != nil {
		t.Fatal(e)
	}
	var result map[string]json.RawMessage
	if e = json.Unmarshal(raw, &result); e != nil {
		t.Fatal(e)
	}
	set := func(key string, v any) {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		result[key] = b
	}
	production, e := l.Training.ContainerRequest()
	if e != nil {
		t.Fatal(e)
	}
	set("bindings", wire.ExpectedBindings(production, agentwire.ApprovedModel().Repository))
	set("job_id", production.JobID)
	set("attempt_id", production.AttemptID)
	set("dataset_version_id", production.DatasetVersionID)
	set("dataset_manifest_sha256", production.DatasetManifestSHA256)
	set("memory_enforcement", "linux_cgroup_memory_cpu_fsize_wall")
	set("device_profile", wire.ProductionProfile)
	set("event_count", 3)
	var train, validation int64
	if e = f.s.DB.QueryRow(ctx, "SELECT (manifest->>'train_rows')::bigint,(manifest->>'validation_rows')::bigint FROM dataset_versions WHERE id=$1", f.input.DatasetVersionID).Scan(&train, &validation); e != nil {
		t.Fatal(e)
	}
	set("train_rows", train)
	set("validation_rows", validation)
	set("checkpoints", []wire.Checkpoint{{GlobalStep: 1, Path: "checkpoint-1", ReceiptSHA256: digest(receipt)}})
	var assets []datawire.Asset
	if e = json.Unmarshal(result["files"], &assets); e != nil {
		t.Fatal(e)
	}
	for i := range assets {
		if assets[i].Path == "checkpoints/checkpoint-1/projectone-checkpoint.json" {
			assets[i].SizeBytes = int64(len(receipt))
			assets[i].SHA256 = digest(receipt)
		}
	}
	set("files", assets)
	raw, e = json.Marshal(result)
	if e != nil {
		t.Fatal(e)
	}
	artifact, e := f.s.BeginUpload(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, "artifact", raw)
	if e != nil {
		t.Fatal("scoped synthetic production envelope", e)
	}
	adapterFiles := map[string][]byte{}
	for _, asset := range artifact.Files {
		adapterFiles[asset.Path], e = os.ReadFile(filepath.Join(output, "adapter", asset.Path))
		if e != nil {
			t.Fatal(e)
		}
	}
	uploadActualFiles(t, f, l, artifact, adapterFiles)
	accepted, e := finish(artifact.ID)
	if e != nil || accepted.ReferenceID != artifact.ID || accepted.Kind != "artifact" {
		t.Fatal("actual finite adapter bytes acceptance", e)
	}
	if count(t, f, "SELECT count(*) FROM training_artifacts") != 1 || count(t, f, "SELECT count(*) FROM training_jobs WHERE state='succeeded'") != 0 || count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='held'") != 1 {
		t.Fatal("output acceptance falsely completed job/released physical GPU")
	}
	t.Run("M10_adapter_source_requires_positive_physical_stop", func(t *testing.T) {
		_, e := f.s.AcceptedAdapterSnapshot(ctx, f.portal, f.input.WorkspaceID, l.Training.Scope.JobID)
		expected(t, e, "TRAINING_RESULT_UNAVAILABLE")
	})
	if _, e = f.s.DB.Exec(ctx, "UPDATE training_checkpoints SET file_inventory='{}' WHERE id=$1", u.ID); e == nil {
		t.Fatal("accepted checkpoint inventory mutable")
	}
	if _, e = f.s.DB.Exec(ctx, "UPDATE training_artifacts SET manifest='{}' WHERE id=$1", artifact.ID); e == nil {
		t.Fatal("accepted artifact manifest mutable")
	}
	stop := stopFixture(t, f, l)
	stop.Proof = "owned_container_removed"
	container := strings.Repeat("a", 64)
	stop.ContainerID = &container
	resultSHA := digest(raw)
	stop.OutcomeSHA256 = &resultSHA
	assetPath := filepath.Join(f.s.Identity.Config.Training.ArtifactRoot, "artifacts", l.Training.Scope.JobID, l.Training.AttemptID, artifact.ID, "adapter_config.json")
	original, e := os.ReadFile(assetPath)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(assetPath, append(append([]byte{}, original...), '\n'), 0600); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.RecordStop(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, stop, nil)
	expected(t, e, "TRAINING_STORAGE_PATH_INVALID")
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='held'") != 1 {
		t.Fatal("corrupt accepted bytes released physical ownership")
	}
	if e = os.WriteFile(assetPath, original, 0600); e != nil {
		t.Fatal(e)
	}
	handler := transport.New(f.s.Identity)
	handler.Training.Store = f.s.Store
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	client := httpfixture.New(t, server, "admin", httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json"))
	client.Headers = map[string]string{"Authorization": "Bearer " + f.credential}
	path := "/agent/v1/nodes/" + f.input.NodeID + "/tasks/" + l.TaskID + "/training-stops"
	body := wire.StopSubmission{LeaseToken: l.LeaseToken, Receipt: stop, Outcome: json.RawMessage("null")}
	response := client.Call("POST", path, body, 200, "TrainingStopAcceptanceResponse")
	if response["state"] != "succeeded" || response["duplicate"] != false || count(t, f, "SELECT count(*) FROM training_jobs WHERE state='succeeded' AND accepted_artifact_id IS NOT NULL") != 1 || count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='released'") != 1 {
		t.Fatal("verified artifact and exact stop not committed together")
	}
	client.Call("POST", path, body, 200, "TrainingStopAcceptanceResponse")
	t.Run("M10_actual_adapter_source_current_scope_integrity_and_private_assets", func(t *testing.T) {
		source, e := f.s.AcceptedAdapterSnapshot(ctx, f.portal, f.input.WorkspaceID, l.Training.Scope.JobID)
		if e != nil || source.Descriptor.UploadID != artifact.ID || source.Command.AttemptID != l.Training.AttemptID || source.IdentitySHA256() == "" {
			t.Fatal("accepted source", e)
		}
		if e = f.s.VerifyAcceptedAdapter(ctx, source); e != nil {
			t.Fatal("actual published source hash verification", e)
		}
		for _, name := range []string{"adapter_model.safetensors", "adapter_config.json", "README.md"} {
			file, asset, e := f.s.OpenAcceptedAdapterAsset(ctx, source, name)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := io.ReadAll(file)
			file.Close()
			if e != nil || asset.SHA256 != digest(raw) || !bytes.Equal(raw, adapterFiles[name]) {
				t.Fatal("actual scoped source bytes", name, e)
			}
		}
		_, _, e = f.s.OpenAcceptedAdapterAsset(ctx, source, "optimizer.pt")
		expected(t, e, "TRAINING_NOT_FOUND")
		checkpoint := source
		checkpoint.Descriptor.Kind = "checkpoint"
		expected(t, f.s.VerifyAcceptedAdapter(ctx, checkpoint), "TRAINING_DATA_INVALID")
		_, e = f.s.AcceptedAdapterSnapshot(ctx, f.portal, f.input.WorkspaceID, uuid.NewString())
		expected(t, e, "TRAINING_NOT_FOUND")
		if e = os.WriteFile(assetPath, append(append([]byte{}, original...), '\n'), 0600); e != nil {
			t.Fatal(e)
		}
		if e = f.s.VerifyAcceptedAdapter(ctx, source); e == nil {
			t.Fatal("corrupt accepted source passed independent check")
		}
		if e = os.WriteFile(assetPath, original, 0600); e != nil {
			t.Fatal(e)
		}
		fresh, e := f.s.AcceptedAdapterSnapshot(ctx, f.portal, f.input.WorkspaceID, l.Training.Scope.JobID)
		if e != nil || fresh.IdentitySHA256() != source.IdentitySHA256() || f.s.VerifyAcceptedAdapter(ctx, fresh) != nil {
			t.Fatal("repaired immutable source identity", e)
		}
	})
	human := httpfixture.New(t, server, "portal", httpfixture.Schemas(t, "../../../../../api/openapi/v1/training/openapi.json"))
	human.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": f.password}, 200, "")
	humanBase := "/api/v1/workspaces/" + f.input.WorkspaceID + "/training/jobs/" + l.Training.Scope.JobID + "/files/"
	for _, asset := range []string{"adapter_model.safetensors", "adapter_config.json", "README.md", "manifest.json"} {
		r, e := http.NewRequest("GET", httpfixture.PortalOrigin+humanBase+asset, nil)
		if e != nil {
			t.Fatal(e)
		}
		// Obtain a real browser cookie from the actual account service, never forge a session hash.
		_, session, _, e := f.s.Identity.Login(ctx, "admin@example.invalid", f.password, "portal", "", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		r.AddCookie(&http.Cookie{Name: "__Host-portal_session", Value: session})
		response, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil || response.StatusCode != 200 || response.Header.Get("X-Artifact-SHA256") != digest(b) || response.Header.Get("Content-Length") != strconv.Itoa(len(b)) || !strings.Contains(response.Header.Get("Content-Disposition"), "training-"+l.Training.Scope.JobID+"-"+asset) {
			t.Fatal("actual safe accepted export headers/bytes", asset, e, response.StatusCode)
		}
		if asset == "manifest.json" {
			var m training.AdapterProvenance
			if e = json.Unmarshal(b, &m); e != nil || m.ArtifactID != artifact.ID || m.TargetQualification != "not_established_by_training_output" || strings.Contains(string(b), f.s.Identity.Config.Training.ArtifactRoot) {
				t.Fatal("unsafe or falsely qualified manufacturer provenance", e)
			}
		} else if !bytes.Equal(b, adapterFiles[asset]) {
			t.Fatal("export differs from independently accepted CPU bytes")
		}
	}
	human.Call("GET", humanBase+"optimizer.pt", nil, 404, "Problem")
	if _, e = f.s.DB.Exec(ctx, `CREATE FUNCTION reject_adapter_export() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='training.adapter_exported' THEN RAISE EXCEPTION 'SYNTHETIC_ADAPTER_EXPORT_AUDIT_FAILURE'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_adapter_export BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_adapter_export()`); e != nil {
		t.Fatal(e)
	}
	human.Call("GET", humanBase+"adapter_config.json", nil, 503, "Problem")
	t.Run("M10_adapter_source_foreign_workspace_current_viewer_and_epoch", func(t *testing.T) {
		other, e := f.s.Workspaces.Create(ctx, f.admin, "Isolated adapter source scope", f.portal.Me.User.ID, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		_, e = f.s.AcceptedAdapterSnapshot(ctx, f.portal, other.ID, l.Training.Scope.JobID)
		expected(t, e, "TRAINING_NOT_FOUND")
		if _, e = f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = f.s.AcceptedAdapterSnapshot(ctx, f.portal, f.input.WorkspaceID, l.Training.Scope.JobID); e != nil {
			t.Fatal("current authorized viewer cannot inspect immutable source", e)
		}
		if _, e = f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = f.s.AcceptedAdapterSnapshot(ctx, f.portal, f.input.WorkspaceID, l.Training.Scope.JobID); e == nil {
			t.Fatal("stale epoch resolved an accepted adapter")
		}
	})
}

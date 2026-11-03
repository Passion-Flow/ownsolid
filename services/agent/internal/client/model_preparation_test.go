package client

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// This transport fixture asserts exact TLS bytes and acknowledgments. It does not
// claim a prepared model, GPU execution, or server-side report acceptance.
func preparationClientCommand(t *testing.T) wire.Command {
	t.Helper()
	const id = "00000000-0000-4000-8000-000000000001"
	hash := strings.Repeat("a", 64)
	image := "sha256:" + strings.Repeat("b", 64)
	cfg := wire.DefaultConfiguration()
	cfgSHA, _ := cfg.Digest()
	train := trainingwire.DefaultConfiguration()
	trainSHA, _ := train.Digest()
	c := wire.Command{SchemaVersion: 1, Scope: wire.Scope{TenantID: id, WorkspaceID: id, PreparationID: id}, AttemptID: id, AttemptNumber: 1, ModelID: id, ModelCatalogSHA256: wire.ApprovedModelCatalogSHA256, TrainingJobID: id, TrainingAttemptID: id, TrainingConfiguration: train, TrainingConfigurationSHA256: trainSHA, HeldoutID: id, HeldoutManifestSHA256: hash, GPUUUID: "GPU-" + id, Configuration: cfg, ConfigurationSHA256: cfgSHA, Recipe: wire.ExecutionRecipe{SchemaVersion: 1, WorkerVersion: wire.WorkerVersion, ImageReference: wire.ImageRepository + "@sha256:" + hash, Identity: trainingwire.ExecutionIdentity{Profile: trainingwire.ProductionProfile, Python: trainingwire.PythonVersion, WorkerSourceSHA256: hash, ImageID: &image}, Resources: wire.DefaultResources(), DataLimits: datawire.Limits{MaximumBytes: 1048576, MaximumRows: 256, MaximumLineBytes: 16384, MaximumTextBytes: 4096, MaximumSourceBytes: 256, MaximumErrors: 10}, RecipeSHA256: wire.RecipeDigest(), ProcessorSHA256: wire.ProcessorDigest()}, Inputs: wire.InputInventory{AdapterFiles: []datawire.Asset{{Path: "README.md", SizeBytes: 248, SHA256: hash}, {Path: "adapter_config.json", SizeBytes: 1092, SHA256: hash}, {Path: "adapter_model.safetensors", SizeBytes: 2308432, SHA256: hash}}, Heldout: datawire.Asset{Path: "heldout.jsonl", SizeBytes: 512, SHA256: hash}}}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	return c
}

func TestActualTLSPreparationRangesOriginalUnicodeMetadataAndRestrictedStop(t *testing.T) {
	command := preparationClientCommand(t)
	id := command.AttemptID
	base := "/agent/v1/nodes/" + id + "/tasks/" + id
	lease, credential := strings.Repeat("A", 43), strings.Repeat("B", 43)
	content := []byte("synthetic bounded heldout transport\n")
	asset := datawire.Asset{Path: "heldout.jsonl", SizeBytes: int64(len(content)), SHA256: preparationDigest(content)}
	files := wire.ApprovedBaseFiles()
	encodedFiles, _ := json.Marshal(files)
	metadata := []byte(`{"preparation_id":"` + id + `", "attempt_id":"` + id + `", "model_files":` + string(encodedFiles) + ", \"transport_fixture_only\":\"<\u6807\u7b7e>&\"}")
	outcome := []byte(`{"schema_version":1, "worker_version":"` + wire.WorkerVersion + `","status":"failed","preparation_id":"` + id + `","attempt_id":"` + id + `","error_code":"MODEL_PREPARATION_PROCESSOR_TIMEOUT"}`)
	outcomeSHA := preparationDigest(outcome)
	commandSHA, _ := command.Digest()
	container := strings.Repeat("1", 64)
	receipt := wire.StopReceipt{SchemaVersion: 1, ReceiptID: id, JournalID: id, NodeID: id, TaskID: id, Scope: command.Scope, AttemptID: id, AttemptNumber: 1, CommandSHA256: commandSHA, ImageID: *command.Recipe.Identity.ImageID, GPUUUID: command.GPUUUID, ContainerName: wire.ContainerName(id), ContainerID: &container, Proof: "owned_container_removed", OwnedContainerAbsent: true, OutcomeSHA256: &outcomeSHA, CompletedAt: time.Now().UTC()}
	corrupt, foreign, omitOutcome := false, false, false
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+credential {
			t.Error("unscoped request")
			w.WriteHeader(401)
			return
		}
		respond := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"data": v})
		}
		switch strings.TrimPrefix(r.URL.Path, base) {
		case "/model-preparation-input-files":
			var input wire.InputFileRequest
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.ValidateAsset(asset) != nil || input.LeaseToken != lease || input.Kind != "heldout" {
				t.Error("unbound range")
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.Itoa(len(content)))
			h := asset.SHA256
			if corrupt {
				h = strings.Repeat("0", 64)
			}
			w.Header().Set("X-Artifact-SHA256", h)
			w.Write(content)
		case "/model-preparation-events":
			var input wire.EventSubmission
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.LeaseToken != lease || len(input.Events) != 1 {
				t.Error("unbound events")
				w.WriteHeader(400)
				return
			}
			target := id
			if foreign {
				target = "00000000-0000-4000-8000-000000000002"
			}
			respond(map[string]any{"items": []wire.EventAcceptance{{PreparationID: target, AttemptID: id, Sequence: 1, State: "running"}}})
		case "/model-preparation-uploads":
			raw, _ := io.ReadAll(r.Body)
			input, e := wire.DecodeUploadSubmission(raw)
			if e != nil || !bytes.Equal(input.Metadata, metadata) || input.LeaseToken != lease {
				t.Error("original Unicode metadata changed")
				w.WriteHeader(400)
				return
			}
			target := id
			if foreign {
				target = "00000000-0000-4000-8000-000000000002"
			}
			respond(wire.Upload{ID: id, PreparationID: target, AttemptID: id, ManifestSHA256: preparationDigest(metadata), State: "uploading", Files: files, ChunkBytes: wire.TransferChunkBytes, CreatedAt: time.Now().UTC()})
		case "/model-preparation-uploads/" + id + "/chunks":
			raw, _ := io.ReadAll(r.Body)
			if !bytes.Equal(raw, content) || r.Header.Get("X-Model-Preparation-Lease-Token") != lease || r.Header.Get("X-Model-Preparation-Asset") != "config.json" || r.Header.Get("X-Model-Preparation-Offset") != "0" || r.Header.Get("X-Model-Preparation-SHA256") != asset.SHA256 || r.ContentLength != asset.SizeBytes || len(r.TransferEncoding) != 0 {
				t.Error("chunk bytes/headers changed")
				w.WriteHeader(400)
				return
			}
			respond(wire.ChunkAcceptance{UploadID: id, Asset: "config.json", Offset: 0, SizeBytes: asset.SizeBytes, SHA256: asset.SHA256, ReceivedBytes: asset.SizeBytes})
		case "/model-preparation-uploads/" + id + "/finish":
			respond(wire.OutputAcceptance{UploadID: id, PreparationID: id, AttemptID: id, ManifestSHA256: preparationDigest(metadata)})
		case "/model-preparation-stops":
			raw, _ := io.ReadAll(r.Body)
			input, e := wire.DecodeStopSubmission(raw)
			if e != nil || input.Receipt.ValidateFor(command, id, id) != nil || input.LeaseToken != lease || !omitOutcome && !bytes.Equal(input.Outcome, outcome) || omitOutcome && input.Outcome != nil {
				t.Error("raw outcome/receipt changed")
				w.WriteHeader(400)
				return
			}
			respond(wire.StopAcceptance{PreparationID: id, AttemptID: id, ReceiptID: id, State: "failed", GPUReleased: true})
		default:
			t.Error("unexpected route")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	directory := t.TempDir()
	ca := filepath.Join(directory, "ca.pem")
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := New(server.URL, ca)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.Credential = credential
	ctx := context.Background()
	if e = c.PreparationFile(ctx, base, lease, "heldout", asset, filepath.Join(directory, "heldout.jsonl")); e != nil {
		t.Fatal(e)
	}
	event := []byte(`{"sequence":1,"preparation_id":"` + id + `","attempt_id":"` + id + `","kind":"phase","phase":"evaluating_base"}`)
	if _, e = c.PreparationEvents(ctx, base, lease, command, 3, [][]byte{event}); e != nil {
		t.Fatal(e)
	}
	upload, e := c.BeginPreparationUpload(ctx, base, lease, command, metadata)
	if e != nil {
		t.Fatal(e)
	}
	chunk := wire.ChunkRequest{Asset: "config.json", Length: asset.SizeBytes, SHA256: asset.SHA256}
	if _, e = c.PreparationChunk(ctx, base, lease, id, chunk, content); e != nil {
		t.Fatal(e)
	}
	if _, e = c.FinishPreparationUpload(ctx, base, lease, upload); e != nil {
		t.Fatal(e)
	}
	if _, e = c.PreparationStop(ctx, base, lease, command, receipt, outcome); e != nil {
		t.Fatal(e)
	}
	omitOutcome = true
	if _, e = c.PreparationStop(ctx, base, lease, command, receipt, nil); e != nil {
		t.Fatal("previously stored outcome cleanup", e)
	}
	foreign = true
	if _, e = c.PreparationEvents(ctx, base, lease, command, 3, [][]byte{event}); e == nil {
		t.Fatal("foreign event acknowledgment accepted")
	}
	if _, e = c.BeginPreparationUpload(ctx, base, lease, command, metadata); e == nil {
		t.Fatal("foreign upload acknowledgment accepted")
	}
	foreign = false
	corrupt = true
	if e = c.PreparationFile(ctx, base, lease, "heldout", asset, filepath.Join(directory, "corrupt.jsonl")); e == nil {
		t.Fatal("corrupt transfer accepted")
	}
	before := calls
	if e = c.PreparationFile(ctx, base+"?redirect=forbidden", lease, "heldout", asset, filepath.Join(directory, "unsafe")); e == nil {
		t.Fatal("unsafe route")
	}
	if _, e = c.BeginPreparationUpload(ctx, base, lease, command, append(metadata, '\n')); e == nil {
		t.Fatal("non-preservable metadata")
	}
	if _, e = c.PreparationChunk(ctx, base, lease, id, chunk, []byte("wrong")); e == nil {
		t.Fatal("unbound body")
	}
	if _, e = c.PreparationEvents(ctx, base, lease, command, 3, [][]byte{[]byte(strings.Replace(string(event), `"sequence":1`, `"sequence":2`, 1))}); e == nil {
		t.Fatal("wrong event")
	}
	if calls != before {
		t.Fatal("invalid local intent performed HTTP effects")
	}
}

func TestPreparationOutputTimeoutKeepsProtectedTLSAndOrdinaryBudgetIndependent(t *testing.T) {
	command := preparationClientCommand(t)
	id := command.AttemptID
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(70 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": wire.OutputAcceptance{UploadID: id, PreparationID: id, AttemptID: id, ManifestSHA256: strings.Repeat("a", 64)}})
	}))
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := New(server.URL, ca)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.HTTP.Timeout = 20 * time.Millisecond
	ordinary := c.HTTP.Transport.(*http.Transport)
	if c.ConfigurePreparationOutputs(0) == nil || c.ConfigurePreparationOutputs(3601) == nil {
		t.Fatal("unbounded output timeout")
	}
	if e = c.ConfigurePreparationOutputs(1); e != nil {
		t.Fatal(e)
	}
	if c.ConfigurePreparationOutputs(1) == nil {
		t.Fatal("live timeout mutation")
	}
	private := c.preparationHTTP.Transport.(*http.Transport)
	if private == ordinary || private.TLSClientConfig.RootCAs != ordinary.TLSClientConfig.RootCAs || private.Proxy != nil || private.MaxConnsPerHost < 2 || private.ResponseHeaderTimeout != time.Second || c.HTTP.Timeout != 20*time.Millisecond {
		t.Fatal("TLS trust, proxy or independent ordinary limits changed")
	}
	upload := wire.Upload{ID: id, PreparationID: id, AttemptID: id, ManifestSHA256: strings.Repeat("a", 64)}
	if _, e = c.FinishPreparationUpload(context.Background(), "/agent/v1/nodes/"+id+"/tasks/"+id, strings.Repeat("A", 43), upload); e != nil {
		t.Fatal("bounded long output verification failed", e)
	}
}

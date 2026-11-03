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

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func TestActualTLSBoundTrainingRangesRawMetadataChunksAndRestrictedStop(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	base := "/agent/v1/nodes/" + id + "/tasks/" + id
	lease := strings.Repeat("A", 43)
	credential := strings.Repeat("B", 43)
	content := []byte("synthetic bounded training input\n")
	asset := datawire.Asset{Path: "train.jsonl", SizeBytes: int64(len(content)), SHA256: trainingDigest(content)}
	metadata := []byte(`{"schema_version": 1, "synthetic_contract_only":true}`)
	outcome := []byte(`{"schema_version":1, "worker_version":"` + wire.WorkerVersion + `","status":"failed","job_id":"` + id + `","attempt_id":"` + id + `","error_code":"TRAINING_PROCESSOR_TIMEOUT"}`)
	outcomeSHA := trainingDigest(outcome)
	image := "sha256:" + strings.Repeat("c", 64)
	configuration := wire.DefaultConfiguration()
	configurationSHA, _ := configuration.Digest()
	command := wire.Command{SchemaVersion: 1, Scope: wire.Scope{TenantID: id, WorkspaceID: id, JobID: id}, AttemptID: id, AttemptNumber: 1, ModelID: id, DatasetID: id, DatasetVersionID: id, DatasetManifestSHA256: strings.Repeat("d", 64), ModelCatalogSHA256: agentwire.ModelCatalogDigest(), GPUUUID: "GPU-" + id, Configuration: configuration, ConfigurationSHA256: configurationSHA, Recipe: wire.ExecutionRecipe{SchemaVersion: 1, WorkerVersion: wire.WorkerVersion, ImageReference: "projectone/training@sha256:" + strings.Repeat("e", 64), Identity: wire.ExecutionIdentity{Profile: wire.ProductionProfile, Python: wire.PythonVersion, WorkerSourceSHA256: strings.Repeat("f", 64), ImageID: &image}, Resources: wire.DefaultResources(), DataLimits: datawire.Limits{MaximumBytes: 16777216, MaximumRows: 2000, MaximumLineBytes: 65536, MaximumTextBytes: 16384, MaximumSourceBytes: 512, MaximumErrors: 200}}, Inputs: wire.InputInventory{Train: asset, Validation: datawire.Asset{Path: "validation.jsonl", SizeBytes: 10, SHA256: strings.Repeat("a", 64)}}}
	commandSHA, _ := command.Digest()
	container := strings.Repeat("1", 64)
	receipt := wire.StopReceipt{SchemaVersion: 1, ReceiptID: id, JournalID: id, NodeID: id, TaskID: id, Scope: command.Scope, AttemptID: id, AttemptNumber: 1, CommandSHA256: commandSHA, ImageID: image, GPUUUID: command.GPUUUID, ContainerName: wire.ContainerName(id), ContainerID: &container, Proof: "owned_container_removed", OwnedContainerAbsent: true, OutcomeSHA256: &outcomeSHA, CompletedAt: time.Now().UTC()}
	corrupt := false
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+credential {
			t.Error("missing scoped machine credential")
			w.WriteHeader(401)
			return
		}
		respond := func(value any) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"data": value})
		}
		switch strings.TrimPrefix(r.URL.Path, base) {
		case "/training-input-files":
			var input wire.InputFileRequest
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.ValidateAsset(asset) != nil || input.LeaseToken != lease || input.Kind != "dataset" {
				t.Error("input range not bound")
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.Itoa(len(content)))
			digest := asset.SHA256
			if corrupt {
				digest = strings.Repeat("0", 64)
			}
			w.Header().Set("X-Artifact-SHA256", digest)
			w.Write(content)
		case "/training-events":
			var input wire.EventSubmission
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.LeaseToken != lease || len(input.Events) != 1 {
				t.Error("events not bounded")
				w.WriteHeader(400)
				return
			}
			respond(map[string]any{"items": []wire.EventAcceptance{{JobID: id, AttemptID: id, Sequence: 1, JobState: "preparing"}}})
		case "/training-uploads":
			raw, _ := io.ReadAll(r.Body)
			input, e := wire.DecodeUploadSubmission(raw)
			if e != nil || !bytes.Equal(input.Metadata, metadata) || input.LeaseToken != lease {
				t.Error("original receipt bytes changed")
				w.WriteHeader(400)
				return
			}
			respond(wire.Upload{ID: id, JobID: id, AttemptID: id, Kind: "checkpoint", GlobalStep: 1, ManifestSHA256: trainingDigest(metadata), State: "uploading", Files: []datawire.Asset{asset}, ChunkBytes: wire.TransferChunkBytes, CreatedAt: time.Now().UTC()})
		case "/training-uploads/" + id + "/chunks":
			raw, _ := io.ReadAll(r.Body)
			if !bytes.Equal(raw, content) || r.Header.Get("X-Training-Lease-Token") != lease || r.Header.Get("X-Training-Asset") != asset.Path || r.Header.Get("X-Training-Offset") != "0" || r.Header.Get("X-Training-SHA256") != asset.SHA256 || r.ContentLength != asset.SizeBytes || len(r.TransferEncoding) != 0 {
				t.Error("chunk bytes/headers changed")
				w.WriteHeader(400)
				return
			}
			respond(wire.ChunkAcceptance{UploadID: id, Asset: asset.Path, SizeBytes: asset.SizeBytes, SHA256: asset.SHA256, ReceivedBytes: asset.SizeBytes})
		case "/training-uploads/" + id + "/finish":
			respond(wire.OutputAcceptance{UploadID: id, ReferenceID: id, Kind: "checkpoint", GlobalStep: 1})
		case "/training-stops":
			raw, _ := io.ReadAll(r.Body)
			input, e := wire.DecodeStopSubmission(raw)
			if e != nil || !bytes.Equal(input.Outcome, outcome) || input.Receipt.ValidateFor(command, id, id) != nil || input.LeaseToken != lease {
				t.Error("raw outcome/receipt changed")
				w.WriteHeader(400)
				return
			}
			respond(wire.StopAcceptance{JobID: id, AttemptID: id, ReceiptID: id, State: "failed", GPUReleased: true})
		default:
			t.Error("unexpected training route")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	directory := t.TempDir()
	ca := filepath.Join(directory, "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	c, e := New(server.URL, ca)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.Credential = credential
	ctx := context.Background()
	if e = c.TrainingFile(ctx, base, lease, "dataset", asset, filepath.Join(directory, "input.jsonl")); e != nil {
		t.Fatal(e)
	}
	event := []byte(`{"sequence":1,"job_id":"` + id + `","attempt_id":"` + id + `","global_step":0,"kind":"phase","phase":"loading_model"}`)
	if _, e = c.TrainingEvents(ctx, base, lease, command, [][]byte{event}); e != nil {
		t.Fatal(e)
	}
	upload, e := c.BeginTrainingUpload(ctx, base, lease, "checkpoint", metadata)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.TrainingChunk(ctx, base, lease, id, wire.ChunkRequest{Asset: asset.Path, Length: asset.SizeBytes, SHA256: asset.SHA256}, content); e != nil {
		t.Fatal(e)
	}
	if _, e = c.FinishTrainingUpload(ctx, base, lease, upload); e != nil {
		t.Fatal(e)
	}
	if _, e = c.TrainingStop(ctx, base, lease, command, receipt, outcome); e != nil {
		t.Fatal(e)
	}
	corrupt = true
	if e = c.TrainingFile(ctx, base, lease, "dataset", asset, filepath.Join(directory, "corrupt.jsonl")); e == nil {
		t.Fatal("corrupt transport hash accepted")
	}
	before := calls
	if e = c.TrainingFile(ctx, base+"?redirect=forbidden", lease, "dataset", asset, filepath.Join(directory, "unsafe.jsonl")); e == nil {
		t.Fatal("unsafe path accepted")
	}
	if _, e = c.BeginTrainingUpload(ctx, base, lease, "checkpoint", append(metadata, '\n')); e == nil {
		t.Fatal("non-preservable metadata accepted")
	}
	if _, e = c.TrainingChunk(ctx, base, lease, id, wire.ChunkRequest{Asset: asset.Path, Length: asset.SizeBytes, SHA256: asset.SHA256}, []byte("wrong")); e == nil {
		t.Fatal("unbound chunk sent")
	}
	if calls != before {
		t.Fatal("locally invalid intent performed HTTP effects")
	}
}

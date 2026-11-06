package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/training"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func runtimeTrainingLease() agentwire.Lease {
	const id = "00000000-0000-4000-8000-000000000001"
	configuration := wire.DefaultConfiguration()
	sha, _ := configuration.Digest()
	image := "sha256:" + strings.Repeat("a", 64)
	command := wire.Command{SchemaVersion: 1, Scope: wire.Scope{TenantID: id, WorkspaceID: id, JobID: id}, AttemptID: id, AttemptNumber: 1, ModelID: id, DatasetID: id, DatasetVersionID: id, DatasetManifestSHA256: strings.Repeat("b", 64), ModelCatalogSHA256: agentwire.ModelCatalogDigest(), GPUUUID: "GPU-" + id, Configuration: configuration, ConfigurationSHA256: sha, Recipe: wire.ExecutionRecipe{SchemaVersion: 1, WorkerVersion: wire.WorkerVersion, ImageReference: "projectone/training@sha256:" + strings.Repeat("c", 64), Identity: wire.ExecutionIdentity{Profile: wire.ProductionProfile, Python: wire.PythonVersion, WorkerSourceSHA256: strings.Repeat("d", 64), ImageID: &image}, Resources: wire.DefaultResources(), DataLimits: datawire.Limits{MaximumBytes: 16777216, MaximumRows: 2000, MaximumLineBytes: 65536, MaximumTextBytes: 16384, MaximumSourceBytes: 512, MaximumErrors: 200}}, Inputs: wire.InputInventory{Train: datawire.Asset{Path: "train.jsonl", SizeBytes: 10, SHA256: strings.Repeat("e", 64)}, Validation: datawire.Asset{Path: "validation.jsonl", SizeBytes: 10, SHA256: strings.Repeat("f", 64)}}}
	return agentwire.Lease{TaskID: id, LeaseToken: strings.Repeat("A", 43), Attempt: 1, Operation: wire.RunOperation, Training: &command}
}

// No physical process exists in this guard fixture; its never-created proof is explicitly synthetic.
type guardTrainingFixture struct {
	begun              atomic.Bool
	entered            atomic.Bool
	acknowledged       atomic.Bool
	initiallyCancelled bool
	blockedRequest     bool
	connection         *client.Client
	begins             int
	destination        string
}

func (m *guardTrainingFixture) Begin(string, agentwire.Lease) (training.Journal, error) {
	m.begun.Store(true)
	m.begins++
	return training.Journal{}, nil
}
func (m *guardTrainingFixture) Execute(ctx context.Context, node string, lease agentwire.Lease, _ training.Transport) (wire.StopReceipt, []byte, error) {
	m.initiallyCancelled = ctx.Err() != nil
	m.entered.Store(true)
	if m.blockedRequest && ctx.Err() == nil {
		_ = m.connection.TrainingFile(ctx, "/agent/v1/nodes/"+node+"/tasks/"+lease.TaskID, lease.LeaseToken, "dataset", lease.Training.Inputs.Train, m.destination)
	} else {
		<-ctx.Done()
	}
	digest, _ := lease.Training.Digest()
	id := lease.Training.AttemptID
	receipt := wire.StopReceipt{SchemaVersion: 1, ReceiptID: id, JournalID: id, NodeID: node, TaskID: lease.TaskID, Scope: lease.Training.Scope, AttemptID: id, AttemptNumber: 1, CommandSHA256: digest, ImageID: *lease.Training.Recipe.Identity.ImageID, GPUUUID: lease.Training.GPUUUID, ContainerName: wire.ContainerName(id), Proof: "never_created", OwnedContainerAbsent: true, CompletedAt: time.Now().UTC()}
	return receipt, nil, ctx.Err()
}
func (m *guardTrainingFixture) AcceptStop(wire.StopReceipt) error {
	m.acknowledged.Store(true)
	return nil
}

func TestTrainingRenewalGuardPreservesCleanupAfterRefusalsAndBlockedTransfers(t *testing.T) {
	for _, mode := range []string{"denied", "refused_while_running", "blocked", "stop_unavailable"} {
		t.Run(mode, func(t *testing.T) {
			fastRenewal(t)
			manager := &guardTrainingFixture{}
			var renewals atomic.Int64
			lease := runtimeTrainingLease()
			node := lease.Training.Scope.TenantID
			var stops atomic.Int64
			var requests atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if !manager.begun.Load() {
					t.Error("delivered claim was not journaled before first remote effect")
				}
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/renewals"):
					if mode == "denied" || renewals.Add(1) > 3 {
						w.WriteHeader(409)
						json.NewEncoder(w).Encode(map[string]any{"code": "AGENT_LEASE_STALE"})
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"data": agentwire.RenewedLease{LeaseExpiresAt: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), ValidForMilliseconds: 80}})
				case strings.HasSuffix(r.URL.Path, "/training-input-files"):
					io.Copy(io.Discard, r.Body)
					select {
					case <-r.Context().Done():
					case <-time.After(1500 * time.Millisecond):
						t.Error("cancelled transfer did not close")
					}
				case strings.HasSuffix(r.URL.Path, "/training-stops"):
					stops.Add(1)
					if mode == "stop_unavailable" {
						w.WriteHeader(503)
						json.NewEncoder(w).Encode(map[string]any{"code": "STORAGE_UNAVAILABLE"})
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"data": wire.StopAcceptance{JobID: lease.Training.Scope.JobID, AttemptID: lease.Training.AttemptID, ReceiptID: lease.Training.AttemptID, State: "failed", GPUReleased: true}})
				default:
					t.Error("training used generic result or unexpected endpoint")
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			connection := &client.Client{Origin: server.URL, HTTP: server.Client(), Credential: strings.Repeat("B", 43)}
			defer connection.Close()
			manager.connection = connection
			manager.blockedRequest = mode == "blocked"
			manager.destination = filepath.Join(t.TempDir(), "input.jsonl")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			started := time.Now()
			e := executeTraining(ctx, connection, "/agent/v1/nodes/"+node, node, lease, manager)
			if e == nil || time.Since(started) > time.Second || stops.Load() != 1 || manager.begins != 1 || !manager.entered.Load() || manager.acknowledged.Load() != (mode != "stop_unavailable") || manager.initiallyCancelled != (mode == "denied") {
				t.Fatal("training lease/cleanup fence lost", e)
			}
		})
	}
}

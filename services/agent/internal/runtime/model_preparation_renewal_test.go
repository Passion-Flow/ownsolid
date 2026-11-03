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
	"github.com/Passion-Flow/ownsolid/services/agent/internal/modelpreparations"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// No physical process exists in this guard fixture; its never-created proof is explicitly synthetic.
type guardPreparationFixture struct {
	begun              atomic.Bool
	entered            atomic.Bool
	acknowledged       atomic.Bool
	initiallyCancelled bool
	blockedRequest     bool
	connection         *client.Client
	begins             int
	destination        string
}

func (m *guardPreparationFixture) Begin(string, agentwire.Lease) (modelpreparations.Journal, error) {
	m.begun.Store(true)
	m.begins++
	return modelpreparations.Journal{}, nil
}
func (m *guardPreparationFixture) Execute(ctx context.Context, node string, lease agentwire.Lease, _ modelpreparations.Transport) (wire.StopReceipt, []byte, error) {
	m.initiallyCancelled = ctx.Err() != nil
	m.entered.Store(true)
	if m.blockedRequest && ctx.Err() == nil {
		_ = m.connection.PreparationFile(ctx, "/agent/v1/nodes/"+node+"/tasks/"+lease.TaskID, lease.LeaseToken, "heldout", lease.Preparation.Inputs.Heldout, m.destination)
	} else {
		<-ctx.Done()
	}
	digest, _ := lease.Preparation.Digest()
	id := lease.Preparation.AttemptID
	receipt := wire.StopReceipt{SchemaVersion: 1, ReceiptID: id, JournalID: id, NodeID: node, TaskID: lease.TaskID, Scope: lease.Preparation.Scope, AttemptID: id, AttemptNumber: 1, CommandSHA256: digest, ImageID: *lease.Preparation.Recipe.Identity.ImageID, GPUUUID: lease.Preparation.GPUUUID, ContainerName: wire.ContainerName(id), Proof: "never_created", OwnedContainerAbsent: true, CompletedAt: time.Now().UTC()}
	return receipt, nil, ctx.Err()
}
func (m *guardPreparationFixture) AcceptStop(wire.StopReceipt) error {
	m.acknowledged.Store(true)
	return nil
}

func TestPreparationRenewalGuardPreservesCleanupAfterDeniedExpiredAndBlockedTransfers(t *testing.T) {
	for _, mode := range []string{"denied", "expires", "blocked", "stop_unavailable"} {
		t.Run(mode, func(t *testing.T) {
			manager := &guardPreparationFixture{}
			lease := runtimePreparationLease()
			node := lease.Preparation.Scope.TenantID
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
					if mode == "denied" {
						w.WriteHeader(409)
						json.NewEncoder(w).Encode(map[string]any{"code": "AGENT_LEASE_STALE"})
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"data": agentwire.RenewedLease{LeaseExpiresAt: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), ValidForMilliseconds: 80}})
				case strings.HasSuffix(r.URL.Path, "/model-preparation-input-files"):
					io.Copy(io.Discard, r.Body)
					select {
					case <-r.Context().Done():
					case <-time.After(1500 * time.Millisecond):
						t.Error("cancelled transfer did not close")
					}
				case strings.HasSuffix(r.URL.Path, "/model-preparation-stops"):
					stops.Add(1)
					if mode == "stop_unavailable" {
						w.WriteHeader(503)
						json.NewEncoder(w).Encode(map[string]any{"code": "STORAGE_UNAVAILABLE"})
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"data": wire.StopAcceptance{PreparationID: lease.Preparation.Scope.PreparationID, AttemptID: lease.Preparation.AttemptID, ReceiptID: lease.Preparation.AttemptID, State: "failed", GPUReleased: true}})
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
			e := executePreparation(ctx, connection, "/agent/v1/nodes/"+node, node, lease, manager, 15)
			if e == nil || time.Since(started) > time.Second || stops.Load() != 1 || manager.begins != 1 || !manager.entered.Load() || manager.acknowledged.Load() != (mode != "stop_unavailable") || manager.initiallyCancelled != (mode == "denied") {
				t.Fatal("training lease/cleanup fence lost", e)
			}
		})
	}
}

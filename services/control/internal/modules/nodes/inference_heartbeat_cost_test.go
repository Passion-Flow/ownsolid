package nodes_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentcost"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInferenceHeartbeatStatementCost(t *testing.T) {
	const smallPeers, largePeers = 50, 3000
	small := measureInferenceHeartbeats(t, smallPeers)
	large := measureInferenceHeartbeats(t, largePeers)
	compareObservationCosts(t, []string{"first inference report", "inference update", "foreign credential"}, small, large)
}

func measureInferenceHeartbeats(t *testing.T, peers int) map[string]agentcost.Cost {
	t.Helper()
	f := newObservationCostFixture(t, wire.DockerInferenceProfile, peers)
	h := f.handshake
	h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, ObservedAt: h.Facts.ObservedAt}
	measured := map[string]agentcost.Cost{}
	var latest inferenceHeartbeatSnapshot
	for _, probe := range []struct{ name, credential, probeError, outcome string }{
		{name: "first inference report", credential: f.node.Credential},
		{name: "inference update", credential: f.node.Credential, probeError: "INFERENCE_NVIDIA_UNAVAILABLE"},
		{name: "foreign credential", credential: security.Token(), probeError: "INFERENCE_DOCKER_UNAVAILABLE", outcome: "AGENT_UNAUTHORIZED"},
	} {
		h.Inference.ProbeErrorCode = probe.probeError
		f.client.Headers = map[string]string{"Authorization": "Bearer " + probe.credential}
		var reply map[string]any
		measured[probe.name] = f.record(t, func() {
			if probe.outcome != "" {
				refusal := f.client.Call("POST", f.path, h, 401, "Problem")
				if refusal["code"] != probe.outcome {
					t.Fatalf("%s returned %v", probe.name, refusal["code"])
				}
			} else {
				reply = f.client.Call("POST", f.path, h, 200, "DispositionResponse")
			}
		})
		actual := readInferenceHeartbeat(t, f.account.DB, f.node.NodeID)
		if probe.outcome != "" {
			if actual != latest {
				t.Fatal("refused heartbeat changed the node or inference observation")
			}
			continue
		}
		raw, err := json.Marshal(reply)
		if err != nil {
			t.Fatal("encode heartbeat response", err)
		}
		var out wire.Disposition
		if err = json.Unmarshal(raw, &out); err != nil {
			t.Fatal("decode heartbeat response", err)
		}
		if out.Status != "active" || !out.ServerTime.Equal(actual.heartbeatAt) {
			t.Fatal("heartbeat reply did not match its committed timestamp")
		}
		var stored wire.InferenceObservation
		if json.Unmarshal([]byte(actual.observation), &stored) != nil || !reflect.DeepEqual(stored, *h.Inference) || !actual.local {
			t.Fatal("inference report did not retain the exact observation and node scope")
		}
		latest = actual
	}
	return measured
}

type inferenceHeartbeatSnapshot struct {
	observation             string
	receivedAt, heartbeatAt time.Time
	local                   bool
}

func readInferenceHeartbeat(t *testing.T, db *pgxpool.Pool, node string) inferenceHeartbeatSnapshot {
	t.Helper()
	var state inferenceHeartbeatSnapshot
	if err := db.QueryRow(t.Context(), `SELECT i.observation::text,i.received_at,n.last_heartbeat_at,
  i.tenant_id=n.tenant_id AND i.workspace_id=n.workspace_id AND i.site_id=n.site_id AND i.cell_id=n.cell_id
  FROM inference_nodes i JOIN managed_nodes n ON n.id=i.node_id WHERE n.id=$1`, node).Scan(&state.observation, &state.receivedAt, &state.heartbeatAt, &state.local); err != nil {
		t.Fatal(err)
	}
	return state
}

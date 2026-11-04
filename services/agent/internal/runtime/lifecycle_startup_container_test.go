//go:build linux && model_lifecycle_container_integration

package runtime

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/training"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestActualLinuxLifecycleConcreteCycleAndNativeEngineArchitectureRefusal(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("PROJECT_ONE_PREPARATION_CONTAINER_FIXTURE") != "isolated-test-only" {
		t.Fatal("isolated Linux root required")
	}
	root, e := os.MkdirTemp("/verification", "startup-")
	if e != nil {
		t.Fatal(e)
	}
	for _, dir := range []string{"state", "inference", "training", "preparation"} {
		if e = os.Mkdir(filepath.Join(root, dir), 0700); e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	id := "12345678-1234-4234-8234-123456789abc"
	credential := strings.Repeat("B", 43)
	var heartbeats, claims atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+credential {
			t.Error("unbound resident request")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/agent/v1/sites/default/cells/default/nodes/" + id + "/heartbeat":
			var h wire.Handshake
			if json.NewDecoder(r.Body).Decode(&h) != nil || h.Validate() != nil || h.ExecutionProfile != wire.DockerModelLifecycleProfile || h.ProtocolVersion != wire.ModelLifecycleVersion || h.Facts.Architecture != "amd64" || h.Inference == nil || h.Inference.ProbeErrorCode == "" {
				t.Error("actual device-free lifecycle handshake missing concrete discovery/inference or exact capabilities")
			}
			heartbeats.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"data": wire.Disposition{Status: "active", ServerTime: time.Now().UTC()}})
		case "/agent/v1/sites/default/cells/default/nodes/" + id + "/tasks/claims":
			claims.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"data": wire.Claim{}})
			cancel()
		default:
			t.Error("unexpected external effect", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	ca := filepath.Join(root, "ca.pem")
	if os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600) != nil {
		t.Fatal("private test CA")
	}
	c := Config{ControlOrigin: server.URL, CAFile: ca, StateDirectory: filepath.Join(root, "state"), DiskPath: root, ExecutionProfile: wire.DockerModelLifecycleProfile, Inference: inference.Config{RootDirectory: filepath.Join(root, "inference"), DockerSocket: "/run/docker.sock"}, Training: training.Config{RootDirectory: filepath.Join(root, "training"), DockerSocket: "/run/docker.sock", ImageReference: os.Getenv("PROJECT_ONE_TRAINING_IMAGE_REFERENCE"), ImageID: os.Getenv("PROJECT_ONE_TRAINING_IMAGE_ID"), WorkerSourceSHA256: os.Getenv("PROJECT_ONE_TRAINING_WORKER_SOURCE")}, Preparation: modelpreparations.Config{RootDirectory: filepath.Join(root, "preparation"), DockerSocket: "/run/docker.sock", ImageReference: os.Getenv("PROJECT_ONE_PREPARATION_IMAGE_REFERENCE"), ImageID: os.Getenv("PROJECT_ONE_PREPARATION_IMAGE_ID"), WorkerSourceSHA256: os.Getenv("PROJECT_ONE_PREPARATION_WORKER_SOURCE")}}
	raw, _ := json.Marshal(c)
	config := filepath.Join(root, "agent.json")
	if os.WriteFile(config, raw, 0600) != nil {
		t.Fatal("protected config")
	}
	loaded, e := LoadConfig(config)
	if e != nil {
		t.Fatal("complete explicit execution configuration", e)
	}
	identity := state.Identity{SiteID: "default", CellID: "default", NodeID: id, Credential: credential, ControlOrigin: c.ControlOrigin, ExecutionProfile: wire.DockerModelLifecycleProfile, ProtocolVersion: wire.ModelLifecycleVersion}
	if e = state.SaveNew(c.StateDirectory, identity); e != nil {
		t.Fatal(e)
	}
	// The real Desktop engine is arm64. Never disguise it as the declared amd64 training engine.
	if e = Run(ctx, loaded); e == nil || e.Error() != "TRAINING_PLATFORM_UNSUPPORTED" {
		t.Fatal("native engine architecture refusal", e)
	}
	if heartbeats.Load() != 0 || claims.Load() != 0 {
		t.Fatal("unsupported engine consumed remote authority")
	}
	im, e := inference.New(c.Inference, c.StateDirectory, c.Policy())
	if e != nil {
		t.Fatal(e)
	}
	defer im.Close()
	tm, e := training.New(c.Training, c.StateDirectory, c.Policy())
	if e != nil {
		t.Fatal(e)
	}
	defer tm.Close()
	pm, e := modelpreparations.New(c.Preparation, c.StateDirectory, c.Policy())
	if e != nil {
		t.Fatal(e)
	}
	defer pm.Close()
	// Real offline image inspection remains feasible even though engine architecture admission is refused.
	if _, e = tm.Lifecycle.Engine.InspectImage(ctx, c.Training); e != nil {
		t.Fatal("actual training image", e)
	}
	if _, e = pm.Lifecycle.Engine.InspectImage(ctx, c.Preparation); e != nil {
		t.Fatal("actual preparation image", e)
	}
	connection, e := client.New(c.ControlOrigin, c.CAFile, c.Policy())
	if e != nil {
		t.Fatal(e)
	}
	defer connection.Close()
	connection.Credential = credential
	if e = cycle(ctx, loaded, connection, identity, nil, im, tm, pm, nil); e != nil && ctx.Err() == nil {
		t.Fatal("actual composite resident cycle", e)
	}
	if heartbeats.Load() != 1 || claims.Load() != 1 {
		t.Fatal("resident cycle did not reach actual TLS", heartbeats.Load(), claims.Load())
	}
	retained, e := state.Load(c.StateDirectory)
	if e != nil || retained != identity {
		t.Fatal("identity changed", e)
	}
	for _, name := range []string{"inference-journal.json", "training-journal.json", "model-preparation-journal.json"} {
		if _, e = os.Stat(filepath.Join(c.StateDirectory, name)); !os.IsNotExist(e) {
			t.Fatal("idle startup fabricated workload", name, e)
		}
	}
	t.Log("Actual Linux amd64 emulated process and real arm64 Docker correctly refuse full runtime engine admission. Independently instantiated concrete managers inspect actual manufactured images and run the idle composite recovery/discovery/heartbeat/claim cycle over real TLS. Control response/task identity is a test double; no full supported-engine startup, tasks, GPU, model execution, enroll/public exposure or systemd qualification.")
}

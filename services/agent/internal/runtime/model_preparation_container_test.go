//go:build linux && model_lifecycle_container_integration

package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/training"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	preparationwire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func TestActualLinuxPreparationCleanupExecutableNeverCreatedJournal(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("PROJECT_ONE_PREPARATION_CONTAINER_FIXTURE") != "isolated-test-only" {
		t.Fatal("explicit isolated Linux root container required")
	}
	root, e := os.MkdirTemp("/verification", "scope-")
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"state", "inference", "training", "preparation"} {
		if e = os.Mkdir(filepath.Join(root, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	train := runtimeTrainingLease().Training
	c := Config{ControlOrigin: "https://admin.example.invalid", StateDirectory: filepath.Join(root, "state"), DiskPath: root, ExecutionProfile: wire.DockerModelLifecycleProfile, Inference: inference.Config{RootDirectory: filepath.Join(root, "inference"), DockerSocket: "/run/docker.sock"}, Training: training.Config{RootDirectory: filepath.Join(root, "training"), DockerSocket: "/run/docker.sock", ImageReference: train.Recipe.ImageReference, ImageID: *train.Recipe.Identity.ImageID, WorkerSourceSHA256: train.Recipe.Identity.WorkerSourceSHA256}, Preparation: modelpreparations.Config{RootDirectory: filepath.Join(root, "preparation"), DockerSocket: "/run/docker.sock", ImageReference: os.Getenv("PROJECT_ONE_PREPARATION_IMAGE_REFERENCE"), ImageID: os.Getenv("PROJECT_ONE_PREPARATION_IMAGE_ID"), WorkerSourceSHA256: os.Getenv("PROJECT_ONE_PREPARATION_WORKER_SOURCE")}}

	lease := runtimePreparationLease()
	lease.Preparation.Recipe.ImageReference = c.Preparation.ImageReference
	lease.Preparation.Recipe.Identity.ImageID = &c.Preparation.ImageID
	lease.Preparation.Recipe.Identity.WorkerSourceSHA256 = c.Preparation.WorkerSourceSHA256
	// Unique task/attempt identifiers remain synthetic; no GPU/container launch occurs in this profile.
	var random [16]byte
	if _, e = rand.Read(random[:]); e != nil {
		t.Fatal(e)
	}
	random[6] = random[6]&0x0f | 0x40
	random[8] = random[8]&0x3f | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", random[:4], random[4:6], random[6:8], random[8:10], random[10:])
	lease.TaskID = id
	lease.Preparation.AttemptID = id
	if e = state.SaveNew(c.StateDirectory, state.Identity{NodeID: id, Credential: lease.LeaseToken, ControlOrigin: c.ControlOrigin, ExecutionProfile: wire.DockerModelLifecycleProfile, ProtocolVersion: wire.ModelLifecycleVersion}); e != nil {
		t.Fatal(e)
	}
	m, e := modelpreparations.New(c.Preparation, c.StateDirectory, c.Policy())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	j, e := m.Lifecycle.Store.Begin(id, lease.TaskID, lease.LeaseToken, *lease.Preparation)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	challenge := preparationwire.RemovalChallenge{SchemaVersion: 1, ChallengeID: id, NodeID: id, TaskID: j.TaskID, Command: j.Command, CommandSHA256: j.CommandSHA256, IssuedAt: now, ExpiresAt: now.Add(preparationwire.RemovalChallengeLifetime)}
	write := func(name string, value any) string {
		t.Helper()
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(root, name)
		if e = os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	configuration, challengeFile := write("agent.json", c), write("challenge.json", challenge)
	before, e := os.ReadFile(filepath.Join(c.StateDirectory, "identity.json"))
	if e != nil {
		t.Fatal(e)
	}
	run := func(output string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "/verification/projectone-agent", "remove-fenced-preparation", "--config", configuration, "--confirm-fenced-node", id, "--preparation-id", j.Command.Scope.PreparationID, "--challenge-file", challengeFile, "--receipt-file", output)
		raw, e := command.CombinedOutput()
		if e != nil {
			t.Log("actual cleanup stable error", string(raw))
		}
		return e
	}
	lock, e := state.Lock(c.StateDirectory)
	if e != nil {
		t.Fatal(e)
	}
	receiptFile := filepath.Join(root, "receipt.json")
	if run(receiptFile) == nil {
		t.Fatal("resident agent lock bypass")
	}
	lock.Close()
	if _, e = os.Stat(receiptFile); !os.IsNotExist(e) {
		t.Fatal("lock refusal created receipt")
	}
	if e = run(receiptFile); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(receiptFile)
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := preparationwire.DecodeRemovalReceipt(raw, time.Now())
	if e != nil || receipt.Stop.Proof != "never_created" || !receipt.Stop.OwnedContainerAbsent {
		t.Fatal("actual Docker absence and native CLI receipt", e)
	}
	if run(receiptFile) == nil {
		t.Fatal("operator receipt overwritten")
	}
	after, e := os.ReadFile(filepath.Join(c.StateDirectory, "identity.json"))
	if e != nil || string(after) != string(before) {
		t.Fatal("revoked retained identity changed", e)
	}
	t.Log("Actual Linux amd64 executable, protected root configuration/socket, private journal, agent lock, real Docker name absence and fsynced cleanup receipt passed. Synthetic task/node IDs and never-created scope only; no NVIDIA execution.")
}

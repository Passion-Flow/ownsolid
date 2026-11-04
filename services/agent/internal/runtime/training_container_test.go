//go:build linux && training_container_integration

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
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/training"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func TestActualLinuxTrainingCleanupExecutableNeverCreatedJournal(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("PROJECT_ONE_TRAINING_CONTAINER_FIXTURE") != "isolated-test-only" {
		t.Fatal("explicit isolated Linux root container required")
	}
	root, e := os.MkdirTemp("/verification", "scope-")
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"state", "inference", "training"} {
		if e = os.Mkdir(filepath.Join(root, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	c := Config{ControlOrigin: "https://admin.example.invalid", StateDirectory: filepath.Join(root, "state"), DiskPath: root, ExecutionProfile: wire.DockerAIProfile, Inference: inference.Config{RootDirectory: filepath.Join(root, "inference"), DockerSocket: "/run/docker.sock"}, Training: training.Config{RootDirectory: filepath.Join(root, "training"), DockerSocket: "/run/docker.sock", ImageReference: os.Getenv("PROJECT_ONE_TRAINING_IMAGE_REFERENCE"), ImageID: os.Getenv("PROJECT_ONE_TRAINING_IMAGE_ID"), WorkerSourceSHA256: os.Getenv("PROJECT_ONE_TRAINING_WORKER_SOURCE")}}
	lease := runtimeTrainingLease()
	lease.Training.Recipe.ImageReference = c.Training.ImageReference
	lease.Training.Recipe.Identity.ImageID = &c.Training.ImageID
	lease.Training.Recipe.Identity.WorkerSourceSHA256 = c.Training.WorkerSourceSHA256
	// Unique task/attempt identifiers remain synthetic; no GPU/container launch occurs in this profile.
	var random [16]byte
	if _, e = rand.Read(random[:]); e != nil {
		t.Fatal(e)
	}
	random[6] = random[6]&0x0f | 0x40
	random[8] = random[8]&0x3f | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", random[:4], random[4:6], random[6:8], random[8:10], random[10:])
	lease.TaskID = id
	lease.Training.AttemptID = id
	if e = state.SaveNew(c.StateDirectory, state.Identity{SiteID: "default", CellID: "default", NodeID: id, Credential: lease.LeaseToken, ControlOrigin: c.ControlOrigin, ExecutionProfile: wire.DockerAIProfile, ProtocolVersion: wire.AIVersion}); e != nil {
		t.Fatal(e)
	}
	m, e := training.New(c.Training, c.StateDirectory, c.Policy())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	j, e := m.Lifecycle.Store.Begin(id, lease.TaskID, lease.LeaseToken, *lease.Training)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	challenge := trainingwire.RemovalChallenge{SchemaVersion: 1, ChallengeID: id, NodeID: id, TaskID: j.TaskID, Command: j.Command, CommandSHA256: j.CommandSHA256, IssuedAt: now, ExpiresAt: now.Add(trainingwire.RemovalChallengeLifetime)}
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
		command := exec.CommandContext(ctx, "/verification/projectone-agent", "remove-fenced-training", "--config", configuration, "--confirm-fenced-node", id, "--training-job-id", j.Command.Scope.JobID, "--challenge-file", challengeFile, "--receipt-file", output)
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
	receipt, e := trainingwire.DecodeRemovalReceipt(raw, time.Now())
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

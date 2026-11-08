package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"testing"
)

func TestProtectedConfigurationDefaultsAndOverrides(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"control_origin": "https://admin.example.invalid", "state_directory": root, "disk_path": root}
	path := filepath.Join(root, "agent.json")
	write := func() {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	c, err := LoadConfig(path)
	if err != nil || c.Policy() != settings.Default() {
		t.Fatal("legacy defaults changed", err)
	}
	input["runtime"] = map[string]any{"control_connections": 3, "backoff_max_seconds": 45}
	write()
	c, err = LoadConfig(path)
	if err != nil || c.Policy().ControlConnections != 3 || c.Policy().BackoffMaxSeconds != 45 || c.Policy().ControlRequestSeconds != 15 {
		t.Fatal("partial override failed", err)
	}
	for _, invalid := range []map[string]any{{"control_connections": 0}, {"unknown_secret": "synthetic"}, {"docker_stop_seconds": 30}} {
		input["runtime"] = invalid
		write()
		if _, err = LoadConfig(path); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}

func TestLifecycleProfileRequiresAllOwnedExecutionConfiguration(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(map[string]any{"control_origin": "https://admin.example.invalid", "state_directory": root, "disk_path": root, "execution_profile": wire.DockerModelLifecycleProfile})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "agent.json")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadConfig(path); e == nil {
		t.Fatal("lifecycle capabilities advertised without their actual executors")
	}
}

func TestLifecycleConfigurationSeparatesAllOwnedRootsAndExplicitProfile(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	os.Chmod(root, 0700)
	train := runtimeTrainingLease().Training
	preparation := runtimePreparationLease().Preparation
	c := Config{ControlOrigin: "https://admin.example.invalid", StateDirectory: root, DiskPath: root, ExecutionProfile: wire.DockerModelLifecycleProfile}
	c.Inference.RootDirectory = filepath.Join(root, "inference-separate")
	// Roots must also be outside the resident state directory.
	c.StateDirectory = filepath.Join(root, "state")
	os.Mkdir(c.StateDirectory, 0700)
	c.Inference.DockerSocket = "/run/docker.sock"
	c.Training.RootDirectory = filepath.Join(root, "training-separate")
	c.Training.DockerSocket = c.Inference.DockerSocket
	c.Training.ImageReference = train.Recipe.ImageReference
	c.Training.ImageID = *train.Recipe.Identity.ImageID
	c.Training.WorkerSourceSHA256 = train.Recipe.Identity.WorkerSourceSHA256
	c.Preparation.RootDirectory = filepath.Join(root, "preparation-separate")
	c.Preparation.DockerSocket = c.Inference.DockerSocket
	c.Preparation.ImageReference = preparation.Recipe.ImageReference
	c.Preparation.ImageID = *preparation.Recipe.Identity.ImageID
	c.Preparation.WorkerSourceSHA256 = preparation.Recipe.Identity.WorkerSourceSHA256
	path := filepath.Join(root, "cleanup.json")
	write := func() {
		raw, e := json.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write()
	if _, e = LoadPreparationCleanupConfig(path); e != nil {
		t.Fatal("explicit offline cleanup configuration", e)
	}
	if _, e = LoadConfig(path); e != nil {
		t.Fatal("complete explicit lifecycle execution configuration refused", e)
	}
	c.Preparation.RootDirectory = c.Training.RootDirectory
	write()
	if _, e = LoadPreparationCleanupConfig(path); e == nil {
		t.Fatal("cross-owned root overlap")
	}
	c.Preparation.RootDirectory = filepath.Join(root, "preparation-separate")
	c.ExecutionProfile = wire.DockerAIProfile
	write()
	if _, e = LoadPreparationCleanupConfig(path); e == nil {
		t.Fatal("implicit old profile upgrade")
	}
}

func TestSessionAddressesAreValidated(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "agent.json")
	load := func(addresses any) (Config, error) {
		raw, err := json.Marshal(map[string]any{"control_origin": "https://agents.example.invalid", "state_directory": root, "disk_path": root, "session_addresses": addresses})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return LoadConfig(path)
	}
	c, err := load([]string{"10.0.0.11:9443", "10.0.0.12:9443"})
	if err != nil || len(c.SessionAddresses) != 2 {
		t.Fatalf("gateways refused: %v", err)
	}
	for _, invalid := range []any{[]string{"10.0.0.11"}, []string{"10.0.0.11:9443", "10.0.0.11:9443"}, "10.0.0.11:9443"} {
		if _, err = load(invalid); err == nil {
			t.Fatalf("%v accepted", invalid)
		}
	}
}

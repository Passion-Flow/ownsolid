//go:build linux && model_lifecycle_container_integration

package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestActualLinuxReceiptRenderedLifecycleConfigurationLoadsProtectedParser(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("PROJECT_ONE_PREPARATION_CONTAINER_FIXTURE") != "isolated-test-only" {
		t.Fatal("isolated root fixture required")
	}
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_RENDERED_AGENT_FILE"))
	if e != nil {
		t.Fatal(e)
	}
	var c Config
	if json.Unmarshal(raw, &c) != nil {
		t.Fatal("actual generated configuration required")
	}
	root := filepath.Dir(c.StateDirectory)
	if !regexp.MustCompile(`^/verification/lifecycle-config-[a-f0-9]{32}$`).MatchString(root) || c.StateDirectory != filepath.Join(root, "state") || c.Inference.RootDirectory != filepath.Join(root, "inference") || c.Training.RootDirectory != filepath.Join(root, "training") || c.Preparation.RootDirectory != filepath.Join(root, "preparation") {
		t.Fatal("new exact isolated generated target paths required")
	}
	if e = os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	for _, name := range []string{"state", "inference", "training", "preparation"} {
		if e = os.Mkdir(filepath.Join(root, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	path := filepath.Join(root, "agent.json")
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("private copied generated input")
	}
	loaded, e := LoadConfig(path)
	if e != nil || loaded.ExecutionProfile != wire.DockerModelLifecycleProfile || loaded.Preparation.OutputTimeoutSeconds() != 47 || loaded.Training.ImageID == "" || loaded.Preparation.ImageID == "" {
		t.Fatal("real parser rejected complete receipt-bound nondefault configuration", e)
	}
	environment := filepath.Join(root, "agent.env")
	if os.WriteFile(environment, []byte("PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS=59\nPROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_MAXIMUM_CPU_THREADS=2\nPROJECT_ONE_AGENT_RUNTIME_BACKOFF_MAX_SECONDS=41\n"), 0600) != nil {
		t.Fatal("private policy fixture")
	}
	overridden, e := LoadConfigWithEnvironment(path, environment)
	if e != nil || overridden.Preparation.OutputTimeoutSeconds() != 59 || overridden.Preparation.Policy().MaximumCPUThreads != 2 || overridden.Policy().BackoffMaxSeconds != 41 {
		t.Fatal("actual Linux explicit policy mapping", e)
	}
	cleanup, e := LoadPreparationCleanupConfigWithEnvironment(path, environment)
	if e != nil || !reflect.DeepEqual(cleanup, overridden) {
		t.Fatal("actual cleanup configuration diverged", e)
	}
	loaded.Preparation.RootDirectory = loaded.Training.RootDirectory
	changed, _ := json.Marshal(loaded)
	if os.WriteFile(path, changed, 0600) != nil {
		t.Fatal("test override")
	}
	if _, e = LoadConfig(path); e == nil {
		t.Fatal("overlapping generated roots accepted")
	}
	t.Log("Actual release-rendered worker identities/nondefault47-second transfer/private config load and overlapping-root refusal through the product Linux parser; no service installation, enrollment, target engine or GPU execution.")
}

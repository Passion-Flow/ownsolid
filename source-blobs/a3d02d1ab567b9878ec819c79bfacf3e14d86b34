package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func lifecycleEnvironmentFixture(t *testing.T) (Config, string, string) {
	t.Helper()
	_, base, _ := registryConfigurationFixture(t)
	root := filepath.Dir(base)
	train, prep := runtimeTrainingLease().Training, runtimePreparationLease().Preparation
	c := Config{ControlOrigin: "https://admin.example.invalid", StateDirectory: filepath.Join(root, "state"), DiskPath: root, ExecutionProfile: wire.DockerModelLifecycleProfile}
	c.Inference.RootDirectory = filepath.Join(root, "inference")
	c.Inference.DockerSocket = "/run/docker.sock"
	c.Training.RootDirectory = filepath.Join(root, "training")
	c.Training.DockerSocket = c.Inference.DockerSocket
	c.Training.ImageReference = train.Recipe.ImageReference
	c.Training.ImageID = *train.Recipe.Identity.ImageID
	c.Training.WorkerSourceSHA256 = train.Recipe.Identity.WorkerSourceSHA256
	c.Preparation.RootDirectory = filepath.Join(root, "preparation")
	c.Preparation.DockerSocket = c.Inference.DockerSocket
	c.Preparation.ImageReference = prep.Recipe.ImageReference
	c.Preparation.ImageID = *prep.Recipe.Identity.ImageID
	c.Preparation.WorkerSourceSHA256 = prep.Recipe.Identity.WorkerSourceSHA256
	if e := os.WriteFile(base, mustJSON(t, c), 0600); e != nil {
		t.Fatal(e)
	}
	return c, base, filepath.Join(root, "explicit.env")
}
func writeEnvironment(t *testing.T, path, raw string) {
	t.Helper()
	if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestLifecycleExplicitEnvironmentNondefaultAndCleanupUseSamePolicy(t *testing.T) {
	c, base, env := lifecycleEnvironmentFixture(t)
	want := c
	want.ControlOrigin = "https://operator.example.invalid"
	want.CAFile = filepath.Join(c.DiskPath, "ca.pem")
	want.JoinTokenFile = filepath.Join(c.DiskPath, "join-token")
	want.StateDirectory = filepath.Join(c.DiskPath, "alternate-state")
	if e := os.Mkdir(want.StateDirectory, 0700); e != nil {
		t.Fatal(e)
	}
	want.DiskPath = want.StateDirectory
	want.Inference.RootDirectory = filepath.Join(c.DiskPath, "alternate-inference")
	want.Inference.DockerSocket = "/run/operator-docker.sock"
	want.Training.RootDirectory = filepath.Join(c.DiskPath, "alternate-training")
	want.Training.DockerSocket = want.Inference.DockerSocket
	want.Preparation.RootDirectory = filepath.Join(c.DiskPath, "alternate-preparation")
	want.Preparation.DockerSocket = want.Inference.DockerSocket
	want.Preparation.OutputSeconds = 47
	p, tp, pp := settings.Default(), c.Training.Policy(), c.Preparation.Policy()
	// Independent admitted variants cover every numeric timeout/resource policy key.
	for _, value := range []any{&p, &tp, &pp} {
		v := reflect.ValueOf(value).Elem()
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			n := f.Int() + 1
			if v.Type().Field(i).Name == "MaximumCPUThreads" {
				n = 7
			}
			if v.Type().Field(i).Name == "InferenceTaskSeconds" {
				n = 1739
			}
			f.SetInt(n)
		}
	}
	want.Runtime = &p
	want.Training.Runtime = &tp
	want.Preparation.Runtime = &pp
	var config map[string]any
	if json.Unmarshal(mustJSON(t, want), &config) != nil {
		t.Fatal("fixture JSON")
	}
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		}
	}
	data, e := os.ReadFile("../../../../api/configuration/v1/agent-environment/schema.json")
	if e != nil || json.Unmarshal(data, &schema) != nil {
		t.Fatal("environment contract", e)
	}
	var lines []string
	for key, property := range schema.Properties {
		if key == registryConfigurationKey || strings.HasPrefix(key, "PROJECT_ONE_AGENT_COMPONENTS_") {
			continue
		}
		path := strings.Split(strings.TrimPrefix(property.Description, "Explicit override for "), ";")[0]
		var value any = config
		for _, part := range strings.Split(path, ".") {
			value = value.(map[string]any)[part]
		}
		var text string
		switch v := value.(type) {
		case string:
			text = string(mustJSON(t, v))
		case float64:
			text = strconv.FormatInt(int64(v), 10)
		default:
			t.Fatal("contract path", path)
		}
		lines = append(lines, key+"="+text)
	}
	if len(lines) != 71 {
		t.Fatal("explicit full lifecycle mapping coverage", len(lines))
	}
	sort.Strings(lines)
	writeEnvironment(t, env, strings.Join(lines, "\n"))
	t.Setenv("PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS", "false")
	loaded, e := LoadConfigWithEnvironment(base, env)
	if e != nil || !reflect.DeepEqual(loaded, want) {
		t.Fatal("nondefault complete typed mapping", e)
	}
	cleanup, e := LoadPreparationCleanupConfigWithEnvironment(base, env)
	if e != nil || !reflect.DeepEqual(cleanup, loaded) {
		t.Fatal("cleanup policy/root diverged", e)
	}
	omitted, e := LoadConfigWithEnvironment(base, "")
	if e != nil || omitted.Preparation.OutputTimeoutSeconds() != 300 || omitted.Preparation.Runtime != nil || omitted.ExecutionProfile != wire.DockerModelLifecycleProfile {
		t.Fatal("ambient override/default changed", e)
	}
}
func TestLifecycleExplicitEnvironmentRejectsUnsafeOrContradictoryOverrides(t *testing.T) {
	_, base, env := lifecycleEnvironmentFixture(t)
	for _, raw := range []string{
		"", "UNKNOWN=false", "PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS=", "PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS=false", "PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS=0", "PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS=3601", "PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS=1.5", "PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS=+4", "PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS=04", "PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS=99999999999999999999", "PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS=47\nPROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS=47", "PROJECT_ONE_AGENT_MODEL_PREPARATION_ROOT_DIRECTORY=$HOME/test", "PROJECT_ONE_AGENT_MODEL_PREPARATION_RUNTIME_STOP_SECONDS=30", "PROJECT_ONE_AGENT_RUNTIME_CONTROL_CONNECTIONS=1", "PROJECT_ONE_AGENT_EXECUTION_PROFILE=docker_ai", "PROJECT_ONE_AGENT_MODEL_PREPARATION_DOCKER_SOCKET=/run/other.sock", "PROJECT_ONE_AGENT_MODEL_PREPARATION_IMAGE_ID=latest",
	} {
		writeEnvironment(t, env, raw)
		if _, e := LoadConfigWithEnvironment(base, env); e == nil {
			t.Fatalf("unsafe override accepted: %q", raw)
		}
	}
	if e := os.WriteFile(base, []byte(`{"unknown":false}`), 0600); e != nil {
		t.Fatal(e)
	}
	writeEnvironment(t, env, "PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS=47")
	if _, e := LoadConfigWithEnvironment(base, env); e == nil {
		t.Fatal("env repaired invalid base")
	}
}
func TestExplicitEnvironmentCannotEnablePreparationOnOlderProfiles(t *testing.T) {
	_, base, _ := registryConfigurationFixture(t)
	env := filepath.Join(filepath.Dir(base), "old.env")
	writeEnvironment(t, env, "PROJECT_ONE_AGENT_MODEL_PREPARATION_OUTPUT_SECONDS=47")
	if _, e := LoadConfigWithEnvironment(base, env); e == nil {
		t.Fatal("implicit profile upgrade")
	}
}

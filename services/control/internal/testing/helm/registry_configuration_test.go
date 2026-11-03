//go:build helm_integration

package helm_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func registryValues(t *testing.T) map[string]any {
	t.Helper()
	v := values(t)
	storage := object(t, v["storage"])
	storage["registry-cas"] = map[string]any{"existingClaim": "synthetic-registry-cas", "mountPath": "/mnt/registry-root"}
	storage["registry-prepared"] = map[string]any{"existingClaim": "synthetic-registry-prepared", "mountPath": "/mnt/registry-root/prepared"}
	set(v, "configuration.policyEnvironmentFile", "registry-policy.env")
	set(v, "secrets.files", append(array(t, at(t, v, "secrets", "files")), "registry-policy.env", "registry.json", "registry-ca.pem", "registry-credential.json", "release-trust.json"))
	return v
}

func TestRegistryHelmPrivatePolicyReadOnlyPreparedAndDisabledOmission(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		v := values(t)
		if enabled {
			v = registryValues(t)
		}
		docs := documents(t, run(t, v, true, "template"))
		validateOfficial(t, docs)
		run(t, v, true, "lint")
		pod := at(t, docs["Deployment"], "spec", "template", "spec")
		control := array(t, at(t, pod, "containers"))[0]
		found := map[string]bool{}
		for _, mount := range array(t, at(t, control, "volumeMounts")) {
			name := at(t, mount, "name")
			if name == "registry-prepared" {
				found["prepared"] = at(t, mount, "mountPath") == "/mnt/registry-root/prepared" && at(t, mount, "readOnly") == true
			}
			if name == "registry-cas" {
				found["cas"] = at(t, mount, "mountPath") == "/mnt/registry-root" && at(t, mount, "readOnly") != true
			}
		}
		if found["cas"] != enabled || found["prepared"] != enabled {
			t.Fatal("registry storage mount ownership changed")
		}
		expected := []any{"serve", "--config", "/run/projectone/private/control.yaml"}
		if enabled {
			expected = append(expected, "--env-file", "/run/projectone/private/registry-policy.env")
		}
		if !reflect.DeepEqual(at(t, control, "args"), expected) {
			t.Fatal("private component policy mapping changed")
		}
	}
}

func TestRegistryHelmRequiresBothClaimsPolicyAndSeparatePaths(t *testing.T) {
	for _, name := range []string{"missing-cas", "missing-prepared", "missing-policy", "undeclared-policy", "same-claim", "private-overlap", "equal-root", "outside-root", "noncanonical", "unknown-field", "false-slot"} {
		t.Run(name, func(t *testing.T) {
			v := registryValues(t)
			storage := object(t, v["storage"])
			switch name {
			case "missing-cas":
				delete(storage, "registry-cas")
			case "missing-prepared":
				delete(storage, "registry-prepared")
			case "missing-policy":
				set(v, "configuration.policyEnvironmentFile", "")
			case "undeclared-policy":
				set(v, "configuration.policyEnvironmentFile", "not-declared.env")
			case "same-claim":
				set(v, "storage.registry-cas.existingClaim", "synthetic-models")
			case "private-overlap":
				set(v, "storage.registry-prepared.mountPath", "/run/projectone/child")
			case "equal-root":
				set(v, "storage.registry-prepared.mountPath", "/mnt/registry-root")
			case "outside-root":
				set(v, "storage.registry-prepared.mountPath", "/mnt/outside")
			case "noncanonical":
				set(v, "storage.registry-cas.mountPath", "/mnt/../other")
			case "unknown-field":
				set(v, "storage.registry-prepared.readOnly", false)
			case "false-slot":
				storage["registry-cas"] = false
			}
			run(t, v, false, "template")
		})
	}
}

func TestRegistryComposeExactBindingsWithoutEngineAccess(t *testing.T) {
	root := t.TempDir()
	privateClient := filepath.Join(root, "client")
	if err := os.Mkdir(privateClient, 0700); err != nil {
		t.Fatal(err)
	}
	plugin := os.Getenv("PROJECT_ONE_COMPOSE_PLUGIN_DIRECTORY")
	if !filepath.IsAbs(plugin) {
		t.Fatal("explicit installed Compose plugin directory required")
	}
	if info, err := os.Stat(filepath.Join(plugin, "docker-compose")); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		t.Fatal("actual Compose plugin required", err)
	}
	var pin struct {
		Version   string `json:"version"`
		Artifacts map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"artifacts"`
	}
	pinBytes, err := os.ReadFile(filepath.Join(repository(t), "tools/dev/toolchain/compose.json"))
	if err != nil || json.Unmarshal(pinBytes, &pin) != nil {
		t.Fatal("versioned Compose pin required", err)
	}
	filename := filepath.Join(plugin, "docker-compose")
	canonical, err := filepath.EvalSymlinks(filename)
	if err != nil || canonical != filename {
		t.Fatal("canonical private Compose artifact required", err)
	}
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	expected := pin.Artifacts[runtime.GOOS+"-"+runtime.GOARCH].SHA256
	if copyErr != nil || closeErr != nil || len(expected) != 64 || hex.EncodeToString(hash.Sum(nil)) != expected {
		t.Fatal("actual Compose artifact differs from the declared host pin")
	}
	rawConfig, err := json.Marshal(map[string]any{"cliPluginsExtraDirs": []string{plugin}})
	if err != nil || os.WriteFile(filepath.Join(privateClient, "config.json"), rawConfig, 0600) != nil {
		t.Fatal("private Compose plugin configuration failed", err)
	}
	environment := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PROJECT_ONE_") && !strings.HasPrefix(entry, "DOCKER_") && !strings.HasPrefix(entry, "COMPOSE_") {
			environment = append(environment, entry)
		}
	}
	inputs := map[string]string{"PROJECT_ONE_COMPOSE_PROJECT": "projectone-registry-render", "PROJECT_ONE_DATA_ROOT": filepath.Join(root, "database"), "PROJECT_ONE_CONFIG_ROOT": filepath.Join(root, "private"), "PROJECT_ONE_CONTROL_IMAGE": "projectone/control@sha256:" + strings.Repeat("a", 64), "PROJECT_ONE_PLATFORM": "linux/amd64", "PROJECT_ONE_RUNTIME_UID": "10033", "PROJECT_ONE_RUNTIME_GID": "10034", "PROJECT_ONE_POLICY_ENVIRONMENT_BASENAME": "registry-policy.env", "PROJECT_ONE_REGISTRY_MOUNT_HOST_ROOT": filepath.Join(root, "cas"), "PROJECT_ONE_REGISTRY_MOUNT_CONTAINER_ROOT": "/mnt/registry-root", "PROJECT_ONE_REGISTRY_PREPARED_HOST_ROOT": filepath.Join(root, "release"), "PROJECT_ONE_REGISTRY_PREPARED_CONTAINER_ROOT": "/mnt/registry-root/prepared"}
	compose := func(missing string) ([]byte, error) {
		env := append([]string{}, environment...)
		for name, value := range inputs {
			if name != missing {
				env = append(env, name+"="+value)
			}
		}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		// An intentionally absent private socket makes accidental engine access fail.
		command := exec.CommandContext(ctx, "docker", "--config", privateClient, "--host", "unix://"+filepath.Join(root, "no-engine.sock"), "compose", "--env-file", "/dev/null", "-f", filepath.Join(repository(t), "deploy/compose/base/compose.yaml"), "-f", filepath.Join(repository(t), "deploy/compose/registry/compose.yaml"), "config", "--format", "json")
		command.Env = env
		return command.CombinedOutput()
	}
	raw, err := compose("")
	var document map[string]any
	if err != nil || json.Unmarshal(raw, &document) != nil {
		t.Fatalf("actual Compose projection failed: %v %s", err, raw)
	}
	control := at(t, document, "services", "control")
	if !reflect.DeepEqual(at(t, control, "command"), []any{"serve", "--config", "/run/projectone/control.yaml", "--env-file", "/run/projectone/registry-policy.env"}) {
		t.Fatal("private policy argv changed")
	}
	found := map[string]bool{}
	for _, mount := range array(t, at(t, control, "volumes")) {
		target := at(t, mount, "target")
		if target == "/mnt/registry-root" || target == "/mnt/registry-root/prepared" {
			expectedSource := inputs["PROJECT_ONE_REGISTRY_MOUNT_HOST_ROOT"]
			if target == "/mnt/registry-root/prepared" {
				expectedSource = inputs["PROJECT_ONE_REGISTRY_PREPARED_HOST_ROOT"]
			}
			if at(t, mount, "source") != expectedSource || at(t, mount, "type") != "bind" || at(t, mount, "bind", "create_host_path") != false || (at(t, mount, "read_only") == true) != (target == "/mnt/registry-root/prepared") {
				t.Fatal("registry bind source, read-only policy or exclusive creation changed")
			}
			found[target.(string)] = true
		}
	}
	if len(found) != 2 {
		t.Fatal("missing registry mounts")
	}
	for name := range inputs {
		if strings.HasPrefix(name, "PROJECT_ONE_REGISTRY_") || name == "PROJECT_ONE_POLICY_ENVIRONMENT_BASENAME" {
			output, err := compose(name)
			if err == nil || !strings.Contains(string(output), "required variable") {
				t.Fatal("missing required input did not fail actual interpolation", name)
			}
		}
	}
}

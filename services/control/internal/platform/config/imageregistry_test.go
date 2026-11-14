package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/registryconfig"
)

// These are configuration controls with explicit synthetic release hashes.
// No registry, release approval, credential use or network request is simulated as acceptance.
func registryConfigurationFixture(t *testing.T) (string, registryconfig.Config) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := registryconfig.Defaults()
	c.Endpoint = "https://sensitive-private-marker.invalid:11443"
	c.AllowedCIDRs = []string{"127.0.0.0/8"}
	c.CAFile = filepath.Join(root, "ca.pem")
	c.CredentialFile = filepath.Join(root, "credential.json")
	c.StoreRoot = filepath.Join(root, "cas")
	c.PreparedReleaseRoot = filepath.Join(root, "prepared")
	c.ReleaseArchiveSHA256 = strings.Repeat("a", 64)
	c.ReleaseManifestSHA256 = strings.Repeat("b", 64)
	c.Verification.MountRoot = root
	c.Verification.TrustFile = filepath.Join(root, "trust.json")
	c.OperationSeconds, c.RequestSeconds, c.GrantSeconds = 73, 31, 49
	c.MaximumPins, c.MaximumReferences, c.MaximumConcurrentImports, c.PageSize = 8, 17, 2, 3
	filename := filepath.Join(root, "component.json")
	writeRegistryConfiguration(t, filename, c)
	return filename, c
}

func writeRegistryConfiguration(t *testing.T, filename string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filename, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func registryParentYAML(t *testing.T, filename string) []byte {
	t.Helper()
	c := validConfig()
	p := DefaultRuntimePolicy()
	c.Runtime = &p
	c.ImageRegistryConfigFile = filename
	raw, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestImageRegistryTypedSelectionOmissionAndPrivateDiagnostics(t *testing.T) {
	filename, expected := registryConfigurationFixture(t)
	t.Setenv(registryConfigEnvironment, "/ambient-must-not-be-read")
	c, err := Decode(registryParentYAML(t, ""))
	if err != nil || c.ImageRegistryConfigFile != "" || c.ImageRegistry != nil {
		t.Fatal("omitted component did not remain disabled", err)
	}
	c, err = Decode(registryParentYAML(t, filename))
	if err != nil || c.ImageRegistry == nil || c.ImageRegistry.OperationSeconds != expected.OperationSeconds || c.ImageRegistry.RequestSeconds != 31 || c.ImageRegistry.PageSize != 3 || c.ImageRegistry.MaximumConcurrentImports != 2 || c.ImageRegistry.Verification.AllowTestFixture || c.ImageRegistry.OperationTimeout() != 73*time.Second {
		t.Fatal("typed nondefault or false configuration changed", err)
	}
	raw, err := json.Marshal(c)
	if err != nil || strings.Contains(string(raw), "sensitive-private-marker") || strings.Contains(string(raw), "maximum_retained_bytes") {
		t.Fatal("resolved private component body entered diagnostics", err)
	}
}

func TestImageRegistryExplicitEnvironmentPrecedenceAndRefusals(t *testing.T) {
	first, _ := registryConfigurationFixture(t)
	second, next := registryConfigurationFixture(t)
	next.RequestSeconds = 32
	writeRegistryConfiguration(t, second, next)
	environment := workspaceEnvironmentFixture(t, registryConfigEnvironment+"="+second+"\n")
	c, err := decodeWithEnvironment(registryParentYAML(t, first), environment)
	if err != nil || c.ImageRegistryConfigFile != second || c.ImageRegistry == nil || c.ImageRegistry.RequestSeconds != 32 {
		t.Fatal("explicit env did not override valid YAML", err)
	}
	for _, input := range []string{registryConfigEnvironment + "=\n", registryConfigEnvironment + "=relative\n", registryConfigEnvironment + "=/run/../private/input\n", registryConfigEnvironment + "=" + first + "\n" + registryConfigEnvironment + "=" + second + "\n", "PROJECT_ONE_REGISTRY_UNKNOWN=false\n"} {
		if _, err := decodeWithEnvironment(registryParentYAML(t, first), workspaceEnvironmentFixture(t, input)); err == nil {
			t.Fatal("ambiguous component env accepted")
		}
	}
}

func TestImageRegistryInvalidYAMLIsNotRepairedByEnvironment(t *testing.T) {
	filename, _ := registryConfigurationFixture(t)
	environment := workspaceEnvironmentFixture(t, registryConfigEnvironment+"="+filename+"\n")
	for _, value := range []string{"null", "false", "123", "[]", "{}", "''", "relative", "'/run/../private/input'"} {
		raw := append(registryParentYAML(t, ""), []byte("\nimage_registry_config_file: "+value+"\n")...)
		if _, err := decodeWithEnvironment(raw, environment); err == nil {
			t.Fatal("invalid YAML repaired by environment", value)
		}
	}
	raw := append(registryParentYAML(t, filename), []byte("\nimage_registry_config_file: "+filename+"\n")...)
	if _, err := decodeWithEnvironment(raw, environment); err == nil {
		t.Fatal("duplicate YAML selector accepted")
	}
}

func TestImageRegistryComponentPrivateFileAndBodyRefusals(t *testing.T) {
	filename, good := registryConfigurationFixture(t)
	for _, body := range []string{`{"unknown_secret":"do-not-print"}`, `{"operation_seconds":null}`, `{"operation_seconds":1,"operation_seconds":2}`, `{"verification":{"allow_test_fixture":true}}`} {
		if err := os.WriteFile(filename, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Decode(registryParentYAML(t, filename))
		if err == nil || strings.Contains(err.Error(), "do-not-print") || strings.Contains(err.Error(), filename) {
			t.Fatal("invalid or unredacted component accepted", err)
		}
	}
	writeRegistryConfiguration(t, filename, good)
	if err := os.Chmod(filename, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(registryParentYAML(t, filename)); err == nil {
		t.Fatal("public configuration accepted")
	}
	if err := os.Chmod(filename, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(filename), "link.json")
	if err := os.Symlink(filename, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(registryParentYAML(t, link)); err == nil {
		t.Fatal("symlink component accepted")
	}
}

func TestImageRegistryActiveStoreSeparationAndContradictions(t *testing.T) {
	filename, value := registryConfigurationFixture(t)
	c := validConfig()
	c.ImageRegistryConfigFile, c.ImageRegistry = filename, &value
	if err := c.validateImageRegistryConfiguration(); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{value.StoreRoot, filepath.Join(value.StoreRoot, "child"), filepath.Dir(value.StoreRoot), value.PreparedReleaseRoot} {
		for _, owner := range []string{"models", "datasets", "training", "preparations"} {
			bad := c
			switch owner {
			case "models":
				bad.Models.ArtifactRoot = root
			case "datasets":
				bad.Datasets.ArtifactRoot = root
			case "training":
				bad.Training.ArtifactRoot = root
			case "preparations":
				bad.ModelPreparation.ArtifactRoot = root
			}
			if bad.validateImageRegistryConfiguration() == nil {
				t.Fatal("active store overlap accepted", owner)
			}
		}
	}
	c.ImageRegistryConfigFile = ""
	if c.validateImageRegistryConfiguration() == nil {
		t.Fatal("resolved component without selector accepted")
	}
	c.ImageRegistryConfigFile, c.ImageRegistry = filename, nil
	if c.validateImageRegistryConfiguration() == nil {
		t.Fatal("selector without resolved component accepted")
	}
}

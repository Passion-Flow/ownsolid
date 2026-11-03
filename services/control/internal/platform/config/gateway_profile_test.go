package config

import (
	"os"
	"testing"
)

func TestGatewayProfileTypedBoundary(t *testing.T) {
	for _, file := range []string{"gateway.example.yaml", "gateway-http.example.yaml"} {
		raw, e := os.ReadFile("../../../config/examples/control/" + file)
		if e != nil {
			t.Fatal(e)
		}
		c, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_APPLICATION_PROFILE=gateway\nPROJECT_ONE_MODELS_ARTIFACT_ROOT=/approved/private-models\nPROJECT_ONE_MODELS_IMPORT_SECONDS=120\n"))
		if e != nil || !c.GatewayOnly() || !c.ComponentRuntime() || !c.NodeRuntime() || !c.WorkspaceRuntime() || c.Models.ArtifactRoot != "/approved/private-models" || c.Policy().Models.ImportSeconds != 120 {
			t.Fatal(c.Server.Profile(), e)
		}
		for _, env := range []string{"PROJECT_ONE_MODELS_ARTIFACT_ROOT=\n", "PROJECT_ONE_MODELS_ARTIFACT_ROOT=/\n", "PROJECT_ONE_MODELS_ARTIFACT_ROOT=relative\n", "PROJECT_ONE_MODELS_ARTIFACT_ROOT=/a/../b\n", "PROJECT_ONE_APPLICATION_PROFILE=false\n", "PROJECT_ONE_MODELS_IMPORT_SECONDS=0\n", "PROJECT_ONE_MODELS_ENABLED=false\n"} {
			if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env)); e == nil {
				t.Fatal("invalid input admitted", env)
			}
		}
		for _, change := range []func(*Config){func(c *Config) { c.ModelCache.Enabled = true }, func(c *Config) { c.Datasets.ArtifactRoot = "/later" }, func(c *Config) { c.Training.ArtifactRoot = "/later" }, func(c *Config) { c.ModelPreparation.ArtifactRoot = "/later" }, func(c *Config) { c.SSO.Enabled = true }} {
			copy := c
			change(&copy)
			if copy.ValidateApplicationProfile() == nil {
				t.Fatal("later owner admitted")
			}
		}
	}
}

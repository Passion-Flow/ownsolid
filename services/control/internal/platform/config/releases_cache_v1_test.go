package config

import (
	"encoding/json"
	"os"
	"testing"
)

func TestReleasesCacheV1ConfigurationPreservesHistoricalProfiles(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/releases-cache-v1.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := decodeWithEnvironment(raw, "")
	if e != nil || !c.ReleasesCacheV1Only() || c.ReleasesOnly() || !c.ReleasesRuntime() || !c.LocalizationRuntime() || !c.AccountRuntime() || !c.ModelsRuntime() || !c.PortalRuntime() || c.ModelCache.Enabled {
		t.Fatal("versioned cumulative graph or default cache selection", e)
	}
	env := workspaceEnvironmentFixture(t, "PROJECT_ONE_MODEL_CACHE_ENABLED=true\nPROJECT_ONE_MODEL_CACHE_TLS=false\nPROJECT_ONE_MODEL_CACHE_ADDRESS=127.0.0.1:44499\n")
	selected, e := decodeWithEnvironment(raw, env)
	if e != nil || !selected.ModelCache.Enabled || selected.ModelCache.TLS || selected.ModelCache.Address != "127.0.0.1:44499" {
		t.Fatal("explicit cache environment/nondefault/false mapping", e)
	}
	old := selected
	profile := "releases"
	old.Server.ApplicationProfile = &profile
	if old.ValidateApplicationProfile() == nil {
		t.Fatal("historical M17 profile admitted cache")
	}
	old.ModelCache.Enabled = false
	if e = old.ValidateApplicationProfile(); e != nil {
		t.Fatal("historical M17 profile changed", e)
	}
	for _, change := range []func(*Config){
		func(v *Config) { v.ReleaseViewConfigFile = "" },
		func(v *Config) { v.TaskEvents.Enabled = true },
		func(v *Config) { v.MongoDB.Enabled = true },
		func(v *Config) { v.AWS.Enabled = true },
		func(v *Config) { v.MonitoringConfigFile = "/run/projectone/future.json" },
		func(v *Config) { v.MiddlewareConfigFile = "/run/projectone/future.json" },
		func(v *Config) { v.PrivateClusterConfigFile = "/run/projectone/future.json" },
		func(v *Config) { v.ImageRegistryConfigFile = "/run/projectone/future.json" },
		func(v *Config) { v.ObjectStorage = &ObjectStorageConfiguration{} },
		func(v *Config) { v.Database.Engine = MySQLDatabaseEngine },
	} {
		v := selected
		change(&v)
		if v.ValidateApplicationProfile() == nil {
			t.Fatal("unready owner or backend admitted")
		}
	}
	for _, bad := range []string{"PROJECT_ONE_APPLICATION_PROFILE=releases-cache-v2\n", "PROJECT_ONE_APPLICATION_PROFILE=\n", "PROJECT_ONE_MODEL_CACHE_ENABLED=TRUE\n", "PROJECT_ONE_MODEL_CACHE_TIMEOUT_MILLISECONDS=0\n", "PROJECT_ONE_MODEL_CACHE_UNKNOWN=false\n"} {
		if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, bad)); e == nil {
			t.Fatal("invalid profile/policy admitted", bad)
		}
	}
	for _, bad := range []string{"null", "false", "releases-cache-v2", "Releases-cache-v1"} {
		if validateServerTransportYAML([]byte("server:\n  application_profile: "+bad+"\n")) == nil {
			t.Fatal("invalid YAML profile admitted", bad)
		}
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	data, e := os.ReadFile("../../../../../api/configuration/v1/control-account/schema.json")
	if e != nil || json.Unmarshal(data, &schema) != nil {
		t.Fatal("profile schema unavailable", e)
	}
	found := 0
	profiles := map[string]bool{}
	for _, profile := range schema.Properties["application_profile"].Enum {
		if profiles[profile] || validateServerTransportYAML([]byte("server:\n  application_profile: "+profile+"\n")) != nil {
			t.Fatal("schema accepts a duplicate or unsupported runtime profile", profile)
		}
		profiles[profile] = true
		if profile == ReleasesCacheV1Profile {
			found++
		}
	}
	if found != 1 {
		t.Fatal("versioned profile absent or duplicated in schema")
	}
	for _, profile := range []string{"account", "workspaces", "nodes", "components", "maintenance", "models", "gateway", "datasets", "training", "lifecycle", "sso", "knowledge", "workflows", "plugins", "portal", "localization", "releases", "platform", ReleasesCacheV1Profile} {
		if !profiles[profile] {
			t.Fatal("runtime profile absent from schema", profile)
		}
	}
}

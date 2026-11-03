package config

import (
	"os"
	"strings"
	"testing"
)

func TestM17CumulativeReleaseProfileStrictBoundary(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/localization-cumulative.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	raw = []byte(strings.Replace(string(raw), "application_profile: localization", "application_profile: releases", 1) + "\nrelease_view_config_file: /run/projectone/admin-view.json\n")
	c, e := decodeWithEnvironment(raw, "")
	if e != nil {
		t.Fatal(e)
	}
	if !c.ReleasesOnly() || c.LocalizationOnly() || !c.LocalizationRuntime() || !c.PortalRuntime() || !c.PluginsRuntime() || !c.WorkflowsRuntime() || !c.KnowledgeRuntime() || !c.SSORuntime() || !c.LifecycleRuntime() || !c.AccountRuntime() {
		t.Fatal("bounded cumulative release graph")
	}
	for _, change := range []func(*Config){func(v *Config) { v.ReleaseViewConfigFile = "" }, func(v *Config) { v.MiddlewareConfigFile = "/run/projectone/future.json" }, func(v *Config) { v.PrivateClusterConfigFile = "/run/projectone/future.json" }, func(v *Config) { v.MonitoringConfigFile = "/run/projectone/future.json" }, func(v *Config) { v.TaskEvents.Enabled = true }, func(v *Config) { v.ModelPreparation = ModelPreparationConfiguration{} }} {
		b := c
		change(&b)
		if b.ValidateApplicationProfile() == nil {
			t.Fatal("missing/future owner admitted")
		}
	}
	for _, old := range []string{"portal", "localization", "plugins", "platform-unknown"} {
		b := c
		b.Server.ApplicationProfile = &old
		if b.ValidateApplicationProfile() == nil {
			t.Fatal("release policy admitted outside bounded graph", old)
		}
	}
	t.Setenv(releaseViewConfigurationEnvironment, "/ambient/ignored.json")
	selected, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_PLUGINS_ENABLED=false\nPROJECT_ONE_RELEASE_VIEW_CONFIG_FILE=/run/projectone/selected-view.json\n"))
	if e != nil || selected.Plugins.Enabled || selected.ReleaseViewConfigFile != "/run/projectone/selected-view.json" {
		t.Fatal("explicit mapping/false", e)
	}
	for _, bad := range []string{"PROJECT_ONE_RELEASE_VIEW_CONFIG_FILE=\n", "PROJECT_ONE_RELEASE_VIEW_CONFIG_FILE=null\n", "PROJECT_ONE_RELEASE_VIEW_UNKNOWN=false\n", "PROJECT_ONE_RELEASE_VIEW_CONFIG_FILE=/a\nPROJECT_ONE_RELEASE_VIEW_CONFIG_FILE=/b\n"} {
		if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, bad)); e == nil {
			t.Fatal("ambiguous policy admitted", bad)
		}
	}
}

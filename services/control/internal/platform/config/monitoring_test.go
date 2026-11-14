package config

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func monitoringParentYAML(t *testing.T, reference string) []byte {
	t.Helper()
	c := validConfig()
	c.MonitoringConfigFile = reference
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestMonitoringExplicitReferenceAndPrivateProjection(t *testing.T) {
	t.Setenv(monitoringConfigurationEnvironment, "/ambient-must-not-be-used")
	c, e := Decode(monitoringParentYAML(t, ""))
	if e != nil || c.MonitoringConfigFile != "" {
		t.Fatal("ambient policy admitted", e)
	}
	first, second := "/private/monitoring/first.json", "/private/monitoring/second.json"
	env := workspaceEnvironmentFixture(t, monitoringConfigurationEnvironment+"="+second+"\n")
	c, e = decodeWithEnvironment(monitoringParentYAML(t, first), env)
	if e != nil || c.MonitoringConfigFile != second {
		t.Fatal("explicit reference precedence", e)
	}
	raw, e := json.Marshal(c)
	if e != nil || strings.Contains(string(raw), "private/monitoring") {
		t.Fatal("private reference exposed", e)
	}
	for _, body := range []string{monitoringConfigurationEnvironment + "=", monitoringConfigurationEnvironment + "=relative", monitoringConfigurationEnvironment + "=/private/../changed", monitoringConfigurationEnvironment + "=" + first + "\n" + monitoringConfigurationEnvironment + "=" + second, "PROJECT_ONE_MONITORING_UNKNOWN=false"} {
		if _, e := decodeWithEnvironment(monitoringParentYAML(t, ""), workspaceEnvironmentFixture(t, body+"\n")); e == nil {
			t.Fatal("invalid environment admitted", body)
		}
	}
}

func TestMonitoringReferenceRejectsWrongTypesAndNoncanonicalPaths(t *testing.T) {
	base := string(monitoringParentYAML(t, ""))
	for _, value := range []string{"null", "false", "7", "[]", "{}", "''", "relative", "/private/../changed.json"} {
		if _, e := Decode([]byte(base + "\nmonitoring_config_file: " + value + "\n")); e == nil {
			t.Fatal("invalid YAML admitted", value)
		}
	}
}

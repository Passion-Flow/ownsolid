package config

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func privateClusterParentYAML(t *testing.T, reference string) []byte {
	t.Helper()
	c := validConfig()
	c.PrivateClusterConfigFile = reference
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestPrivateClusterExplicitReferenceAndPrivateProjection(t *testing.T) {
	t.Setenv(privateClusterConfigurationEnvironment, "/ambient-must-not-be-used")
	c, e := Decode(privateClusterParentYAML(t, ""))
	if e != nil || c.PrivateClusterConfigFile != "" {
		t.Fatal("ambient policy admitted", e)
	}
	first, second := "/private/cluster/first.json", "/private/cluster/second.json"
	env := workspaceEnvironmentFixture(t, privateClusterConfigurationEnvironment+"="+second+"\n")
	c, e = decodeWithEnvironment(privateClusterParentYAML(t, first), env)
	if e != nil || c.PrivateClusterConfigFile != second {
		t.Fatal("explicit reference precedence", e)
	}
	raw, e := json.Marshal(c)
	if e != nil || strings.Contains(string(raw), "private/cluster") {
		t.Fatal("private reference exposed", e)
	}
	for _, body := range []string{privateClusterConfigurationEnvironment + "=", privateClusterConfigurationEnvironment + "=relative", privateClusterConfigurationEnvironment + "=/private/../changed", privateClusterConfigurationEnvironment + "=" + first + "\n" + privateClusterConfigurationEnvironment + "=" + second, "PROJECT_ONE_PRIVATE_CLUSTER_UNKNOWN=false"} {
		if _, e := decodeWithEnvironment(privateClusterParentYAML(t, ""), workspaceEnvironmentFixture(t, body+"\n")); e == nil {
			t.Fatal("invalid environment admitted", body)
		}
	}
}

func TestPrivateClusterReferenceRejectsWrongTypesAndNoncanonicalPaths(t *testing.T) {
	base := string(privateClusterParentYAML(t, ""))
	for _, value := range []string{"null", "false", "7", "[]", "{}", "''", "relative", "/private/../changed.json"} {
		if _, e := Decode([]byte(base + "\nprivate_cluster_config_file: " + value + "\n")); e == nil {
			t.Fatal("invalid YAML admitted", value)
		}
	}
}

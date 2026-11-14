package config

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func releaseViewParentYAML(t *testing.T, reference string) []byte {
	t.Helper()
	c := validConfig()
	c.ReleaseViewConfigFile = reference
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestReleaseViewExplicitReferenceAndPrivateProjection(t *testing.T) {
	t.Setenv(releaseViewConfigurationEnvironment, "/ambient-must-not-be-used")
	c, e := Decode(releaseViewParentYAML(t, ""))
	if e != nil || c.ReleaseViewConfigFile != "" {
		t.Fatal("ambient policy admitted", e)
	}
	first, second := "/private/releaseView/first.json", "/private/releaseView/second.json"
	env := workspaceEnvironmentFixture(t, releaseViewConfigurationEnvironment+"="+second+"\n")
	c, e = decodeWithEnvironment(releaseViewParentYAML(t, first), env)
	if e != nil || c.ReleaseViewConfigFile != second {
		t.Fatal("explicit reference precedence", e)
	}
	raw, e := json.Marshal(c)
	if e != nil || strings.Contains(string(raw), "private/releaseView") {
		t.Fatal("private reference exposed", e)
	}
	for _, body := range []string{releaseViewConfigurationEnvironment + "=", releaseViewConfigurationEnvironment + "=relative", releaseViewConfigurationEnvironment + "=/private/../changed", releaseViewConfigurationEnvironment + "=" + first + "\n" + releaseViewConfigurationEnvironment + "=" + second, "PROJECT_ONE_RELEASE_VIEW_UNKNOWN=false"} {
		if _, e := decodeWithEnvironment(releaseViewParentYAML(t, ""), workspaceEnvironmentFixture(t, body+"\n")); e == nil {
			t.Fatal("invalid environment admitted", body)
		}
	}
}

func TestReleaseViewReferenceRejectsWrongTypesAndNoncanonicalPaths(t *testing.T) {
	base := string(releaseViewParentYAML(t, ""))
	for _, value := range []string{"null", "false", "7", "[]", "{}", "''", "relative", "/private/../changed.json"} {
		if _, e := Decode([]byte(base + "\nrelease_view_config_file: " + value + "\n")); e == nil {
			t.Fatal("invalid YAML admitted", value)
		}
	}
}

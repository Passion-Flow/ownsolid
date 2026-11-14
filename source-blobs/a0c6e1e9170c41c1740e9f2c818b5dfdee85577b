package config

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func middlewareParentYAML(t *testing.T, path, environment string) []byte {
	t.Helper()
	c := validConfig()
	c.MiddlewareConfigFile, c.MiddlewareEnvironmentFile = path, environment
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestMiddlewareTypedReferencesAndExplicitEnvironment(t *testing.T) {
	t.Setenv(middlewareConfigurationEnvironment, "/ambient-must-not-be-used")
	c, e := Decode(middlewareParentYAML(t, "", ""))
	if e != nil || c.MiddlewareConfigFile != "" || c.MiddlewareEnvironmentFile != "" {
		t.Fatal("omission read ambient reference", e)
	}
	first, second, policy := "/private/profiles/first.json", "/private/profiles/second.json", "/private/profiles/policy.env"
	env := workspaceEnvironmentFixture(t, middlewareConfigurationEnvironment+"="+second+"\n"+middlewarePolicyEnvironment+"="+policy+"\n")
	c, e = decodeWithEnvironment(middlewareParentYAML(t, first, ""), env)
	if e != nil || c.MiddlewareConfigFile != second || c.MiddlewareEnvironmentFile != policy {
		t.Fatal("explicit reference precedence lost", e)
	}
	raw, e := json.Marshal(c)
	if e != nil || strings.Contains(string(raw), "private/profiles") {
		t.Fatal("private references leaked", e)
	}
	for _, body := range []string{middlewareConfigurationEnvironment + "=", middlewareConfigurationEnvironment + "=relative", middlewarePolicyEnvironment + "=" + policy, middlewareConfigurationEnvironment + "=" + first + "\n" + middlewareConfigurationEnvironment + "=" + second, "PROJECT_ONE_MIDDLEWARE_UNKNOWN=false", middlewareConfigurationEnvironment + "=" + first + "\n" + middlewarePolicyEnvironment + "=" + first} {
		if _, e := decodeWithEnvironment(middlewareParentYAML(t, "", ""), workspaceEnvironmentFixture(t, body+"\n")); e == nil {
			t.Fatal("invalid reference admitted", body)
		}
	}
}

func TestMiddlewareReferencesRejectNullFalseAliasAndContradiction(t *testing.T) {
	base := string(middlewareParentYAML(t, "", ""))
	for _, field := range []string{"middleware_config_file: null", "middleware_config_file: false", "middleware_config_file: 7", "middleware_config_file: []", "middleware_config_file: relative", "middleware_config_file: /private/../changed.json", "middleware_environment_file: /private/policy.env", "middleware_config_file: /private/input\nmiddleware_environment_file: /private/input"} {
		if _, e := Decode([]byte(base + "\n" + field + "\n")); e == nil {
			t.Fatal("invalid YAML reference admitted", field)
		}
	}
}

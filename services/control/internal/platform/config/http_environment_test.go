package config

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestHTTPRequestEnvironmentDefaultsNondefaultAndBoundary(t *testing.T) {
	t.Setenv(httpRequestEnvironment, "299")
	raw := workspaceConfigInput(t)
	c, e := decodeWithEnvironment(raw, "")
	if e != nil || c.Policy().HTTP.RequestSeconds != DefaultRuntimePolicy().HTTP.RequestSeconds {
		t.Fatal("ambient HTTP request budget changed owned default", e)
	}
	input := validConfig()
	policy := DefaultRuntimePolicy()
	policy.HTTP.ReadHeaderSeconds, policy.HTTP.RequestSeconds = 1, 21
	input.Runtime = &policy
	raw, _ = yaml.Marshal(input)
	for _, count := range []string{"1", "31", "300"} {
		c, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, httpRequestEnvironment+"='"+count+"'\n"))
		if e != nil || c.Policy().HTTP.ReadHeaderSeconds != 1 {
			t.Fatal("valid existing HTTP budget override/boundary failed", e)
		}
		if count == "31" && c.Policy().HTTP.RequestSeconds != 31 {
			t.Fatal("nondefault private request deadline did not reach typed policy")
		}
	}
	input.Runtime.HTTP.RequestSeconds = 0
	invalid, _ := yaml.Marshal(input)
	if _, e := decodeWithEnvironment(invalid, workspaceEnvironmentFixture(t, httpRequestEnvironment+"=31\n")); e == nil || e.Error() != "CONFIG_HTTP_BOUNDS_INVALID" {
		t.Fatal("private override masked invalid YAML HTTP budget", e)
	}
}

func TestHTTPRequestEnvironmentInvalidFalseDuplicateAndRedaction(t *testing.T) {
	for _, value := range []string{"", "false", "0", "301", "-1", "1.5", "9999999999999999999999999", "$(sensitive-value)", "1"} {
		// One second conflicts with the unchanged five-second default header budget.
		if _, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, httpRequestEnvironment+"="+value+"\n")); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" || strings.Contains(e.Error(), "sensitive-value") {
			t.Fatal("invalid HTTP budget/combination accepted or private input leaked", e)
		}
	}
	if _, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, httpRequestEnvironment+"=31\n"+httpRequestEnvironment+"=32\n")); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" {
		t.Fatal("duplicate HTTP policy authority accepted", e)
	}
}

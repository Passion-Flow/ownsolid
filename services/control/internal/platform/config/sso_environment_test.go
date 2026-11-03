package config

import (
	"fmt"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestSSOMaintenanceEnvironmentDefaultsOverridesAndBoundaries(t *testing.T) {
	raw := workspaceConfigInput(t)
	t.Setenv(ssoCleanupEnvironment, "3599")
	c, e := decodeWithEnvironment(raw, "")
	if e != nil || c.Policy().SSO.CleanupSeconds != DefaultSSOPolicy().CleanupSeconds {
		t.Fatal("ambient SSO policy changed owned defaults", e)
	}
	policy := DefaultRuntimePolicy()
	policy.SSO.CleanupSeconds, policy.SSO.CleanupTimeoutSeconds, policy.SSO.CleanupBatchSize, policy.SSO.FlowRetentionDays = 15, 2, 3, 4
	input := validConfig()
	input.Runtime = &policy
	raw, e = yaml.Marshal(input)
	if e != nil {
		t.Fatal(e)
	}
	c, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, ssoCleanupBatchEnvironment+"='2'\n"))
	if e != nil || c.Policy().SSO.CleanupBatchSize != 2 || c.Policy().SSO.FlowRetentionDays != 4 || c.Policy().SSO.CleanupSeconds != 15 || c.Policy().SSO.CleanupTimeoutSeconds != 2 {
		t.Fatal("explicit maintenance override lost typed YAML policies", e)
	}
	for _, body := range []string{
		ssoCleanupEnvironment + "=10\n" + ssoCleanupTimeoutEnvironment + "=1\n" + ssoCleanupBatchEnvironment + "=1\n" + ssoFlowRetentionEnvironment + "=1\n",
		ssoCleanupEnvironment + "=3600\n" + ssoCleanupTimeoutEnvironment + "=30\n" + ssoCleanupBatchEnvironment + "=1000\n" + ssoFlowRetentionEnvironment + "=90\n",
	} {
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body)); e != nil {
			t.Fatal("supported maintenance boundary rejected", e)
		}
	}
	input.Runtime.SSO.CleanupSeconds = 0
	invalid, _ := yaml.Marshal(input)
	if _, e := decodeWithEnvironment(invalid, workspaceEnvironmentFixture(t, ssoCleanupEnvironment+"=60\n")); e == nil || e.Error() != "CONFIG_SSO_POLICY_INVALID" {
		t.Fatal("environment masked invalid YAML maintenance policy", e)
	}
}

func TestSSOMaintenanceEnvironmentRejectsInvalidWithoutSecretDiagnostics(t *testing.T) {
	for _, field := range []struct {
		key              string
		minimum, maximum int
	}{
		{ssoCleanupEnvironment, 10, 3600}, {ssoCleanupTimeoutEnvironment, 1, 30}, {ssoCleanupBatchEnvironment, 1, 1000}, {ssoFlowRetentionEnvironment, 1, 90},
	} {
		for _, value := range []string{"", "false", "-1", fmt.Sprint(field.minimum - 1), fmt.Sprint(field.maximum + 1), "1.5", "$(touch sensitive-value)", "999999999999999999999999"} {
			if _, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, field.key+"="+value+"\n")); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" || strings.Contains(e.Error(), "sensitive-value") {
				t.Fatal("invalid maintenance policy accepted or input leaked", e)
			}
		}
		if _, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, field.key+"=10\n"+field.key+"=20\n")); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" {
			t.Fatal("duplicate maintenance field accepted", e)
		}
	}
	if _, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, "PROJECT_ONE_SSO_UNKNOWN=sensitive-value\n")); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" {
		t.Fatal("unknown maintenance field accepted", e)
	}
}

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInferenceTaskPolicyOverridesAndRefusal(t *testing.T) {
	fields := []struct {
		key                  string
		low, high, alternate int
	}{
		{nodeInferenceDeployEnvironment, 120, 1800, 900},
		{nodeTaskLeaseEnvironment, 10, 30, 17},
		{nodeMaximumTaskAttemptsEnvironment, 1, 3, 2},
		{nodeTaskSweepBatchEnvironment, 1, 1000, 7},
	}
	raw := workspaceConfigInput(t)
	for _, f := range fields {
		t.Run(f.key, func(t *testing.T) {
			t.Setenv(f.key, fmt.Sprint(f.alternate))
			c, e := decodeWithEnvironment(raw, "")
			if e != nil {
				t.Fatal(e)
			}
			actual, defaults := c.Policy(), DefaultRuntimePolicy()
			if *actual.nodeEnvironmentField(f.key) != *defaults.nodeEnvironmentField(f.key) {
				t.Fatal("ambient policy accepted")
			}
			for _, v := range []int{f.low, f.high, f.alternate} {
				body := fmt.Sprintf("%s=120\n%s=%d\n", nodeTaskEnvironment, f.key, v)
				c, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body))
				if e != nil {
					t.Fatal("valid explicit policy refused", e)
				}
				p := c.Policy()
				if *p.nodeEnvironmentField(f.key) != v {
					t.Fatal("typed override lost")
				}
			}
			for _, v := range []string{"", "false", "1.5", fmt.Sprint(f.low - 1), fmt.Sprint(f.high + 1), "99999999999999999999", "$(private-input)"} {
				_, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, f.key+"="+v+"\n"))
				if e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" || strings.Contains(e.Error(), "private-input") {
					t.Fatal("invalid policy accepted or leaked", e)
				}
			}
			if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, f.key+"=20\n"+f.key+"=21\n")); e == nil {
				t.Fatal("duplicate accepted")
			}
		})
	}
	if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, nodeTaskEnvironment+"=900\n"+nodeInferenceDeployEnvironment+"=300\n")); e == nil {
		t.Fatal("contradictory deadline accepted")
	}
}

func TestInferenceTaskPolicyExistingDeploymentMapping(t *testing.T) {
	root := filepath.Clean("../../../../../")
	checks := map[string][]string{
		"deploy/compose/base/workspace-policy.yaml":                    {"--env-file", "PROJECT_ONE_POLICY_ENVIRONMENT_BASENAME"},
		"deploy/helm/projectone/templates/_pod.tpl":                    {"configuration.policyEnvironmentFile", "--env-file"},
		"services/control/config/examples/control/node-policy.helm.values.example.yaml": {"policyEnvironmentFile: node-policy.env", "node-policy.env"},
		"services/control/config/examples/control/node-policy.env.example":              {nodeInferenceDeployEnvironment, nodeTaskLeaseEnvironment, nodeMaximumTaskAttemptsEnvironment, nodeTaskSweepBatchEnvironment},
	}
	for path, needles := range checks {
		raw, e := os.ReadFile(filepath.Join(root, path))
		if e != nil {
			t.Fatal(e)
		}
		for _, needle := range needles {
			if !strings.Contains(string(raw), needle) {
				t.Fatal("deployment policy mapping missing", path, needle)
			}
		}
	}
}

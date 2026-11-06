package config

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

var nodeEnvironmentBounds = []struct {
	key                          string
	minimum, maximum, nondefault int
}{
	{nodeMaximumLiveNodesEnvironment, 1, 10000, 5},
	{nodeMaximumLiveJoinEnvironment, 1, 10000, 3},
	{nodeJoinTTLEnvironment, 60, 3600, 900}, {nodeOnlineEnvironment, 20, 300, 45},
	{nodeOfflineEnvironment, 30, 3600, 180}, {nodeTaskEnvironment, 120, 1800, 400},
	{nodeReconcileEnvironment, 1, 60, 11}, {nodeReconcileTimeoutEnvironment, 1, 30, 6},
	{nodeStateRefreshEnvironment, 30, 3600, 301},
	{uiPageEnvironment, 1, 100, 7}, {uiPollEnvironment, 1000, 60000, 2700},
}

func nodeEnvironmentBody(values map[string]int) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var body strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&body, "%s='%d'\n", k, values[k])
	}
	return body.String()
}
func TestNodeEnvironmentDefaultsOverridesAndBoundaries(t *testing.T) {
	for _, field := range nodeEnvironmentBounds {
		t.Setenv(field.key, fmt.Sprint(field.nondefault))
	}
	raw := workspaceConfigInput(t)
	c, e := decodeWithEnvironment(raw, "")
	if e != nil {
		t.Fatal(e)
	}
	policy, defaults := c.Policy(), DefaultRuntimePolicy()
	for _, field := range nodeEnvironmentBounds {
		if *policy.nodeEnvironmentField(field.key) != *defaults.nodeEnvironmentField(field.key) {
			t.Fatal("ambient node/UI policy changed owned defaults", field.key)
		}
	}
	values := map[string]int{}
	for _, field := range nodeEnvironmentBounds {
		values[field.key] = field.nondefault
	}
	c, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, nodeEnvironmentBody(values)))
	if e != nil {
		t.Fatal("combined nondefault policy refused", e)
	}
	policy = c.Policy()
	for _, field := range nodeEnvironmentBounds {
		if *policy.nodeEnvironmentField(field.key) != field.nondefault {
			t.Fatal("override did not reach typed node/UI policy", field.key)
		}
	}
	for _, field := range nodeEnvironmentBounds {
		t.Run(field.key, func(t *testing.T) {
			for _, value := range []int{field.minimum, field.maximum} {
				t.Run(fmt.Sprint(value), func(t *testing.T) {
					values := map[string]int{nodeOnlineEnvironment: 20, nodeOfflineEnvironment: 3600}
					values[field.key] = value
					c, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, nodeEnvironmentBody(values)))
					if e != nil {
						t.Fatal("supported node/UI bound refused", e)
					}
					policy := c.Policy()
					if *policy.nodeEnvironmentField(field.key) != value {
						t.Fatal("node/UI bound lost")
					}
				})
			}
		})
	}
}
func TestNodeEnvironmentInvalidDuplicateCrossFieldAndRedaction(t *testing.T) {
	raw := workspaceConfigInput(t)
	for _, field := range nodeEnvironmentBounds {
		t.Run(field.key, func(t *testing.T) {
			for index, value := range []string{"", "false", "-1", fmt.Sprint(field.minimum - 1), fmt.Sprint(field.maximum + 1), "1.5", "999999999999999999999999", "$(sensitive-value)"} {
				t.Run(fmt.Sprintf("invalid_%d", index), func(t *testing.T) {
					if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, field.key+"="+value+"\n")); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" || strings.Contains(e.Error(), "sensitive-value") {
						t.Fatal("invalid node/UI policy accepted or input leaked", e)
					}
				})
			}
			if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, field.key+"=20\n"+field.key+"=21\n")); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" {
				t.Fatal("duplicate node/UI authority accepted", e)
			}
		})
	}
	for _, body := range []string{nodeOnlineEnvironment + "=120\n" + nodeOfflineEnvironment + "=120\n", "PROJECT_ONE_NODE_UNKNOWN=sensitive-value\n"} {
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body)); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" || strings.Contains(e.Error(), "sensitive-value") {
			t.Fatal("invalid node combination/unknown field accepted or leaked", e)
		}
	}
}
func TestNodeEnvironmentDoesNotMaskInvalidBaseYAML(t *testing.T) {
	for _, field := range []string{nodeOnlineEnvironment, uiPageEnvironment} {
		input := validConfig()
		policy := DefaultRuntimePolicy()
		*policy.nodeEnvironmentField(field) = 0
		input.Runtime = &policy
		raw, e := yaml.Marshal(input)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, field+"=30\n")); e == nil || !strings.HasPrefix(e.Error(), "CONFIG_") || e.Error() == "CONFIG_POLICY_ENVIRONMENT_INVALID" {
			t.Fatal("override masked invalid base YAML", e)
		}
	}
}

func TestLivenessFlushMustFitTheOnlineWindow(t *testing.T) {
	raw := workspaceConfigInput(t)
	for _, c := range []struct {
		online, flush int
		valid         bool
	}{{20, 3, true}, {20, 4, false}, {60, 10, true}, {60, 11, false}, {60, 0, false}} {
		body := nodeEnvironmentBody(map[string]int{nodeOnlineEnvironment: c.online, nodeLivenessFlushEnvironment: c.flush})
		decoded, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body))
		if c.valid != (e == nil) {
			t.Errorf("online %d flush %d: %v", c.online, c.flush, e)
		}
		if e == nil && decoded.Policy().Nodes.PacingWindowSeconds() != c.online-c.flush {
			t.Errorf("online %d flush %d: window %d", c.online, c.flush, decoded.Policy().Nodes.PacingWindowSeconds())
		}
	}
	values := map[string]int{nodeStateRefreshEnvironment: 29}
	if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, nodeEnvironmentBody(values))); e == nil {
		t.Error("a state refresh under its bound was accepted")
	}
}

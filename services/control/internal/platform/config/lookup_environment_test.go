package config

import (
	"fmt"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestLookupProjectionEnvironmentDefaultsOverridesAndBounds(t *testing.T) {
	t.Setenv(uiLookupEnvironment, "2")
	raw := workspaceConfigInput(t)
	c, e := decodeWithEnvironment(raw, "")
	if e != nil || c.Policy().UI.LookupPageSize != DefaultRuntimePolicy().UI.LookupPageSize {
		t.Fatal("ambient lookup setting changed owned defaults", e)
	}
	input := validConfig()
	policy := DefaultRuntimePolicy()
	policy.UI.LookupPageSize = 7
	input.Runtime = &policy
	raw, e = yaml.Marshal(input)
	if e != nil {
		t.Fatal(e)
	}
	for _, count := range []int{1, 2, 100} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			body := fmt.Sprintf("%s='%d'\n%s=7\n%s=31\n", uiLookupEnvironment, count, uiPageEnvironment, httpRequestEnvironment)
			c, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body))
			if e != nil || c.Policy().UI.LookupPageSize != count || c.Policy().UI.PageSize != 7 || c.Policy().HTTP.RequestSeconds != 31 {
				t.Fatal("combined private lookup override/boundary did not reach typed policy", e)
			}
		})
	}
}

func TestLookupProjectionEnvironmentInvalidAndRedacted(t *testing.T) {
	for index, value := range []string{"", "false", "0", "101", "-1", "1.5", "999999999999999999999999", "$(sensitive-value)", "\"2\" trailing"} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			_, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, uiLookupEnvironment+"="+value+"\n"))
			if e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" || strings.Contains(e.Error(), "sensitive-value") {
				t.Fatal("invalid lookup policy accepted or input exposed", e)
			}
		})
	}
	for index, body := range []string{uiLookupEnvironment + "=2\n" + uiLookupEnvironment + "=3\n", "PROJECT_ONE_UI_LOOKUP_UNKNOWN=sensitive-value\n"} {
		t.Run("key_"+fmt.Sprint(index), func(t *testing.T) {
			_, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, body))
			if e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" || strings.Contains(e.Error(), "sensitive-value") {
				t.Fatal("duplicate/unknown lookup authority accepted or input exposed", e)
			}
		})
	}
}

func TestLookupProjectionEnvironmentCannotRepairInvalidBaseYAML(t *testing.T) {
	for _, invalid := range []int{0, 101} {
		input := validConfig()
		policy := DefaultRuntimePolicy()
		policy.UI.LookupPageSize = invalid
		input.Runtime = &policy
		raw, e := yaml.Marshal(input)
		if e != nil {
			t.Fatal(e)
		}
		_, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, uiLookupEnvironment+"=2\n"))
		if e == nil || e.Error() != "CONFIG_UI_BOUNDS_INVALID" {
			t.Fatal("private override concealed invalid base lookup policy", e)
		}
	}
}

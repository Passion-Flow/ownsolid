package config

import (
	"fmt"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
)

func modelPolicyFields() []struct {
	key, yamlKey          string
	lower, upper, changed int
} {
	return []struct {
		key, yamlKey          string
		lower, upper, changed int
	}{
		{modelImportSecondsEnvironment, "import_seconds", 60, 7200, 90},
		{modelImportPollEnvironment, "import_poll_seconds", 1, 60, 3},
		{modelCleanupEnvironment, "cleanup_seconds", 1, 30, 4},
		{modelMaximumAttemptsEnvironment, "maximum_attempts", 1, 3, 2},
		{modelMaximumBundleEnvironment, "maximum_bundle_bytes", int(modelartifact.MinimumArchiveBytes()), ModelImportSchemaMaximumBundleBytes, 1600000000},
	}
}

func TestModelImportPolicyEnvironment(t *testing.T) {
	raw := workspaceConfigInput(t)
	for _, field := range modelPolicyFields() {
		t.Setenv(field.key, fmt.Sprint(field.changed))
	}
	c, e := decodeWithEnvironment(raw, "")
	if e != nil || c.Policy().Models != DefaultModelsPolicy() {
		t.Fatal("ambient model policy changed owned defaults", e)
	}
	legacy, e := decodeWithEnvironment(append(raw, []byte("\nruntime: null\n")...), "")
	if e != nil || legacy.Policy().Models != DefaultModelsPolicy() {
		t.Fatal("legacy null runtime did not retain owned model defaults", e)
	}
	for _, field := range modelPolicyFields() {
		for _, number := range []int{field.lower, field.changed, field.upper} {
			t.Run(field.yamlKey+"_"+fmt.Sprint(number), func(t *testing.T) {
				input := validConfig()
				policy := DefaultRuntimePolicy()
				*policy.Models.environmentField(field.key) = field.changed
				input.Runtime = &policy
				raw, e := yaml.Marshal(input)
				if e != nil {
					t.Fatal(e)
				}
				body := fmt.Sprintf("%s='%d'\n%s=31\n%s=7\n", field.key, number, httpRequestEnvironment, uiLookupEnvironment)
				c, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body))
				got := c.Policy().Models
				if e != nil || *got.environmentField(field.key) != number || c.Policy().HTTP.RequestSeconds != 31 || c.Policy().UI.LookupPageSize != 7 {
					t.Fatal("private model override/boundary/precedence", e)
				}
			})
		}
	}
}

func TestModelImportPolicyInvalidEnvironment(t *testing.T) {
	for _, field := range modelPolicyFields() {
		for index, value := range []string{"", "false", "-1", "1.5", "999999999999999999999999", "$(sensitive-value)", "\"2\" trailing", fmt.Sprint(field.lower - 1), fmt.Sprint(field.upper + 1)} {
			t.Run(field.yamlKey+"_"+fmt.Sprint(index), func(t *testing.T) {
				_, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, field.key+"="+value+"\n"))
				if e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" || strings.Contains(e.Error(), "sensitive-value") {
					t.Fatal("invalid model input accepted/exposed", e)
				}
			})
		}
	}
	for index, body := range []string{modelMaximumAttemptsEnvironment + "=2\n" + modelMaximumAttemptsEnvironment + "=3\n", "PROJECT_ONE_MODELS_UNKNOWN=sensitive-value\n"} {
		t.Run("key_"+fmt.Sprint(index), func(t *testing.T) {
			_, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, body))
			if e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" {
				t.Fatal("duplicate/unknown model policy", e)
			}
		})
	}
}

func TestModelImportPolicyStrictYAMLAndInvalidBase(t *testing.T) {
	for index, body := range []string{"null", "{maximum_attempts: null}", "{maximum_attempts: false}", "{maximum_attempts: 1.5}", "{maximum_attempts: '2'}", "{maximum_attempts: 2, maximum_attempts: 3}", "{unknown: 2}", "&settings {maximum_attempts: 2}"} {
		t.Run("yaml_"+fmt.Sprint(index), func(t *testing.T) {
			raw := append(workspaceConfigInput(t), []byte("\nruntime:\n  models: "+body+"\n")...)
			_, e := decodeWithEnvironment(raw, "")
			if e == nil || strings.Contains(e.Error(), "settings") {
				t.Fatal("coerced/null/alias/unknown model policy accepted/exposed", e)
			}
		})
	}
	for _, field := range modelPolicyFields() {
		t.Run("base_"+field.yamlKey, func(t *testing.T) {
			input := validConfig()
			policy := DefaultRuntimePolicy()
			*policy.Models.environmentField(field.key) = field.lower - 1
			input.Runtime = &policy
			raw, e := yaml.Marshal(input)
			if e != nil {
				t.Fatal(e)
			}
			_, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, field.key+"="+fmt.Sprint(field.changed)+"\n"))
			if e == nil || e.Error() != "CONFIG_MODELS_BOUNDS_INVALID" {
				t.Fatal("override repaired invalid base YAML", e)
			}
		})
	}
}

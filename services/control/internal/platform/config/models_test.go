package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	"go.yaml.in/yaml/v3"
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

func TestModelImportExpiryConfig(t *testing.T) {
	const workspace = "12345678-1234-4234-8234-123456789abc"
	c := validConfig()
	c.Models.ArtifactRoot = "/var/lib/ownsolid/models"
	raw, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := Decode(raw)
	if err != nil || legacy.Policy().Models.ImportExpiry != nil {
		t.Fatal("legacy configuration enabled expiry", err)
	}
	base := strings.Replace(string(raw), "runtime: null\n", "", 1) + "\nruntime:\n  models:\n    import_expiry: "
	for _, seconds := range []int{3600, 86400, 31536000} {
		input := fmt.Sprintf("%s{workspace_id: %q, inactive_seconds: %d}\n", base, workspace, seconds)
		c, err := Decode([]byte(input))
		want := ModelImportExpiryPolicy{WorkspaceID: workspace, InactiveSeconds: seconds}
		if got := c.Policy().Models.ImportExpiry; err != nil || got == nil || *got != want {
			t.Fatalf("valid expiry policy %d was rejected: %v", seconds, err)
		}
	}
	for name, policy := range map[string]string{
		"null": "null", "empty": "{}", "anchor": "&policy {workspace_id: " + workspace + ", inactive_seconds: 3600}",
		"alias": "*policy", "workspace_only": "{workspace_id: " + workspace + "}", "interval_only": "{inactive_seconds: 3600}",
		"quoted_interval":        "{workspace_id: " + workspace + ", inactive_seconds: '3600'}",
		"fractional_interval":    "{workspace_id: " + workspace + ", inactive_seconds: 3600.5}",
		"interval_below_minimum": "{workspace_id: " + workspace + ", inactive_seconds: 3599}",
		"interval_above_maximum": "{workspace_id: " + workspace + ", inactive_seconds: 31536001}",
		"nil_workspace":          "{workspace_id: 00000000-0000-0000-0000-000000000000, inactive_seconds: 3600}",
		"noncanonical_workspace": "{workspace_id: " + strings.ToUpper(workspace) + ", inactive_seconds: 3600}",
		"invalid_workspace":      "{workspace_id: synthetic-invalid, inactive_seconds: 3600}",
		"duplicate":              "{workspace_id: " + workspace + ", inactive_seconds: 3600, inactive_seconds: 7200}",
		"unknown":                "{workspace_id: " + workspace + ", inactive_seconds: 3600, enabled: true}",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode([]byte(base + policy + "\n")); err == nil || strings.Contains(err.Error(), "synthetic-invalid") {
				t.Fatal("invalid expiry policy accepted or exposed", err)
			}
		})
	}
}

func TestModelImportExpiryRequiresPostgreSQLModelStore(t *testing.T) {
	for _, probe := range []struct{ engine, root string }{
		{PostgreSQLDatabaseEngine, ""}, {PostgreSQLDatabaseEngine, "relative"}, {PostgreSQLDatabaseEngine, "/"},
		{MySQLDatabaseEngine, "/var/lib/ownsolid/models"}, {DamengDatabaseEngine, "/var/lib/ownsolid/models"},
	} {
		c := validConfig()
		c.Database.Engine = probe.engine
		c.Database.MySQLConfigurationFile = "/etc/ownsolid/mysql.yaml"
		c.Models.ArtifactRoot = probe.root
		policy := DefaultRuntimePolicy()
		policy.Models.ImportExpiry = &ModelImportExpiryPolicy{
			WorkspaceID: "12345678-1234-4234-8234-123456789abc", InactiveSeconds: 3600,
		}
		c.Runtime = &policy
		if err := c.validateModelImportExpiry(); err == nil || err.Error() != "CONFIG_MODEL_IMPORT_EXPIRY_REQUIRES_POSTGRESQL_AND_MODEL_STORE" {
			t.Fatalf("expected expiry prerequisite refusal for %q/%q, got %v", probe.engine, probe.root, err)
		}
	}
}

package dameng

import (
	"encoding/json"
	"strings"
	"testing"
)

func schemaTestConfiguration() SchemaConfiguration {
	return SchemaConfiguration{1, "FIXTURE_OWNER", "FIXTURE_RUNTIME", 3, 10, 120, 5, 512, 16384}
}
func TestDamengSchemaConfigurationRejectsInvalidAndFalse(t *testing.T) {
	c := schemaTestConfiguration()
	if c.Validate() != nil {
		t.Fatal("valid schema policy refused")
	}
	for _, mutate := range []func(*SchemaConfiguration){func(c *SchemaConfiguration) { c.Schema = "SYSDBA" }, func(c *SchemaConfiguration) { c.RuntimeUser = c.Schema }, func(c *SchemaConfiguration) { c.StepSeconds = 0 }, func(c *SchemaConfiguration) { c.TotalSeconds = 1 }, func(c *SchemaConfiguration) { c.MaximumMetadataRows = 1 }, func(c *SchemaConfiguration) { c.MaximumMetadataBytes = 1 }, func(c *SchemaConfiguration) { c.RuntimeUser = "INJECT;GRANT DBA" }} {
		bad := c
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid schema policy admitted")
		}
	}
	raw, _ := json.Marshal(c)
	path := odbcPrivatePolicyFixture(t, raw)
	if _, e := LoadSchemaConfiguration(path, ""); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{strings.Replace(string(raw), `"step_seconds":10`, `"step_seconds":false`, 1), strings.TrimSuffix(string(raw), "}") + `,"format_version":1}`, `null`, strings.TrimSuffix(string(raw), "}") + `,"unknown":true}`} {
		path := odbcPrivatePolicyFixture(t, []byte(raw))
		if _, e := LoadSchemaConfiguration(path, ""); e == nil {
			t.Fatal("non-strict schema policy admitted")
		}
	}
}

func TestDamengSchemaEnvironmentExplicitOverride(t *testing.T) {
	c := schemaTestConfiguration()
	raw, _ := json.Marshal(c)
	base := odbcPrivatePolicyFixture(t, raw)
	c.StepSeconds = 12
	raw, _ = json.Marshal(c)
	override := odbcPrivatePolicyFixture(t, raw)
	key := "PROJECT_ONE_DAMENG_SCHEMA_CONFIGURATION_FILE="
	env := odbcPrivatePolicyFixture(t, []byte("# explicit override\n"+key+override+"\n"))
	got, e := LoadSchemaConfiguration(base, env)
	if e != nil || got.StepSeconds != 12 {
		t.Fatal("explicit protected override failed", e)
	}
	for _, body := range []string{"", key + override + "\n" + key + override, "UNRELATED=" + override, key + "relative/config.json", key + "false"} {
		env := odbcPrivatePolicyFixture(t, []byte(body))
		if _, e := LoadSchemaConfiguration(base, env); e == nil {
			t.Fatal("invalid environment accepted")
		}
	}
}

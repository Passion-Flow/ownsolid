package config

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func nativeServingConfigInput(t *testing.T, database string) []byte {
	t.Helper()
	var root yaml.Node
	if e := yaml.Unmarshal(workspaceConfigInput(t), &root); e != nil {
		t.Fatal(e)
	}
	var replacement yaml.Node
	if e := yaml.Unmarshal([]byte(database), &replacement); e != nil {
		t.Fatal(e)
	}
	top := root.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == "database" {
			top.Content[i+1] = replacement.Content[0]
		}
	}
	raw, e := yaml.Marshal(&root)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

const nativeServingTestDatabase = `engine: dameng
dameng:
  runtime_configuration_file: /private/synthetic/runtime.json
  schema_configuration_file: /private/synthetic/schema.json
`

func TestDamengServingTypedBaseAndExplicitEnvironment(t *testing.T) {
	raw := nativeServingConfigInput(t, nativeServingTestDatabase)
	t.Setenv(databaseEngineEnvironment, PostgreSQLDatabaseEngine)
	t.Setenv(damengReadinessEnvironment, "false")
	c, e := decodeWithEnvironment(raw, "")
	if e != nil || c.DatabaseEngine() != DamengDatabaseEngine || c.Database.Dameng == nil || c.Database.Dameng.ReadinessSeconds != DefaultDamengReadinessSeconds {
		t.Fatal("native default or ambient isolation", e)
	}
	original := c.Database.Dameng
	if e = c.applyDamengServingEnvironment(damengReadinessEnvironment, "27"); e != nil || c.Database.Dameng == original || original.ReadinessSeconds != DefaultDamengReadinessSeconds {
		t.Fatal("override mutated shared base", e)
	}
	body := databaseEngineEnvironment + "=dameng\n" + damengRuntimeConfigurationEnvironment + "=/private/explicit/runtime.json\n" + damengSchemaConfigurationEnvironment + "=/private/explicit/schema.json\n" + damengReadinessEnvironment + "=300\n"
	c, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body))
	if e != nil || c.Database.Dameng.ReadinessSeconds != 300 || c.Database.Dameng.RuntimeConfigurationFile != "/private/explicit/runtime.json" || c.Database.Dameng.SchemaConfigurationFile != "/private/explicit/schema.json" {
		t.Fatal("explicit projection", e)
	}
	for _, body := range []string{
		databaseEngineEnvironment + "=postgresql\n",
		mysqlConfigurationEnvironment + "=/private/secret.json\n",
		mysqlMigrationStepEnvironment + "=17\n",
		damengReadinessEnvironment + "=false\n",
		damengReadinessEnvironment + "=0\n",
		damengReadinessEnvironment + "=301\n",
		damengReadinessEnvironment + "=1.5\n",
		damengReadinessEnvironment + "=\n",
		damengReadinessEnvironment + "=1\n" + damengReadinessEnvironment + "=2\n",
		damengRuntimeConfigurationEnvironment + "=relative-secret\n",
		damengRuntimeConfigurationEnvironment + "=/private/synthetic/schema.json\n",
		"PROJECT_ONE_DAMENG_UNKNOWN=secret\n",
	} {
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body)); e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatal("invalid native policy accepted or leaked", e)
		}
	}
	for _, body := range []string{databaseEngineEnvironment + "=dameng\n", damengReadinessEnvironment + "=17\n"} {
		if _, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, body)); e == nil {
			t.Fatal("environment created native selection without a valid base")
		}
	}
}

func TestDamengServingRejectsMalformedBaseBeforeOverride(t *testing.T) {
	for _, database := range []string{
		"engine: dameng\n", "engine: dameng\ndameng: null\n", "engine: dameng\ndameng: false\n",
		strings.Replace(nativeServingTestDatabase, "engine: dameng", "engine: postgresql", 1),
		strings.Replace(nativeServingTestDatabase, "/private/synthetic/runtime.json", "false", 1),
		strings.Replace(nativeServingTestDatabase, "/private/synthetic/runtime.json", "null", 1),
		strings.Replace(nativeServingTestDatabase, "/private/synthetic/runtime.json", "relative/secret", 1),
		strings.Replace(nativeServingTestDatabase, "/private/synthetic/schema.json", "/private/synthetic/runtime.json", 1),
		nativeServingTestDatabase + "  readiness_seconds: false\n",
		nativeServingTestDatabase + "  readiness_seconds: null\n",
		nativeServingTestDatabase + "  readiness_seconds: 0\n",
		nativeServingTestDatabase + "  readiness_seconds: 301\n",
		nativeServingTestDatabase + "  readiness_seconds: 1\n  readiness_seconds: 2\n",
		nativeServingTestDatabase + "  unknown: secret\n",
		nativeServingTestDatabase + "dsn_file: ''\n",
		nativeServingTestDatabase + "max_open_connections: 10\n",
		nativeServingTestDatabase + "lock_timeout_milliseconds: 0\n",
		nativeServingTestDatabase + "mysql_migrations: {}\n",
	} {
		raw := nativeServingConfigInput(t, database)
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, damengReadinessEnvironment+"=10\n")); e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatal("override repaired malformed base or leaked", e)
		}
	}
}

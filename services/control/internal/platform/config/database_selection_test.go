package config

import (
	"fmt"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestDatabaseSelectionLegacyExplicitAndContradictions(t *testing.T) {
	c := validConfig()
	if c.DatabaseEngine() != PostgreSQLDatabaseEngine || c.ValidateDatabaseSelection() != nil {
		t.Fatal("legacy selection lost")
	}
	c.Database.Engine = MySQLDatabaseEngine
	c.Database.MySQLConfigurationFile = "/private/synthetic/mysql.json"
	if e := c.ValidateDatabaseSelection(); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.Database.Engine = "false" }, func(c *Config) { c.Database.Engine = "unknown" }, func(c *Config) { c.Database.MySQLConfigurationFile = "" }, func(c *Config) { c.Database.MySQLConfigurationFile = "relative/mysql.json" }, func(c *Config) { c.Database.DSNFile = "/private/synthetic/pg-dsn" }, func(c *Config) { c.Database.Engine = PostgreSQLDatabaseEngine }} {
		bad := c
		mutate(&bad)
		if e := bad.ValidateDatabaseSelection(); e == nil || strings.Contains(e.Error(), "private/synthetic") {
			t.Fatal("invalid selection accepted or leaked", e)
		}
	}
}
func TestDatabaseSelectionEnvironmentPrecedenceAndRedaction(t *testing.T) {
	raw := workspaceConfigInput(t)
	t.Setenv(databaseEngineEnvironment, "mysql")
	t.Setenv(mysqlConfigurationEnvironment, "/private/ambient/mysql.json")
	c, e := decodeWithEnvironment(raw, "")
	if e != nil || c.DatabaseEngine() != PostgreSQLDatabaseEngine || c.Database.MySQLConfigurationFile != "" {
		t.Fatal("ambient selection accepted", e)
	}
	body := databaseEngineEnvironment + "='mysql'\n" + mysqlConfigurationEnvironment + "='/private/explicit/mysql.json'\n" + mysqlMigrationStepEnvironment + "=17\n" + mysqlMigrationTotalEnvironment + "=90\n"
	c, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body))
	if e != nil || c.DatabaseEngine() != MySQLDatabaseEngine || c.MySQLMigrationPolicy().StepSeconds != 17 || c.MySQLMigrationPolicy().TotalSeconds != 90 {
		t.Fatal("explicit selection lost", e)
	}
	for _, body := range []string{databaseEngineEnvironment + "=false\n", databaseEngineEnvironment + "=\n", databaseEngineEnvironment + "=unknown\n", mysqlConfigurationEnvironment + "=relative-secret\n", databaseEngineEnvironment + "=postgresql\n" + mysqlConfigurationEnvironment + "=/private/secret.json\n", databaseEngineEnvironment + "=mysql\n", databaseEngineEnvironment + "=postgresql\n" + databaseEngineEnvironment + "=mysql\n", "PROJECT_ONE_DATABASE_UNKNOWN=secret-detail\n"} {
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body)); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" || strings.Contains(e.Error(), "secret") {
			t.Fatal("unsafe explicit selection accepted or leaked", e)
		}
	}
}
func TestMySQLMigrationPolicyBoundsAndEnvironment(t *testing.T) {
	fields := []struct {
		key       string
		low, high int
	}{{mysqlMigrationLockEnvironment, 1, 60}, {mysqlMigrationStepEnvironment, 1, 300}, {mysqlMigrationTotalEnvironment, 31, 1800}, {mysqlMigrationCleanupEnvironment, 1, 30}, {mysqlMigrationMetadataEnvironment, 4096, 1 << 20}}
	raw := workspaceConfigInput(t)
	for _, f := range fields {
		t.Run(f.key, func(t *testing.T) {
			for _, v := range []int{f.low, f.high} {
				body := fmt.Sprintf("%s=%d\n", f.key, v)
				if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body)); e != nil {
					t.Fatal("valid migration budget refused", e)
				}
			}
			for _, v := range []string{"", "false", "1.5", fmt.Sprint(f.low - 1), fmt.Sprint(f.high + 1), "99999999999999999999999", "$(sensitive)"} {
				if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, f.key+"="+v+"\n")); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" || strings.Contains(e.Error(), "sensitive") {
					t.Fatal("invalid budget accepted/leaked", e)
				}
			}
		})
	}
	if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, mysqlMigrationTotalEnvironment+"=31\n"+mysqlMigrationLockEnvironment+"=60\n")); e == nil {
		t.Fatal("contradictory migration budget accepted")
	}
}
func TestDatabaseSelectionYAMLBeforeEnvironment(t *testing.T) {
	for _, selection := range []string{"engine: false", "engine: null", "engine: ''", "engine: unknown", "engine: postgresql\nengine: mysql", "mysql_migrations:\n    total_seconds: 0", "mysql_migrations:\n    maximum_metadata_bytes: false"} {
		c := validConfig()
		raw, e := yaml.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		// Modify the real owning database mapping without duplicate top-level YAML.
		raw = []byte(strings.Replace(string(raw), "database:\n", "database:\n    "+strings.ReplaceAll(selection, "\n", "\n    ")+"\n", 1))
		if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, databaseEngineEnvironment+"=postgresql\n")); e == nil || !strings.HasPrefix(e.Error(), "CONFIG_") {
			t.Fatal("override masked invalid base YAML", selection, e)
		}
	}
}

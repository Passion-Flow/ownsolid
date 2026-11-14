package config

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestMySQLRuntimeAccessExplicitHostIntentAndContradictions(t *testing.T) {
	for _, host := range []string{"localhost", "node.example.invalid", "127.0.0.1", "::1", "2001:db8::1"} {
		c := Config{}
		c.Database.Engine = MySQLDatabaseEngine
		c.Database.MySQLConfigurationFile = "/private/synthetic/mysql.json"
		c.Database.MySQLRuntimeAccess = &MySQLRuntimeAccessConfiguration{AccountHost: host, IntentFile: "/private/synthetic/role-intent.json"}
		if e := c.ValidateDatabaseSelection(); e != nil {
			t.Fatal("explicit target refused", e)
		}
		c.Database.Engine = PostgreSQLDatabaseEngine
		c.Database.MySQLConfigurationFile = ""
		if e := c.ValidateDatabaseSelection(); e == nil {
			t.Fatal("MySQL provisioning selected for PostgreSQL")
		}
	}
	for _, host := range []string{"", "%", "127.0.0.%", "_", "true", "false", "::", "0.0.0.0", "224.0.0.1", "::ffff:127.0.0.1", "fe80::1%en0", "'@'host", "node\n.example.invalid", "UPPER.example.invalid"} {
		if (MySQLRuntimeAccessConfiguration{host, "/private/synthetic/intent"}).Validate() == nil {
			t.Fatal("unsafe account host accepted")
		}
	}
	for _, path := range []string{"", "/", "relative", "/private/synthetic/../intent", "/private/synthetic/intent\n"} {
		if (MySQLRuntimeAccessConfiguration{"localhost", path}).Validate() == nil {
			t.Fatal("unsafe intent reference accepted")
		}
	}
}

func TestMySQLRuntimeAccessProtectedEnvironmentIgnoresAmbientAndValidatesPairs(t *testing.T) {
	raw := workspaceConfigInput(t)
	t.Setenv(mysqlRuntimeAccountHostEnvironment, "localhost")
	t.Setenv(mysqlRuntimeIntentFileEnvironment, "/private/ambient/intent")
	if c, e := decodeWithEnvironment(raw, ""); e != nil || c.Database.MySQLRuntimeAccess != nil {
		t.Fatal("ambient provisioning adopted", e)
	}
	prefix := databaseEngineEnvironment + "=mysql\n" + mysqlConfigurationEnvironment + "=/private/synthetic/mysql.json\n"
	body := prefix + mysqlRuntimeAccountHostEnvironment + "='node.example.invalid'\n" + mysqlRuntimeIntentFileEnvironment + "='/private/synthetic/role-intent'\n"
	c, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body))
	if e != nil || c.Database.MySQLRuntimeAccess == nil || c.Database.MySQLRuntimeAccess.AccountHost != "node.example.invalid" || c.Database.MySQLRuntimeAccess.IntentFile != "/private/synthetic/role-intent" {
		t.Fatal("explicit nondefault pair lost", e)
	}
	c.Database.MySQLRuntimeAccess.AccountHost = "before.example.invalid"
	c.Database.MySQLRuntimeAccess.IntentFile = "/private/synthetic/before-intent"
	base, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	overridden, e := decodeWithEnvironment(base, workspaceEnvironmentFixture(t, body))
	if e != nil || overridden.Database.MySQLRuntimeAccess.AccountHost != "node.example.invalid" || overridden.Database.MySQLRuntimeAccess.IntentFile != "/private/synthetic/role-intent" {
		t.Fatal("protected environment did not override independently valid YAML", e)
	}
	for _, body := range []string{prefix + mysqlRuntimeAccountHostEnvironment + "=localhost\n", prefix + mysqlRuntimeIntentFileEnvironment + "=/private/secret\n", prefix + mysqlRuntimeAccountHostEnvironment + "=false\n", prefix + mysqlRuntimeAccountHostEnvironment + "=%\n", prefix + mysqlRuntimeIntentFileEnvironment + "=relative-secret\n", mysqlRuntimeAccountHostEnvironment + "=localhost\n" + mysqlRuntimeIntentFileEnvironment + "=/private/secret\n", prefix + mysqlRuntimeAccountHostEnvironment + "=localhost\n" + mysqlRuntimeAccountHostEnvironment + "=other.invalid\n"} {
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body)); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" || strings.Contains(e.Error(), "secret") {
			t.Fatal("invalid/partial/duplicate environment accepted or leaked", e)
		}
	}
}

func TestMySQLRuntimeAccessYAMLRejectsNullFalseUnknownAndMaskedInvalidBase(t *testing.T) {
	for _, value := range []string{"null", "false", "{}", "{account_host: false, intent_file: /private/synthetic/intent}", "{account_host: localhost, intent_file: null}", "{account_host: localhost, intent_file: /private/synthetic/intent, other: true}", "{account_host: localhost, account_host: other.invalid, intent_file: /private/synthetic/intent}"} {
		c := validConfig()
		raw, e := yaml.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		raw = []byte(strings.Replace(string(raw), "database:\n", "database:\n    mysql_runtime_access: "+value+"\n", 1))
		if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, databaseEngineEnvironment+"=mysql\n"+mysqlConfigurationEnvironment+"=/private/synthetic/mysql.json\n"+mysqlRuntimeAccountHostEnvironment+"=localhost\n"+mysqlRuntimeIntentFileEnvironment+"=/private/synthetic/intent\n")); e == nil || !strings.HasPrefix(e.Error(), "CONFIG_") {
			t.Fatal("invalid YAML masked by environment", value, e)
		}
	}
}

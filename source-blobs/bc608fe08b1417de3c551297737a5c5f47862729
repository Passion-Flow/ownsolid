package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func mysqlBackupFixture() MySQLBackupConfiguration {
	p := DefaultMySQLBackupPolicy()
	p.ClientFile, p.DumpFile = "/private/synthetic/mysql", "/private/synthetic/mysqldump"
	p.ClientSHA256, p.DumpSHA256 = strings.Repeat("a", 64), strings.Repeat("b", 64)
	p.ArtifactManifestFile, p.ScratchRoot = "/private/synthetic/artifacts.json", "/private/synthetic/scratch"
	return p
}

func TestMySQLBackupTypedBoundsAndContradictorySelection(t *testing.T) {
	p := mysqlBackupFixture()
	if e := p.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*MySQLBackupConfiguration){
		func(p *MySQLBackupConfiguration) { p.ClientFile = "relative" }, func(p *MySQLBackupConfiguration) { p.DumpFile = p.ClientFile },
		func(p *MySQLBackupConfiguration) { p.ScratchRoot = "/" }, func(p *MySQLBackupConfiguration) { p.ArtifactManifestFile = "false" },
		func(p *MySQLBackupConfiguration) { p.ClientSHA256 = strings.Repeat("0", 64) }, func(p *MySQLBackupConfiguration) { p.DumpSHA256 = "REPLACE_WITH_ACTUAL_SHA256" },
		func(p *MySQLBackupConfiguration) { p.OperationSeconds = 29 }, func(p *MySQLBackupConfiguration) { p.OperationSeconds = 86401 },
		func(p *MySQLBackupConfiguration) { p.TerminationGraceSeconds = 0 }, func(p *MySQLBackupConfiguration) { p.TerminationGraceSeconds = 61 },
		func(p *MySQLBackupConfiguration) { p.MaximumSQLBytes = (1 << 20) - 1 }, func(p *MySQLBackupConfiguration) { p.MaximumSQLBytes = (1 << 40) + 1 },
		func(p *MySQLBackupConfiguration) { p.MaximumManifestBytes = 4095 }, func(p *MySQLBackupConfiguration) { p.MaximumManifestBytes = (16 << 20) + 1 },
		func(p *MySQLBackupConfiguration) { p.MinimumFreeBytes = (1 << 20) - 1 },
		func(p *MySQLBackupConfiguration) { p.MaximumStoreBytes = (1 << 20) - 1 },
		func(p *MySQLBackupConfiguration) { p.MaximumStoreBytes = (1 << 40) + 1 },
		func(p *MySQLBackupConfiguration) { p.MaximumStoreFiles = 0 },
		func(p *MySQLBackupConfiguration) { p.MaximumStoreFiles = 1000001 },
	} {
		bad := p
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid native backup policy accepted")
		}
	}
	c := Config{}
	c.Database.MySQLBackup = &p
	if c.ValidateDatabaseSelection() == nil {
		t.Fatal("MySQL native policy adopted by PG")
	}
	c.Database.Engine, c.Database.MySQLConfigurationFile = MySQLDatabaseEngine, "/private/synthetic/mysql.json"
	if e := c.ValidateDatabaseSelection(); e != nil {
		t.Fatal(e)
	}
	old, e := json.Marshal(Config{})
	if e != nil || strings.Contains(string(old), "MySQLBackup") {
		t.Fatal("absent policy changed old protected snapshots")
	}
}

func TestMySQLBackupEnvironmentCoversEveryTypedFieldAndIgnoresAmbient(t *testing.T) {
	base := workspaceConfigInput(t)
	p := mysqlBackupFixture()
	p.OperationSeconds, p.TerminationGraceSeconds, p.MaximumSQLBytes, p.MaximumManifestBytes, p.MinimumFreeBytes = 61, 3, 7<<20, 8192, 5<<20
	body := databaseEngineEnvironment + "=mysql\n" + mysqlConfigurationEnvironment + "=/private/synthetic/mysql.json\n"
	value, typ := reflect.ValueOf(p), reflect.TypeOf(p)
	for i := 0; i < value.NumField(); i++ {
		key := mysqlBackupEnvironmentPrefix + strings.ToUpper(typ.Field(i).Tag.Get("yaml"))
		if !databaseSelectionEnvironment(key) {
			t.Fatal("missing explicit owning environment dispatcher", key)
		}
		encoded, e := json.Marshal(value.Field(i).Interface())
		if e != nil {
			t.Fatal(e)
		}
		body += key + "=" + string(encoded) + "\n"
		t.Setenv(key, "false")
	}
	if c, e := decodeWithEnvironment(base, ""); e != nil || c.Database.MySQLBackup != nil {
		t.Fatal("ambient native inputs adopted", e)
	}
	c, e := decodeWithEnvironment(base, workspaceEnvironmentFixture(t, body))
	if e != nil || c.Database.MySQLBackup == nil || *c.Database.MySQLBackup != p {
		t.Fatal("nondefault exact typed environment lost", e)
	}
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	for _, suffix := range []string{"OPERATION_SECONDS=false", "OPERATION_SECONDS=0", "OPERATION_SECONDS=+60", "OPERATION_SECONDS=060", "MAXIMUM_SQL_BYTES=1099511627777", "UNKNOWN=1", "CLIENT_FILE=", "CLIENT_SHA256=false"} {
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, mysqlBackupEnvironmentPrefix+suffix+"\n")); e == nil {
			t.Fatal("invalid/false/unknown native environment accepted", suffix)
		}
	}
	if _, e := decodeWithEnvironment(base, workspaceEnvironmentFixture(t, body+mysqlBackupEnvironmentPrefix+"CLIENT_FILE=/private/duplicate\n")); e == nil {
		t.Fatal("duplicate native setting accepted")
	}
}

func TestMySQLBackupYAMLStrictBeforeEnvironment(t *testing.T) {
	for _, value := range []string{"null", "false", "{}", "{client_file: false}", "{client_file: /private/x, unknown: true}", "{operation_seconds: 30, operation_seconds: 60}"} {
		c := validConfig()
		raw, e := yaml.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		raw = []byte(strings.Replace(string(raw), "database:\n", "database:\n    mysql_backup: "+value+"\n", 1))
		if _, e = decodeWithEnvironment(raw, ""); e == nil {
			t.Fatal("invalid native YAML accepted", value)
		}
	}
}

func TestMySQLBackupYAMLOmittedPolicyDefaultsAndExplicitEnvironmentPrecedence(t *testing.T) {
	c := validConfig()
	c.Database.Engine, c.Database.DSNFile, c.Database.MySQLConfigurationFile = MySQLDatabaseEngine, "", "/private/synthetic/mysql.json"
	p := mysqlBackupFixture()
	c.Database.MySQLBackup = &p
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	var document map[string]any
	if e = yaml.Unmarshal(raw, &document); e != nil {
		t.Fatal(e)
	}
	database := document["database"].(map[string]any)
	backup := database["mysql_backup"].(map[string]any)
	for _, field := range []string{"operation_seconds", "termination_grace_seconds", "maximum_store_bytes", "maximum_store_files", "maximum_sql_bytes", "maximum_manifest_bytes", "minimum_free_bytes"} {
		delete(backup, field)
	}
	raw, e = yaml.Marshal(document)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := Decode(raw)
	if e != nil || parsed.Database.MySQLBackup == nil || *parsed.Database.MySQLBackup != p {
		t.Fatal("omitted native execution defaults lost", e)
	}
	parsed, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, mysqlBackupEnvironmentPrefix+"OPERATION_SECONDS=73\n"))
	if e != nil || parsed.Database.MySQLBackup.OperationSeconds != 73 || parsed.Database.MySQLBackup.TerminationGraceSeconds != p.TerminationGraceSeconds {
		t.Fatal("explicit native override did not preserve other defaults", e)
	}
}

package databaseaccess

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func mysqlLiteralInput(t *testing.T) MySQLRuntimeInput {
	t.Helper()
	password, e := NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	c := config.Config{}
	c.Database.Engine = config.MySQLDatabaseEngine
	c.Database.MySQLConfigurationFile = "/private/synthetic/mysql.json"
	c.Database.MySQLRuntimeAccess = &config.MySQLRuntimeAccessConfiguration{AccountHost: "localhost", IntentFile: "/private/synthetic/runtime-intent.json"}
	return MySQLRuntimeInput{configuration: c, password: password, fingerprint: strings.Repeat("a", 64), connection: mysqlstore.Configuration{Host: "localhost", Port: 3306, Database: "synthetic_test", User: "synthetic_runtime", PasswordFile: "/private/synthetic/password", CAFile: "/private/synthetic/ca.pem", ServerName: "localhost", ExpectedVersion: "8.4.8", Policy: mysqlstore.DefaultPolicy()}}
}

func TestMySQLRuntimeExactGrantMatrixAndLiteralRefusal(t *testing.T) {
	expected := mysqlExpectedGrants()
	if len(applicationTables) != 85 || len(mysqlVariantTables) != 22 || len(mysqlTables()) != 108 || len(expected) != 433 {
		t.Fatal("duplicate/missing explicit table grants")
	}
	if !expected["schema_migrations:SELECT"] || !expected["model_publications:UPDATE"] {
		t.Fatal("journal/MySQL publication owner missing")
	}
	for _, table := range append(mysqlTables(), "schema_migrations") {
		for _, privilege := range []string{"CREATE", "DROP", "ALTER", "TRIGGER", "REFERENCES", "GRANT OPTION", "EXECUTE"} {
			if expected[table+":"+privilege] {
				t.Fatal("runtime receives elevated authority")
			}
		}
	}
	for _, privilege := range []string{"INSERT", "UPDATE", "DELETE"} {
		if expected["schema_migrations:"+privilege] {
			t.Fatal("runtime may mutate the migration journal")
		}
	}
	i := mysqlLiteralInput(t)
	intent := mysqlRuntimeIntent{Format: mysqlRuntimeIntentFormat, ID: uuid.NewString(), TargetSHA256: strings.Repeat("b", 64), InputSHA256: i.fingerprint}
	sha := strings.Repeat("c", 64)
	statement, e := mysqlCreateStatement(i, intent, sha)
	if e != nil {
		t.Fatal(e)
	}
	for _, required := range []string{"IDENTIFIED WITH caching_sha2_password BY '", "REQUIRE SSL", "MAX_USER_CONNECTIONS ", "PASSWORD EXPIRE NEVER ACCOUNT LOCK ATTRIBUTE '", intent.ID, sha} {
		if !strings.Contains(statement, required) {
			t.Fatal("missing fixed account invariant")
		}
	}
	if fmt.Sprint(i) != "MYSQL_RUNTIME_INPUT_REDACTED" || fmt.Sprintf("%#v", i) != "MYSQL_RUNTIME_INPUT_REDACTED" {
		t.Fatal("credential-bearing input formatting not redacted")
	}
	for _, change := range []func(*MySQLRuntimeInput){func(v *MySQLRuntimeInput) { v.password = "' OR 1=1 --" }, func(v *MySQLRuntimeInput) { v.connection.User = "root'@'host" }, func(v *MySQLRuntimeInput) { v.connection.Database = "db`; DROP USER root" }, func(v *MySQLRuntimeInput) { v.connection.Policy.MaximumOpenConnections = 0 }} {
		bad := i
		change(&bad)
		if _, e = mysqlCreateStatement(bad, intent, sha); e == nil {
			t.Fatal("unsafe account statement constructed")
		}
	}
	for _, change := range []func(*mysqlRuntimeIntent){func(v *mysqlRuntimeIntent) { v.ID = "' dangerous" }, func(v *mysqlRuntimeIntent) { v.Format = "postgresql" }, func(v *mysqlRuntimeIntent) { v.InputSHA256 = strings.Repeat("d", 64) }, func(v *mysqlRuntimeIntent) { v.TargetSHA256 = "' dangerous" }} {
		bad := intent
		change(&bad)
		if _, e = mysqlCreateStatement(i, bad, sha); e == nil {
			t.Fatal("foreign/unsafe intent constructed")
		}
	}
	if _, e = mysqlCreateStatement(i, intent, "' unsafe"); e == nil {
		t.Fatal("unsafe attribute constructed")
	}
}

func TestMySQLRuntimeGrantsMatchActual265Catalog(t *testing.T) {
	type object struct {
		File   string `json:"file"`
		Object string `json:"object"`
		Kind   string `json:"kind"`
	}
	var tables []string
	count := 0
	for _, name := range []string{"reference.json", "evolution.json"} {
		raw, e := assets.Files.ReadFile(name)
		var document struct {
			Journal object   `json:"journal"`
			Steps   []object `json:"steps"`
		}
		if e != nil || json.Unmarshal(raw, &document) != nil {
			t.Fatal("actual MySQL catalog", e)
		}
		count += len(document.Steps)
		if name == "reference.json" {
			if document.Journal.Kind != "create_table" || document.Journal.Object != "schema_migrations" {
				t.Fatal("journal identity changed")
			}
			document.Steps = append(document.Steps, document.Journal)
		}
		for _, item := range document.Steps {
			if item.Kind != "create_table" {
				continue
			}
			raw, e := assets.Files.ReadFile(item.File)
			if e != nil || !strings.HasPrefix(string(raw), "CREATE TABLE "+item.Object+" (") {
				t.Fatal("grant object differs from actual DDL", item.File, e)
			}
			tables = append(tables, item.Object)
		}
	}
	sort.Strings(tables)
	wanted := append(mysqlTables(), "schema_migrations")
	sort.Strings(wanted)
	if count != 265 || len(tables) != 109 || !reflect.DeepEqual(tables, wanted) {
		t.Fatalf("MySQL grants differ from exact active tables: %v / %v", tables, wanted)
	}
	actual := mysqlExpectedGrants()
	for _, table := range tables {
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			wanted := table != "schema_migrations" || privilege == "SELECT"
			if actual[table+":"+privilege] != wanted {
				t.Fatal("missing or elevated table grant", table, privilege)
			}
		}
	}
}

func TestMySQLRuntimeProtectedInputChangesAndMissingOwnerRefuse(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	path := writeRuntimeConfiguration(t, root, "")
	base, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	c, e := config.Decode(base)
	if e != nil {
		t.Fatal(e)
	}
	i := mysqlLiteralInput(t)
	i.connection.PasswordFile = filepath.Join(root, "password")
	i.connection.CAFile = filepath.Join(root, "ca.pem")
	c.Database.Engine = config.MySQLDatabaseEngine
	c.Database.DSNFile = ""
	c.Database.MySQLConfigurationFile = filepath.Join(root, "mysql.json")
	c.Database.MySQLRuntimeAccess = &config.MySQLRuntimeAccessConfiguration{AccountHost: "localhost", IntentFile: filepath.Join(root, "intent.json")}
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	configuration, e := json.Marshal(i.connection)
	if e != nil {
		t.Fatal(e)
	}
	// CA bytes are an explicit parser fixture, not a TLS or native acceptance.
	inputs := map[string][]byte{path: raw, c.Database.MySQLConfigurationFile: configuration, i.connection.PasswordFile: []byte(i.password), i.connection.CAFile: []byte("synthetic parser CA fixture")}
	for name, raw := range inputs {
		if e = os.WriteFile(name, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	input, e := LoadMySQLRuntime(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = input.verify(); e != nil {
		t.Fatal(e)
	}
	for name, raw := range inputs {
		if e = os.WriteFile(name, append(append([]byte{}, raw...), '\n'), 0600); e != nil {
			t.Fatal(e)
		}
		if e = input.verify(); e == nil || strings.Contains(e.Error(), i.password) {
			t.Fatal("changed input accepted or leaked")
		}
		if e = os.WriteFile(name, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if status, e := CheckMySQL(t.Context(), nil, input); e == nil || status != "" {
		t.Fatal("missing actual installation accepted")
	}
	if status, e := PrepareMySQL(t.Context(), nil, MySQLRuntimeInput{}); e == nil || status != "" {
		t.Fatal("missing private runtime input accepted")
	}
	if e = os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadMySQLRuntime(path); e == nil {
		t.Fatal("public control snapshot accepted")
	}
}

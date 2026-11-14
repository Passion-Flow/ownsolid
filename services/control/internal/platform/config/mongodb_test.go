package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mongoDBFixture() MongoDBConfiguration {
	p := DefaultMongoDBConfiguration()
	p.Enabled = true
	p.Sources = []MongoDBSource{{ID: "training", TenantID: "11111111-1111-4111-8111-111111111111", WorkspaceID: "22222222-2222-4222-8222-222222222222", URIFile: "/run/private/mongo-uri", TLS: true, Database: "training", Collection: "examples", Fields: []string{"question", "answer"}}}
	return p
}
func TestMongoDBConfigurationBoundsAndScope(t *testing.T) {
	if e := mongoDBFixture().Validate(); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*MongoDBConfiguration){
		func(p *MongoDBConfiguration) { p.TimeoutSeconds = 0 }, func(p *MongoDBConfiguration) { p.BatchSize = 1001 }, func(p *MongoDBConfiguration) { p.MaximumConcurrent = 3 }, func(p *MongoDBConfiguration) { p.MaximumAttempts = 21 }, func(p *MongoDBConfiguration) { p.Enabled = false },
		func(p *MongoDBConfiguration) { p.Sources[0].TenantID = "*" }, func(p *MongoDBConfiguration) { p.Sources[0].WorkspaceID = "" }, func(p *MongoDBConfiguration) { p.Sources[0].Fields = []string{"$where"} }, func(p *MongoDBConfiguration) { p.Sources[0].Fields = []string{"a", "a"} }, func(p *MongoDBConfiguration) { p.Sources[0].URIFile = "relative" }, func(p *MongoDBConfiguration) { p.Sources[0].Collection = "system.users" }, func(p *MongoDBConfiguration) { p.Sources = append(p.Sources, p.Sources[0]) },
	} {
		p := mongoDBFixture()
		mutate(&p)
		if p.Validate() == nil {
			t.Fatal("invalid MongoDB configuration accepted")
		}
	}
}
func TestMongoDBPrivateConfigurationStrictAndFalse(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	path := filepath.Join(dir, "mongodb.json")
	write := func(raw []byte) {
		t.Helper()
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	p := mongoDBFixture()
	raw, _ := json.Marshal(p)
	write(raw)
	c := Config{MongoDBConfigFile: path}
	if e := c.resolveMongoDBConfiguration(); e != nil || !c.MongoDB.Enabled {
		t.Fatal(e)
	}
	disabled, _ := json.Marshal(DefaultMongoDBConfiguration())
	write(disabled)
	if e := c.resolveMongoDBConfiguration(); e != nil || c.MongoDB.Enabled {
		t.Fatal("false not preserved", e)
	}
	for _, bad := range []string{strings.Replace(string(raw), `"enabled":true`, `"enabled":"false"`, 1), strings.Replace(string(raw), `"enabled":true`, `"enabled":true,"enabled":false`, 1), strings.Replace(string(raw), `"enabled":true`, `"unknown_secret":"do-not-print","enabled":true`, 1), strings.Replace(string(raw), `"enabled":true,`, "", 1)} {
		write([]byte(bad))
		e := c.resolveMongoDBConfiguration()
		if e == nil || strings.Contains(e.Error(), "do-not-print") {
			t.Fatal("invalid or unredacted configuration", e)
		}
	}
	write(raw)
	os.Chmod(path, 0644)
	if c.resolveMongoDBConfiguration() == nil {
		t.Fatal("public configuration accepted")
	}
}
func TestMongoDBEnvironmentUsesExplicitFileOnly(t *testing.T) {
	c := defaultConfiguration()
	t.Setenv(mongoDBConfigEnvironment, "/must-not-be-read")
	if e := c.resolveWorkspaceEnvironment(""); e != nil {
		t.Fatal(e)
	}
	if c.MongoDBConfigFile != "" {
		t.Fatal("ambient environment read")
	}
	file := workspaceEnvironmentFixture(t, mongoDBConfigEnvironment+"='/run/projectone/private/mongodb.json'\n")
	if e := c.resolveWorkspaceEnvironment(file); e != nil || c.MongoDBConfigFile != "/run/projectone/private/mongodb.json" {
		t.Fatal(e)
	}
	for _, body := range []string{mongoDBConfigEnvironment + "=\n", mongoDBConfigEnvironment + "=a\n" + mongoDBConfigEnvironment + "=b\n"} {
		if c.resolveWorkspaceEnvironment(workspaceEnvironmentFixture(t, body)) == nil {
			t.Fatal("empty/duplicate reference accepted")
		}
	}
}

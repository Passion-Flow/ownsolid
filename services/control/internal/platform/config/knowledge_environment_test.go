package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestM12CumulativeProfileProtectedEnvironmentAndLifetime(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/knowledge-cumulative.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := decodeWithEnvironment(raw, "")
	if e != nil || !c.KnowledgeOnly() || !c.SSORuntime() || !c.LifecycleRuntime() || !c.AccountRuntime() || c.SSOOnly() || c.LifecycleOnly() {
		t.Fatal("cumulative profile", e)
	}
	// Nondefault complete deployment input, with decimal score and explicit false.
	env := "PROJECT_ONE_KNOWLEDGE_ENABLED=false\nPROJECT_ONE_KNOWLEDGE_MINIMUM_SCORE=-0.125\nPROJECT_ONE_KNOWLEDGE_WORKER_THREADS=2\nPROJECT_ONE_KNOWLEDGE_QUERY_SECONDS=50\nPROJECT_ONE_KNOWLEDGE_PYTHON_EXECUTABLE=/approved/bin/python3.12\nPROJECT_ONE_KNOWLEDGE_WORKER_FILE=/approved/code/embed.py\nPROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY=/approved/encoder\nPROJECT_ONE_KNOWLEDGE_SCRATCH_ROOT=/private/knowledge-scratch\n"
	got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env))
	if e != nil || got.Knowledge.Enabled || got.Policy().Knowledge.MinimumScore != -0.125 || got.Policy().Knowledge.WorkerThreads != 2 || got.Policy().Knowledge.QuerySeconds != 50 || got.Knowledge.ModelDirectory != "/approved/encoder" {
		t.Fatal("nondefault same artifact", e)
	}
	t.Setenv("PROJECT_ONE_KNOWLEDGE_ENABLED", "true")
	got, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env))
	if e != nil || got.Knowledge.Enabled {
		t.Fatal("ambient setting gained authority", e)
	}
	// Each policy field traverses the complete protected loader, not only a setter.
	v := reflect.ValueOf(c.Policy().Knowledge)
	for i := 0; i < v.NumField(); i++ {
		key := "PROJECT_ONE_KNOWLEDGE_" + strings.ToUpper(v.Type().Field(i).Tag.Get("yaml"))
		value := fmt.Sprint(v.Field(i).Interface())
		got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, key+"="+value+"\n"))
		if e != nil || got.Policy().Knowledge != c.Policy().Knowledge {
			t.Fatal("unmapped deployment field", key, e)
		}
		for _, bad := range []string{"", "false", "null", "NaN", "1e999", "99999999999999999999999999999"} {
			if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, key+"="+bad+"\n")); e == nil {
				t.Fatal("invalid policy admitted", key, bad)
			}
		}
	}
	private := t.TempDir()
	os.Chmod(private, 0700)
	file := filepath.Join(private, "gateways.json")
	write := func(s string) {
		t.Helper()
		if e := os.WriteFile(file, []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(`[{"origin":"https://gateway.example.invalid:9443","dial_addresses":["127.0.0.1"],"ca_file":"/private/gateway.pem"}]`)
	reference := "PROJECT_ONE_KNOWLEDGE_GATEWAYS_FILE=" + file + "\n"
	got, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, reference))
	if e != nil || len(got.Knowledge.Gateways) != 1 || got.Knowledge.Gateways[0].CAFile != "/private/gateway.pem" {
		t.Fatal("protected trust inventory", e)
	}
	write(`[]`)
	got, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, reference))
	if e != nil || len(got.Knowledge.Gateways) != 0 {
		t.Fatal("retrieval-only inventory", e)
	}
	for _, bad := range []string{`null`, `[null]`, `[{"origin":"https://one.invalid","origin":"https://two.invalid"}]`, `[{"Origin":"https://one.invalid"}]`, `[{"unknown":true}]`, `[{"origin":null}]`, `[{"origin":"https://gateway.invalid","dial_addresses":["0.0.0.0"]}]`, `[{"origin":"http://gateway.invalid","dial_addresses":["127.0.0.1"]}]`} {
		write(bad)
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, reference)); e == nil {
			t.Fatal("invalid gateway inventory admitted")
		}
	}
	for _, bad := range []string{"PROJECT_ONE_KNOWLEDGE_UNKNOWN=1\n", "PROJECT_ONE_KNOWLEDGE_ENABLED=FALSE\n", "PROJECT_ONE_KNOWLEDGE_ENABLED=1\n", "PROJECT_ONE_KNOWLEDGE_ENABLED=false\nPROJECT_ONE_KNOWLEDGE_ENABLED=false\n", "PROJECT_ONE_KNOWLEDGE_SCRATCH_ROOT=/var/lib/projectone/models\n", "PROJECT_ONE_HTTP_REQUEST_SECONDS=70\n", "PROJECT_ONE_HTTP_STARTUP_SECONDS=59\n", "PROJECT_ONE_HTTP_SHUTDOWN_SECONDS=10\n", "PROJECT_ONE_KNOWLEDGE_LEASE_SECONDS=240\n"} {
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, bad)); e == nil {
			t.Fatal("invalid/cross-field deployment input", bad)
		}
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.Workflows.Enabled = true }, func(c *Config) { c.Plugins.Enabled = true }, func(c *Config) { c.ModelPreparation = ModelPreparationConfiguration{} }, func(c *Config) { c.Training = TrainingConfiguration{} }, func(c *Config) { c.MongoDB.Enabled = true }} {
		bad := c
		mutate(&bad)
		if bad.ValidateApplicationProfile() == nil {
			t.Fatal("future or missing owner admitted")
		}
	}
}

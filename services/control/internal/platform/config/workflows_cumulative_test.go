package config

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestM13CumulativeCompositionAndFutureRefusal(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/workflows-cumulative.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := decodeWithEnvironment(raw, "")
	if e != nil {
		t.Fatal(e)
	}
	if !c.WorkflowsOnly() || !c.KnowledgeRuntime() || !c.SSORuntime() || !c.LifecycleRuntime() || !c.AccountRuntime() || c.KnowledgeOnly() || c.SSOOnly() || c.LifecycleOnly() {
		t.Fatal("composition boundary")
	}
	for _, change := range []func(*Config){func(c *Config) { c.Plugins.Enabled = true }, func(c *Config) { c.MongoDB.Enabled = true }, func(c *Config) { c.TaskEvents.Enabled = true }, func(c *Config) { c.ModelPreparation = ModelPreparationConfiguration{} }, func(c *Config) { c.Training = TrainingConfiguration{} }} {
		b := c
		change(&b)
		if b.ValidateApplicationProfile() == nil {
			t.Fatal("future/missing owner admitted")
		}
	}
	c.Workflows.Enabled = false
	c.Knowledge.Enabled = false
	c.SSO.Enabled = false
	if e = c.ValidateApplicationProfile(); e != nil {
		t.Fatal("disabled local recovery", e)
	}
}

func TestM13CumulativeEnvironmentSchemaAndStrictDeploymentInputs(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/workflows-cumulative.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := decodeWithEnvironment(raw, "")
	if e != nil {
		t.Fatal(e)
	}
	var schema struct {
		Properties map[string]struct {
			Environment string `json:"x-environment"`
			Path        string `json:"x-runtime-path"`
			Default     any    `json:"default"`
		}
	}
	b, e := os.ReadFile("../../../../../api/configuration/v1/control-workflows/schema.json")
	if e != nil || json.Unmarshal(b, &schema) != nil {
		t.Fatal(e)
	}
	example, e := os.ReadFile("../../../config/examples/control/workflows-policy.env.example")
	if e != nil {
		t.Fatal(e)
	}
	v := reflect.ValueOf(c.Policy().Workflows)
	if len(schema.Properties) != v.NumField()+1 {
		t.Fatal("schema field coverage")
	}
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Tag.Get("yaml")
		key := "PROJECT_ONE_WORKFLOWS_" + strings.ToUpper(name)
		value := fmt.Sprint(v.Field(i).Interface())
		got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, key+"="+value+"\n"))
		if e != nil || got.Policy().Workflows != c.Policy().Workflows || schema.Properties[name].Environment != key || schema.Properties[name].Path != "runtime.workflows."+name || schema.Properties[name].Default != float64(v.Field(i).Int()) || !strings.Contains(string(example), key+"="+value+"\n") {
			t.Fatal("schema/env owner drift", name, e)
		}
		for _, bad := range []string{"", "false", "null", "1.5", "9999999999999999999999999"} {
			if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, key+"="+bad+"\n")); e == nil {
				t.Fatal("malformed integer admitted", name)
			}
		}
	}
	env := "PROJECT_ONE_WORKFLOWS_ENABLED=false\nPROJECT_ONE_WORKFLOWS_MAXIMUM_RUNS=11\nPROJECT_ONE_WORKFLOWS_GRAPH_BYTES=8192\n"
	t.Setenv("PROJECT_ONE_WORKFLOWS_ENABLED", "true")
	got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env))
	if e != nil || got.Workflows.Enabled || got.Policy().Workflows.MaximumRuns != 11 || got.Policy().Workflows.GraphBytes != 8192 {
		t.Fatal("same artifact false/nondefault", e)
	}
	for _, bad := range []string{"PROJECT_ONE_WORKFLOWS_ENABLED=FALSE\n", "PROJECT_ONE_WORKFLOWS_ENABLED=1\n", "PROJECT_ONE_WORKFLOWS_ENABLED=null\n", "PROJECT_ONE_WORKFLOWS_ENABLED=false\nPROJECT_ONE_WORKFLOWS_ENABLED=false\n", "PROJECT_ONE_WORKFLOWS_UNKNOWN=1\n", "PROJECT_ONE_WORKFLOWS_RUN_SECONDS=100\n", "PROJECT_ONE_WORKFLOWS_LEASE_SECONDS=180\n", "PROJECT_ONE_KNOWLEDGE_ENABLED=false\n"} {
		if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, bad)); e == nil {
			t.Fatal("invalid/contradictory deployment admitted", bad)
		}
	}
	for _, bad := range []string{"workflows: null\n", "workflows: {enabled: null}\n", "workflows: {enabled: false, enabled: true}\n", "workflows: {enabled: false, unknown: 1}\n", "runtime: {workflows: null}\n"} {
		if _, e = decodeWithEnvironment([]byte(bad), ""); e == nil {
			t.Fatal("malformed YAML admitted", bad)
		}
	}
}

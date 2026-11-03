package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestDatasetsProfileProtectedEnvironment(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/datasets.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_DATASETS_ARTIFACT_ROOT=/approved/data\nPROJECT_ONE_DATASETS_PYTHON_EXECUTABLE=/approved/python3.12\nPROJECT_ONE_DATASETS_PROCESSOR_FILE=/approved/process.py\n"))
	if e != nil || !c.DatasetsOnly() || !c.GatewayRuntime() || !c.ModelsRuntime() || c.Datasets.ArtifactRoot != "/approved/data" {
		t.Fatal(c, e)
	}
	var walk func(reflect.Value, string)
	walk = func(value reflect.Value, prefix string) {
		typ := value.Type()
		for i := 0; i < value.NumField(); i++ {
			f := value.Field(i)
			name := prefix + strings.ToUpper(typ.Field(i).Tag.Get("yaml"))
			if f.Kind() == reflect.Struct {
				walk(f, name+"_")
				continue
			}
			key := "PROJECT_ONE_DATASETS_" + name
			if name == prefix {
				key = "PROJECT_ONE_DATASETS_" + prefix + strings.ToUpper(typ.Field(i).Tag.Get("json"))
			}
			if !c.Policy().Datasets.environmentKey(key) {
				t.Fatal(key)
			}
			n := f.Int() + 1
			if strings.HasSuffix(key, "MAXIMUM_SOURCE_BYTES") {
				n = f.Int() - 1
			}
			if strings.HasSuffix(key, "MAXIMUM_ERRORS") {
				n = f.Int() - 1
			}
			env := fmt.Sprintf("%s=%d\n", key, n)
			got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env))
			if e != nil {
				t.Fatal(key, e)
			}
			want := c.Policy().Datasets
			if want.applyEnvironment(key, fmt.Sprint(n)) != nil || !reflect.DeepEqual(got.Policy().Datasets, want) {
				t.Fatal("unmapped", key)
			}
			for _, v := range []string{"false", "true", "", "-1", "99999999999999999999999999"} {
				if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, key+"="+v+"\n")); e == nil {
					t.Fatal("invalid", key, v)
				}
			}
			if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env+env)); e == nil {
				t.Fatal("duplicate", key)
			}
		}
	}
	walk(reflect.ValueOf(DefaultDatasetPolicy()), "")
	for _, env := range []string{"PROJECT_ONE_DATASETS_UNKNOWN=1\n", "PROJECT_ONE_DATASETS_ARTIFACT_ROOT=/var/lib/projectone/models/nested\n", "PROJECT_ONE_DATASETS_PROCESSOR_FILE=relative\n", "PROJECT_ONE_DATASETS_ARTIFACT_ROOT=\n"} {
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env)); e == nil {
			t.Fatal("invalid group admitted", env)
		}
	}
	for _, change := range []func(*Config){func(c *Config) { c.Training.ArtifactRoot = "/later" }, func(c *Config) { c.MongoDB.Enabled = true }, func(c *Config) { c.ModelPreparation.ArtifactRoot = "/later" }} {
		v := c
		change(&v)
		if v.ValidateApplicationProfile() == nil {
			t.Fatal("later owner admitted")
		}
	}
}

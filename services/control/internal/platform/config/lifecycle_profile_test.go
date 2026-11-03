package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLifecycleProfileProtectedEnvironment(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/lifecycle.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_TRAINING_ARTIFACT_ROOT=/approved/training\n"))
	if e != nil || !c.LifecycleOnly() || !c.GatewayRuntime() || !c.ModelsRuntime() || c.Training.ArtifactRoot != "/approved/training" || !c.TrainingRuntime() {
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
			key := "PROJECT_ONE_MODEL_LIFECYCLE_" + name
			if name == prefix {
				key = "PROJECT_ONE_MODEL_LIFECYCLE_" + prefix + strings.ToUpper(typ.Field(i).Tag.Get("json"))
			}
			if !c.Policy().ModelLifecycle.environmentKey(key) {
				t.Fatal(key)
			}
			n := f.Int() + 1
			if strings.HasSuffix(key, "MAXIMUM_OUTPUT_FILES") || strings.HasSuffix(key, "MAXIMUM_OUTPUT_BYTES") {
				n = f.Int() - 1
			}
			env := fmt.Sprintf("%s=%d\n", key, n)
			got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env))
			if e != nil {
				t.Fatal(key, e)
			}
			want := c.Policy().ModelLifecycle
			if want.applyEnvironment(key, fmt.Sprint(n)) != nil || !reflect.DeepEqual(got.Policy().ModelLifecycle, want) {
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
	walk(reflect.ValueOf(DefaultModelLifecyclePolicy()), "")
	for key, value := range map[string]string{
		"PROJECT_ONE_MODEL_PREPARATION_ARTIFACT_ROOT":        "/approved/preparations",
		"PROJECT_ONE_MODEL_PREPARATION_IMAGE_REFERENCE":      "projectone/model-lifecycle@sha256:" + strings.Repeat("1", 64),
		"PROJECT_ONE_MODEL_PREPARATION_IMAGE_ID":             "sha256:" + strings.Repeat("2", 64),
		"PROJECT_ONE_MODEL_PREPARATION_WORKER_SOURCE_SHA256": strings.Repeat("3", 64),
	} {
		got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, key+"="+value+"\n"))
		if e != nil {
			t.Fatal(key, e)
		}
		want := c.ModelPreparation
		want.applyEnvironment(key, value)
		if got.ModelPreparation != want {
			t.Fatal("unmapped artifact", key)
		}
	}
	for _, env := range []string{"PROJECT_ONE_MODEL_LIFECYCLE_UNKNOWN=1\n", "PROJECT_ONE_MODEL_PREPARATION_ARTIFACT_ROOT=/var/lib/projectone/models/nested\n", "PROJECT_ONE_MODEL_PREPARATION_ARTIFACT_ROOT=relative\n", "PROJECT_ONE_MODEL_PREPARATION_ARTIFACT_ROOT=\n", "PROJECT_ONE_MODEL_PREPARATION_IMAGE_ID=latest\n", "PROJECT_ONE_MODEL_PREPARATION_IMAGE_REFERENCE=projectone/model-lifecycle:latest\n", "PROJECT_ONE_MODEL_PREPARATION_WORKER_SOURCE_SHA256=BAD\n"} {
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env)); e == nil {
			t.Fatal("invalid group admitted", env)
		}
	}
	for _, change := range []func(*Config){func(c *Config) { c.MongoDB.Enabled = true }, func(c *Config) { c.ModelPreparation = ModelPreparationConfiguration{} }, func(c *Config) { c.Training = TrainingConfiguration{} }} {
		v := c
		change(&v)
		if v.ValidateApplicationProfile() == nil {
			t.Fatal("future owner or missing dependency admitted")
		}
	}
}

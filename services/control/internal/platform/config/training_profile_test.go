package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestTrainingProfileProtectedEnvironment(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/training.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_TRAINING_ARTIFACT_ROOT=/approved/training\n"))
	if e != nil || !c.TrainingOnly() || !c.GatewayRuntime() || !c.ModelsRuntime() || c.Training.ArtifactRoot != "/approved/training" {
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
			key := "PROJECT_ONE_TRAINING_" + name
			if name == prefix {
				key = "PROJECT_ONE_TRAINING_" + prefix + strings.ToUpper(typ.Field(i).Tag.Get("json"))
			}
			if !c.Policy().Training.environmentKey(key) {
				t.Fatal(key)
			}
			n := f.Int() + 1
			if strings.HasSuffix(key, "MAXIMUM_OUTPUT_FILES") {
				n = f.Int() - 1
			}
			env := fmt.Sprintf("%s=%d\n", key, n)
			got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env))
			if e != nil {
				t.Fatal(key, e)
			}
			want := c.Policy().Training
			if want.applyEnvironment(key, fmt.Sprint(n)) != nil || !reflect.DeepEqual(got.Policy().Training, want) {
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
	walk(reflect.ValueOf(DefaultTrainingPolicy()), "")
	for key, value := range map[string]string{
		"PROJECT_ONE_TRAINING_IMAGE_REFERENCE":      "projectone/training@sha256:" + strings.Repeat("d", 64),
		"PROJECT_ONE_TRAINING_IMAGE_ID":             "sha256:" + strings.Repeat("e", 64),
		"PROJECT_ONE_TRAINING_WORKER_SOURCE_SHA256": strings.Repeat("f", 64),
	} {
		got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, key+"="+value+"\n"))
		if e != nil {
			t.Fatal(key, e)
		}
		want := c.Training
		want.applyEnvironment(key, value)
		// The root override above was only in the independent first load.
		want.ArtifactRoot = "/var/lib/projectone/training"
		if got.Training != want {
			t.Fatal("unmapped manufactured identity", key)
		}
	}
	for _, env := range []string{"PROJECT_ONE_TRAINING_UNKNOWN=1\n", "PROJECT_ONE_TRAINING_ARTIFACT_ROOT=/var/lib/projectone/models/nested\n", "PROJECT_ONE_TRAINING_ARTIFACT_ROOT=relative\n", "PROJECT_ONE_TRAINING_ARTIFACT_ROOT=\n", "PROJECT_ONE_TRAINING_IMAGE_ID=latest\n", "PROJECT_ONE_TRAINING_IMAGE_REFERENCE=projectone/training:latest\n", "PROJECT_ONE_TRAINING_WORKER_SOURCE_SHA256=BAD\n"} {
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env)); e == nil {
			t.Fatal("invalid group admitted", env)
		}
	}
	for _, change := range []func(*Config){func(c *Config) { c.MongoDB.Enabled = true }, func(c *Config) { c.ModelPreparation.ArtifactRoot = "/later" }, func(c *Config) { c.Training = TrainingConfiguration{} }} {
		v := c
		change(&v)
		if v.ValidateApplicationProfile() == nil {
			t.Fatal("later owner or missing training admitted")
		}
	}
}

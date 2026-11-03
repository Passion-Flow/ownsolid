package config

import (
	"os"
	"testing"
)

func TestMVPPostgreSQLCPUV1SelectsFullGraphAndRequiredOwners(t *testing.T) {
	raw, err := os.ReadFile("../../../config/examples/control/mvp-postgresql-cpu-v1.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !c.MVPPostgreSQLCPUV1Only() || !c.FullPlatformRuntime() || c.AccountRuntime() || c.ReleasesRuntime() {
		t.Fatal("wrong graph")
	}
	for _, mutate := range []func(*Config){
		func(v *Config) { v.Database.Engine = MySQLDatabaseEngine },
		func(v *Config) { v.ReleaseViewConfigFile = "" },
		func(v *Config) { v.Models.ArtifactRoot = "" },
		func(v *Config) { v.Datasets.ProcessorFile = "" },
		func(v *Config) { v.Training.ArtifactRoot = "" },
		func(v *Config) { v.ModelPreparation.ArtifactRoot = "" },
		func(v *Config) { v.Server.TransportMode = TransportHTTP },
	} {
		v := c
		mutate(&v)
		if v.ValidateApplicationProfile() == nil {
			t.Fatal("missing required owner admitted")
		}
	}
	env := workspaceEnvironmentFixture(t, "PROJECT_ONE_APPLICATION_PROFILE=mvp-postgresql-cpu-v1\nPROJECT_ONE_MODEL_CACHE_ENABLED=false\n")
	c, err = decodeWithEnvironment(raw, env)
	if err != nil || c.ModelCache.Enabled || c.AccountRuntime() {
		t.Fatal("typed explicit false/profile", err)
	}
	for _, bad := range []string{"null", "false", "mvp-postgresql-cpu-v2", "MVP-postgresql-cpu-v1"} {
		if validateServerTransportYAML([]byte("server:\n  application_profile: "+bad+"\n")) == nil {
			t.Fatal("bad profile", bad)
		}
	}
}

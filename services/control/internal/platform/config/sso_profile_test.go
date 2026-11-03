package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestM11CumulativeProfileAndCompleteProtectedEnvironment(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/sso-cumulative.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := decodeWithEnvironment(raw, "")
	if e != nil || !c.SSOOnly() || !c.LifecycleRuntime() || !c.TrainingRuntime() || !c.DatasetRuntime() || !c.AccountRuntime() || c.LifecycleOnly() {
		t.Fatal("cumulative composition", e)
	}
	for _, enabled := range []string{"true", "false"} {
		got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_SSO_ENABLED="+enabled+"\n"))
		if e != nil || got.SSO.Enabled != (enabled == "true") {
			t.Fatal("boolean authority", e)
		}
	}
	policy := reflect.ValueOf(DefaultSSOPolicy())
	for i := 0; i < policy.NumField(); i++ {
		name := "PROJECT_ONE_SSO_" + strings.ToUpper(policy.Type().Field(i).Tag.Get("yaml"))
		n := int(policy.Field(i).Int()) + 1
		got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, fmt.Sprintf("%s=%d\n", name, n)))
		want := c.Policy().SSO
		want.applyMaintenanceEnvironment(name, n)
		if e != nil || got.Policy().SSO != want {
			t.Fatal("unmapped field", name, e)
		}
		for _, value := range []string{"", "false", "-1", "99999999999999999999999999"} {
			if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, name+"="+value+"\n")); e == nil {
				t.Fatal("invalid field", name)
			}
		}
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, name+"=1\n"+name+"=1\n")); e == nil {
			t.Fatal("duplicate", name)
		}
	}
	private := t.TempDir()
	os.Chmod(private, 0700)
	inventory := filepath.Join(private, "providers.json")
	valid := `[{"origin":"https://provider.example.invalid:9443","issuer":"https://provider.example.invalid:9443","dial_addresses":["127.0.0.1"],"ca_file":"/private/ca.pem"}]`
	if e := os.WriteFile(inventory, []byte(valid), 0600); e != nil {
		t.Fatal(e)
	}
	env := "PROJECT_ONE_SSO_ENABLED=true\nPROJECT_ONE_SSO_KEYRING_FILE=/private/current-keyring.json\nPROJECT_ONE_SSO_PROVIDERS_FILE=" + inventory + "\n"
	got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env))
	if e != nil || got.SSO.Providers[0].Origin != "https://provider.example.invalid:9443" || got.SSO.KeyringFile != "/private/current-keyring.json" {
		t.Fatal("provider reference", e)
	}
	for _, invalid := range []string{`null`, `[] trailing`, `[{"origin":"https://one.invalid","origin":"https://two.invalid"}]`, `[{"Origin":"https://one.invalid"}]`, `[{"unknown":true}]`, `[{"origin":null}]`} {
		os.WriteFile(inventory, []byte(invalid), 0600)
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, env)); e == nil {
			t.Fatal("invalid inventory admitted")
		}
	}
	for _, invalid := range []string{"PROJECT_ONE_SSO_UNKNOWN=1\n", "PROJECT_ONE_SSO_ENABLED=TRUE\n", "PROJECT_ONE_SSO_ENABLED=1\n", "PROJECT_ONE_SSO_KEYRING_FILE=relative\n", "PROJECT_ONE_APPLICATION_PROFILE=lifecycle\nPROJECT_ONE_SSO_ENABLED=true\n"} {
		if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, invalid)); e == nil {
			t.Fatal("invalid mode admitted")
		}
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.Knowledge.Enabled = true }, func(c *Config) { c.ModelPreparation = ModelPreparationConfiguration{} }, func(c *Config) { c.MongoDB.Enabled = true }, func(c *Config) { c.Training = TrainingConfiguration{} }} {
		bad := c
		mutate(&bad)
		if bad.ValidateApplicationProfile() == nil {
			t.Fatal("future owner or missing cumulative dependency")
		}
	}
}

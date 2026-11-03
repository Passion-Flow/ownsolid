package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
	"reflect"
	"strings"
	"testing"
)

func TestM16CumulativeCompositionAndStrictEnvironment(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/localization-cumulative.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	c, e := decodeWithEnvironment(raw, "")
	if e != nil {
		t.Fatal(e)
	}
	if !c.LocalizationOnly() || c.PortalOnly() || !c.PortalRuntime() || !c.PluginsRuntime() || c.PluginsOnly() || !c.WorkflowsRuntime() || !c.KnowledgeRuntime() || !c.AccountRuntime() || !c.SSORuntime() || !c.LifecycleRuntime() || c.WorkflowsOnly() {
		t.Fatal("bounded composition")
	}
	for _, change := range []func(*Config){func(v *Config) { v.TaskEvents.Enabled = true }, func(v *Config) { v.MongoDB.Enabled = true }, func(v *Config) { v.ModelPreparation = ModelPreparationConfiguration{} }} {
		b := c
		change(&b)
		if b.ValidateApplicationProfile() == nil {
			t.Fatal("future/missing owner admitted")
		}
	}
	var schema struct {
		Properties map[string]struct {
			Environment string `json:"x-environment"`
			Path        string `json:"x-runtime-path"`
			Default     any    `json:"default"`
		}
	}
	b, e := os.ReadFile("../../../../../api/configuration/v1/control-portal/schema.json")
	if e != nil || json.Unmarshal(b, &schema) != nil {
		t.Fatal(e)
	}
	example, e := os.ReadFile("../../../config/examples/control/portal-policy.env.example")
	if e != nil {
		t.Fatal(e)
	}
	policy := reflect.ValueOf(c.Policy().Portal)
	if len(schema.Properties) != policy.NumField() {
		t.Fatal("policy coverage")
	}
	for i := 0; i < policy.NumField(); i++ {
		name := policy.Type().Field(i).Tag.Get("yaml")
		key := "PROJECT_ONE_PORTAL_" + strings.ToUpper(name)
		value := fmt.Sprint(policy.Field(i).Interface())
		got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, key+"="+value+"\n"))
		if e != nil || got.Policy().Portal != c.Policy().Portal || schema.Properties[name].Environment != key || schema.Properties[name].Path != "runtime.portal."+name || schema.Properties[name].Default != float64(policy.Field(i).Int()) || !strings.Contains(string(example), key+"="+value+"\n") {
			t.Fatal("policy mapping", name, e)
		}
		for _, bad := range []string{"", "false", "null", "1.5", "99999999999999999999999"} {
			if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, key+"="+bad+"\n")); e == nil {
				t.Fatal("bad policy admitted", name)
			}
		}
	}
	gotPortal, err := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_PORTAL_COMMIT_SECONDS=7\nPROJECT_ONE_PORTAL_NOTIFICATION_RETENTION_DAYS=14\nPROJECT_ONE_PORTAL_NOTIFICATION_MAINTENANCE_BATCH=25\nPROJECT_ONE_PORTAL_NOTIFICATION_MAINTENANCE_SECONDS=45\n"))
	if err != nil || gotPortal.Policy().Portal != (PortalPolicy{7, 14, 25, 45}) {
		t.Fatal("nondefault portal mapping", err)
	}
	for _, invalid := range []string{"PROJECT_ONE_PORTAL_UNKNOWN=1\n", "PROJECT_ONE_PORTAL_COMMIT_SECONDS=0\n", "PROJECT_ONE_PORTAL_COMMIT_SECONDS=31\n", "PROJECT_ONE_PORTAL_COMMIT_SECONDS=1\nPROJECT_ONE_PORTAL_COMMIT_SECONDS=1\n", "PROJECT_ONE_PORTAL_NOTIFICATION_RETENTION_DAYS=366\n", "PROJECT_ONE_PORTAL_NOTIFICATION_MAINTENANCE_BATCH=1001\n", "PROJECT_ONE_PORTAL_NOTIFICATION_MAINTENANCE_SECONDS=3601\n", "PROJECT_ONE_PORTAL_COMMIT_SECONDS=30\nPROJECT_ONE_HTTP_REQUEST_SECONDS=20\n"} {
		if _, err := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, invalid)); err == nil {
			t.Fatal("invalid portal override admitted", invalid)
		}
	}
	got, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_PLUGINS_ENABLED=false\nPROJECT_ONE_PLUGINS_MAXIMUM_MODELS=7\n"))
	if e != nil || got.Plugins.Enabled || got.Policy().Plugins.MaximumModels != 7 {
		t.Fatal("same artifact overrides", e)
	}
	for _, bad := range []string{"PROJECT_ONE_PLUGINS_ENABLED=FALSE\n", "PROJECT_ONE_PLUGINS_ENABLED=true\n", "PROJECT_ONE_PLUGINS_ENABLED=1\n", "PROJECT_ONE_PLUGINS_ENABLED=false\nPROJECT_ONE_PLUGINS_ENABLED=false\n", "PROJECT_ONE_PLUGINS_UNKNOWN=1\n", "PROJECT_ONE_PLUGINS_MANIFEST_BYTES=16384\nPROJECT_ONE_PLUGINS_CAPSULE_BYTES=2048\n"} {
		if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, bad)); e == nil {
			t.Fatal("invalid deployment admitted", bad)
		}
	}
	tool := c
	tool.Knowledge.Enabled = false
	public, _, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := wire.KeyID(public)
	tool.Plugins = PluginConfiguration{Enabled: true, TrustedKeys: []PluginTrustKey{{KeyID: id, PublicKey: base64.StdEncoding.EncodeToString(public)}}}
	tool.Workflows.Enabled = true
	directory := t.TempDir()
	os.Chmod(directory, 0700)
	trust := filepath.Join(directory, "public-trust.json")
	encoded, _ := json.Marshal(tool.Plugins.TrustedKeys)
	os.WriteFile(trust, encoded, 0600)
	overridden, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_PLUGINS_TRUSTED_KEYS_FILE="+trust+"\nPROJECT_ONE_PLUGINS_ENABLED=true\nPROJECT_ONE_KNOWLEDGE_ENABLED=false\n"))
	if e != nil || !overridden.Plugins.Enabled || overridden.Knowledge.Enabled || len(overridden.Plugins.TrustedKeys) != 1 {
		t.Fatal("public trust/tool-only mapping", e)
	}
	for _, invalid := range []string{"null", "[]", "[{\"key_id\":null,\"public_key\":\"bad\"}]", "[{\"key_id\":\"bad\",\"key_id\":\"bad\",\"public_key\":\"bad\"}]", "[{\"Key_id\":\"bad\",\"public_key\":\"bad\"}]"} {
		os.WriteFile(trust, []byte(invalid), 0600)
		if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_PLUGINS_TRUSTED_KEYS_FILE="+trust+"\nPROJECT_ONE_PLUGINS_ENABLED=true\n")); e == nil {
			t.Fatal("malformed trust accepted")
		}
	}
	if e = tool.ValidateApplicationProfile(); e != nil {
		t.Fatal("tool-only profile", e)
	}
}

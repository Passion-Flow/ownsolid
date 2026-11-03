package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

func TestM13ToolOnlyCompleteConfigurationLoader(t *testing.T) {
	public, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := wire.KeyID(public)
	if err != nil {
		t.Fatal(err)
	}
	fixture := func() Config {
		c := validConfig()
		c.Workflows.Enabled = true
		c.Knowledge.Enabled = false
		c.Plugins = PluginConfiguration{Enabled: true, TrustedKeys: []PluginTrustKey{{KeyID: keyID, PublicKey: base64.StdEncoding.EncodeToString(public)}}}
		policy := DefaultRuntimePolicy()
		policy.HTTP.RequestSeconds, policy.HTTP.StartupSeconds, policy.HTTP.ShutdownSeconds = 90, 60, 30
		c.Runtime = &policy
		return c
	}
	load := func(t *testing.T, c Config, environment string) (Config, error) {
		t.Helper()
		raw, err := yaml.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		path := filepath.Join(root, "control.yaml")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		env := ""
		if environment != "" {
			// Reuse the canonical, owner-only fixture required by readPrivateFile;
			// macOS t.TempDir may otherwise retain the /var -> /private/var alias.
			env = workspaceEnvironmentFixture(t, environment)
		}
		return LoadWithEnvironment(path, env)
	}
	for _, tc := range []struct {
		name   string
		change func(*Config)
		code   string
	}{
		{"tool_only", func(*Config) {}, ""},
		{"mixed", func(c *Config) { c.Knowledge = knowledgeFixture() }, ""},
		{"knowledge_only", func(c *Config) { c.Knowledge = knowledgeFixture(); c.Plugins.Enabled = false }, ""},
		{"no_executor", func(c *Config) { c.Plugins.Enabled = false }, "CONFIG_WORKFLOW_HTTP_BUDGET_INVALID"},
		{"missing_trust", func(c *Config) { c.Plugins.TrustedKeys = nil }, "CONFIG_PLUGIN_TRUST_INVALID"},
		{"invalid_trust", func(c *Config) { c.Plugins.TrustedKeys[0].PublicKey = "not-a-public-key" }, "CONFIG_PLUGIN_TRUST_INVALID"},
		{"wrong_key_identity", func(c *Config) { c.Plugins.TrustedKeys[0].KeyID = strings.Repeat("0", 64) }, "CONFIG_PLUGIN_TRUST_INVALID"},
		{"enabled_knowledge_missing_assets", func(c *Config) { c.Knowledge.Enabled = true }, "CONFIG_KNOWLEDGE_INVALID"},
		{"enabled_knowledge_insecure_gateway", func(c *Config) {
			c.Knowledge = knowledgeFixture()
			c.Knowledge.Gateways[0].Origin = "http://gateway.example.invalid"
		}, "CONFIG_KNOWLEDGE_INVALID"},
		{"workflow_commit_budget", func(c *Config) { c.Runtime.Workflows.CommitSeconds = 20; c.Runtime.HTTP.RequestSeconds = 15 }, "CONFIG_WORKFLOW_HTTP_BUDGET_INVALID"},
		{"plugin_commit_budget", func(c *Config) { c.Runtime.Plugins.CommitSeconds = 20; c.Runtime.HTTP.RequestSeconds = 15 }, "CONFIG_PLUGIN_HTTP_BUDGET_INVALID"},
		{"conservative_shutdown_budget", func(c *Config) {
			c.Runtime.HTTP.ShutdownSeconds = c.Runtime.Workflows.CommitSeconds + c.Runtime.Knowledge.KeyCleanupSeconds + c.Runtime.Knowledge.ChildStopSeconds - 1
		}, "CONFIG_WORKFLOW_HTTP_BUDGET_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture()
			tc.change(&c)
			loaded, err := load(t, c, "")
			if tc.code == "" {
				if err != nil || loaded.Workflows.Enabled != c.Workflows.Enabled || loaded.Plugins.Enabled != c.Plugins.Enabled || loaded.Knowledge.Enabled != c.Knowledge.Enabled {
					t.Fatal("complete profile loader", err)
				}
			} else if err == nil || err.Error() != tc.code || strings.Contains(err.Error(), base64.StdEncoding.EncodeToString(public)) {
				t.Fatal("profile refusal or safe error changed", tc.code, err)
			}
		})
	}
	t.Run("explicit_policy_file_and_false_flag", func(t *testing.T) {
		t.Setenv("PROJECT_ONE_KNOWLEDGE_ENABLED", "true") // Ambient variables have no profile authority.
		loaded, err := load(t, fixture(), "PROJECT_ONE_HTTP_REQUEST_SECONDS=95\nPROJECT_ONE_HTTP_STARTUP_SECONDS=65\nPROJECT_ONE_HTTP_SHUTDOWN_SECONDS=35\n")
		if err != nil || loaded.Knowledge.Enabled || !loaded.Workflows.Enabled || !loaded.Plugins.Enabled || loaded.Policy().HTTP.RequestSeconds != 95 || loaded.Policy().HTTP.StartupSeconds != 65 || loaded.Policy().HTTP.ShutdownSeconds != 35 {
			t.Fatal("explicit policy mapping or false value", err)
		}
		if loaded, err := load(t, fixture(), "PROJECT_ONE_KNOWLEDGE_ENABLED=false\n"); err != nil || loaded.Knowledge.Enabled {
			t.Fatal("declared protected false switch must preserve tool-only configuration", err)
		}
		if _, err := load(t, fixture(), "PROJECT_ONE_KNOWLEDGE_ENABLED=1\n"); err == nil {
			t.Fatal("invalid boolean must refuse")
		}
	})
	t.Run("tracked_example_requires_actual_public_trust", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "examples", "control", "workflow-tool-only.example.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		c := fixture()
		if err := yaml.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		if _, err := load(t, c, ""); err == nil || err.Error() != "CONFIG_PLUGIN_TRUST_INVALID" {
			t.Fatal("unprovisioned example must refuse", err)
		}
		c.Plugins.TrustedKeys = fixture().Plugins.TrustedKeys
		loaded, err := load(t, c, "")
		if err != nil || loaded.Knowledge.Enabled || !loaded.Workflows.Enabled || !loaded.Plugins.Enabled {
			t.Fatal("prepared example profile", err)
		}
	})
}
